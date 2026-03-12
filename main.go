package main

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"embed" // Built-in Go module to embed folders into .exe file
	"encoding/json"
	"io"
	"log"
	"net/http" // Required for HTTP Client (Update) and FileServer
	"net/url"
	"os"
	"os/exec"
	"os/signal" // Module to capture OS signals (Shutdown/Close)
	"path/filepath"
	"runtime"
	"strings"
	"syscall" // syscall module
	"time"

	"flowork-engine/internal/packer"
	"flowork-engine/internal/runner"
	"flowork-engine/internal/socket"
	"flowork-engine/internal/watcher"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/filesystem" // Fiber Middleware specifically for embedded files
	"github.com/shirou/gopsutil/v3/process"             // Library to scan and kill old processes
	"github.com/sqweek/dialog"                          // Library to display Native OS Popups
)

// Compiler instruction to embed the 'store' folder AND 'icon.ico' into the .exe file
//go:embed store/* icon.ico
var embeddedFiles embed.FS

// Current Engine version constant to match with the server
const CurrentEngineVersion = "1.0.1"

// Function to lock the working directory so "apps" and "nodes" folders are not read from System32 when run as .exe
func lockWorkingDirectory() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	exeDir := filepath.Dir(exePath)

	// Ignore folder changes if the application is run using "go run" (which is in the OS Temp folder)
	if strings.Contains(strings.ToLower(exeDir), "temp") || strings.Contains(strings.ToLower(exeDir), "tmp") {
		return
	}

	// Lock working directory absolutely to the .exe file location
	if err := os.Chdir(exeDir); err == nil {
		log.Printf("[Engine] 📍 System directory absolutely locked to: %s", exeDir)
	}
}

// Function to prevent application stacking (Single Instance)
func killPreviousInstances() {
	currentPID := int32(os.Getpid())
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	exeName := filepath.Base(exePath)

	procs, err := process.Processes()
	if err != nil {
		return
	}

	for _, p := range procs {
		if p.Pid != currentPID {
			name, err := p.Name()
			// Check if there is a process with the same name (e.g. flowork-engine.exe)
			if err == nil && (name == exeName || name == exeName+".exe") {
				log.Printf("[Engine] ⚠️ Old instance detected (PID: %d). Stopping process to prevent stacking...", p.Pid)
				p.Kill()
				time.Sleep(500 * time.Millisecond) // Give OS time to release Port 5000
			}
		}
	}
}

// Special function to open link in STANDARD BROWSER (Standard Tab), not App mode
func openBrowser(url string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url) // empty string "" is important in Windows so URLs with '&' don't error
	case "darwin":
		cmd = exec.Command("open", url)
	default: // linux, freebsd, etc
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
	log.Printf("[Engine] 🌐 Opening standard browser tab to: %s\n", url)
}

func openAppWindow(url string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// Prioritize Google Chrome to avoid accidentally opening in Internet Explorer.
		cmd = exec.Command("cmd", "/c", "start", "chrome", "--app="+url)
		if err := cmd.Run(); err != nil {
			// If Chrome is missing, fallback to Edge
			cmd = exec.Command("cmd", "/c", "start", "msedge", "--app="+url)
			if err := cmd.Run(); err != nil {
				// If both are missing, fallback to open standard URL via default browser (avoids blank IE)
				cmd = exec.Command("cmd", "/c", "start", "", url)
				cmd.Run()
			}
		}
	case "darwin":
		cmd = exec.Command("open", "-n", "-a", "Google Chrome", "--args", "--app="+url)
		cmd.Start()
	case "linux":
		cmd = exec.Command("google-chrome", "--app="+url)
		cmd.Start()
	default:
		cmd = exec.Command("xdg-open", url)
		cmd.Start()
	}
	log.Printf("[Engine] 🖥️ Opening desktop application window to: %s\n", url)
}

