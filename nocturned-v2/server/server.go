package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/usenocturne/nocturned/bluetooth"
	"github.com/usenocturne/nocturned/utils"
)

// ServerV2 is the v2 HTTP server for the nocturne service
type ServerV2 struct {
	manager  *bluetooth.ManagerV2
	wsHub    *utils.WebSocketHub
	upgrader websocket.Upgrader
	server   *http.Server
}

// NewServerV2 creates a new v2 server instance
func NewServerV2(manager *bluetooth.ManagerV2, wsHub *utils.WebSocketHub) *ServerV2 {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for development
		},
	}

	return &ServerV2{
		manager:  manager,
		wsHub:    wsHub,
		upgrader: upgrader,
	}
}

// Start starts the v2 HTTP server
func (s *ServerV2) Start(port int) error {
	mux := http.NewServeMux()

	// API routes with method checking
	mux.HandleFunc("/api/v2/status", s.methodHandler("GET", s.handleStatus))
	mux.HandleFunc("/api/v2/health", s.methodHandler("GET", s.handleHealth))
	
	// Bluetooth management
	mux.HandleFunc("/api/v2/bluetooth/status", s.methodHandler("GET", s.handleBluetoothStatus))
	mux.HandleFunc("/api/v2/bluetooth/stats", s.methodHandler("GET", s.handleBluetoothStats))
	
	// Media control
	mux.HandleFunc("/api/v2/media/play-state", s.methodHandler("POST", s.handlePlayState))
	mux.HandleFunc("/api/v2/media/volume", s.methodHandler("POST", s.handleVolume))
	mux.HandleFunc("/api/v2/media/current", s.methodHandler("GET", s.handleCurrentMedia))
	
	// Album art
	mux.HandleFunc("/api/v2/media/album-art", s.methodHandler("POST", s.handleAlbumArt))
	mux.HandleFunc("/api/v2/media/album-art/status", s.methodHandler("GET", s.handleAlbumArtStatus))
	mux.HandleFunc("/api/v2/media/album-art/cache", s.multiMethodHandler([]string{"GET", "DELETE"}, s.handleAlbumArtCache))
	mux.HandleFunc("/api/v2/media/album-art/", s.handleAlbumArtByHashRoute)
	
	// Weather
	mux.HandleFunc("/api/v2/weather", s.methodHandler("POST", s.handleWeather))
	mux.HandleFunc("/api/v2/weather/current", s.methodHandler("GET", s.handleCurrentWeather))
	
	// V1 compatibility routes (for nocturne-ui compatibility)
	mux.HandleFunc("/bluetooth/status", s.methodHandler("GET", s.handleBluetoothStatus))
	mux.HandleFunc("/media/status", s.methodHandler("GET", s.handleCurrentMedia))
	mux.HandleFunc("/media/play", s.methodHandler("POST", s.handleV1MediaPlay))
	mux.HandleFunc("/media/pause", s.methodHandler("POST", s.handleV1MediaPause))
	mux.HandleFunc("/media/next", s.methodHandler("POST", s.handleV1MediaNext))
	mux.HandleFunc("/media/previous", s.methodHandler("POST", s.handleV1MediaPrevious))
	mux.HandleFunc("/media/volume/", s.handleV1MediaVolume)
	mux.HandleFunc("/media/seek/", s.handleV1MediaSeek)
	mux.HandleFunc("/info", s.methodHandler("GET", s.handleV1Info))
	mux.HandleFunc("/device/date", s.methodHandler("GET", s.handleV1Date))
	mux.HandleFunc("/device/date/settimezone", s.methodHandler("POST", s.handleV1DateTimezone))
	mux.HandleFunc("/api/weather/current", s.methodHandler("GET", s.handleCurrentWeather)) // V1 weather compatibility
	
	// Apply middleware to most routes
	handler := loggingMiddleware(corsMiddleware(mux))
	
	// Create a new mux that handles WebSocket separately to avoid middleware issues
	mainMux := http.NewServeMux()
	
	// WebSocket endpoint - handled directly without middleware
	mainMux.HandleFunc("/ws", s.handleWebSocketDirect)
	
	// All other routes through middleware
	mainMux.Handle("/", handler)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mainMux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("HTTP_SRV: Starting v2 HTTP server on port %d", port)
	log.Printf("HTTP_SRV: Available endpoints:")
	log.Printf("HTTP_SRV:   GET  /api/v2/status")
	log.Printf("HTTP_SRV:   GET  /api/v2/health")
	log.Printf("HTTP_SRV:   GET  /api/v2/bluetooth/status")
	log.Printf("HTTP_SRV:   POST /api/v2/media/play-state")
	log.Printf("HTTP_SRV:   POST /api/v2/media/album-art")
	log.Printf("HTTP_SRV:   GET  /ws (WebSocket)")
	
	return s.server.ListenAndServe()
}

