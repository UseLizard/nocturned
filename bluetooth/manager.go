package bluetooth

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/usenocturne/nocturned/utils"
)

// ManagerV2 manages the v2 Bluetooth Low Energy service
type ManagerV2 struct {
	mu              sync.RWMutex
	conn            *dbus.Conn
	wsHub           *utils.WebSocketHub
	client          *BleClientV2
	albumArtHandler *AlbumArtHandler
	isRunning       bool
	stopChan        chan struct{}
	
	// State
	currentPlayState    *PlayStateMessage
	currentVolume       *VolumeMessage
	currentWeather      *WeatherMessage
	currentDeviceInfo   *DeviceInfoMessage
	connectionStatus    *ConnectionStatusMessage
	currentMediaState   *MediaState
	
	// Configuration
	rateLimitConfig *RateLimitConfig
	chunkSize       int
	
	// Activity tracking for health checks
	lastActivity    time.Time
	
	// Callbacks
	playStateCallback func(*PlayStateMessage)
	volumeCallback    func(*VolumeMessage)
	weatherCallback   func(*WeatherMessage)
}

// NewManagerV2 creates a new v2 manager instance
func NewManagerV2(wsHub *utils.WebSocketHub) (*ManagerV2, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to system D-Bus: %w", err)
	}
	
	manager := &ManagerV2{
		conn:            conn,
		wsHub:           wsHub,
		stopChan:        make(chan struct{}),
		rateLimitConfig: DefaultRateLimitConfig(),
		chunkSize:       DefaultChunkSize,
		lastActivity:    time.Now(),
		connectionStatus: &ConnectionStatusMessage{
			Status:   ConnectionStateDisconnected,
			LastSeen: 0,
		},
	}
	
	// Initialize album art handler
	manager.albumArtHandler = NewAlbumArtHandler()
	manager.albumArtHandler.SetCallback(func(data []byte, stats *AlbumArtTransferStats) {
		// Record activity for health check timing
		manager.recordActivity()
		
		// Store album art in the utils package for other components
		utils.SetCurrentAlbumArt(data)
		
		// Broadcast via WebSocket with detailed transfer stats
		if manager.wsHub != nil {
			manager.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/album_art_received",
				Payload: map[string]interface{}{
					"size":         stats.Size,
					"size_kb":      float64(stats.Size) / 1024.0,
					"chunks":       stats.Chunks,
					"duration_ms":  stats.Duration.Milliseconds(),
					"duration_str": stats.Duration.String(),
					"speed_kbps":   stats.SpeedKBps,
					"speed_mbps":   stats.SpeedMbps,
					"hash":         stats.Hash[:16] + "...",
					"timestamp":    time.Now().Unix(),
					"start_time":   stats.StartTime.Unix(),
					"end_time":     stats.EndTime.Unix(),
				},
			})
		}
		
		// Also broadcast that album art is now available at the endpoint
		manager.broadcastAlbumArtAvailable(stats.Hash)
	})
	
	// Set save callback to save album art to file
	manager.albumArtHandler.SetSaveCallback(manager.saveAlbumArtToFile)
	
	// Initialize BLE client
	manager.client = NewBleClientV2(conn, wsHub)
	manager.client.albumArtHandler = manager.albumArtHandler
	
	// Set media state callback
	manager.client.SetMediaStateCallback(manager.UpdateMediaState)
	
	return manager, nil
}

// Start starts the v2 manager
func (m *ManagerV2) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.isRunning {
		return fmt.Errorf("manager already running")
	}
	
	log.Println("BT_MGR: Starting Nocturne v2 Manager")
	
	// Start the BLE client
	log.Println("BT_MGR: Starting BLE client...")
	m.client.Start()
	log.Println("BT_MGR: BLE client started")
	
	// Start background routines
	log.Println("BT_MGR: Starting background monitoring routines...")
	go m.monitorConnection()
	go m.cleanupRoutine()
	log.Println("BT_MGR: Background routines started")
	
	m.isRunning = true
	log.Println("BT_MGR: Nocturne v2 Manager started successfully")
	return nil
}