// Special function to force update with Native OS Popup
func forceUpdate() {
	log.Println("[Engine] 🛑 UPDATE REQUIRED! Showing update popup to user...")

	// Display Native OS Popup (Execution Blocking)
	// This dialog blocks the Go process so the port 5000 server won't start
	dialog.Message("Mandatory System Update!\n\nYour Flowork OS version (v%s) is outdated or the system failed to verify the connection to the central server.\n\nYou cannot use this Engine without the latest version.\nClick OK to download the latest version.", CurrentEngineVersion).
		Title("Flowork OS - Update Required").
		Info()

	// Open page using standard browser and kill app ONLY after popup is closed/OK
	openBrowser("https://update.floworkos.com")
	os.Exit(0)
}

// Strict update checking function
func checkUpdate() {
	log.Println("[Engine] 🔄 Checking Flowork OS system version...")

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	// Manipulate HTTP Request to disguise as a standard Web Browser
	req, err := http.NewRequest("GET", "https://floworkos.com/update-engine.txt", nil)
	if err != nil {
		log.Printf("[Engine] ⚠️ Failed to create request: %v\n", err)
		forceUpdate()
		return
	}

	// Set User-Agent to Chrome Windows to avoid getting banned by Cloudflare/WAF Server
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 FloworkOS-Engine/1.0")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Engine] ⚠️ Update server is down / inaccessible: %v\n", err)
		forceUpdate() // Web down -> Still force update
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Engine] ⚠️ HTTP Error %d while checking for updates.\n", resp.StatusCode)
		forceUpdate() // Status not 200 -> Still force update
		return
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[Engine] ⚠️ Failed to read server response: %v\n", err)
		forceUpdate() // Failed to read body -> Still force update
		return
	}

	latestVersion := strings.TrimSpace(string(bodyBytes))

	// If the server version does NOT MATCH the current version
	if latestVersion != CurrentEngineVersion {
		log.Printf("[Engine] 🚀 Outdated version! (Current: %s | Latest: %s)\n", CurrentEngineVersion, latestVersion)
		forceUpdate()
		return
	}

	log.Printf("[Engine] ✅ System is Up-to-Date (v%s)\n", CurrentEngineVersion)
}

