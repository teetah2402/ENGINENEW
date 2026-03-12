package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall" // [ADDED] Absolute requirement to hide Windows CMD

	"flowork-engine/internal/packer" // Cryptography module
	"flowork-engine/internal/watcher" // [ADDED] Import untuk mendeteksi Global Dev Mode
)

type NodeManager struct {
	BaseDir string
}

func NewNodeManager(baseDir string) *NodeManager {
	return &NodeManager{BaseDir: baseDir}
}

func (nm *NodeManager) ScanNodes() []map[string]interface{} {
	var nodes []map[string]interface{}

	entries, err := ioutil.ReadDir(nm.BaseDir)
	if err != nil {
		return nodes
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".nflow") {
			nodeFileName := entry.Name()
			nodeID := strings.TrimSuffix(nodeFileName, ".nflow")
			tempDir := packer.GenerateSecretPath()
			err := packer.DecryptAndUnpack(filepath.Join(nm.BaseDir, nodeFileName), tempDir)
			if err != nil {
				log.Printf("  [Scanner] ❌ Failed to decrypt node %s: %v\n", nodeFileName, err)
				os.RemoveAll(tempDir)
				continue
			}

			schemaPath := filepath.Join(tempDir, "schema.json")
			var customParameters []interface{}
			schemaBytes, err := ioutil.ReadFile(schemaPath)

			if err != nil {
				log.Printf("  [Schema] ⚠️ schema.json file not found inside package %s\n", nodeFileName)
			} else {
				var schemaData map[string]interface{}
				if errJSON := json.Unmarshal(schemaBytes, &schemaData); errJSON != nil {
					log.Printf("  [Schema] ❌ JSON Parsing ERROR in %s: %v\n", nodeFileName, errJSON)
				} else {
					if params, ok := schemaData["parameters"].([]interface{}); ok {
						customParameters = params
					}
				}
			}
			os.RemoveAll(tempDir)
			if customParameters == nil {
				customParameters = []interface{}{}
			}

			displayName := strings.Title(strings.ReplaceAll(nodeID, "-", " "))
			nodeName := fmt.Sprintf("engine.secure.%s", nodeID)

			nodes = append(nodes, map[string]interface{}{
				"name":        nodeName,
				"displayName": "📦 " + displayName,
				"description": "Secure Executable Node (.nflow)",
				"category":    "Physical Engine (Encrypted)",
				"color":       "#3572A5",
				"inputs":      []map[string]string{{"id": "in-0", "label": "Data Input", "type": "any"}},
				"outputs":     []map[string]string{{"id": "out-0", "label": "Output Result", "type": "any"}},
				"parameters":  customParameters,
			})
			log.Printf("  └─ Found & registered (Secure Node): %s\n", nodeName)
		} else if entry.IsDir() && watcher.IsDevMode && !strings.HasPrefix(entry.Name(), ".") && entry.Name() != "libs" {
			// [ADDED] DEV MODE: Read schema.json directly from raw folder!
			nodeID := entry.Name()
			schemaPath := filepath.Join(nm.BaseDir, nodeID, "schema.json")
			var customParameters []interface{}
			schemaBytes, err := ioutil.ReadFile(schemaPath)

			if err != nil {
				log.Printf("  [Schema] ⚠️ schema.json file not found inside raw package %s\n", nodeID)
			} else {
				var schemaData map[string]interface{}
				if errJSON := json.Unmarshal(schemaBytes, &schemaData); errJSON != nil {
					log.Printf("  [Schema] ❌ JSON Parsing ERROR in %s: %v\n", nodeID, errJSON)
				} else {
					if params, ok := schemaData["parameters"].([]interface{}); ok {
						customParameters = params
					}
				}
			}

			if customParameters == nil {
				customParameters = []interface{}{}
			}

			displayName := strings.Title(strings.ReplaceAll(nodeID, "-", " "))
			nodeName := fmt.Sprintf("engine.secure.%s", nodeID)

			nodes = append(nodes, map[string]interface{}{
				"name":        nodeName,
				"displayName": "🛠️ " + displayName + " (Dev Mode)",
				"description": "Raw Executable Node (Dev Mode)",
				"category":    "Physical Engine (Raw Mode)",
				"color":       "#FF006E", // Color tag distinction for developer
				"inputs":      []map[string]string{{"id": "in-0", "label": "Data Input", "type": "any"}},
				"outputs":     []map[string]string{{"id": "out-0", "label": "Output Result", "type": "any"}},
				"parameters":  customParameters,
			})
			log.Printf("  └─ Found & registered (Dev Node): %s\n", nodeName)
		}
	}
	return nodes
}