// Stop stops the v2 manager
func (m *ManagerV2) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if !m.isRunning {
		return
	}
	
	log.Println("BT_MGR: Stopping Nocturne v2 Manager")
	
	// Stop the BLE client
	log.Println("BT_MGR: Stopping BLE client...")
	m.client.Stop()
	log.Println("BT_MGR: BLE client stopped")
	
	// Stop background routines
	log.Println("BT_MGR: Stopping background routines...")
	close(m.stopChan)
	log.Println("BT_MGR: Background routines stopped")
	
	m.isRunning = false
	log.Println("BT_MGR: Nocturne v2 Manager stopped")
}

// SendPlayStateUpdate sends a play state update to the connected device
func (m *ManagerV2) SendPlayStateUpdate(playState *PlayStateMessage) error {
	m.mu.Lock()
	m.currentPlayState = playState
	m.mu.Unlock()
	
	log.Printf("Sending play state update: %s - %s", playState.Artist, playState.TrackTitle)
	
	// Create the command
	cmd := &Command{
		Type:    MsgTypePlayStateUpdate,
		Payload: EncodePlayStateUpdate(
			playState.TrackTitle,
			playState.Artist,
			playState.Album,
			playState.AlbumArtHash,
			playState.Duration,
			playState.Position,
			playState.PlayState,
		)[3:], // Remove the message header as it's added by sendCommand
		ID:      fmt.Sprintf("playstate_%d", time.Now().UnixNano()),
	}
	
	m.client.sendCommand(cmd)
	
	// Broadcast via WebSocket
	if m.wsHub != nil {
		m.wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "media/play_state_update",
			Payload: playState,
		})
	}
	
	return nil
}

// SendVolumeUpdate sends a volume update to the connected device
func (m *ManagerV2) SendVolumeUpdate(volume byte) error {
	volumeMsg := &VolumeMessage{Volume: volume}
	
	m.mu.Lock()
	m.currentVolume = volumeMsg
	m.mu.Unlock()
	
	log.Printf("Sending volume update: %d%%", volume)
	
	cmd := &Command{
		Type:    MsgTypeVolumeUpdate,
		Payload: EncodeVolumeUpdate(volume)[3:], // Remove message header
		ID:      fmt.Sprintf("volume_%d", time.Now().UnixNano()),
	}
	
	m.client.sendCommand(cmd)
	
	// Broadcast via WebSocket
	if m.wsHub != nil {
		m.wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "media/volume_update",
			Payload: volumeMsg,
		})
	}
	
	return nil
}

// SendWeatherUpdate sends weather information to the connected device
func (m *ManagerV2) SendWeatherUpdate(weather *WeatherMessage) error {
	m.mu.Lock()
	m.currentWeather = weather
	m.mu.Unlock()
	
	log.Printf("Sending weather update: %s, %d°C", weather.Condition, weather.Temperature/10)
	
	// Encode weather data
	payload := make([]byte, 0)
	payload = append(payload, byte(weather.Temperature>>8))   // High byte
	payload = append(payload, byte(weather.Temperature&0xFF)) // Low byte
	payload = append(payload, weather.Humidity)
	payload = append(payload, []byte(weather.Condition)...)
	payload = append(payload, 0) // Null terminator
	payload = append(payload, []byte(weather.Location)...)
	payload = append(payload, 0) // Null terminator
	
	cmd := &Command{
		Type:    MsgTypeWeatherUpdate,
		Payload: payload,
		ID:      fmt.Sprintf("weather_%d", time.Now().UnixNano()),
	}
	
	m.client.sendCommand(cmd)
	
	// Broadcast via WebSocket
	if m.wsHub != nil {
		m.wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "weather/update",
			Payload: weather,
		})
	}
	
	return nil
}

