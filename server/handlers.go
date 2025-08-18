package server

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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vishvananda/netlink"
	"github.com/usenocturne/nocturned/listeners"
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

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	s.wsHub.AddClient(conn)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleAcceptPairing(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.AcceptPairing(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to accept pairing"})
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDenyPairing(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.DenyPairing(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to deny pairing"})
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeviceInfo(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	info, err := s.btManager.GetDeviceInfo(address)
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
}

func (s *Server) handleRemoveDevice(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.RemoveDevice(address); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to remove device: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleConnectDevice(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.ConnectDevice(address); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to connect to device: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleDisconnectDevice(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.DisconnectDevice(address); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to disconnect device: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleBluetoothNetworkStatus(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleConnectNetwork(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.ConnectNetwork(address); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to connect to Bluetooth network: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleGetDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	devices, err := s.btManager.GetDevices()
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
}

func (s *Server) handleGetBrightness(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleSetBrightness(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleResetCounter(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleFactoryReset(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleSetTimezone(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleGetDate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	t := time.Now()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"time": t.Format(time.TimeOnly), "date": t.Format(time.DateOnly)})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
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
			s.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "update_progress",
				Payload: utils.ProgressMessage{
					Type:          "progress",
					Stage:         "download",
					BytesComplete: complete,
					BytesTotal:    total,
					Speed:         float64(int(speed*10)) / 10,
					Percent:       float64(int(percent*10)) / 10,
				},
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
		if err := utils.UpdateSystem(imgPath, sum, func(progress utils.ProgressMessage) {
			s.wsHub.Broadcast(utils.WebSocketEvent{
				Type:    "update_progress",
				Payload: progress,
			})
		}); err != nil {
			utils.SetUpdateStatus(false, "", fmt.Sprintf("Failed to update system: %v", err))
			s.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "update_completion",
				Payload: utils.CompletionMessage{
					Type:    "completion",
					Stage:   "flash",
					Success: false,
					Error:   fmt.Sprintf("Failed to update system: %v", err),
				},
			})
			return
		}

		utils.SetUpdateStatus(false, "", "")
		s.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "update_completion",
			Payload: utils.CompletionMessage{
				Type:    "completion",
				Stage:   "flash",
				Success: true,
			},
		})
	}()

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(utils.OKResponse{Status: "success"}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to encode JSON: " + err.Error()})
		return
	}
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleFetchJSON(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleMediaStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"connected": false, "state": nil})
		return
	}
	state := s.btManager.GetMediaState()
	connected := s.btManager.IsMediaConnected()

	response := map[string]interface{}{
		"connected": connected,
		"state":     state,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response"})
		return
	}
}

func (s *Server) handleMediaPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.SendMediaCommand("play", nil, nil); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send play command: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleMediaPause(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.SendMediaCommand("pause", nil, nil); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send pause command: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleMediaNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.SendMediaCommand("next", nil, nil); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send next command: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleMediaPrevious(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.SendMediaCommand("previous", nil, nil); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send previous command: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleMediaSeek(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	if err := s.btManager.SendMediaCommand("seek_to", &position, nil); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to send seek command: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleMediaVolume(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	// Update local state immediately for instant UI feedback
	s.btManager.UpdateLocalVolume(volume)
	
	// Broadcast the updated state via WebSocket
	if state := s.btManager.GetMediaState(); state != nil {
		s.wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "media/state_update",
			Payload: state,
		})
	}
	
	// Send volume command to Android asynchronously
	go func() {
		if err := s.btManager.SendMediaCommand("set_volume", nil, &volume); err != nil {
			log.Printf("Failed to send volume command: %v", err)
		}
	}()

	// Return success immediately for responsive UI
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleAlbumArtQuery(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	// We don't have it, send a NACK to request the full data
	if err := s.btManager.RequestAlbumArt(req.TrackID, req.Checksum); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to request album art: " + err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "needed"})
}

func (s *Server) handleAlbumArt(w http.ResponseWriter, r *http.Request) {
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
	s.wsHub.Broadcast(utils.WebSocketEvent{
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
}

func (s *Server) handleMediaSimulate(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleGetAlbumArt(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleGetAlbumArtFile(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleGetLatestGradient(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	// Look for the most recent gradient file
	gradientDir := "/var/nocturne/gradients"
	
	// Read directory contents
	entries, err := os.ReadDir(gradientDir)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to read gradients directory"})
		return
	}

	// Find the most recent gradient file
	var latestFile string
	var latestTime time.Time
	
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".png") {
			continue
		}
		
		info, err := entry.Info()
		if err != nil {
			continue
		}
		
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestFile = entry.Name()
		}
	}

	if latestFile == "" {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "No gradient files found"})
		return
	}

	// Construct full path
	gradientPath := filepath.Join(gradientDir, latestFile)
	
	// Read the file
	data, err := os.ReadFile(gradientPath)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to read gradient file"})
		return
	}
	
	// Set content type and cache headers
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache") // Don't cache gradients since they update
	
	// Write the image data
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Server) handleTestAlbumArtRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	bleClient := s.btManager.GetBleClient()
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
	s.wsHub.Broadcast(utils.WebSocketEvent{
		Type:    "test/album_art_requested",
		Payload: map[string]interface{}{
			"timestamp": time.Now().UnixMilli(),
		},
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "requested"})
}

func (s *Server) handleTestAlbumArtStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	bleClient := s.btManager.GetBleClient()
	if bleClient == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "BLE not initialized"})
		return
	}

	status := bleClient.GetTestTransferStatus()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleTestAlbumArtImage(w http.ResponseWriter, r *http.Request) {
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
}

func (s *Server) handleBTSpeedTestStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}

	bleClient := s.btManager.GetBleClient()
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
}

func (s *Server) handleBLEStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	bleClient := s.btManager.GetBleClient()
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
}

func (s *Server) handleBLEConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	bleClient := s.btManager.GetBleClient()
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
}

func (s *Server) handleBLEDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	bleClient := s.btManager.GetBleClient()
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
}

func (s *Server) handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	response := NetworkStatusResponse{
		Status: listeners.GetCurrentNetworkStatus(),
	}
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleGetProfiles(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	profileManager := s.btManager.GetProfileManager()
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
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	profileManager := s.btManager.GetProfileManager()
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
}

func (s *Server) handleConnectProfile(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	profileManager := s.btManager.GetProfileManager()
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
}

func (s *Server) handleDisconnectProfile(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	profileManager := s.btManager.GetProfileManager()
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
}

func (s *Server) handleDetectProfiles(w http.ResponseWriter, r *http.Request) {
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

	if s.btManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Bluetooth not initialized"})
		return
	}
	profileManager := s.btManager.GetProfileManager()
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
		"last_detected_unix": time.Now().Unix(),
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Error encoding response"})
		return
	}
}

// GetCurrentAlbumArt returns the current in-memory album art
func GetCurrentAlbumArt() []byte {
	// This function needs to be moved to a more appropriate package
	// and the global variable should be handled differently.
	return nil
}

func detectImageFormat(data []byte) string {
	if len(data) > 8 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return http.DetectContentType(data)
}
