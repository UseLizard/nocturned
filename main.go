package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vishvananda/netlink"

	ping "github.com/prometheus-community/pro-bing"
	"github.com/usenocturne/nocturned/bluetooth"
	"github.com/usenocturne/nocturned/utils"
)

type InfoResponse struct {
	Version string `json:"version"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type NetworkStatusResponse struct {
	Status string `json:"status"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

func networkChecker(hub *utils.WebSocketHub) {
	const (
		host          = "1.1.1.1"
		interval      = 1 // seconds
		failThreshold = 3
	)

	failCount := 0

	isOnline := false
	pinger, err := ping.NewPinger(host)
	if err == nil {
		pinger.Count = 1
		pinger.Timeout = 1 * time.Second
		pinger.Interval = 1 * time.Second
		pinger.SetPrivileged(true)
		err = pinger.Run()
		if err == nil && pinger.Statistics().PacketsRecv > 0 {
			currentNetworkStatus = "online"
			hub.Broadcast(utils.WebSocketEvent{
				Type:    "network_status",
				Payload: map[string]string{"status": "online"},
			})
			isOnline = true
		} else {
			currentNetworkStatus = "offline"
			hub.Broadcast(utils.WebSocketEvent{
				Type:    "network_status",
				Payload: map[string]string{"status": "offline"},
			})
		}
	} else {
		hub.Broadcast(utils.WebSocketEvent{
			Type:    "network_status",
			Payload: map[string]string{"status": "offline"},
		})
	}

	for {
		pinger, err := ping.NewPinger(host)
		if err != nil {
			log.Printf("Failed to create pinger: %v", err)
			failCount++
		} else {
			pinger.Count = 1
			pinger.Timeout = 1 * time.Second
			pinger.Interval = 1 * time.Second
			pinger.SetPrivileged(true)
			err = pinger.Run()
			if err != nil || pinger.Statistics().PacketsRecv == 0 {
				failCount++
			} else {
				failCount = 0
				if !isOnline {
					currentNetworkStatus = "online"
					hub.Broadcast(utils.WebSocketEvent{
						Type:    "network_status",
						Payload: map[string]string{"status": "online"},
					})
					isOnline = true
				}
			}
		}

		if failCount >= failThreshold && isOnline {
			currentNetworkStatus = "offline"
			hub.Broadcast(utils.WebSocketEvent{
				Type:    "network_status",
				Payload: map[string]string{"status": "offline"},
			})
			isOnline = false
		}

		time.Sleep(interval * time.Second)
	}
}

var currentNetworkStatus = "offline"

// Global in-memory album art storage
var (
	currentAlbumArt      []byte
	currentAlbumArtMutex sync.RWMutex
)

// SetCurrentAlbumArt updates the global in-memory album art
func SetCurrentAlbumArt(data []byte) {
	currentAlbumArtMutex.Lock()
	// Make a copy to avoid external modifications
	if data != nil {
		currentAlbumArt = make([]byte, len(data))
		copy(currentAlbumArt, data)
	} else {
		currentAlbumArt = nil
	}
	currentAlbumArtMutex.Unlock()
}

// GetCurrentAlbumArt returns the current in-memory album art
func GetCurrentAlbumArt() []byte {
	currentAlbumArtMutex.RLock()
	defer currentAlbumArtMutex.RUnlock()
	// Return the actual data - the HTTP handler will write it directly
	// This avoids an unnecessary copy
	return currentAlbumArt
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
		"conn_min_interval": "6",    // 7.5ms
		"conn_max_interval": "12",   // 15ms
		"conn_latency": "0",         // No slave latency
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
			bleClient.SetAlbumArtCallback(SetCurrentAlbumArt)
		}
	}

	if err := utils.InitBrightness(); err != nil {
		log.Printf("Failed to initialize brightness: %v", err)
	}

	broadcastProgress := func(progress utils.ProgressMessage) {
		wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "update_progress",
			Payload: progress,
		})
	}

	broadcastCompletion := func(completion utils.CompletionMessage) {
		wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "update_completion",
			Payload: completion,
		})
	}

	// WebSockets
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Failed to upgrade connection: %v", err)
			return
		}
		wsHub.AddClient(conn)
	})

	// GET /info
	http.HandleFunc("/info", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		content, err := os.ReadFile("/etc/nocturne/version.txt")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Error reading version file"})
			return
		}
		version := strings.TrimSpace(string(content))

		response := InfoResponse{
			Version: version,
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response"})
			return
		}
	}))

	// POST /bluetooth/discover/on
	// Discoverable mode endpoints removed - nocturned is a BLE central/client only
	// It scans for and connects to peripherals, it doesn't need to advertise

	// POST /bluetooth/pairing/accept
	http.HandleFunc("/bluetooth/pairing/accept", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.AcceptPairing(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to accept pairing"})
			return
		}

		w.WriteHeader(http.StatusOK)
	}))

	// POST /bluetooth/pairing/deny
	http.HandleFunc("/bluetooth/pairing/deny", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.DenyPairing(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to deny pairing"})
			return
		}

		w.WriteHeader(http.StatusOK)
	}))

	// GET /bluetooth/info/{address}
	http.HandleFunc("/bluetooth/info/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		address := strings.TrimPrefix(r.URL.Path, "/bluetooth/info/")
		if address == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth address is required"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		info, err := btManager.GetDeviceInfo(address)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to get device info: " + err.Error()})
			return
		}

		if err := json.NewEncoder(w).Encode(info); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response: " + err.Error()})
			return
		}
	}))

	// POST /bluetooth/remove/{address}
	http.HandleFunc("/bluetooth/remove/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		address := strings.TrimPrefix(r.URL.Path, "/bluetooth/remove/")
		if address == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth address is required"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.RemoveDevice(address); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to remove device: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
	}))

	// POST /bluetooth/connect/{address}
	http.HandleFunc("/bluetooth/connect/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		address := strings.TrimPrefix(r.URL.Path, "/bluetooth/connect/")
		if address == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth address is required"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.ConnectDevice(address); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to connect to device: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /bluetooth/disconnect/{address}
	http.HandleFunc("/bluetooth/disconnect/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		address := strings.TrimPrefix(r.URL.Path, "/bluetooth/disconnect/")
		if address == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth address is required"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.DisconnectDevice(address); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to disconnect device: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// GET /bluetooth/network
	http.HandleFunc("/bluetooth/network", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		link, err := netlink.LinkByName("bnep0")
		if err != nil || link.Attrs().Flags&net.FlagUp == 0 {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "down"})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "up"})
	}))

	// POST /bluetooth/network/{address}
	http.HandleFunc("/bluetooth/network/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		address := strings.TrimPrefix(r.URL.Path, "/bluetooth/network/")
		if address == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth address is required"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.ConnectNetwork(address); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to connect to Bluetooth network: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// GET /bluetooth/devices
	http.HandleFunc("/bluetooth/devices", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		devices, err := btManager.GetDevices()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to get devices: " + err.Error()})
			return
		}

		if devices == nil {
			devices = []utils.BluetoothDeviceInfo{}
		}

		if err := json.NewEncoder(w).Encode(devices); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response: " + err.Error()})
			return
		}
	}))

	// GET /device/brightness
	http.HandleFunc("/device/brightness", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		brightness, err := utils.GetBrightness()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to get brightness: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]int{"brightness": brightness})
	}))

	// POST /device/brightness/{value}
	http.HandleFunc("/device/brightness/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		valueStr := strings.TrimPrefix(r.URL.Path, "/device/brightness/")
		value, err := strconv.Atoi(valueStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid brightness value"})
			return
		}

		if err := utils.SetBrightness(value); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to set brightness: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /device/resetcounter
	http.HandleFunc("/device/resetcounter", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if err := utils.ResetCounter(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /device/factoryreset
	http.HandleFunc("/device/factoryreset", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if err := utils.FactoryReset(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /device/power/shutdown
	http.HandleFunc("/device/power/shutdown", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if err := utils.Shutdown(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /device/power/reboot
	http.HandleFunc("/device/power/reboot", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if err := utils.Reboot(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /device/date/settimezone
	http.HandleFunc("/device/date/settimezone", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		resp, err := http.Get("https://api.usenocturne.com/v1/timezone")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to fetch timezone: " + err.Error()})
			return
		}
		defer resp.Body.Close()

		var requestData struct {
			Timezone string `json:"timezone"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&requestData); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to decode timezone response: " + err.Error()})
			return
		}

		if err := utils.SetTimezone(requestData.Timezone); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success", "timezone": requestData.Timezone})
	}))

	// GET /device/date
	http.HandleFunc("/device/date", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		t := time.Now()

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"time": t.Format(time.TimeOnly), "date": t.Format(time.DateOnly)})
	}))

	// POST /update
	http.HandleFunc("/update", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		var requestData utils.UpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid request body: " + err.Error()})
			return
		}

		status := utils.GetUpdateStatus()
		if status.InProgress {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Update already in progress"})
			return
		}

		go func() {
			utils.SetUpdateStatus(true, "download", "")

			tempDir, err := os.MkdirTemp("/data/tmp", "update-*")
			if err != nil {
				utils.SetUpdateStatus(false, "", fmt.Sprintf("Failed to create temp directory: %v", err))
				return
			}
			defer os.RemoveAll(tempDir)

			imgPath := filepath.Join(tempDir, "update.img.gz")
			imgResp, err := http.Get(requestData.ImageURL)
			if err != nil {
				utils.SetUpdateStatus(false, "", fmt.Sprintf("Failed to download image: %v", err))
				return
			}
			defer imgResp.Body.Close()

			imgFile, err := os.Create(imgPath)
			if err != nil {
				utils.SetUpdateStatus(false, "", fmt.Sprintf("Failed to create image file: %v", err))
				return
			}
			defer imgFile.Close()

			contentLength := imgResp.ContentLength
			progressReader := utils.NewProgressReader(imgResp.Body, contentLength, func(complete, total int64, speed float64) {
				percent := float64(complete) / float64(total) * 100
				broadcastProgress(utils.ProgressMessage{
					Type:          "progress",
					Stage:         "download",
					BytesComplete: complete,
					BytesTotal:    total,
					Speed:         float64(int(speed*10)) / 10,
					Percent:       float64(int(percent*10)) / 10,
				})
			})

			if _, err := io.Copy(imgFile, progressReader); err != nil {
				utils.SetUpdateStatus(false, "", fmt.Sprintf("Failed to save image file: %v", err))
				return
			}

			sumResp, err := http.Get(requestData.SumURL)
			if err != nil {
				utils.SetUpdateStatus(false, "", fmt.Sprintf("Failed to download checksum: %v", err))
				return
			}
			defer sumResp.Body.Close()

			sumBytes, err := io.ReadAll(sumResp.Body)
			if err != nil {
				utils.SetUpdateStatus(false, "", fmt.Sprintf("Failed to read checksum: %v", err))
				return
			}

			sumParts := strings.Fields(string(sumBytes))
			if len(sumParts) != 2 {
				utils.SetUpdateStatus(false, "", "Invalid checksum format")
				return
			}
			sum := sumParts[0]

			utils.SetUpdateStatus(true, "flash", "")
			if err := utils.UpdateSystem(imgPath, sum, broadcastProgress); err != nil {
				utils.SetUpdateStatus(false, "", fmt.Sprintf("Failed to update system: %v", err))
				broadcastCompletion(utils.CompletionMessage{
					Type:    "completion",
					Stage:   "flash",
					Success: false,
					Error:   fmt.Sprintf("Failed to update system: %v", err),
				})
				return
			}

			utils.SetUpdateStatus(false, "", "")
			broadcastCompletion(utils.CompletionMessage{
				Type:    "completion",
				Stage:   "flash",
				Success: true,
			})
		}()

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(utils.OKResponse{Status: "success"}); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to encode JSON: " + err.Error()})
			return
		}
	}))

	// GET /update/status
	http.HandleFunc("/update/status", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if err := json.NewEncoder(w).Encode(utils.GetUpdateStatus()); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to encode JSON: " + err.Error()})
			return
		}
	}))

	// POST /fetchjson
	http.HandleFunc("/fetchjson", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing or invalid url"})
			return
		}

		client := &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 { // 10 redirects, may change?
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
		resp, err := client.Get(req.URL)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to fetch remote JSON: " + err.Error()})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Remote server returned status: " + resp.Status})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := io.Copy(w, resp.Body); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to write JSON: " + err.Error()})
			return
		}
	}))

	go networkChecker(wsHub)

	// GET /media/status
	http.HandleFunc("/media/status", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"connected": false, "state": nil})
			return
		}
		state := btManager.GetMediaState()
		connected := btManager.IsMediaConnected()

		response := map[string]interface{}{
			"connected": connected,
			"state":     state,
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response"})
			return
		}
	}))

	// POST /media/play
	http.HandleFunc("/media/play", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.SendMediaCommand("play", nil, nil); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send play command: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /media/pause
	http.HandleFunc("/media/pause", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.SendMediaCommand("pause", nil, nil); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send pause command: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /media/next
	http.HandleFunc("/media/next", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.SendMediaCommand("next", nil, nil); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send next command: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /media/previous
	http.HandleFunc("/media/previous", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.SendMediaCommand("previous", nil, nil); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send previous command: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /media/seek/{position_ms}
	http.HandleFunc("/media/seek/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		positionStr := strings.TrimPrefix(r.URL.Path, "/media/seek/")
		position, err := strconv.Atoi(positionStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid position value"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		if err := btManager.SendMediaCommand("seek_to", &position, nil); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send seek command: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /media/volume/{percent}
	http.HandleFunc("/media/volume/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		volumeStr := strings.TrimPrefix(r.URL.Path, "/media/volume/")
		volume, err := strconv.Atoi(volumeStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid volume value"})
			return
		}

		if volume < 0 || volume > 100 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Volume must be between 0 and 100"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		// Update local state immediately for instant UI feedback
		btManager.UpdateLocalVolume(volume)
		
		// Broadcast the updated state via WebSocket
		if state := btManager.GetMediaState(); state != nil {
			wsHub.Broadcast(utils.WebSocketEvent{
				Type:    "media/state_update",
				Payload: state,
			})
		}
		
		// Send volume command to Android asynchronously
		go func() {
			if err := btManager.SendMediaCommand("set_volume", nil, &volume); err != nil {
				log.Printf("Failed to send volume command: %v", err)
			}
		}()

		// Return success immediately for responsive UI
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /media/albumart - receive album art from companion app
	http.HandleFunc("/media/albumart/query", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		var req struct {
			TrackID  string `json:"track_id"`
			Checksum string `json:"checksum"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid request body"})
			return
		}

		// Check if we already have this album art
		albumArtDir := "/var/nocturne/albumart"
		filePath := filepath.Join(albumArtDir, req.Checksum + ".webp")

		if _, err := os.Stat(filePath); err == nil {
			// We already have it, send an ACK
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "exists"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		// We don't have it, send a NACK to request the full data
		if err := btManager.RequestAlbumArt(req.TrackID, req.Checksum); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to request album art: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "needed"})
	}))


	// POST /media/albumart - receive album art from companion app
	http.HandleFunc("/media/albumart", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Parse multipart form to handle file upload
		err := r.ParseMultipartForm(10 << 20) // 10 MB limit
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to parse multipart form: " + err.Error()})
			return
		}

		file, header, err := r.FormFile("albumart")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Album art file not found: " + err.Error()})
			return
		}
		defer file.Close()

		// Validate file type
		if !strings.HasPrefix(header.Header.Get("Content-Type"), "image/") {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "File must be an image"})
			return
		}

		// Read file content for hashing
		fileContent, err := io.ReadAll(file)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to read file content: " + err.Error()})
			return
		}

		// Calculate SHA-256 hash
		hash := sha256.Sum256(fileContent)
		hashStr := hex.EncodeToString(hash[:])

		// Create album art directory if it doesn't exist
		albumArtDir := "/var/nocturne/albumart"
		if err := os.MkdirAll(albumArtDir, 0755); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to create album art directory: " + err.Error()})
			return
		}

		// Check if file with this hash already exists
		extension := filepath.Ext(header.Filename)
		if extension == "" {
			// Default to .jpg if no extension
			extension = ".jpg"
		}
		filename := fmt.Sprintf("%s%s", hashStr, extension)
		filePath := filepath.Join(albumArtDir, filename)

		// Check if file already exists
		if _, err := os.Stat(filePath); err == nil {
			// File already exists, return existing file info
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":       "exists",
				"message":      "Album art already exists",
				"filename":     filename,
				"path":         filePath,
				"hash":         hashStr,
				"skip_reason":  "duplicate",
			})
			return
		}

		// Create the file
		dst, err := os.Create(filePath)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to create album art file: " + err.Error()})
			return
		}
		defer dst.Close()

		// Write the file content
		if _, err := dst.Write(fileContent); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to save album art: " + err.Error()})
			return
		}

		// Broadcast album art update via WebSocket
		wsHub.Broadcast(utils.WebSocketEvent{
			Type: "album_art_updated",
			Payload: map[string]interface{}{
				"filename": filename,
				"path":     filePath,
				"hash":     hashStr,
				"size":     len(fileContent),
			},
		})

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "uploaded",
			"filename": filename,
			"path":     filePath,
			"hash":     hashStr,
			"size":     len(fileContent),
		})
	}))

	// POST /media/simulate - for testing purposes
	http.HandleFunc("/media/simulate", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		var req struct {
			Artist    string `json:"artist"`
			Track     string `json:"track"`
			IsPlaying bool   `json:"is_playing"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid request body"})
			return
		}

		// Simulation is no longer supported with BLE-only implementation
		log.Println("Media simulation endpoint called - not supported in BLE-only mode")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// GET /api/albumart - serve current album art or list gallery
	http.HandleFunc("/api/albumart", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Check if client wants JSON (gallery list)
		if r.Header.Get("Accept") == "application/json" {
			// Return list of album art files
			galleryDir := "/var/nocturne/albumart"
			
			type AlbumArtItem struct {
				Filename string `json:"filename"`
				Artist   string `json:"artist"`
				Album    string `json:"album"`
				Added    string `json:"added"`
			}
			
			response := struct {
				Files []AlbumArtItem `json:"files"`
				Count int           `json:"count"`
			}{
				Files: []AlbumArtItem{},
			}
			
			// Read directory
			if entries, err := os.ReadDir(galleryDir); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".jpg") || strings.HasSuffix(entry.Name(), ".webp")) {
						item := AlbumArtItem{
							Filename: entry.Name(),
						}
						
						// Try to read metadata
						// Handle both .jpg and .webp files
						baseName := entry.Name()
						if strings.HasSuffix(baseName, ".jpg") {
							baseName = strings.TrimSuffix(baseName, ".jpg")
						} else if strings.HasSuffix(baseName, ".webp") {
							baseName = strings.TrimSuffix(baseName, ".webp")
						}
						metadataFile := filepath.Join(galleryDir, baseName + ".json")
						if data, err := os.ReadFile(metadataFile); err == nil {
							var metadata map[string]interface{}
							if json.Unmarshal(data, &metadata) == nil {
								if artist, ok := metadata["artist"].(string); ok {
									item.Artist = artist
								}
								if album, ok := metadata["album"].(string); ok {
									item.Album = album
								}
								if added, ok := metadata["added"].(string); ok {
									item.Added = added
								}
							}
						}
						
						response.Files = append(response.Files, item)
					}
				}
			}
			
			response.Count = len(response.Files)
			
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		// Log when album art is requested
		log.Printf("Album art requested at %s", time.Now().Format("15:04:05.000"))
		
		// First check if we have album art in memory
		data := GetCurrentAlbumArt()
		
		if data == nil {
			log.Printf("No album art in memory, checking disk...")
			// Fall back to reading from disk
			albumArtPath := "/tmp/album_art.jpg"
			
			// Check if file exists (no retry needed with synchronous processing)
			_, statErr := os.Stat(albumArtPath)
			
			if os.IsNotExist(statErr) {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(ErrorResponse{Error: "Album art not found"})
				return
			}
			
			// Read the file (no retry needed with synchronous processing)
			var readErr error
			data, readErr = os.ReadFile(albumArtPath)
			
			if readErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to read album art"})
				return
			}
		} else {
			log.Printf("Serving album art from memory (%d bytes)", len(data))
		}
		
		// Detect image format from data
		contentType := detectImageFormat(data)
		
		// Set content type and cache headers
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		
		// Write the image data
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}))

	// GET /api/albumart/{filename} - serve specific album art from gallery
	http.HandleFunc("/api/albumart/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Extract filename from URL path
		filename := strings.TrimPrefix(r.URL.Path, "/api/albumart/")
		
		// Validate filename (prevent path traversal)
		if filename == "" || strings.Contains(filename, "..") || strings.Contains(filename, "/") {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid filename"})
			return
		}
		
		// Construct full path
		albumArtPath := filepath.Join("/var/nocturne/albumart", filename)
		
		// Check if file exists
		if _, err := os.Stat(albumArtPath); os.IsNotExist(err) {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Album art not found"})
			return
		}
		
		// Read the file
		data, err := os.ReadFile(albumArtPath)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to read album art"})
			return
		}
		
		// Set content type and cache headers (allow caching for gallery items)
		w.Header().Set("Content-Type", "image/webp")
		w.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours
		
		// Write the image data
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}))

	// POST /api/test/album-art/request - test endpoint to request album art for testing
	http.HandleFunc("/api/test/album-art/request", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		bleClient := btManager.GetBleClient()
		if bleClient == nil || !bleClient.IsConnected() {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE not connected"})
			return
		}

		// Send test album art request command
		err := bleClient.SendTestAlbumArtRequest()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send test album art request: " + err.Error()})
			return
		}

		// Notify WebSocket clients that test transfer is starting
		wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "test/album_art_requested",
			Payload: map[string]interface{}{
				"timestamp": time.Now().UnixMilli(),
			},
		})

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "requested"})
	}))

	// GET /api/test/album-art/status - get test transfer status
	http.HandleFunc("/api/test/album-art/status", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		bleClient := btManager.GetBleClient()
		if bleClient == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE not initialized"})
			return
		}

		status := bleClient.GetTestTransferStatus()
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(status)
	}))

	// GET /api/test/album-art/image - serve test album art
	http.HandleFunc("/api/test/album-art/image", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Log when test album art is requested
		log.Printf("Test album art requested at %s", time.Now().Format("15:04:05.000"))
		
		// Check test album art location
		testAlbumArtPath := "/tmp/test_album_art.jpg"
		
		// Check if file exists
		_, statErr := os.Stat(testAlbumArtPath)
		
		if os.IsNotExist(statErr) {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Test album art not found"})
			return
		}
		
		// Read the file
		data, readErr := os.ReadFile(testAlbumArtPath)
		
		if readErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to read test album art"})
			return
		}
		
		log.Printf("Serving test album art (%d bytes)", len(data))
		
		// Detect image format from data
		contentType := detectImageFormat(data)
		
		// Set content type and cache headers
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		
		// Write the image data
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}))

	// POST /api/test/bt-speed/start - start BT speed test
	http.HandleFunc("/api/test/bt-speed/start", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}

		bleClient := btManager.GetBleClient()
		if bleClient == nil || !bleClient.IsConnected() {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE not connected"})
			return
		}

		// Send speed test start command to Android companion
		err := bleClient.SendSpeedTestStart()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: fmt.Sprintf("Failed to start speed test: %v", err)})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "started"})
	}))

	// GET /media/ble/status
	http.HandleFunc("/media/ble/status", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		bleClient := btManager.GetBleClient()
		if bleClient == nil {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"connected": false,
				"status": "ble_client_not_initialized",
			})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": bleClient.IsConnected(),
			"status": func() string {
				if bleClient.IsConnected() {
					return "connected"
				}
				return "disconnected"
			}(),
		})
	}))

	// POST /media/ble/connect
	http.HandleFunc("/media/ble/connect", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		bleClient := btManager.GetBleClient()
		if bleClient == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE client not initialized"})
			return
		}

		if bleClient.IsConnected() {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "already_connected"})
			return
		}

		// Trigger a new connection attempt in a goroutine
		go func() {
			log.Println("Manual BLE connection attempt triggered via API")
			if err := bleClient.DiscoverAndConnect(); err != nil {
				log.Printf("Manual BLE connection failed: %v", err)
			}
		}()

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "connecting"})
	}))

	// POST /media/ble/disconnect
	http.HandleFunc("/media/ble/disconnect", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		bleClient := btManager.GetBleClient()
		if bleClient == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE client not initialized"})
			return
		}

		if !bleClient.IsConnected() {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "already_disconnected"})
			return
		}

		bleClient.Disconnect()

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "disconnected"})
	}))

	// GET /network/status
	http.HandleFunc("/network/status", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		response := NetworkStatusResponse{
			Status: currentNetworkStatus,
		}
		json.NewEncoder(w).Encode(response)
	}))

	// GET /bluetooth/profiles/{address}
	http.HandleFunc("/bluetooth/profiles/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		address := strings.TrimPrefix(r.URL.Path, "/bluetooth/profiles/")
		if address == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth address is required"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		profileManager := btManager.GetProfileManager()
		if profileManager == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Profile manager not available"})
			return
		}

		config := profileManager.GetProfileConfiguration(address)
		response := utils.ProfileStatusResponse{
			DeviceAddress: address,
			Profiles:      config.Profiles,
			LastUpdated:   time.Now().Unix(),
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response"})
			return
		}
	}))

	// POST /bluetooth/profiles/update
	http.HandleFunc("/bluetooth/profiles/update", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		var req utils.ProfileUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid request body: " + err.Error()})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		profileManager := btManager.GetProfileManager()
		if profileManager == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Profile manager not available"})
			return
		}

		if err := profileManager.UpdateProfile(req); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to update profile: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /bluetooth/profiles/connect
	http.HandleFunc("/bluetooth/profiles/connect", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		var req utils.ProfileConnectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid request body: " + err.Error()})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		profileManager := btManager.GetProfileManager()
		if profileManager == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Profile manager not available"})
			return
		}

		if err := profileManager.ConnectProfile(req); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to connect profile: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /bluetooth/profiles/disconnect/{address}/{uuid}
	http.HandleFunc("/bluetooth/profiles/disconnect/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/bluetooth/profiles/disconnect/"), "/")
		if len(pathParts) != 2 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Address and profile UUID are required"})
			return
		}

		address := pathParts[0]
		profileUUID := pathParts[1]

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		profileManager := btManager.GetProfileManager()
		if profileManager == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Profile manager not available"})
			return
		}

		if err := profileManager.DisconnectProfile(address, profileUUID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to disconnect profile: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// GET /bluetooth/profiles/detect/{address}
	http.HandleFunc("/bluetooth/profiles/detect/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		address := strings.TrimPrefix(r.URL.Path, "/bluetooth/profiles/detect/")
		if address == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth address is required"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		profileManager := btManager.GetProfileManager()
		if profileManager == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Profile manager not available"})
			return
		}

		supportedProfiles, err := profileManager.DetectDeviceProfiles(address)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to detect profiles: " + err.Error()})
			return
		}

		response := map[string]interface{}{
			"device_address":      address,
			"supported_profiles":  supportedProfiles,
			"profile_count":       len(supportedProfiles),
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response"})
			return
		}
	}))

	// GET /bluetooth/profiles/logs
	http.HandleFunc("/bluetooth/profiles/logs", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		limit := 100 // Default limit
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
				limit = parsedLimit
			}
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}
		profileManager := btManager.GetProfileManager()
		if profileManager == nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Profile manager not available"})
			return
		}

		logs := profileManager.GetProfileLogs(limit)
		response := map[string]interface{}{
			"logs":  logs,
			"count": len(logs),
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response"})
			return
		}
	}))

	// POST /api/bluetooth/disconnect - Disconnect BLE for reconnection
	http.HandleFunc("/api/bluetooth/disconnect", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Disconnect BLE
		if btManager != nil {
			if bleClient := btManager.GetBleClient(); bleClient != nil {
				bleClient.Disconnect()
				log.Println("BLE disconnected for reconnection")
			}
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "disconnected"})
	}))

	// POST /api/time/sync - Request time sync from companion
	http.HandleFunc("/api/time/sync", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Send time sync request via BLE
		if bleClient := btManager.GetBleClient(); bleClient != nil {
			if err := bleClient.SendCommand("request_timestamp", nil, nil); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to request time sync: " + err.Error()})
				return
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE not connected"})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "time_sync_requested"})
	}))

	// POST /api/track/refresh - Request track data refresh from companion
	http.HandleFunc("/api/track/refresh", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Send track refresh request via BLE
		if bleClient := btManager.GetBleClient(); bleClient != nil {
			if err := bleClient.SendCommand("request_track_refresh", nil, nil); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to request track refresh: " + err.Error()})
				return
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE not connected"})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "track_refresh_requested"})
	}))

	// GET /api/weather/data - Get weather data with smart caching and refresh logic
	http.HandleFunc("/api/weather/data", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Check cache and filesystem for recent weather data
		isRecentData := func(weatherData *utils.WeatherUpdate) bool {
			if weatherData == nil || weatherData.Timestamp == 0 {
				return false
			}
			oneHour := int64(60 * 60 * 1000) // 1 hour in milliseconds
			now := time.Now().UnixMilli()
			dataAge := now - weatherData.Timestamp
			return dataAge < oneHour
		}

		// Try to get weather data from memory first
		var weatherState *utils.WeatherState
		if btManager != nil {
			bleClient := btManager.GetBleClient()
			if bleClient != nil {
				weatherState = bleClient.GetWeatherState()
			}
		}

		// Check if we have recent data in memory
		if weatherState != nil && weatherState.HourlyData != nil && isRecentData(weatherState.HourlyData) {
			log.Printf("Serving recent weather data from memory")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(weatherState.HourlyData)
			return
		}

		// Try to load from filesystem
		hourlyPath := filepath.Join("/var/nocturne/weather", "hourly_weather.json")
		if hourlyData, _, err := utils.LoadWeatherFile(hourlyPath); err == nil {
			if isRecentData(hourlyData) {
				log.Printf("Serving recent weather data from filesystem")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(hourlyData)
				return
			}
		}

		// No recent data found - request fresh data from companion app
		log.Printf("No recent weather data found, requesting refresh from companion app")
		if btManager != nil {
			bleClient := btManager.GetBleClient()
			if bleClient != nil && bleClient.IsConnected() {
				// Request fresh weather data
				if err := bleClient.RequestWeatherRefresh(); err != nil {
					log.Printf("Failed to request weather refresh: %v", err)
				} else {
					log.Printf("Weather refresh requested from companion app")
				}
			}
		}

		// Return empty response indicating refresh was triggered
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "refresh_requested"})
	}))

	// GET /api/weather/current - Get current weather data
	http.HandleFunc("/api/weather/current", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Try to get weather data from memory first
		weatherState := &utils.WeatherState{}
		
		if btManager != nil {
			bleClient := btManager.GetBleClient()
			if bleClient != nil {
				memoryWeatherState := bleClient.GetWeatherState()
				if memoryWeatherState != nil {
					weatherState = memoryWeatherState
				}
			}
		}

		// Also try to load from files
		hourlyPath := filepath.Join("/var/nocturne/weather", "hourly_weather.json")
		weeklyPath := filepath.Join("/var/nocturne/weather", "weekly_weather.json")
		
		if hourlyData, _, err := utils.LoadWeatherFile(hourlyPath); err == nil {
			weatherState.HourlyData = hourlyData
		}
		
		if weeklyData, _, err := utils.LoadWeatherFile(weeklyPath); err == nil {
			weatherState.WeeklyData = weeklyData
		}

		// If no weather data found anywhere
		if weatherState.HourlyData == nil && weatherState.WeeklyData == nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "No weather data available"})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(weatherState)
	}))

	// POST /api/weather/refresh - Request weather refresh from companion
	http.HandleFunc("/api/weather/refresh", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if btManager == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
			return
		}

		bleClient := btManager.GetBleClient()
		if bleClient == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE not connected"})
			return
		}

		if err := bleClient.RequestWeatherRefresh(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to request weather refresh: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "weather_refresh_requested"})
	}))

	// POST /api/screenshot - Take server-side screenshot
	http.HandleFunc("/api/screenshot", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		// Create screenshots directory
		screenshotDir := "/var/nocturne/screenshots"
		if err := os.MkdirAll(screenshotDir, 0755); err != nil {
			log.Printf("Failed to create screenshot directory: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to create screenshot directory"})
			return
		}

		// Generate filename with timestamp
		timestamp := time.Now().Format("2006-01-02T15-04-05-000Z")
		filename := fmt.Sprintf("screenshot_%s.png", timestamp)
		filePath := filepath.Join(screenshotDir, filename)

		// Method 1: Try Chrome DevTools Protocol first
		screenshotTaken := false
		
		// Try to find Chrome process and get debug port
		if chromeDebugPort := findChromeDebugPort(); chromeDebugPort != 0 {
			if err := takeScreenshotViaDevTools(chromeDebugPort, filePath); err == nil {
				screenshotTaken = true
				log.Printf("Screenshot taken via Chrome DevTools: %s", filePath)
			} else {
				log.Printf("Chrome DevTools screenshot failed: %v", err)
			}
		}

		// Method 2: Try wkhtmltoimage if Chrome DevTools failed
		if !screenshotTaken {
			if err := takeScreenshotViaWkhtml(filePath); err == nil {
				screenshotTaken = true
				log.Printf("Screenshot taken via wkhtmltoimage: %s", filePath)
			} else {
				log.Printf("wkhtmltoimage screenshot failed: %v", err)
			}
		}

		// Method 3: Try xvfb-run with cutycapt as last resort
		if !screenshotTaken {
			if err := takeScreenshotViaXvfb(filePath); err == nil {
				screenshotTaken = true
				log.Printf("Screenshot taken via xvfb: %s", filePath)
			} else {
				log.Printf("xvfb screenshot failed: %v", err)
			}
		}

		if screenshotTaken {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "success",
				"filename": filename,
				"path": filePath,
				"method": "server-side",
			})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "All screenshot methods failed"})
		}
	}))

	// POST /api/device/reboot - Reboot the device
	http.HandleFunc("/api/device/reboot", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "rebooting"})

		// Give time for response to be sent
		go func() {
			time.Sleep(1 * time.Second)
			log.Println("Rebooting device...")
			// Use syscall to reboot
			if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
				log.Printf("Failed to reboot: %v", err)
				// Try alternative method
				exec.Command("reboot").Run()
			}
		}()
	}))

	port := os.Getenv("PORT")
	if port == "" {
		port = "5000"
	}

	// Simple signal protection - ignore signals that might interfere with SPP
	signal.Ignore(syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGHUP, syscall.SIGPIPE)

	// Start HTTP server in a goroutine with error recovery
	go func() {
		for {
			log.Printf("Server starting on :%s", port)
			err := http.ListenAndServe(":"+port, nil)
			if err != nil {
				log.Printf("HTTP server error: %v - will retry in 5 seconds", err)
				time.Sleep(5 * time.Second)
			}
		}
	}()

	// Keep main thread alive - only exit on explicit kill signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	
	for {
		sig := <-sigChan
		log.Printf("Received signal: %v", sig)
		
		// Only exit on explicit termination signals
		if sig == syscall.SIGINT || sig == syscall.SIGTERM || sig == syscall.SIGKILL {
			log.Println("Shutting down nocturned...")
			break
		}
		// Ignore other signals
		log.Printf("Ignoring signal: %v", sig)
	}
}

// detectImageFormat detects the image format from the data bytes
func detectImageFormat(data []byte) string {
	if len(data) < 12 {
		return "application/octet-stream"
	}
	
	// Check for JPEG (starts with FF D8 FF)
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	
	// Check for PNG (starts with 89 50 4E 47 0D 0A 1A 0A)
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 &&
		data[4] == 0x0D && data[5] == 0x0A && data[6] == 0x1A && data[7] == 0x0A {
		return "image/png"
	}
	
	// Check for WebP (starts with RIFF....WEBP)
	if data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
		return "image/webp"
	}
	
	// Default to JPEG if unknown
	return "image/jpeg"
}

// Helper functions for screenshot functionality
func findChromeDebugPort() int {
	// Look for Chrome processes with debug port
	cmd := exec.Command("ps", "aux")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "chromium") && strings.Contains(line, "--remote-debugging-port") {
			// Extract port number from command line
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "--remote-debugging-port" || strings.HasPrefix(part, "--remote-debugging-port=") {
					var portStr string
					if part == "--remote-debugging-port" && i+1 < len(parts) {
						portStr = parts[i+1]
					} else if strings.HasPrefix(part, "--remote-debugging-port=") {
						portStr = strings.Split(part, "=")[1]
					}
					if port, err := strconv.Atoi(portStr); err == nil {
						return port
					}
				}
			}
		}
	}
	return 0
}

func takeScreenshotViaDevTools(port int, filePath string) error {
	// First, get the list of tabs to find the active one
	tabsURL := fmt.Sprintf("http://localhost:%d/json", port)
	
	// Get tabs
	cmd := exec.Command("curl", "-s", tabsURL)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get tabs: %v", err)
	}
	
	// Parse JSON to find the first tab
	var tabs []map[string]interface{}
	if err := json.Unmarshal(output, &tabs); err != nil {
		return fmt.Errorf("failed to parse tabs JSON: %v", err)
	}
	
	if len(tabs) == 0 {
		return fmt.Errorf("no tabs found")
	}
	
	// Get the first tab's websocket URL
	tab := tabs[0]
	_, ok := tab["webSocketDebuggerUrl"].(string)
	if !ok {
		return fmt.Errorf("no websocket URL found for tab")
	}
	
	// Take screenshot using Page.captureScreenshot
	screenshotCmd := fmt.Sprintf(`
		curl -s -X POST -H "Content-Type: application/json" \
		-d '{"id":1,"method":"Page.captureScreenshot","params":{"format":"png","quality":100}}' \
		http://localhost:%d/json/runtime/evaluate 2>/dev/null
	`, port)
	
	// Execute screenshot command
	cmd = exec.Command("sh", "-c", screenshotCmd)
	screenshotOutput, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("screenshot command failed: %v", err)
	}
	
	// Parse response to get base64 image data
	var response map[string]interface{}
	if err := json.Unmarshal(screenshotOutput, &response); err != nil {
		// If JSON parsing fails, try a different approach
		return takeScreenshotViaSimpleCurl(port, filePath)
	}
	
	// Extract base64 data from response
	if result, ok := response["result"].(map[string]interface{}); ok {
		if data, ok := result["data"].(string); ok {
			// Decode base64 and save to file
			imageData, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				return fmt.Errorf("failed to decode base64: %v", err)
			}
			
			return os.WriteFile(filePath, imageData, 0644)
		}
	}
	
	return fmt.Errorf("no image data found in response")
}

func takeScreenshotViaSimpleCurl(port int, filePath string) error {
	// Simpler approach: just save a rendered HTML to image using curl
	// This is a fallback method
	htmlFile := "/tmp/screenshot.html"
	
	// Create a simple HTML file that captures the current page
	htmlContent := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <style>
        body { margin: 0; padding: 0; background: black; }
        iframe { width: 100vw; height: 100vh; border: none; }
    </style>
</head>
<body>
    <iframe src="http://localhost:5000"></iframe>
</body>
</html>`)
	
	if err := os.WriteFile(htmlFile, []byte(htmlContent), 0644); err != nil {
		return fmt.Errorf("failed to create HTML file: %v", err)
	}
	
	// Try to trigger a screenshot by evaluating JavaScript
	jsCode := fmt.Sprintf(`
		// Try to trigger download of current page as image
		const canvas = document.createElement('canvas');
		const ctx = canvas.getContext('2d');
		canvas.width = window.innerWidth || 800;
		canvas.height = window.innerHeight || 480;
		
		// Fill with current background
		ctx.fillStyle = getComputedStyle(document.body).background || '#000000';
		ctx.fillRect(0, 0, canvas.width, canvas.height);
		
		// Convert to blob and trigger download
		canvas.toBlob(function(blob) {
			const url = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = url;
			a.download = 'screenshot.png';
			document.body.appendChild(a);
			a.click();
			document.body.removeChild(a);
			URL.revokeObjectURL(url);
		});
	`)
	
	evalURL := fmt.Sprintf("http://localhost:%d/json/runtime/evaluate", port)
	payload := map[string]interface{}{
		"expression": jsCode,
	}
	
	jsonData, _ := json.Marshal(payload)
	cmd := exec.Command("curl", "-s", "-X", "POST", "-H", "Content-Type: application/json", 
		"-d", string(jsonData), evalURL)
	
	return cmd.Run()
}

func takeScreenshotViaWkhtml(filePath string) error {
	// Use wkhtmltoimage to capture the page
	cmd := exec.Command("wkhtmltoimage", 
		"--width", "800",
		"--height", "480", 
		"--disable-smart-width",
		"http://localhost:5000", 
		filePath)
	return cmd.Run()
}

func takeScreenshotViaXvfb(filePath string) error {
	// Use xvfb-run with cutycapt
	cmd := exec.Command("xvfb-run", "-a", "-s", "-screen 0 800x480x24",
		"cutycapt", 
		"--url=http://localhost:5000",
		"--out=" + filePath,
		"--min-width=800",
		"--min-height=480")
	return cmd.Run()
}