// SendAlbumArt sends album art data to the connected device
func (m *ManagerV2) SendAlbumArt(hash string, data []byte) error {
	log.Printf("Sending album art: %s (%d bytes)", hash, len(data))
	
	return m.albumArtHandler.SendAlbumArt(hash, data, func(msg []byte) error {
		// Send via the album art TX characteristic
		if m.client.albumArtTxChar != nil {
			return m.client.albumArtTxChar.Call("org.bluez.GattCharacteristic1.WriteValue", 0, msg, map[string]interface{}{}).Store()
		}
		return fmt.Errorf("album art characteristic not available")
	})
}

// GetCurrentState returns the current state of the system
func (m *ManagerV2) GetCurrentState() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	state := map[string]interface{}{
		"connection_status": m.connectionStatus,
		"is_running":        m.isRunning,
	}
	
	if m.currentPlayState != nil {
		state["play_state"] = m.currentPlayState
	}
	
	if m.currentVolume != nil {
		state["volume"] = m.currentVolume
	}
	
	if m.currentWeather != nil {
		state["weather"] = m.currentWeather
	}
	
	if m.currentDeviceInfo != nil {
		state["device_info"] = m.currentDeviceInfo
	}
	
	if m.currentMediaState != nil {
		state["media_state"] = m.currentMediaState
	}
	
	return state
}

// UpdateMediaState updates the current media state from FullState messages
func (m *ManagerV2) UpdateMediaState(mediaState *MediaState) {
	// Record activity for health check timing
	m.recordActivity()
	
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Check if this is a new album (different album_hash)
	var needsAlbumArt bool
	if m.currentMediaState == nil || m.currentMediaState.AlbumHash != mediaState.AlbumHash {
		needsAlbumArt = true
	}
	
	m.currentMediaState = mediaState
	log.Printf("🎵 Updated media state: %s - %s (%s) [hash: %d]", mediaState.Artist, mediaState.Track, mediaState.Album, mediaState.AlbumHash)
	
	// Check for album art if this is a new album - skip hash validation and use hash directly
	if needsAlbumArt && mediaState.AlbumHash != 0 {
		go m.checkAndRequestAlbumArtByHash(mediaState.AlbumHash)
	}
	
	// Broadcast the updated media state to WebSocket clients immediately
	if m.wsHub != nil {
		m.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/state_update",
			Payload: map[string]interface{}{
				"artist":         mediaState.Artist,
				"album":          mediaState.Album,
				"track":          mediaState.Track,
				"duration_ms":    mediaState.DurationMs,
				"position_ms":    mediaState.PositionMs,
				"is_playing":     mediaState.IsPlaying,
				"volume_percent": mediaState.Volume,
				"album_hash":     mediaState.AlbumHash,
				"last_update":    mediaState.LastUpdate.Unix(),
			},
		})
		log.Printf("🎵 Broadcasted media state update via WebSocket")
	}
}

// GetCurrentMediaState returns the current media state
func (m *ManagerV2) GetCurrentMediaState() *MediaState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return m.currentMediaState
}

