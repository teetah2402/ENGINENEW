package socket

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"       // For copying stream file
	"log"
	"net/http" // For file download requests
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall" // Absolute requirement to hide Windows CMD
	"time"

	"flowork-engine/internal/packer"
	"flowork-engine/internal/runner"
	"flowork-engine/internal/watcher" // [ADDED] Memanggil status Dev Mode global

	"github.com/gofiber/fiber/v2"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/sqweek/dialog"
	"github.com/valyala/fasthttp/fasthttpadaptor"
	"github.com/zishang520/socket.io/v2/socket"
)

// Structure to track active Sandboxes
type SandboxInfo struct {
	Path     string
	RefCount int
}

type SocketHandler struct {
	io           *socket.Server
	nodeManager  *runner.NodeManager
	isBusy       bool
	sandboxes    map[string]*SandboxInfo // Map storing active secret folders
	sandboxMutex sync.Mutex              // Thread-safe mutex lock
}

func NewSocketHandler(nm *runner.NodeManager) *SocketHandler {
	io := socket.NewServer(nil, nil)
	handler := &SocketHandler{
		io:          io,
		nodeManager: nm,
		isBusy:      false,
		sandboxes:   make(map[string]*SandboxInfo), // Initialize map
	}

	handler.setupEvents()
	go handler.telemetryLoop()

	return handler
}

func (s *SocketHandler) FiberHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		fasthttpadaptor.NewFastHTTPHandler(s.io.ServeHandler(nil))(c.Context())
		return nil
	}
}

func (s *SocketHandler) BroadcastRefresh() {
	s.io.Of("/gui-socket", nil).Emit("engine:needs_refresh", map[string]interface{}{})
}

