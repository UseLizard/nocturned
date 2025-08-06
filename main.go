package main

import (
	"crypto/sha256"
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
	maxRetries := 30
	testFile := "/var/.nocturne_test"
	
	for i := 0; i < maxRetries; i++ {
		// Try to write a test file
		if err := os.WriteFile(testFile, []byte("test"), 0644); err == nil {
			// Clean up test file
			os.Remove(testFile)
			log.Printf("Filesystem ready after %d seconds", i)
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
	
	// Retry directory creation with exponential backoff
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.MkdirAll(albumArtDir, 0755); err == nil {
			break
		}
		
		log.Printf("Failed to create album art directory (attempt %d/5): %v", attempt+1, err)
		time.Sleep(time.Duration(1<<uint(attempt)) * time.Second) // Exponential backoff
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

	btManager, err := bluetooth.NewBluetoothManager(wsHub)
	if err != nil {
		log.Fatal("Failed to initialize bluetooth manager:", err)
	}
	
	// Set the album art callback to store in memory immediately
	if bleClient := btManager.GetBleClient(); bleClient != nil {
		bleClient.SetAlbumArtCallback(SetCurrentAlbumArt)
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
	http.HandleFunc("/bluetooth/discover/on", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if err := btManager.SetDiscoverable(true); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to enable discoverable mode: " + err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))

	// POST /bluetooth/discover/off
	http.HandleFunc("/bluetooth/discover/off", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
			return
		}

		if err := btManager.SetDiscoverable(false); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to disable discoverable mode"})
			return
		}

		w.WriteHeader(http.StatusOK)
	}))

	// POST /bluetooth/pairing/accept
	http.HandleFunc("/bluetooth/pairing/accept", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
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

	// GET /media/ble/status
	http.HandleFunc("/media/ble/status", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
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
		if bleClient := btManager.GetBleClient(); bleClient != nil {
			bleClient.Disconnect()
			log.Println("BLE disconnected for reconnection")
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
			if err := bleClient.SendCommand("request_time_sync", nil, nil); err != nil {
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

	log.Printf("Server starting on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
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