// Stop stops the HTTP server
func (s *ServerV2) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// handleStatus returns the overall system status
func (s *ServerV2) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"version":   "2.0.0",
		"timestamp": time.Now().Unix(),
		"status":    "running",
		"system":    s.manager.GetCurrentState(),
	}
	
	writeJSONResponse(w, http.StatusOK, status)
}

// handleHealth returns health check information
func (s *ServerV2) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"checks": map[string]interface{}{
			"bluetooth_manager": s.manager != nil,
			"websocket_hub":     s.wsHub != nil,
		},
	}
	
	writeJSONResponse(w, http.StatusOK, health)
}

// handleBluetoothStatus returns Bluetooth connection status
func (s *ServerV2) handleBluetoothStatus(w http.ResponseWriter, r *http.Request) {
	state := s.manager.GetCurrentState()
	
	response := map[string]interface{}{
		"connection_status": state["connection_status"],
		"timestamp":         time.Now().Unix(),
	}
	
	writeJSONResponse(w, http.StatusOK, response)
}

// handleBluetoothStats returns detailed Bluetooth statistics
func (s *ServerV2) handleBluetoothStats(w http.ResponseWriter, r *http.Request) {
	stats := s.manager.GetStats()
	writeJSONResponse(w, http.StatusOK, stats)
}

// handlePlayState handles play state updates
func (s *ServerV2) handlePlayState(w http.ResponseWriter, r *http.Request) {
	var playState bluetooth.PlayStateMessage
	if err := json.NewDecoder(r.Body).Decode(&playState); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid play state data", err)
		return
	}
	
	if err := s.manager.SendPlayStateUpdate(&playState); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send play state", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":    "sent",
		"timestamp": time.Now().Unix(),
	})
}

