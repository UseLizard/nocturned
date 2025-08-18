package bluetooth

import (
	"fmt"
	"log"
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
	
	// Configuration
	rateLimitConfig *RateLimitConfig
	chunkSize       int
	
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
		connectionStatus: &ConnectionStatusMessage{
			Status:   ConnectionStateDisconnected,
			LastSeen: 0,
		},
	}
	
	// Initialize album art handler
	manager.albumArtHandler = NewAlbumArtHandler()
	manager.albumArtHandler.SetCallback(func(data []byte) {
		// Store album art in the utils package for other components
		utils.SetCurrentAlbumArt(data)
		
		// Broadcast via WebSocket
		if manager.wsHub != nil {
			manager.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/album_art_received",
				Payload: map[string]interface{}{
					"size":      len(data),
					"timestamp": time.Now().Unix(),
				},
			})
		}
	})
	
	// Initialize BLE client
	manager.client = NewBleClientV2(conn, wsHub)
	
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
	
	return state
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

// monitorConnection monitors the BLE connection status
func (m *ManagerV2) monitorConnection() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.checkConnectionStatus()
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