func main() {
	// =========================================================================
	// -1. LOCK DIRECTORY TO .EXE FILE LOCATION
	// =========================================================================
	lockWorkingDirectory()

	// =========================================================================
	// 0. SINGLE INSTANCE ENFORCER (Prevents app stacking)
	// =========================================================================
	killPreviousInstances()

	// =========================================================================
	// 1. INITIAL SWEEP (Clean leftover secret folders if engine crashed previously)
	// =========================================================================
	log.Println("[Engine] 🚀 Starting initial system initialization...")
	packer.CleanupOldTempFolders()

	// =========================================================================
	// 2. MANDATORY UPDATE CHECK (Will kill system if failed/outdated)
	// =========================================================================
	checkUpdate()

	// =========================================================================
	// 3. FIBER SERVER & CORE SYSTEM INITIALIZATION
	// =========================================================================
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		// Limit 500MB so large/non-standard manual upload files don't get truncated (corrupted)
		BodyLimit: 500 * 1024 * 1024,
	})

	// Mandatory PNA Middleware for P2P from HTTPS Website
	app.Use(func(c *fiber.Ctx) error {
		c.Set("Access-Control-Allow-Private-Network", "true")
		return c.Next()
	})

	// Explicitly add your Extension ID to the CORS Whitelist
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "http://localhost:5173, http://127.0.0.1:5173, https://floworkos.com, https://www.floworkos.com, chrome-extension://cbcamfbenpgekaihddgmnagdkcddbfim",
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization",
		AllowCredentials: true,
	}))

	os.MkdirAll("nodes", os.ModePerm)
	os.MkdirAll("apps", os.ModePerm)

	// Special endpoint for manual upload feature of .flow / .nflow files from UI
	app.Post("/api/upload", func(c *fiber.Ctx) error {
		fileHeader, err := c.FormFile("file") // var name changed for fileHeader stream
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"success": false, "error": "Failed to read uploaded file."})
		}

		fileType := c.FormValue("type") // "app" or "node"
		targetDir := "apps"
		ext := ".flow"
		if fileType == "node" {
			targetDir = "nodes"
			ext = ".nflow"
		}

		// Ensure folder exists before writing, following Install function structure
		if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
			log.Printf("[Engine] ❌ Failed to create directory %s: %v\n", targetDir, err)
			return c.Status(500).JSON(fiber.Map{"success": false, "error": "Failed to create folder in engine directory."})
		}

		// Sanitize file name and ensure matching extension
		cleanFileName := filepath.Base(fileHeader.Filename)
		if !strings.HasSuffix(cleanFileName, ext) {
			cleanFileName = cleanFileName + ext
		}
		savePath := filepath.Join(targetDir, cleanFileName)

		// Write to disk using Pure io.Copy Stream exact to Install module style
		srcFile, err := fileHeader.Open()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "error": "Failed to open upload file stream."})
		}
		defer srcFile.Close()

		outFile, err := os.Create(savePath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"success": false, "error": "Failed to save package to SSD/Hard drive."})
		}
		defer outFile.Close()

		if _, err = io.Copy(outFile, srcFile); err != nil {
			log.Printf("[Engine] ❌ File copy process interrupted %s: %v\n", savePath, err)
			return c.Status(500).JSON(fiber.Map{"success": false, "error": "Download process corrupted or interrupted."})
		}

		log.Printf("[Engine] ✅ Successfully received manual upload file: %s\n", savePath)
		return c.JSON(fiber.Map{"success": true, "message": "File successfully uploaded to Engine."})
	})

	// Special endpoint to serve application icon to Chrome Tab window
	app.Get("/favicon.ico", func(c *fiber.Ctx) error {
		iconData, err := embeddedFiles.ReadFile("icon.ico")
		if err != nil {
			return c.SendStatus(404)
		}
		c.Set("Content-Type", "image/x-icon")
		return c.Send(iconData)
	})

	// Use Filesystem Middleware so "/store" URL serves embedded files
	app.Use("/store", filesystem.New(filesystem.Config{
		Root:       http.FS(embeddedFiles),
		PathPrefix: "store",
		Browse:     false,
	}))

	// Special Endpoint: Streaming UI data directly from inside the .flow package (In-Memory)
	app.Get("/local-apps/:appName/*", func(c *fiber.Ctx) error {
		appName := c.Params("appName")
		assetPath := c.Params("*")

		assetPath, _ = url.PathUnescape(assetPath)

		flowPath := filepath.Join("apps", appName+".flow")
		if _, err := os.Stat(flowPath); os.IsNotExist(err) {
			return c.Status(404).SendString("Application package file not found.")
		}

		cipherText, err := os.ReadFile(flowPath)
		if err != nil {
			return c.Status(500).SendString("Failed to read application package.")
		}

		block, err := aes.NewCipher(packer.MasterKey)
		if err != nil {
			return c.Status(500).SendString("Cryptographic Cipher Error.")
		}

		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return c.Status(500).SendString("GCM Error.")
		}

		nonceSize := gcm.NonceSize()
		if len(cipherText) < nonceSize {
			return c.Status(500).SendString("Application package is corrupt or too short.")
		}

		nonce, cipherData := cipherText[:nonceSize], cipherText[nonceSize:]
		plainText, err := gcm.Open(nil, nonce, cipherData, nil)
		if err != nil {
			return c.Status(500).SendString("Decryption failed. Master Key does not match.")
		}

		zipReader, err := zip.NewReader(bytes.NewReader(plainText), int64(len(plainText)))
		if err != nil {
			return c.Status(500).SendString("Failed to read ZIP structure in memory.")
		}

		// Dynamic Entry Point Detection System
		if assetPath == "" || assetPath == "/" {
			defaultPopup := "index.html"

			for _, f := range zipReader.File {
				cleanName := strings.ReplaceAll(f.Name, "\\", "/")
				if cleanName == "manifest.json" {
					rc, err := f.Open()
					if err == nil {
						manifestBytes, err := io.ReadAll(rc)
						rc.Close()
						if err == nil {
							var manifest map[string]interface{}
							if json.Unmarshal(manifestBytes, &manifest) == nil {
								if action, ok := manifest["action"].(map[string]interface{}); ok {
									if dp, ok := action["default_popup"].(string); ok && dp != "" {
										defaultPopup = dp
									}
								}
							}
						}
					}
				}
				break
			}

			return c.Redirect("/local-apps/" + appName + "/" + strings.TrimPrefix(defaultPopup, "/"))
		}

		assetPath = strings.TrimPrefix(assetPath, "/")

		// Specific File Search in Memory
		for _, f := range zipReader.File {
			cleanName := strings.ReplaceAll(f.Name, "\\", "/")
			cleanName = strings.TrimPrefix(cleanName, "/")

			if cleanName == assetPath {
				rc, err := f.Open()
				if err != nil {
					return c.Status(500).SendString("Failed to open asset from package.")
				}
				defer rc.Close()

				assetData, err := io.ReadAll(rc)
				if err != nil {
					return c.Status(500).SendString("Failed to extract asset.")
				}

				mimeType := "application/octet-stream"
				ext := strings.ToLower(filepath.Ext(assetPath))

				switch ext {
				case ".html":
					mimeType = "text/html"
				case ".js":
					mimeType = "application/javascript"
				case ".css":
					mimeType = "text/css"
				case ".svg":
					mimeType = "image/svg+xml"
				case ".webp":
					mimeType = "image/webp"
				case ".png":
					mimeType = "image/png"
				case ".jpg", ".jpeg":
					mimeType = "image/jpeg"
				case ".gif":
					mimeType = "image/gif"
				case ".json":
					mimeType = "application/json"
				case ".md":
					mimeType = "text/markdown"
				case ".txt":
					mimeType = "text/plain"
				case ".ttf":
					mimeType = "font/ttf"
				case ".woff":
					mimeType = "font/woff"
				case ".woff2":
					mimeType = "font/woff2"
				case ".mp3":
					mimeType = "audio/mpeg"
				case ".wav":
					mimeType = "audio/wav"
				case ".ogg":
					mimeType = "audio/ogg"
				case ".wasm":
					mimeType = "application/wasm"
				}

				c.Set("Content-Type", mimeType)
				c.Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
				c.Set("Pragma", "no-cache")
				c.Set("Expires", "0")

				return c.Send(assetData)
			}
		}

		return c.Status(404).SendString("Asset '" + assetPath + "' not found inside the application package.")
	})

	nodeManager := runner.NewNodeManager("nodes")
	socketHandler := socket.NewSocketHandler(nodeManager)

	go watcher.StartNodeWatcher("nodes", socketHandler)
	go watcher.StartNodeWatcher("apps", socketHandler)

	app.All("/api/socket.io/*", socketHandler.FiberHandler())

	log.Println(`
     _____ _ OS
    |  ___| | _____      _____  _ __| | __
    | |_  | |/ _ \ \ /\ / / _ \| '__| |/ /
    |  _| | | (_) \ V  V / (_) | |  |   <
    |_|   |_|\___/ \_/\_/ \___/|_|  |_|\_\
                        www.floworkos.com
    `)
	log.Println("📡 Go Engine Standby & Listening at ws://127.0.0.1:5000/gui-socket")
	log.Println("🔒 Secure Local Apps endpoint is running. (In-Memory Streaming)")
	log.Println("🛒 App Store UI available at http://127.0.0.1:5000/store")

	go func() {
		time.Sleep(1 * time.Second)
		openAppWindow("http://127.0.0.1:5000/store/")
	}()

	// =========================================================================
	// 4. SWEEP ON APP CLOSE (Intercept OS Close Signal)
	// =========================================================================
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM) // Catch close signal (including X in cmd)
	go func() {
		<-c
		log.Println("\n[Engine] 🛑 Received system shutdown command.")
		log.Println("[Engine] 🧹 Performing cleanup of virtual sandbox folders...")
		packer.CleanupOldTempFolders() // Absolutely Executed Before Death
		log.Println("[Engine] ✅ System closed cleanly. Goodbye!")
		os.Exit(0)
	}()

	if err := app.Listen(":5000"); err != nil {
		log.Fatalf("Server failed to run: %v", err)
	}
}