// SendMediaCommand sends media control commands to the companion device
func (m *ManagerV2) SendMediaCommand(command string, params map[string]interface{}) error {
	// Record activity for health check timing
	m.recordActivity()
	
	if m.client == nil {
		return fmt.Errorf("BLE client not initialized")
	}
	
	// Optimistically update the stored state immediately for better UI responsiveness
	m.mu.Lock()
	if m.currentMediaState != nil {
		// Create a copy of the current state to modify
		updatedState := *m.currentMediaState
		updatedState.LastUpdate = time.Now()
		
		switch command {
		case "play":
			updatedState.IsPlaying = true
			m.currentMediaState = &updatedState
		case "pause":
			updatedState.IsPlaying = false
			m.currentMediaState = &updatedState
		case "volume":
			if vol, ok := params["volume"].(int); ok {
				updatedState.Volume = vol
				m.currentMediaState = &updatedState
			}
		case "seek":
			if pos, ok := params["position"].(int64); ok {
				updatedState.PositionMs = pos
				m.currentMediaState = &updatedState
			}
		// Note: next/previous don't update state optimistically since we don't know the new track
		}
		
		// Broadcast the optimistic update to WebSocket clients immediately
		if m.wsHub != nil {
			m.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/state_update",
				Payload: map[string]interface{}{
					"artist":         m.currentMediaState.Artist,
					"album":          m.currentMediaState.Album,
					"track":          m.currentMediaState.Track,
					"duration_ms":    m.currentMediaState.DurationMs,
					"position_ms":    m.currentMediaState.PositionMs,
					"is_playing":     m.currentMediaState.IsPlaying,
					"volume_percent": m.currentMediaState.Volume,
					"optimistic":     true, // Flag to indicate this is an optimistic update
				},
			})
		}
	}
	m.mu.Unlock()
	
	// Send the command to the companion device
	var err error
	switch command {
	case "play":
		err = m.client.SendPlayCommand()
		if err == nil {
			log.Printf("🎵 Sent play command, optimistically updated state")
		}
	case "pause":
		err = m.client.SendPauseCommand()
		if err == nil {
			log.Printf("🎵 Sent pause command, optimistically updated state")
		}
	case "next":
		err = m.client.SendNextCommand()
		if err == nil {
			log.Printf("🎵 Sent next command")
		}
	case "previous":
		err = m.client.SendPreviousCommand()
		if err == nil {
			log.Printf("🎵 Sent previous command")
		}
	case "volume":
		if vol, ok := params["volume"].(int); ok {
			err = m.client.SendVolumeCommand(vol)
			if err == nil {
				log.Printf("🎵 Sent volume command: %d%%, optimistically updated state", vol)
			}
		} else {
			return fmt.Errorf("invalid volume parameter")
		}
	case "seek":
		if pos, ok := params["position"].(int64); ok {
			err = m.client.SendSeekCommand(pos)
			if err == nil {
				log.Printf("🎵 Sent seek command: %dms, optimistically updated state", pos)
			}
		} else {
			return fmt.Errorf("invalid seek position parameter")
		}
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
	
	return err
}

// GetAlbumArtHandler returns the album art handler
func (m *ManagerV2) GetAlbumArtHandler() *AlbumArtHandler {
	return m.albumArtHandler
}

// GetBleClient returns the BLE client
func (m *ManagerV2) GetBleClient() *BleClientV2 {
	return m.client
}

// SetPlayStateCallback sets the callback for play state updates
func (m *ManagerV2) SetPlayStateCallback(callback func(*PlayStateMessage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.playStateCallback = callback
}

// SetVolumeCallback sets the callback for volume updates
func (m *ManagerV2) SetVolumeCallback(callback func(*VolumeMessage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.volumeCallback = callback
}

// SetWeatherCallback sets the callback for weather updates
func (m *ManagerV2) SetWeatherCallback(callback func(*WeatherMessage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.weatherCallback = callback
}

// SetAlbumArtEnabled enables or disables album art transfers
func (m *ManagerV2) SetAlbumArtEnabled(enabled bool) {
	if m.albumArtHandler != nil {
		m.albumArtHandler.SetEnabled(enabled)
	}
}

// IsAlbumArtEnabled returns whether album art transfers are enabled
func (m *ManagerV2) IsAlbumArtEnabled() bool {
	if m.albumArtHandler != nil {
		return m.albumArtHandler.IsEnabled()
	}
	return false
}

// UpdateRateLimitConfig updates the rate limiting configuration
func (m *ManagerV2) UpdateRateLimitConfig(config *RateLimitConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if config != nil {
		m.rateLimitConfig = config
		log.Printf("Updated rate limit config: %d cmd/s, burst: %d", 
			config.MaxCommandsPerSecond, config.BurstSize)
	}
}

// recordActivity records the current time as the last activity
func (m *ManagerV2) recordActivity() {
	m.mu.Lock()
	m.lastActivity = time.Now()
	m.mu.Unlock()
}

// monitorConnection monitors the BLE connection status
func (m *ManagerV2) monitorConnection() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			// Only check connection health if we've been idle for 30+ seconds
			m.mu.RLock()
			timeSinceActivity := time.Since(m.lastActivity)
			m.mu.RUnlock()
			
			if timeSinceActivity >= 30*time.Second {
				m.checkConnectionStatus()
			}
		}
	}
}