func (s *SocketHandler) setupEvents() {
	namespace := s.io.Of("/gui-socket", nil)

	namespace.On("connection", func(clients ...any) {
		client := clients[0].(*socket.Socket)
		log.Printf("\n[Engine] 🟢 GUI BROWSER CONNECTED! (SID: %s)\n", client.Id())

		client.Emit("engine:online", map[string]string{
			"engine_id": "local_engine",
			"status":    "ready",
		})

		client.On("disconnect", func(...any) {
			log.Printf("[Engine] 🔴 GUI BROWSER DISCONNECTED. Waiting for reconnection... (SID: %s)\n", client.Id())
		})

		// =========================================================================
		// [ADDED] Handler to Toggle Dev Mode from GUI
		// =========================================================================
		client.On("engine:toggle_dev_mode", func(args ...any) {
			if len(args) > 0 {
				if payload, ok := args[0].(map[string]interface{}); ok {
					if isDev, okDev := payload["is_dev"].(bool); okDev {
						watcher.IsDevMode = isDev
						if isDev {
							log.Printf("\n[Engine] 🛠️ DEV MODE ENABLED! Raw unencrypted folders will now be read directly.\n")
						} else {
							log.Printf("\n[Engine] 🔒 DEV MODE DISABLED! Security constraints restored.\n")
						}
						s.BroadcastRefresh() // Force UI to fetch apps/nodes again
					}
				}
			}
		})

		// =========================================================================
		// [ADDED] Handler to EXIT/SHUTDOWN ENGINE from GUI
		// =========================================================================
		client.On("engine:exit", func(args ...any) {
			log.Println("\n[Engine] 🛑 Received EXIT command from UI Dashboard.")
			packer.CleanupOldTempFolders() // Clean up temp folders before exiting
			os.Exit(0)
		})

		// =========================================================================
		// [ADDED] Handler to RESTART ENGINE from GUI
		// =========================================================================
		client.On("engine:restart", func(args ...any) {
			log.Println("\n[Engine] 🔄 Received RESTART command from UI Dashboard.")
			packer.CleanupOldTempFolders() // Clean up temp folders before restarting

			exePath, _ := os.Executable()
			cmd := exec.Command(exePath)
			cmd.Start() // Start new instance (old instance will be auto-killed by the new one via Single Instance feature)
			os.Exit(0)  // Terminate current instance
		})

		// =========================================================================
		// Handler to detect installed packages
		// =========================================================================
		client.On("engine:get_installed_ids", func(args ...any) {
			installed := map[string]bool{}

			appEntries, _ := os.ReadDir("apps")
			for _, e := range appEntries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".flow") {
					installed[strings.TrimSuffix(e.Name(), ".flow")] = true
				}
			}

			nodeEntries, _ := os.ReadDir("nodes")
			for _, e := range nodeEntries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".nflow") {
					installed[strings.TrimSuffix(e.Name(), ".nflow")] = true
				}
			}

			client.Emit("engine:installed_ids_list", installed)
		})

		// =========================================================================
		// Handler to Uninstall/Remove packages
		// =========================================================================
		client.On("engine:uninstall_module", func(args ...any) {
			if len(args) == 0 {
				return
			}
			payload, ok := args[0].(map[string]interface{})
			if !ok {
				return
			}

			id, _ := payload["id"].(string)
			moduleType, _ := payload["type"].(string)

			targetDir := "apps"
			ext := ".flow"
			if moduleType == "node" {
				targetDir = "nodes"
				ext = ".nflow"
			}

			filePath := filepath.Join(targetDir, id+ext)
			err := os.Remove(filePath)

			if err != nil && !os.IsNotExist(err) {
				log.Printf("[Engine] ❌ Failed to uninstall %s: %v\n", id, err)
				client.Emit("engine:uninstall_result", map[string]interface{}{
					"id":      id,
					"success": false,
					"error":   err.Error(),
				})
			} else {
				log.Printf("[Engine] 🗑️ Successfully removed module %s from disk.\n", id)
				client.Emit("engine:uninstall_result", map[string]interface{}{
					"id":      id,
					"success": true,
				})
			}
		})

		// =========================================================================
		// [RESTORED GOLDEN CODE] Handler for Direct Download to Portable Folder
		// =========================================================================
		client.On("engine:install_module", func(args ...any) {
			if len(args) == 0 {
				return
			}
			payload, ok := args[0].(map[string]interface{})
			if !ok {
				return
			}

			id, _ := payload["id"].(string)
			moduleType, _ := payload["type"].(string)
			name, _ := payload["name"].(string)
			downloadURL, _ := payload["download_url"].(string)

			log.Printf("\n[Engine] 📥 Starting engine download: %s", name)
			log.Printf("[Engine] 🔗 URL: %s", downloadURL)

			go func() {
				// Set relative target folder to maintain portability
				targetDir := "apps"
				ext := ".flow"
				if moduleType == "node" {
					targetDir = "nodes"
					ext = ".nflow"
				}

				// Ensure apps / nodes folders exist (os.ModePerm = 0777)
				if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
					log.Printf("[Engine] ❌ Failed to create directory %s: %v\n", targetDir, err)
					client.Emit("engine:install_result", map[string]interface{}{"id": id, "success": false, "error": "Failed to create folder in engine directory."})
					return
				}

				filePath := filepath.Join(targetDir, id+ext)

				// HTTP fetch process for .flow or .nflow files
				resp, err := http.Get(downloadURL)
				if err != nil {
					log.Printf("[Engine] ❌ Failed to download file: %v\n", err)
					client.Emit("engine:install_result", map[string]interface{}{"id": id, "success": false, "error": "Connection to Flowork server lost."})
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode != 200 {
					log.Printf("[Engine] ❌ HTTP Error %d while downloading %s\n", resp.StatusCode, name)
					client.Emit("engine:install_result", map[string]interface{}{"id": id, "success": false, "error": fmt.Sprintf("Server Error HTTP %d", resp.StatusCode)})
					return
				}

				// Write data stream directly to local file
				out, err := os.Create(filePath)
				if err != nil {
					log.Printf("[Engine] ❌ Failed to write to local file %s: %v\n", filePath, err)
					client.Emit("engine:install_result", map[string]interface{}{"id": id, "success": false, "error": "Failed to save package to SSD/Hard drive."})
					return
				}
				defer out.Close()

				if _, err = io.Copy(out, resp.Body); err != nil {
					log.Printf("[Engine] ❌ File copy process interrupted %s: %v\n", filePath, err)
					client.Emit("engine:install_result", map[string]interface{}{"id": id, "success": false, "error": "Download process corrupted or interrupted."})
					return
				}

				log.Printf("[Engine] ✅ Success! Module %s saved portably at %s\n", name, filePath)
				client.Emit("engine:install_result", map[string]interface{}{
					"id":      id,
					"name":    name,
					"success": true,
					"path":    filePath,
				})
			}()
		})
		// =========================================================================

		client.On("engine:get_nodes", func(args ...any) {
			log.Println("\n[Engine] 🔍 GUI requested list of physical Nodes. Scanning .nflow packages...")
			nodes := s.nodeManager.ScanNodes()
			client.Emit("engine:nodes_list", map[string]interface{}{
				"data": nodes,
			})
			log.Printf("[Engine] ✅ Total of %d Secure Nodes sent to GUI Sidebar.\n", len(nodes))
		})

		client.On("engine:get_apps", func(args ...any) {
			log.Println("\n[Engine] 🔍 GUI requested list of Offline Apps. Reading packages...")
			var appsList []map[string]interface{}

			entries, err := os.ReadDir("apps")
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".flow") {
						appID := strings.TrimSuffix(entry.Name(), ".flow")
						flowPath := filepath.Join("apps", entry.Name())

						tempDir := packer.GenerateSecretPath()
						if err := packer.DecryptAndUnpack(flowPath, tempDir); err == nil {
							manifestPath := filepath.Join(tempDir, "manifest.json")
							if manifestBytes, err := os.ReadFile(manifestPath); err == nil {
								var manifest map[string]interface{}
								if err := json.Unmarshal(manifestBytes, &manifest); err == nil {
									if _, ok := manifest["id"]; !ok {
										manifest["id"] = appID
									}
									manifest["android"] = "yes"
									manifest["desktop"] = "yes"
									manifest["is_local"] = true

									appsList = append(appsList, manifest)
								}
							}
						}
						os.RemoveAll(tempDir)
					} else if entry.IsDir() && watcher.IsDevMode && !strings.HasPrefix(entry.Name(), ".") && entry.Name() != "libs" {
						// [ADDED] DEV MODE: Read manifest.json directly from raw folder!
						appID := entry.Name()
						manifestPath := filepath.Join("apps", entry.Name(), "manifest.json")
						if manifestBytes, err := os.ReadFile(manifestPath); err == nil {
							var manifest map[string]interface{}
							if err := json.Unmarshal(manifestBytes, &manifest); err == nil {
								if _, ok := manifest["id"]; !ok {
									manifest["id"] = appID
								}
								manifest["android"] = "yes"
								manifest["desktop"] = "yes"
								manifest["is_local"] = true
								manifest["name"] = fmt.Sprintf("🛠️ %v (Dev Mode)", manifest["name"])
								appsList = append(appsList, manifest)
							}
						}
					}
				}
			}

			client.Emit("engine:apps_list", map[string]interface{}{
				"data": appsList,
			})
			log.Printf("[Engine] ✅ Total of %d Apps found and sent to GUI Store.\n", len(appsList))
		})

		client.On("engine:pick_folder", func(args ...any) {
			if len(args) == 0 {
				return
			}
			payload, ok := args[0].(map[string]interface{})
			if !ok {
				return
			}
			reqID, okID := payload["request_id"].(string)
			if !okID {
				return
			}

			go func() {
				directory, err := dialog.Directory().Title("Flowork - Select Destination Folder").Browse()
				responseEvent := "engine_folder_picked_" + reqID

				if err != nil {
					client.Emit(responseEvent, map[string]interface{}{"error": err.Error(), "path": ""})
				} else {
					client.Emit(responseEvent, map[string]interface{}{"error": nil, "path": directory})
				}
			}()
		})

		client.On("engine:execute_task", func(args ...any) {
			s.isBusy = true
			defer func() { s.isBusy = false }()

			if len(args) == 0 {
				return
			}
			payload, ok := args[0].(map[string]interface{})
			if !ok {
				return
			}

			taskID, okID := payload["task_id"].(string)
			taskName, okName := payload["task_name"].(string)

			if !okID || !okName {
				log.Printf("[Engine] ❌ ERROR: execute_task payload is incomplete.\n")
				return
			}

			taskPayload := payload["payload"]
			log.Printf("\n[Engine] ⚡ RECEIVED SECURE APP TASK: %s (TaskID: %s)\n", taskName, taskID)

			responseEvent := "engine_task_result_" + taskID

			go func() {
				// [ADDED] Deteksi Bypass untuk Dev Mode
				isRawDevApp := false
				appRawPath := filepath.Join("apps", taskName)
				if watcher.IsDevMode {
					if info, err := os.Stat(appRawPath); err == nil && info.IsDir() {
						isRawDevApp = true
					}
				}

				var secretDir string

				if isRawDevApp {
					log.Printf("[Engine] 🛠️ DEV MODE: Executing raw app folder directly: %s\n", appRawPath)
					secretDir = appRawPath
				} else {
					appFlowPath := filepath.Join("apps", taskName+".flow")
					// [DIKOMENTARI] ZOMBIE CODE: Pengecekan dipindahkan ke dalam logic blok Else untuk Dev Mode
					// if _, err := os.Stat(appFlowPath); os.IsNotExist(err) {
					// 	namespace.Emit(responseEvent, map[string]interface{}{
					// 		"error": fmt.Sprintf("Application package file (%s.flow) not found.", taskName),
					// 		"data":  nil,
					// 	})
					// 	return
					// }

					if _, err := os.Stat(appFlowPath); os.IsNotExist(err) {
						namespace.Emit(responseEvent, map[string]interface{}{
							"error": fmt.Sprintf("Application package file (%s.flow) not found.", taskName),
							"data":  nil,
						})
						return
					}

					// Sandbox Room Guard System (Thread-Safe)
					s.sandboxMutex.Lock()
					sandbox, exists := s.sandboxes[taskName]
					if !exists {
						sandbox = &SandboxInfo{
							Path:     packer.GenerateSecretPath(),
							RefCount: 0,
						}
						s.sandboxes[taskName] = sandbox

						// Only perform Decryption if the Sandbox was just created
						if err := packer.DecryptAndUnpack(appFlowPath, sandbox.Path); err != nil {
							delete(s.sandboxes, taskName)
							s.sandboxMutex.Unlock()
							os.RemoveAll(sandbox.Path)
							namespace.Emit(responseEvent, map[string]interface{}{
								"error": "Application decryption failed: " + err.Error(),
								"data":  nil,
							})
							return
						}
					}

					sandbox.RefCount++
					secretDir = sandbox.Path
					s.sandboxMutex.Unlock()

					// [SMART AUTO-CLEAN] Delete secret folder ONLY if all Start/Stop processes have finished (RefCount == 0)
					defer func() {
						s.sandboxMutex.Lock()
						sandbox.RefCount--
						if sandbox.RefCount <= 0 {
							delete(s.sandboxes, taskName)
							os.RemoveAll(secretDir)
							log.Printf("[Engine] 🧹 Auto-Clean: Deleting Secret Sandbox %s because all processes finished.", taskName)
						}
						s.sandboxMutex.Unlock()
					}()
				}

				schemaPath := filepath.Join(secretDir, "schema.json")
				entryPoint := "script.py"

				if schemaBytes, err := os.ReadFile(schemaPath); err == nil {
					var schema map[string]interface{}
					if err := json.Unmarshal(schemaBytes, &schema); err == nil {
						if ep, ok := schema["entry_point"].(string); ok && ep != "" {
							entryPoint = ep
						}
					}
				}

				scriptPath := filepath.Join(secretDir, entryPoint)
				if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
					namespace.Emit(responseEvent, map[string]interface{}{
						"error": fmt.Sprintf("Executor file (%s) not found in the package.", entryPoint),
						"data":  nil,
					})
					return
				}

				ext := strings.ToLower(filepath.Ext(entryPoint))
				var cmd *exec.Cmd

				if ext == ".py" {
					reqPath := filepath.Join(secretDir, "requirements.txt")
					libsDir := filepath.Join(secretDir, "libs")
					pythonCmd := "python"
					if runtime.GOOS != "windows" {
						pythonCmd = "python3"
					}

					if _, err := os.Stat(reqPath); err == nil {
						if _, err := os.Stat(libsDir); os.IsNotExist(err) {
							log.Printf("[Engine] 📦 Installing Python libraries in Sandbox %s...", taskName)
							installCmd := exec.Command(pythonCmd, "-m", "pip", "install", "-r", "requirements.txt", "-t", "libs")
							installCmd.Dir = secretDir
							// Hide black popup when installing dependencies
							if runtime.GOOS == "windows" {
								installCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
							}
							if out, err := installCmd.CombinedOutput(); err != nil {
								namespace.Emit(responseEvent, map[string]interface{}{"error": "Failed to install pip: " + string(out), "data": nil})
								return
							}
						}
					}

					cmd = exec.Command(pythonCmd, entryPoint)

					env := os.Environ()
					absAppDir, _ := filepath.Abs(secretDir)
					absLibsDir := filepath.Join(absAppDir, "libs")
					customPythonPath := fmt.Sprintf("%s%c%s", absAppDir, os.PathListSeparator, absLibsDir)

					hasPythonPath, hasIOEncoding := false, false
					for i, e := range env {
						if strings.HasPrefix(e, "PYTHONPATH=") {
							env[i] = e + string(os.PathListSeparator) + customPythonPath
							hasPythonPath = true
						}
						if strings.HasPrefix(e, "PYTHONIOENCODING=") {
							env[i] = "PYTHONIOENCODING=utf-8"
							hasIOEncoding = true
						}
					}
					if !hasPythonPath {
						env = append(env, "PYTHONPATH="+customPythonPath)
					}
					if !hasIOEncoding {
						env = append(env, "PYTHONIOENCODING=utf-8")
					}
					cmd.Env = env

				} else if ext == ".js" {
					pkgPath := filepath.Join(secretDir, "package.json")
					nodeModulesDir := filepath.Join(secretDir, "node_modules")

					if _, err := os.Stat(pkgPath); err == nil {
						if _, err := os.Stat(nodeModulesDir); os.IsNotExist(err) {
							installCmd := exec.Command("npm", "install")
							installCmd.Dir = secretDir
							// Hide black popup when installing dependencies
							if runtime.GOOS == "windows" {
								installCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
							}
							if out, err := installCmd.CombinedOutput(); err != nil {
								namespace.Emit(responseEvent, map[string]interface{}{"error": "Failed to npm install: " + string(out), "data": nil})
								return
							}
						}
					}
					cmd = exec.Command("node", entryPoint)

				} else if ext == ".rb" {
					cmd = exec.Command("ruby", entryPoint)
				} else if ext == ".exe" || ext == "" {
					execBinaryPath := filepath.Join(".", entryPoint)
					if runtime.GOOS != "windows" && ext == "" {
						execBinaryPath = "./" + entryPoint
					}
					cmd = exec.Command(execBinaryPath)
				} else {
					cmd = exec.Command(entryPoint)
				}

				cmd.Dir = secretDir
				// Absolute Rule: Hide black CMD window during main script execution
				if runtime.GOOS == "windows" {
					cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				}

				payloadBytes, _ := json.Marshal(taskPayload)
				cmd.Stdin = bytes.NewReader(payloadBytes)

				output, err := cmd.CombinedOutput()
				if err != nil {
					namespace.Emit(responseEvent, map[string]interface{}{
						"error": err.Error() + ": " + string(output),
						"data":  nil,
					})
					return
				}

				var result map[string]interface{}
				if err := json.Unmarshal(output, &result); err != nil {
					namespace.Emit(responseEvent, map[string]interface{}{
						"error": "Invalid JSON output from Native Engine: " + string(output),
						"data":  nil,
					})
					return
				}

				namespace.Emit(responseEvent, map[string]interface{}{
					"error": nil,
					"data":  result["data"],
				})
				log.Printf("[Engine] ✅ SECURE TASK RESULT COMPLETED & SENT BACK TO GUI.\n")
			}()
		})

		client.On("engine:execute_node", func(args ...any) {
			s.isBusy = true
			defer func() { s.isBusy = false }()

			if len(args) == 0 {
				return
			}

			rawPayload, ok := args[0].(map[string]interface{})
			if !ok {
				return
			}

			var payload map[string]interface{}
			if innerPayload, hasInner := rawPayload["payload"].(map[string]interface{}); hasInner {
				payload = innerPayload
			} else {
				payload = rawPayload
			}

			nodeType, okType := payload["node_type"].(string)
			executionID, okID := payload["execution_id"].(string)

			if !okType || !okID {
				return
			}

			inputData := payload["input_data"]
			result, err := s.nodeManager.Execute(nodeType, inputData)
			responseEvent := "engine_response_" + executionID

			if err != nil {
				namespace.Emit(responseEvent, map[string]interface{}{"error": err.Error(), "data": nil})
			} else {
				namespace.Emit(responseEvent, map[string]interface{}{"error": nil, "data": result})
			}
		})
	})
}

func (s *SocketHandler) telemetryLoop() {
	namespace := s.io.Of("/gui-socket", nil)
	for {
		v, _ := mem.VirtualMemory()
		c, _ := cpu.Percent(0, false)

		cpuVal := 0.0
		if len(c) > 0 {
			cpuVal = c[0]
		}

		vitals := map[string]interface{}{
			"is_busy":        s.isBusy,
			"cpu_percent":    cpuVal,
			"memory_percent": v.UsedPercent,
		}

		namespace.Emit("engine:vitals", vitals)
		time.Sleep(2 * time.Second)
	}
}