// handleVolume handles volume updates
func (s *ServerV2) handleVolume(w http.ResponseWriter, r *http.Request) {
	var volumeData struct {
		Volume byte `json:"volume"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&volumeData); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid volume data", err)
		return
	}
	
	if volumeData.Volume > 100 {
		writeErrorResponse(w, http.StatusBadRequest, "Volume must be between 0 and 100", nil)
		return
	}
	
	if err := s.manager.SendVolumeUpdate(volumeData.Volume); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send volume", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":    "sent",
		"volume":    volumeData.Volume,
		"timestamp": time.Now().Unix(),
	})
}

// handleCurrentMedia returns current media state
func (s *ServerV2) handleCurrentMedia(w http.ResponseWriter, r *http.Request) {
	state := s.manager.GetCurrentState()
	
	response := map[string]interface{}{
		"timestamp": time.Now().Unix(),
	}
	
	if playState, exists := state["play_state"]; exists {
		response["play_state"] = playState
	}
	
	if volume, exists := state["volume"]; exists {
		response["volume"] = volume
	}
	
	writeJSONResponse(w, http.StatusOK, response)
}

// handleWeather handles weather updates
func (s *ServerV2) handleWeather(w http.ResponseWriter, r *http.Request) {
	var weather bluetooth.WeatherMessage
	if err := json.NewDecoder(r.Body).Decode(&weather); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid weather data", err)
		return
	}
	
	if err := s.manager.SendWeatherUpdate(&weather); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send weather", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":    "sent",
		"timestamp": time.Now().Unix(),
	})
}

// handleCurrentWeather returns current weather state
func (s *ServerV2) handleCurrentWeather(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"timestamp": time.Now().Unix(),
		"status":    "no_data",
	}
	
	// Try to load weather data from files
	weatherDir := "/var/nocturne/weather"
	
	// Load hourly weather data
	if hourlyData, err := s.loadWeatherFile(filepath.Join(weatherDir, "hourly_weather.json")); err == nil {
		response["hourly"] = hourlyData
		response["status"] = "ok"
	}
	
	// Load weekly weather data  
	if weeklyData, err := s.loadWeatherFile(filepath.Join(weatherDir, "weekly_weather.json")); err == nil {
		response["weekly"] = weeklyData
		response["status"] = "ok"
	}
	
	writeJSONResponse(w, http.StatusOK, response)
}

// loadWeatherFile loads weather data from a JSON file
func (s *ServerV2) loadWeatherFile(filepath string) (map[string]interface{}, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read weather file: %v", err)
	}
	
	var weatherData map[string]interface{}
	if err := json.Unmarshal(data, &weatherData); err != nil {
		return nil, fmt.Errorf("failed to parse weather JSON: %v", err)
	}
	
	return weatherData, nil
}

// handleWebSocket handles WebSocket connections
func (s *ServerV2) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP_SRV: WebSocket connection attempt from %s", r.RemoteAddr)
	
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("HTTP_SRV: WebSocket upgrade failed: %v", err)
		writeErrorResponse(w, http.StatusInternalServerError, "WebSocket upgrade failed", err)
		return
	}
	
	log.Printf("HTTP_SRV: WebSocket connection established with %s", r.RemoteAddr)
	
	// Add client to hub using existing API
	s.wsHub.AddClient(conn)
	
	// Handle cleanup when connection closes
	defer func() {
		log.Printf("HTTP_SRV: WebSocket connection closed with %s", r.RemoteAddr)
		s.wsHub.RemoveClient(conn)
	}()
	
	// Set up ping/pong handling
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	
	// Keep connection alive with ping/pong
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	// Listen for close messages and pings
	go func() {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("HTTP_SRV: WebSocket error: %v", err)
				}
				return
			}
		}
	}()
	
	for {
		select {
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("HTTP_SRV: WebSocket ping failed: %v", err)
				return
			}
		}
	}
}

// writeJSONResponse writes a JSON response
func writeJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}

// writeErrorResponse writes an error response
func writeErrorResponse(w http.ResponseWriter, statusCode int, message string, err error) {
	response := map[string]interface{}{
		"error":     message,
		"timestamp": time.Now().Unix(),
	}
	
	if err != nil {
		response["details"] = err.Error()
		log.Printf("API Error: %s - %v", message, err)
	}
	
	writeJSONResponse(w, statusCode, response)
}

// corsMiddleware adds CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs HTTP requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create a response recorder to capture status code
		rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		
		next.ServeHTTP(rec, r)
		
		duration := time.Since(start)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, rec.statusCode, duration)
	})
}

// responseRecorder wraps http.ResponseWriter to capture status code
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *responseRecorder) WriteHeader(code int) {
	rec.statusCode = code
	rec.ResponseWriter.WriteHeader(code)
}

// methodHandler creates a handler that only accepts specific HTTP methods
func (s *ServerV2) methodHandler(method string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
			return
		}
		handler(w, r)
	}
}

// multiMethodHandler creates a handler that accepts multiple HTTP methods
func (s *ServerV2) multiMethodHandler(methods []string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed := false
		for _, method := range methods {
			if r.Method == method {
				allowed = true
				break
			}
		}
		if !allowed {
			writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
			return
		}
		handler(w, r)
	}
}

// handleAlbumArtByHashRoute handles requests for album art by hash without gorilla/mux
func (s *ServerV2) handleAlbumArtByHashRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}
	
	// Extract hash from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/media/album-art/")
	if path == "" || strings.Contains(path, "/") {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid album art hash", nil)
		return
	}
	
	// Create a temporary request with hash in URL values for compatibility
	r.URL.RawQuery = "hash=" + path
	s.handleAlbumArtByHash(w, r)
}

// extractHashFromPath extracts hash from URL path for album art requests
func extractHashFromPath(path string) string {
	// Remove the base path and extract hash
	parts := strings.Split(strings.TrimPrefix(path, "/api/v2/media/album-art/"), "/")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return ""
}

// handleWebSocketDirect handles WebSocket connections directly without middleware
func (s *ServerV2) handleWebSocketDirect(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	log.Printf("HTTP_SRV: WebSocket connection attempt from %s", r.RemoteAddr)
	
	// Add CORS headers for WebSocket
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("HTTP_SRV: WebSocket upgrade failed: %v", err)
		// Don't call writeErrorResponse here as it might interfere with upgrade
		return
	}
	
	duration := time.Since(start)
	log.Printf("HTTP_SRV: WebSocket connection established with %s in %v", r.RemoteAddr, duration)
	
	// Add client to hub using existing API
	s.wsHub.AddClient(conn)
	
	// Handle cleanup when connection closes
	defer func() {
		log.Printf("HTTP_SRV: WebSocket connection closed with %s", r.RemoteAddr)
		s.wsHub.RemoveClient(conn)
	}()
	
	// Set up ping/pong handling
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	
	// Keep connection alive with ping/pong
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	// Listen for close messages and pings
	go func() {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("HTTP_SRV: WebSocket error: %v", err)
				}
				return
			}
		}
	}()
	
	for {
		select {
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("HTTP_SRV: WebSocket ping failed: %v", err)
				return
			}
		}
	}
}