// checkConnectionStatus checks and updates the connection status
func (m *ManagerV2) checkConnectionStatus() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	wasConnected := m.connectionStatus.Status == ConnectionStateConnected
	isConnected := m.client.isConnected
	
	now := uint64(time.Now().Unix())
	
	if isConnected && !wasConnected {
		// Connection established
		m.connectionStatus.Status = ConnectionStateConnected
		m.connectionStatus.LastSeen = now
		m.connectionStatus.ErrorMessage = ""
		
		log.Println("BT_MGR: BLE connection established successfully")
		
		if m.wsHub != nil {
			m.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "bluetooth/connected",
				Payload: map[string]interface{}{
					"timestamp": now,
				},
			})
		}
	} else if !isConnected && wasConnected {
		// Connection lost
		m.connectionStatus.Status = ConnectionStateDisconnected
		m.connectionStatus.ErrorMessage = "Connection lost"
		
		log.Println("BT_MGR: BLE connection lost, attempting to reconnect...")
		
		if m.wsHub != nil {
			m.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "bluetooth/disconnected",
				Payload: map[string]interface{}{
					"timestamp": now,
				},
			})
		}
	} else if isConnected {
		// Update last seen time (only log periodically to avoid spam)
		m.connectionStatus.LastSeen = now
	}
}

// cleanupRoutine performs periodic cleanup tasks
func (m *ManagerV2) cleanupRoutine() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			// Clean up stale album art transfers
			m.albumArtHandler.CleanupStaleTransfers(5 * time.Minute)
		}
	}
}

// GetStats returns system statistics
func (m *ManagerV2) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	cacheEntries, cacheSize := m.albumArtHandler.GetCacheStats()
	activeTransfers := len(m.albumArtHandler.GetActiveTransfers())
	
	return map[string]interface{}{
		"is_running":        m.isRunning,
		"connection_status": m.connectionStatus,
		"album_art_cache": map[string]interface{}{
			"entries":          cacheEntries,
			"total_size_bytes": cacheSize,
		},
		"album_art_transfers": map[string]interface{}{
			"active": activeTransfers,
		},
		"rate_limiting": map[string]interface{}{
			"max_commands_per_second": m.rateLimitConfig.MaxCommandsPerSecond,
			"burst_size":              m.rateLimitConfig.BurstSize,
			"chunk_delay_ms":          m.rateLimitConfig.ChunkDelay.Milliseconds(),
		},
	}
}

