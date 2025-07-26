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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
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

func main() {
	// Protect the process from supervisor interference
	// Create a new process group to isolate from supervisor signals
	if os.Getenv("UNDER_SUPERVISOR") != "" {
		log.Println("SUPERVISOR_DEBUG: Running under supervisor, setting up signal protection...")
		syscall.Setpgid(0, 0)
	}

	wsHub := utils.NewWebSocketHub()

	btManager, err := bluetooth.NewBluetoothManager(wsHub)
	if err != nil {
		log.Fatal("Failed to initialize bluetooth manager:", err)
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

		if err := btManager.SendMediaCommand("set_volume", nil, &volume); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send volume command: " + err.Error()})
			return
		}

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
		albumArtDir := "/data/etc/nocturne/albumart"
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
		albumArtDir := "/data/etc/nocturne/albumart"
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
			galleryDir := "/data/etc/nocturne/albumart"
			
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
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".webp") {
						item := AlbumArtItem{
							Filename: entry.Name(),
						}
						
						// Try to read metadata
						metadataFile := filepath.Join(galleryDir, strings.TrimSuffix(entry.Name(), ".webp") + ".json")
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

		// Serve the current album art file from /tmp/album_art.webp
		albumArtPath := "/tmp/album_art.webp"
		
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
		
		// Set content type and cache headers
		w.Header().Set("Content-Type", "image/webp")
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
		albumArtPath := filepath.Join("/data/etc/nocturne/albumart", filename)
		
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
