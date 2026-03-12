package watcher

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"flowork-engine/internal/packer" // Import our security module
	"github.com/fsnotify/fsnotify"
)

// [ADDED] Variabel Global untuk menampung status Dev Mode dari GUI
var IsDevMode bool = false

type SocketNotifier interface {
	BroadcastRefresh()
}

func installRequirements(folderPath string) {
	reqFile := filepath.Join(folderPath, "requirements.txt")
	libsFolder := filepath.Join(folderPath, "libs")

	if _, err := os.Stat(reqFile); err == nil {
		os.MkdirAll(libsFolder, os.ModePerm)

		folderName := filepath.Base(folderPath)
		log.Printf("\n[📦 Installer] Detected requirements.txt in: %s\n", folderName)
		log.Printf("[📦 Installer] Starting installation to libs folder...\n")

		pythonCmd := "python"
		if runtime.GOOS != "windows" {
			pythonCmd = "python3"
		}

		cmd := exec.Command(pythonCmd, "-m", "pip", "install", "-U", "-t", libsFolder, "-r", reqFile, "--quiet", "--no-dependencies")
		cmd.Run()
		cmd2 := exec.Command(pythonCmd, "-m", "pip", "install", "-U", "-t", libsFolder, "-r", reqFile, "--quiet")
		if err := cmd2.Run(); err != nil {
			log.Printf("[📦 Installer] ❌ Installation warning (Can be ignored): %v\n", err)
		} else {
			log.Printf("[📦 Installer] ✅ Installation complete for: %s\n", folderName)
		}
	}
}

// New function to handle dynamic folder packing
func processRawFolder(path string, isNode bool, notifier SocketNotifier) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}

	folderName := filepath.Base(path)

	// Ignore libs folder and protected folders (starting with a dot)
	if folderName == "libs" || strings.HasPrefix(folderName, ".") {
		return
	}

	// [ADDED] Logic Bypass untuk Developer Mode
	if IsDevMode {
		log.Printf("[🛠️ Dev Mode] Mengabaikan enkripsi & karantina untuk folder mentah: %s", folderName)
		// Tetap trigger refresh agar UI tahu ada folder mentah yang bisa dieksekusi langsung
		if notifier != nil {
			notifier.BroadcastRefresh()
		}
		return // Keluar dari fungsi, JANGAN lakukan enkripsi ke bawah!
	}

	ext := ".flow"
	if isNode {
		ext = ".nflow"
	}

	outputFile := path + ext

	log.Printf("[🔒 Security] Detected raw folder '%s'. Encrypting to %s format...", folderName, ext)

	// 1. Pack and Encrypt
	err = packer.EncryptAndPack(path, outputFile)
	if err != nil {
		log.Printf("[🔒 Security] ❌ Failed to pack %s: %v", folderName, err)
		return
	}

	// 2. Remove raw traces (rename to hide/zombify)
	// We do not delete permanently yet, just in case the developer needs to edit again
	hiddenPath := filepath.Join(filepath.Dir(path), ".raw_"+folderName)
	os.Rename(path, hiddenPath)

	log.Printf("[🔒 Security] ✅ Success! %s has been encrypted. Raw folder secured as %s", outputFile, filepath.Base(hiddenPath))

	if notifier != nil {
		notifier.BroadcastRefresh()
	}
}

func StartNodeWatcher(baseDir string, notifier SocketNotifier) {
	// Initial setup: Check for raw folders when the Engine starts
	entries, _ := os.ReadDir(baseDir)
	isNodeDir := strings.Contains(baseDir, "nodes")

	for _, entry := range entries {
		// Only process raw folders that do not start with "."
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			processRawFolder(filepath.Join(baseDir, entry.Name()), isNodeDir, notifier)
		}
	}

	// Start live watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	err = watcher.Add(baseDir)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("👀 Watchdog actively monitoring folder: %s\n", baseDir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				// Trigger encryption process when a new folder is copied
				processRawFolder(event.Name, isNodeDir, notifier)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Println("error:", err)
		}
	}
}