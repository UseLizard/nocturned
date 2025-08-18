package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/usenocturne/nocturned/bluetooth"
	"github.com/usenocturne/nocturned/listeners"
	"github.com/usenocturne/nocturned/server"
	"github.com/usenocturne/nocturned/utils"
)

func main() {
	// Wait for filesystem to be ready before any file operations
	if err := waitForFilesystem(); err != nil {
		log.Printf("WARNING: %v - continuing with limited functionality", err)
	}

	// Set up file logging
	logDir := "/var/nocturne"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Warning: Could not create log directory %s: %v", logDir, err)
	}

	// TEMPORARILY DISABLED FILE LOGGING FOR PERFORMANCE TESTING
	if true {
		logFile, err := os.OpenFile(filepath.Join(logDir, "nocturned.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Printf("Warning: Could not open log file: %v", err)
			// Continue with stdout logging only
		} else {
			// Log to both file and stdout
			multiWriter := io.MultiWriter(os.Stdout, logFile)
			log.SetOutput(multiWriter)
			defer logFile.Close()
			log.Println("Logging to /var/nocturne/nocturned.log")
		}
	}
	log.Println("File logging disabled for performance testing")

	// Protect the process from supervisor interference
	// Create a new process group to isolate from supervisor signals
	if os.Getenv("UNDER_SUPERVISOR") != "" {
		log.Println("SUPERVISOR_DEBUG: Running under supervisor, setting up signal protection...")
		syscall.Setpgid(0, 0)
	}

	// Set up album art directory with tmpfs
	if err := setupAlbumArtDirectory(); err != nil {
		log.Printf("Failed to set up album art directory: %v", err)
	}

	// Optimize BLE connection parameters BEFORE any connections
	optimizeBLEParameters()

	wsHub := utils.NewWebSocketHub()
	go wsHub.Run()

	// Retry bluetooth manager initialization with increased attempts
	var btManager *bluetooth.BluetoothManager
	var err error
	for retries := 0; retries < 10; retries++ {
		btManager, err = bluetooth.NewBluetoothManager(wsHub)
		if err == nil {
			log.Printf("Bluetooth manager initialized successfully on attempt %d", retries+1)
			break
		}
		log.Printf("Failed to initialize bluetooth manager (attempt %d/10): %v", retries+1, err)
		if retries < 9 {
			time.Sleep(3 * time.Second) // Wait 3 seconds between retries
		}
	}

	if btManager == nil {
		log.Printf("ERROR: Could not initialize bluetooth manager after 10 attempts - continuing without BLE")
		// Continue running without bluetooth rather than crashing
	}

	// Set the album art callback to store in memory immediately
	if btManager != nil {
		if bleClient := btManager.GetBleClient(); bleClient != nil {
			bleClient.SetAlbumArtCallback(utils.SetCurrentAlbumArt)
		}
	}

	if err := utils.InitBrightness(); err != nil {
		log.Printf("Failed to initialize brightness: %v", err)
	}

	go listeners.NetworkChecker(wsHub)

	s := server.NewServer(btManager, wsHub)
	s.Start()
}

// waitForFilesystem waits for filesystem to be ready
func waitForFilesystem() error {
	maxRetries := 5 // Reduced from 30 to 5 seconds
	testFile := "/var/.nocturne_test"

	for i := 0; i < maxRetries; i++ {
		// Try to write a test file
		if err := os.WriteFile(testFile, []byte("test"), 0644); err == nil {
			// Clean up test file
			os.Remove(testFile)
			if i > 0 {
				log.Printf("Filesystem ready after %d seconds", i)
			}
			return nil
		}

		if i == 0 {
			log.Printf("Waiting for filesystem to be ready...")
		}
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("filesystem not ready after %d seconds", maxRetries)
}

// setupAlbumArtDirectory creates the album art directory in /var which is writable
// disconnectExistingBLEConnections disconnects any active BLE connections
func disconnectExistingBLEConnections() {
	log.Println("Checking for existing BLE connections...")

	// Use bluetoothctl to disconnect any connected devices
	cmd := exec.Command("bluetoothctl", "disconnect")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// This is okay - it just means no device was connected
		log.Printf("No active BLE connections to disconnect")
	} else {
		log.Printf("Disconnected existing BLE connection: %s", strings.TrimSpace(string(output)))
		// Wait a moment for disconnect to complete
		time.Sleep(500 * time.Millisecond)
	}
}

// optimizeBLEParameters sets optimal BLE connection parameters before any connections
func optimizeBLEParameters() {
	log.Println("Setting optimal BLE connection parameters...")

	debugPath := "/sys/kernel/debug/bluetooth/hci0"

	// Check if debugfs is available
	if _, err := os.Stat(debugPath); os.IsNotExist(err) {
		log.Println("Debug filesystem not available, trying to mount...")
		// Try to mount debugfs
		if err := syscall.Mount("none", "/sys/kernel/debug", "debugfs", 0, ""); err != nil {
			log.Printf("Failed to mount debugfs: %v", err)
		}
	}

	// First, disconnect any existing BLE connections
	// This ensures new connections will use the optimized parameters
	disconnectExistingBLEConnections()

	// Optimal parameters for low latency
	params := map[string]string{
		"conn_min_interval":     "6",   // 7.5ms
		"conn_max_interval":     "12",  // 15ms
		"conn_latency":          "0",   // No slave latency
		"supervision_timeout": "500", // 5 seconds
	}

	successCount := 0
	for param, value := range params {
		path := fmt.Sprintf("%s/%s", debugPath, param)
		if err := os.WriteFile(path, []byte(value+"\n"), 0644); err != nil {
			log.Printf("Cannot set %s: %v", param, err)
		} else {
			log.Printf("Set %s to %s", param, value)
			successCount++
		}
	}

	if successCount > 0 {
		log.Printf("Successfully optimized %d BLE parameters for fast album art transfers", successCount)
		log.Println("Connection intervals: 7.5-15ms (vs default 50-70ms)")
	} else {
		log.Println("Could not optimize BLE parameters - transfers may be slower")
	}
}

func setupAlbumArtDirectory() error {
	albumArtDir := "/var/nocturne/albumart"

	// Retry directory creation with minimal delay
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = os.MkdirAll(albumArtDir, 0755); err == nil {
			break
		}

		log.Printf("Failed to create album art directory (attempt %d/3): %v", attempt+1, err)
		if attempt < 2 {
			time.Sleep(500 * time.Millisecond) // Quick retry
		}
	}

	if err != nil {
		// Don't fail hard - album art is not critical
		log.Printf("WARNING: Could not create album art directory: %v - album art will be disabled", err)
		return nil
	}

	// Count existing album art files
	files, err := os.ReadDir(albumArtDir)
	if err == nil {
		count := 0
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".webp") {
				count++
			}
		}
		if count > 0 {
			log.Printf("Found %d existing album art files in %s", count, albumArtDir)
		}
	}

	log.Printf("Album art directory ready at: %s (persistent storage on /var)", albumArtDir)
	return nil
}