// checkAndRequestAlbumArtByHash checks if album art exists using the hash from full state update and requests it if missing
func (m *ManagerV2) checkAndRequestAlbumArtByHash(albumHash int32) {
	// Convert hash to string for file operations
	hashStr := fmt.Sprintf("%d", albumHash)
	
	log.Printf("🎨 Checking album art for hash %s (skipping hash validation as requested)", hashStr)
	
	// First check if it's already in the album art handler's memory cache
	if _, exists := m.albumArtHandler.GetFromCache(hashStr); exists {
		log.Printf("🎨 Album art for hash %s already in memory cache", hashStr)
		return
	}
	
	// Check if album art file exists in /var/album_art/
	albumArtDir := "/var/album_art"
	albumArtPath := filepath.Join(albumArtDir, fmt.Sprintf("%s.webp", hashStr))
	
	// Create directory if it doesn't exist
	if err := os.MkdirAll(albumArtDir, 0755); err != nil {
		log.Printf("🎨 Failed to create album art directory: %v", err)
		return
	}
	
	// Check if file exists
	if _, err := os.Stat(albumArtPath); err == nil {
		log.Printf("🎨 Album art file exists at %s, loading into cache", albumArtPath)
		// Load from file into cache
		if data, err := os.ReadFile(albumArtPath); err == nil {
			m.albumArtHandler.StoreInCache(hashStr, data)
			log.Printf("🎨 Loaded album art from file into cache: %d bytes", len(data))
			// Set as current album art for global access
			utils.SetCurrentAlbumArt(data)
			// Broadcast that album art is available
			m.broadcastAlbumArtAvailable(hashStr)
			return
		} else {
			log.Printf("🎨 Failed to read album art file %s: %v", albumArtPath, err)
		}
	} else {
		log.Printf("🎨 Album art file not found at %s", albumArtPath)
	}
	
	// Album art not found, request it from the companion device using the hash
	log.Printf("🎨 Requesting album art for hash %s from companion device", hashStr)
	if err := m.requestAlbumArtFromCompanion(hashStr); err != nil {
		log.Printf("🎨 Failed to request album art: %v", err)
	}
}

// checkAndRequestAlbumArt is DEPRECATED - use checkAndRequestAlbumArtByHash instead
// This method used SHA-256 metadata hashes which caused inconsistencies
func (m *ManagerV2) checkAndRequestAlbumArt(albumHash int32, album, artist string) {
	log.Printf("🚫 DEPRECATED: checkAndRequestAlbumArt called - using checkAndRequestAlbumArtByHash instead")
	// Forward to the new integer hash method
	m.checkAndRequestAlbumArtByHash(albumHash)
}

// requestAlbumArtFromCompanion sends an album art query to the companion device
func (m *ManagerV2) requestAlbumArtFromCompanion(hashStr string) error {
	if m.client == nil {
		return fmt.Errorf("BLE client not initialized")
	}
	
	// Create album art query command using Binary Protocol V2
	queryPayload := CreateStringPayload(hashStr) // Album art query payload is just the hash string
	message := EncodeMessage(MSG_CMD_ALBUM_ART_QUERY, queryPayload, 0)
	
	// Send via command characteristic
	if m.client.commandRxChar != nil {
		log.Printf("🎨 Sending album art query for hash %s", hashStr)
		return m.client.commandRxChar.Call("org.bluez.GattCharacteristic1.WriteValue", 0, message, map[string]interface{}{}).Store()
	}
	
	return fmt.Errorf("command characteristic not available")
}

// saveAlbumArtToFile saves album art data to the /var/album_art directory
func (m *ManagerV2) saveAlbumArtToFile(hashStr string, data []byte) error {
	albumArtDir := "/var/album_art"
	albumArtPath := filepath.Join(albumArtDir, fmt.Sprintf("%s.webp", hashStr))
	
	// Create directory if it doesn't exist
	if err := os.MkdirAll(albumArtDir, 0755); err != nil {
		return fmt.Errorf("failed to create album art directory: %w", err)
	}
	
	// Write file
	if err := os.WriteFile(albumArtPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write album art file: %w", err)
	}
	
	log.Printf("🎨 Saved album art to %s (%d bytes)", albumArtPath, len(data))
	
	// Note: SetCurrentAlbumArt and broadcastAlbumArtAvailable are handled 
	// by the album art handler callback to avoid duplication
	
	return nil
}

// broadcastAlbumArtAvailable broadcasts a WebSocket event that new album art is available
func (m *ManagerV2) broadcastAlbumArtAvailable(hashStr string) {
	if m.wsHub != nil {
		m.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/album_art_available",
			Payload: map[string]interface{}{
				"hash":      hashStr,
				"endpoint":  fmt.Sprintf("/api/v2/album-art/current"),
				"timestamp": time.Now().Unix(),
			},
		})
		log.Printf("🎨 Broadcasted album art available event for hash %s", hashStr)
	}
}