func ensurePortableRuntime(baseDir string, lang string) (string, error) {
	osDir := filepath.Dir(baseDir)
	if baseDir == "nodes" {
		osDir = "."
	}
	// [FIX] Ubah osDir langsung menjadi Absolute Path agar tidak terjadi duplikasi
	absOsDir, err := filepath.Abs(osDir)
	if err != nil {
		absOsDir = osDir
	}

	runtimeDir := filepath.Join(absOsDir, "runtimes", lang)

	var exeName string

	if runtime.GOOS == "windows" {
		if lang == "python" {
			exeName = "python.exe"
		} else if lang == "node" {
			exeName = "node.exe"
		} else if lang == "ruby" {
			exeName = "ruby.exe"
		}
	} else {
		exeName = lang
		if lang == "python" {
			exeName = "python3"
		}
	}

	exePath := filepath.Join(runtimeDir, exeName)
	altExePath := filepath.Join(runtimeDir, "bin", exeName)
	if _, err := os.Stat(altExePath); err == nil {
		exePath = altExePath
	}
	absExePath, err := filepath.Abs(exePath)
	if err == nil {
		exePath = absExePath
	}

	// Verify if the bundled runtime exists on disk
	if _, err := os.Stat(exePath); err != nil {
		return "", fmt.Errorf("bundled runtime for '%s' not found at %s. Please ensure the Flowork OS package is intact", lang, exePath)
	}

	// Grant execute permission for Mac/Linux
	if runtime.GOOS != "windows" {
		os.Chmod(exePath, 0755)
	}

	return exePath, nil
}

func (nm *NodeManager) Execute(nodeType string, inputData interface{}) (interface{}, error) {
	parts := strings.Split(nodeType, ".")
	if len(parts) < 3 || parts[0] != "engine" {
		return nil, fmt.Errorf("invalid node type")
	}

	nodeID := parts[2]
	nflowPath := filepath.Join(nm.BaseDir, nodeID+".nflow")
	rawNodePath := filepath.Join(nm.BaseDir, nodeID)

	// [ADDED] Logic deteksi apakah kita harus mengeksekusi folder mentah (Dev Mode)
	isRawDevNode := false
	if watcher.IsDevMode {
		if info, err := os.Stat(rawNodePath); err == nil && info.IsDir() {
			isRawDevNode = true
		}
	}

	// [DIKOMENTARI] ZOMBIE CODE: Pengecekan .nflow statis dipindah ke blok else cerdas
	// if _, err := os.Stat(nflowPath); os.IsNotExist(err) {
	// 	return nil, fmt.Errorf("secure module (.nflow) not found: %s", nflowPath)
	// }

	if !isRawDevNode {
		if _, err := os.Stat(nflowPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("secure module (.nflow) not found: %s", nflowPath)
		}
	}

	var secretDir string

	if isRawDevNode {
		log.Printf("[Engine] 🛠️ DEV MODE: Executing raw node folder directly: %s\n", rawNodePath)
		secretDir = rawNodePath
	} else {
		// [DIKOMENTARI] ZOMBIE CODE: Inisialisasi secretDir dipindahkan ke scope atas
		// secretDir := packer.GenerateSecretPath()
		secretDir = packer.GenerateSecretPath()
		if err := packer.DecryptAndUnpack(nflowPath, secretDir); err != nil {
			return nil, fmt.Errorf("decryption failed, package corrupted: %v", err)
		}
	}

	// Dynamic Multi-Language Detection
	scriptPath := ""
	lang := ""
	files, _ := ioutil.ReadDir(secretDir)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".py") {
			scriptPath = f.Name()
			lang = "python"
			break
		} else if strings.HasSuffix(f.Name(), ".js") {
			scriptPath = f.Name()
			lang = "node"
			break
		} else if strings.HasSuffix(f.Name(), ".rb") {
			scriptPath = f.Name()
			lang = "ruby"
			break
		}
	}

	if scriptPath == "" || lang == "" {
		return nil, fmt.Errorf("no valid script (.py, .js, .rb) found inside the package")
	}

	// Invoke Bundled Portable Runtime
	runnerCmd, err := ensurePortableRuntime(nm.BaseDir, lang)
	if err != nil {
		log.Printf("[Engine] ⚠️ Local runtime '%s' missing: %v. Attempting system fallback...\n", lang, err)
		runnerCmd = lang // Fallback to system env
		if lang == "python" && runtime.GOOS != "windows" {
			runnerCmd = "python3"
		}
	}

	absSecretDir, _ := filepath.Abs(secretDir)
	absLibsPath := filepath.Join(absSecretDir, "libs")

	// Install libraries to secretDir based on language
	if lang == "python" {
		reqPath := filepath.Join(secretDir, "requirements.txt")
		if _, err := os.Stat(reqPath); err == nil {
			log.Printf("[Engine] 📦 Installing temporary Python libraries for node %s...\n", nodeID)
			installCmd := exec.Command(runnerCmd, "-m", "pip", "install", "-r", "requirements.txt", "-t", "libs")
			installCmd.Dir = absSecretDir
			// [ADDED] Hide black popup when installing Node Flow dependencies
			if runtime.GOOS == "windows" {
				installCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			}

			// [MODIFIED] Menangkap output error untuk log GUI dan melakukan retry instalasi cerdas
			installOut, errInstall := installCmd.CombinedOutput()
			if errInstall != nil {
				log.Printf("[Engine] ❌ PIP INSTALL ERROR: %v | %s\n", errInstall, string(installOut))
				log.Printf("[Engine] ⚠️ Portable Python gagal menginstal modul. Melakukan retrying instalasi dengan System Python...\n")

				// [FIX] Mengembalikan ke System Python, dan ULANGI perintah pip install!
				runnerCmd = "python"
				if runtime.GOOS != "windows" {
					runnerCmd = "python3"
				}

				retryInstallCmd := exec.Command(runnerCmd, "-m", "pip", "install", "-r", "requirements.txt", "-t", "libs")
				retryInstallCmd.Dir = absSecretDir
				if runtime.GOOS == "windows" {
					retryInstallCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				}

				retryOut, errRetry := retryInstallCmd.CombinedOutput()
				if errRetry != nil {
					log.Printf("[Engine] ❌ SYSTEM PIP ALSO FAILED: %v | %s\n", errRetry, string(retryOut))
				} else {
					log.Printf("[Engine] ✅ Instalasi dengan System Python berhasil.\n")
				}
			}
		}
	} else if lang == "node" {
		pkgPath := filepath.Join(secretDir, "package.json")
		if _, err := os.Stat(pkgPath); err == nil {
			log.Printf("[Engine] 📦 Installing temporary Node.js modules for node %s...\n", nodeID)
			installCmd := exec.Command("npm", "install")
			installCmd.Dir = absSecretDir
			// [ADDED] Hide black popup when installing Node Flow dependencies
			if runtime.GOOS == "windows" {
				installCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			}
			installCmd.Run()
		}
	}

	log.Printf("[Engine] ⚙️ Executing secret file [%s]: %s\n", lang, scriptPath)

	cmd := exec.Command(runnerCmd, scriptPath)
	cmd.Dir = absSecretDir

	// [ADDED] Absolute rule: Hide black CMD window during node flow execution
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}

	env := os.Environ()
	customPythonPath := fmt.Sprintf("%s%c%s", absSecretDir, os.PathListSeparator, absLibsPath)

	hasPythonPath := false
	hasIOEncoding := false

	for i, e := range env {
		if strings.HasPrefix(e, "PYTHONPATH=") {
			env[i] = e + string(os.PathListSeparator) + customPythonPath
			hasPythonPath = true
		} else if strings.HasPrefix(e, "PYTHONIOENCODING=") {
			env[i] = "PYTHONIOENCODING=utf-8"
			hasIOEncoding = true
		}
	}

	if lang == "python" && !hasPythonPath {
		env = append(env, "PYTHONPATH="+customPythonPath)
	}
	if !hasIOEncoding {
		env = append(env, "PYTHONIOENCODING=utf-8")
	}

	cmd.Env = env

	inputBytes, _ := json.Marshal(inputData)
	cmd.Stdin = bytes.NewReader(inputBytes)

	var outBuffer, errBuffer bytes.Buffer
	cmd.Stdout = &outBuffer
	cmd.Stderr = &errBuffer

	errExec := cmd.Run()

	if errBuffer.Len() > 0 {
		log.Printf("[Node Warning] %s\n", errBuffer.String())
	}

	if errExec != nil {
		return nil, fmt.Errorf("execution failed: %v | log: %s", errExec, errBuffer.String())
	}

	rawOutput := strings.TrimSpace(outBuffer.String())
	var resultData interface{}

	if rawOutput != "" {
		if errJSON := json.Unmarshal([]byte(rawOutput), &resultData); errJSON != nil {
			resultData = map[string]string{"output_text": rawOutput}
		}
	} else {
		resultData = map[string]string{"status": "empty", "output": "No response from node."}
	}

	return resultData, nil
}