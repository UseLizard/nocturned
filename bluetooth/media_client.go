package bluetooth

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/godbus/dbus/v5"
	"github.com/usenocturne/nocturned/utils"
)

const (
	SPP_UUID                = "00001101-0000-1000-8000-00805f9b34fb"
	NOCTURNE_COMPANION_NAME = "NocturneCompanion"
	RECONNECT_DELAY         = 30 * time.Second
	MAX_RECONNECT_ATTEMPTS  = 10

	// RFCOMM socket constants
	AF_BLUETOOTH   = 31
	BTPROTO_RFCOMM = 3
	SOCK_STREAM    = 1

	// Dynamic discovery constants
	MAX_RFCOMM_CHANNEL      = 30
	DISCOVERY_WAIT_TIME     = 1 * time.Second    // Reduced wait time for faster discovery
	DISCOVERY_CLEANUP_DELAY = 200 * time.Millisecond // Faster cleanup between tests
	CONNECTION_RETRY_DELAY  = 2 * time.Second    // Slightly longer delay for persistent connection
	HEARTBEAT_INTERVAL      = 30 * time.Second
	CHUNK_TIMEOUT           = 30 * time.Second
)

type AlbumArtTransfer struct {
	Hash         string
	TotalSize    int
	TotalChunks  int
	ReceivedData map[int]string // chunk_index -> data
	StartTime    time.Time
}

type MediaClient struct {
	connected         bool
	targetAddress     string
	wsHub             *utils.WebSocketHub
	btManager         *BluetoothManager
	mu                sync.RWMutex
	reconnectAttempts int
	currentState      *utils.MediaStateUpdate
	stopChan          chan struct{}
	sppConn           net.Conn
	sppConnected      bool

	// Album art chunking
	chunkBuffer       map[string]*AlbumArtTransfer
	chunkMutex        sync.Mutex

	// RFCOMM socket fields
	rfcommFd        int
	rfcommChannel   uint8
	lastActivity    time.Time
	heartbeatTicker *time.Ticker

	// Dynamic discovery fields
	cachedChannel     uint8     // Cache successful channel for faster reconnection
	lastDiscoveryTime time.Time // Track when last discovery was performed
}

// RFCOMM socket address structure
type sockaddr_rc struct {
	family  uint16
	bdaddr  [6]byte
	channel uint8
}

func NewMediaClient(btManager *BluetoothManager, wsHub *utils.WebSocketHub) *MediaClient {
	return &MediaClient{
		btManager:   btManager,
		wsHub:       wsHub,
		stopChan:    make(chan struct{}),
		chunkBuffer: make(map[string]*AlbumArtTransfer),
	}
}

func (mc *MediaClient) DiscoverAndConnect() error {
	log.Println("SPP_LOG: DiscoverAndConnect called, waiting 5 seconds for adapter to initialize...")
	time.Sleep(5 * time.Second)
	mc.mu.Lock()
	defer mc.mu.Unlock()

	address, err := mc.discoverNocturneCompanion()
	if err != nil {
		log.Printf("SPP_LOG: discoverNocturneCompanion failed: %v", err)
		return fmt.Errorf("failed to discover NocturneCompanion: %v", err)
	}
	log.Printf("SPP_LOG: discoverNocturneCompanion succeeded, address: %s", address)

	mc.targetAddress = address
	return mc.connectToDevice()
}

func (mc *MediaClient) discoverNocturneCompanion() (string, error) {
	log.Println("SPP_LOG: discoverNocturneCompanion called")
	devices, err := mc.btManager.GetDevices()
	if err != nil {
		log.Printf("SPP_LOG: GetDevices failed: %v", err)
		return "", fmt.Errorf("failed to get bluetooth devices: %v", err)
	}

	log.Printf("🔍 MediaClient: Scanning %d devices for NocturneCompanion...", len(devices))

	// Log all connected devices for debugging
	for _, device := range devices {
		if device.Connected {
			log.Printf("📱 Found connected device: '%s' (%s) - Paired: %v, SPP: %v",
				device.Name, device.Address, device.Paired, mc.deviceSupportsSPP(device.Address))
		}
	}

	// First try to find device with exact NocturneCompanion name
	for _, device := range devices {
		if device.Paired && device.Connected &&
			(device.Name == NOCTURNE_COMPANION_NAME || device.Alias == NOCTURNE_COMPANION_NAME) {

			log.Printf("Found NocturneCompanion device: %s (%s)", device.Name, device.Address)
			return device.Address, nil
		}
	}

	// If no exact match, look for any connected device with SPP support (more flexible approach)
	for _, device := range devices {
		if device.Paired && device.Connected && mc.deviceSupportsSPP(device.Address) {
			log.Printf("Found potential NocturneCompanion device (SPP enabled): %s (%s)", device.Name, device.Address)
			return device.Address, nil
		}
	}
	log.Println("SPP_LOG: No connected NocturneCompanion device found")
	return "", fmt.Errorf("no connected NocturneCompanion device found")
}

func (mc *MediaClient) deviceSupportsSPP(address string) bool {
	log.Printf("SPP_LOG: deviceSupportsSPP called for address: %s", address)
	devicePath := formatDevicePath(mc.btManager.adapter, address)
	obj := mc.btManager.conn.Object(BLUEZ_BUS_NAME, devicePath)

	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, BLUEZ_DEVICE_INTERFACE).Store(&props); err != nil {
		log.Printf("Failed to get device properties for %s: %v", address, err)
		return false
	}

	if uuids, ok := props["UUIDs"]; ok {
		if uuidList, ok := uuids.Value().([]string); ok {
			for _, uuid := range uuidList {
				if uuid == SPP_UUID {
					log.Printf("SPP_LOG: Device %s supports SPP", address)
					return true
				}
			}
		}
	}
	log.Printf("SPP_LOG: Device %s does not support SPP", address)
	return false
}

func (mc *MediaClient) connectToDevice() error {
	log.Println("SPP_LOG: connectToDevice called")
	if mc.targetAddress == "" {
		log.Println("SPP_LOG: connectToDevice failed: no target address")
		return fmt.Errorf("no target address set")
	}

	log.Printf("Connecting to NocturneCompanion at %s", mc.targetAddress)

	// Establish SPP socket connection
	if err := mc.connectSPP(); err != nil {
		log.Printf("SPP_LOG: connectSPP failed: %v", err)
		return fmt.Errorf("failed to establish SPP connection: %v", err)
	}

	mc.connected = true
	mc.reconnectAttempts = 0

	if mc.wsHub != nil {
		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/connected",
			Payload: map[string]string{
				"address": mc.targetAddress,
				"status":  "connected",
			},
		})
	}

	log.Printf("Successfully connected to NocturneCompanion")
	return nil
}

func (mc *MediaClient) connectSPP() error {
	log.Println("SPP_LOG: connectSPP called")
	// Convert Bluetooth address to byte array
	deviceAddr, err := mc.parseBluetoothAddress(mc.targetAddress)
	if err != nil {
		log.Printf("SPP_LOG: parseBluetoothAddress failed: %v", err)
		return fmt.Errorf("failed to parse Bluetooth address: %v", err)
	}

	// Try cached channel first for direct persistent connection
	if mc.cachedChannel > 0 && time.Since(mc.lastDiscoveryTime) < 30*time.Minute {
		log.Printf("Attempting direct connection to cached channel %d", mc.cachedChannel)
		fd, err := mc.connectRFCOMM(deviceAddr, mc.cachedChannel)
		if err == nil {
			log.Printf("✅ Direct connection successful to cached channel %d", mc.cachedChannel)
			mc.rfcommFd = fd
			mc.rfcommChannel = mc.cachedChannel
			mc.sppConnected = true
			mc.lastActivity = time.Now()
			mc.sppConn = &rfcommConn{fd: fd}

			// Start connection monitoring
			go mc.handleIncomingData()
			go mc.monitorConnection()

			log.Printf("SPP connection established to %s on cached channel %d", mc.targetAddress, mc.cachedChannel)
			return nil
		}
		log.Printf("Cached channel %d failed, performing discovery", mc.cachedChannel)
		mc.cachedChannel = 0 // Clear invalid cache
	}

	// Perform discovery and establish persistent connection in one step
	channel, fd, err := mc.discoverAndConnectNocturneCompanion(deviceAddr)
	if err != nil {
		log.Printf("SPP_LOG: discoverAndConnectNocturneCompanion failed: %v", err)
		return fmt.Errorf("failed to discover and connect to NocturneCompanion: %v", err)
	}
	log.Printf("SPP_LOG: discoverAndConnectNocturneCompanion succeeded, channel: %d, fd: %d", channel, fd)

	mc.rfcommFd = fd
	mc.rfcommChannel = channel
	mc.sppConnected = true
	mc.lastActivity = time.Now()
	mc.cacheSuccessfulChannel(channel)

	// Create a net.Conn wrapper for compatibility
	mc.sppConn = &rfcommConn{fd: fd}

	// Start connection monitoring
	go mc.handleIncomingData()
	go mc.monitorConnection()

	log.Printf("SPP persistent connection established to %s on channel %d", mc.targetAddress, channel)
	return nil
}

func (mc *MediaClient) handleIncomingData() {
	log.Println("SPP_LOG: handleIncomingData goroutine started")
	if mc.sppConn == nil {
		log.Println("SPP_LOG: handleIncomingData failed: sppConn is nil")
		return
	}

	scanner := bufio.NewScanner(mc.sppConn)
	for scanner.Scan() {
		data := scanner.Text()
		if len(data) == 0 {
			continue
		}
		log.Printf("Received SPP data: %s", data)

		// Broadcast received data
		if mc.wsHub != nil {
			mc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/spp_data_received",
				Payload: map[string]interface{}{
					"address":   mc.targetAddress,
					"data":      data,
					"timestamp": time.Now().Unix(),
				},
			})
		}

		// Update activity timestamp
		mc.mu.Lock()
		mc.lastActivity = time.Now()
		mc.mu.Unlock()

		// Try to parse as MediaCommand first (for album art)
		var command utils.MediaCommand
		if err := json.Unmarshal([]byte(data), &command); err == nil && command.Command != "" {
			if command.Command == "album_art" {
				mc.handleAlbumArtCommand(&command)
				continue
			}
		}

		// Parse state update from Android app
		var stateUpdate utils.MediaStateUpdate
		if err := json.Unmarshal([]byte(data), &stateUpdate); err != nil {
			log.Printf("Failed to parse media state update: %v", err)
			continue
		}

		mc.mu.Lock()
		mc.currentState = &stateUpdate
		mc.mu.Unlock()

		// Broadcast to WebSocket clients
		if mc.wsHub != nil {
			mc.wsHub.Broadcast(utils.WebSocketEvent{
				Type:    "media/state_update",
				Payload: stateUpdate,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("SPP connection error: %v", err)
		mc.handleConnectionLoss()
	}
	log.Println("SPP_LOG: handleIncomingData goroutine finished")
}

func (mc *MediaClient) SendCommand(command string, valueMs *int, valuePercent *int) error {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if !mc.connected || mc.targetAddress == "" || !mc.sppConnected || mc.sppConn == nil {
		return fmt.Errorf("not connected to media device")
	}

	cmd := utils.MediaCommand{
		Command:      command,
		ValueMs:      valueMs,
		ValuePercent: valuePercent,
	}

	// Convert command to JSON
	cmdJson, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %v", err)
	}

	// Send command over SPP connection
	_, err = mc.sppConn.Write(append(cmdJson, '\n'))
	if err != nil {
		log.Printf("Failed to send SPP command: %v", err)

		// Broadcast connection error
		if mc.wsHub != nil {
			mc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/connection_error",
				Payload: map[string]interface{}{
					"address":   mc.targetAddress,
					"error":     fmt.Sprintf("Failed to send command: %v", err),
					"timestamp": time.Now().Unix(),
				},
			})
		}

		mc.sppConnected = false
		return fmt.Errorf("failed to send command over SPP: %v", err)
	}

	log.Printf("Sent media command: %s to %s", command, mc.targetAddress)

	// Broadcast data sent event
	if mc.wsHub != nil {
		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/spp_data_sent",
			Payload: map[string]interface{}{
				"address":   mc.targetAddress,
				"command":   command,
				"data":      string(cmdJson),
				"timestamp": time.Now().Unix(),
			},
		})
	}

	if mc.wsHub != nil {
		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/command_sent",
			Payload: map[string]interface{}{
				"command": cmd,
				"address": mc.targetAddress,
			},
		})
	}

	return nil
}

func (mc *MediaClient) Play() error {
	return mc.SendCommand("play", nil, nil)
}

func (mc *MediaClient) Pause() error {
	return mc.SendCommand("pause", nil, nil)
}

func (mc *MediaClient) Next() error {
	return mc.SendCommand("next", nil, nil)
}

func (mc *MediaClient) Previous() error {
	return mc.SendCommand("previous", nil, nil)
}

func (mc *MediaClient) SeekTo(positionMs int) error {
	return mc.SendCommand("seek_to", &positionMs, nil)
}

func (mc *MediaClient) SetVolume(volumePercent int) error {
	if volumePercent < 0 || volumePercent > 100 {
		return fmt.Errorf("volume must be between 0 and 100")
	}
	return mc.SendCommand("set_volume", nil, &volumePercent)
}

func (mc *MediaClient) GetCurrentState() *utils.MediaStateUpdate {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.currentState
}

func (mc *MediaClient) IsConnected() bool {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.connected && mc.targetAddress != "" && mc.sppConnected
}

func (mc *MediaClient) SimulateStateUpdate(artist, track string, isPlaying bool) {
	mc.mu.Lock()
	artistPtr := &artist
	trackPtr := &track

	mc.currentState = &utils.MediaStateUpdate{
		Type:          "stateUpdate",
		Artist:        artistPtr,
		Track:         trackPtr,
		DurationMs:    240000,
		PositionMs:    45000,
		IsPlaying:     isPlaying,
		VolumePercent: 75,
	}
	mc.mu.Unlock()

	if mc.wsHub != nil {
		payload := utils.MediaStatePayload{
			Artist:        artistPtr,
			Track:         trackPtr,
			DurationMs:    240000,
			PositionMs:    45000,
			IsPlaying:     isPlaying,
			VolumePercent: 75,
		}

		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "media/state_update",
			Payload: payload,
		})
	}

	log.Printf("Media state: %s - %s (%s)",
		stringOrDefault(artistPtr, "Unknown"),
		stringOrDefault(trackPtr, "Unknown"),
		map[bool]string{true: "Playing", false: "Paused"}[isPlaying])
}

func (mc *MediaClient) attemptReconnect() {
	if mc.reconnectAttempts >= MAX_RECONNECT_ATTEMPTS {
		log.Printf("Max reconnection attempts reached for media client")
		return
	}

	mc.reconnectAttempts++
	log.Printf("Attempting to reconnect media client (attempt %d/%d)", mc.reconnectAttempts, MAX_RECONNECT_ATTEMPTS)

	time.Sleep(RECONNECT_DELAY)

	if err := mc.DiscoverAndConnect(); err != nil {
		log.Printf("Media client reconnection failed: %v", err)
		mc.attemptReconnect()
	}
}

func (mc *MediaClient) Disconnect() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.connected {
		mc.connected = false
		mc.sppConnected = false

		// Stop monitoring
		if mc.heartbeatTicker != nil {
			mc.heartbeatTicker.Stop()
			mc.heartbeatTicker = nil
		}

		// Close RFCOMM socket
		if mc.rfcommFd > 0 {
			syscall.Close(mc.rfcommFd)
			mc.rfcommFd = -1
		}

		// Close SPP connection
		if mc.sppConn != nil {
			mc.sppConn.Close()
			mc.sppConn = nil
		}

		if mc.wsHub != nil {
			mc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/disconnected",
				Payload: map[string]string{
					"address": mc.targetAddress,
					"status":  "disconnected",
				},
			})
		}
	}
}

func stringOrDefault(s *string, defaultVal string) string {
	if s != nil {
		return *s
	}
	return defaultVal
}

// parseBluetoothAddress converts a Bluetooth address string to byte array
func (mc *MediaClient) parseBluetoothAddress(address string) ([6]byte, error) {
	var addr [6]byte

	// Parse address like "B0:C2:C7:03:BD:6E" to byte array
	// Bluetooth addresses are stored in reverse order
	var parts [6]int
	n, err := fmt.Sscanf(address, "%02X:%02X:%02X:%02X:%02X:%02X",
		&parts[5], &parts[4], &parts[3], &parts[2], &parts[1], &parts[0])
	if err != nil || n != 6 {
		return addr, fmt.Errorf("invalid Bluetooth address format: %s", address)
	}

	for i := 0; i < 6; i++ {
		addr[i] = byte(parts[i])
	}

	return addr, nil
}

// discoverAndConnectNocturneCompanion finds the RFCOMM channel and establishes persistent connection
func (mc *MediaClient) discoverAndConnectNocturneCompanion(deviceAddr [6]byte) (uint8, int, error) {
	log.Printf("SPP_LOG: Starting integrated discovery and connection for NocturneCompanion on %s", mc.targetAddress)

	// Broadcast discovery start event
	if mc.wsHub != nil {
		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/spp_discovery_start",
			Payload: map[string]interface{}{
				"address":   mc.targetAddress,
				"timestamp": time.Now().Unix(),
			},
		})
	}

	// Define priority channels based on previous testing and common SPP usage
	priorityChannels := []uint8{4, 2, 3, 5, 1, 6, 7, 8}

	log.Printf("SPP_LOG: Testing priority channels: %v for persistent connection", priorityChannels)

	// Test priority channels first - keep connection if successful
	for _, channel := range priorityChannels {
		log.Printf("SPP_LOG: Testing channel %d for persistent connection...", channel)
		if fd, success := mc.testChannelForPersistentConnection(deviceAddr, channel); success {
			mc.cacheSuccessfulChannel(channel)
			log.Printf("✅ Found and connected to NocturneCompanion on priority channel %d", channel)
			return channel, fd, nil
		}
	}

	// Test a few additional channels if priority channels fail
	additionalChannels := []uint8{9, 10, 11, 12}
	log.Printf("SPP_LOG: Priority channels failed, testing additional channels: %v", additionalChannels)
	for _, channel := range additionalChannels {
		log.Printf("SPP_LOG: Testing channel %d for persistent connection...", channel)
		if fd, success := mc.testChannelForPersistentConnection(deviceAddr, channel); success {
			mc.cacheSuccessfulChannel(channel)
			log.Printf("✅ Found and connected to NocturneCompanion on additional channel %d", channel)
			return channel, fd, nil
		}
	}

	log.Println("SPP_LOG: NocturneCompanion service not found on any RFCOMM channel")
	return 0, -1, fmt.Errorf("NocturneCompanion service not found on any RFCOMM channel")
}

// discoverNocturneCompanionChannel finds the RFCOMM channel running NocturneCompanion (legacy function)
func (mc *MediaClient) discoverNocturneCompanionChannel(deviceAddr [6]byte) (uint8, error) {
	log.Printf("Starting dynamic RFCOMM discovery for NocturneCompanion on %s", mc.targetAddress)

	// First, try cached channel if we have one and it's recent (extended cache duration)
	if mc.cachedChannel > 0 && time.Since(mc.lastDiscoveryTime) < 30*time.Minute {
		log.Printf("Trying cached channel %d first (cached %v ago)", mc.cachedChannel, time.Since(mc.lastDiscoveryTime))
		if mc.validateNocturneCompanionChannel(deviceAddr, mc.cachedChannel) {
			log.Printf("Cached channel %d still working, skipping discovery", mc.cachedChannel)
			return mc.cachedChannel, nil
		}
		log.Printf("Cached channel %d no longer working, performing limited discovery", mc.cachedChannel)
		mc.cachedChannel = 0 // Clear invalid cache
	}

	// Define priority channels based on previous testing and common SPP usage
	priorityChannels := []uint8{4, 2, 3, 5, 1, 6, 7, 8}

	log.Printf("Testing priority channels: %v (limited discovery to reduce connection churn)", priorityChannels)

	// Test priority channels first (most likely to succeed)
	for _, channel := range priorityChannels {
		log.Printf("Testing high-priority channel %d...", channel)
		if discoveredChannel := mc.testChannelSequentially(deviceAddr, channel); discoveredChannel > 0 {
			mc.cacheSuccessfulChannel(discoveredChannel)
			log.Printf("✅ Found NocturneCompanion on priority channel %d, stopping discovery", discoveredChannel)
			return discoveredChannel, nil
		}
	}

	// Only scan a few additional channels to reduce connection churn
	additionalChannels := []uint8{9, 10, 11, 12}
	log.Printf("Priority channels failed, testing limited additional channels: %v", additionalChannels)
	for _, channel := range additionalChannels {
		log.Printf("Testing additional channel %d...", channel)
		if discoveredChannel := mc.testChannelSequentially(deviceAddr, channel); discoveredChannel > 0 {
			mc.cacheSuccessfulChannel(discoveredChannel)
			log.Printf("✅ Found NocturneCompanion on additional channel %d, stopping discovery", discoveredChannel)
			return discoveredChannel, nil
		}
	}

	return 0, fmt.Errorf("NocturneCompanion service not found on any RFCOMM channel (tested 1-%d)", MAX_RFCOMM_CHANNEL)
}

// testChannelForPersistentConnection tests a channel and keeps connection open if successful
func (mc *MediaClient) testChannelForPersistentConnection(deviceAddr [6]byte, channel uint8) (int, bool) {
	log.Printf("SPP_LOG: Testing channel %d for persistent connection...", channel)

	// Broadcast channel test start
	if mc.wsHub != nil {
		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/spp_channel_test",
			Payload: map[string]interface{}{
				"channel":   channel,
				"address":   mc.targetAddress,
				"timestamp": time.Now().Unix(),
			},
		})
	}

	// Create RFCOMM socket
	fd, err := syscall.Socket(AF_BLUETOOTH, SOCK_STREAM, BTPROTO_RFCOMM)
	if err != nil {
		log.Printf("SPP_LOG: Channel %d: Failed to create socket: %v", channel, err)
		return -1, false
	}

	// Setup socket address
	sockaddr := sockaddr_rc{
		family:  AF_BLUETOOTH,
		bdaddr:  deviceAddr,
		channel: channel,
	}

	// Try to connect
	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&sockaddr)),
		uintptr(unsafe.Sizeof(sockaddr)),
	)

	if errno != 0 {
		log.Printf("SPP_LOG: Channel %d: Connection failed (%v)", channel, errno)

		// Broadcast channel test failure
		if mc.wsHub != nil {
			mc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/spp_channel_failed",
				Payload: map[string]interface{}{
					"channel":   channel,
					"address":   mc.targetAddress,
					"reason":    fmt.Sprintf("Connection failed: %v", errno),
					"timestamp": time.Now().Unix(),
				},
			})
		}

		syscall.Close(fd)
		return -1, false
	}

	log.Printf("SPP_LOG: Channel %d: Connection established. Assuming this is NocturneCompanion.", channel)

	// Broadcast channel success
	if mc.wsHub != nil {
		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/spp_channel_success",
			Payload: map[string]interface{}{
				"channel":   channel,
				"address":   mc.targetAddress,
				"timestamp": time.Now().Unix(),
			},
		})
	}

	return fd, true
}

// testChannelSequentially tests a single channel with proper timing and validation (legacy function)
func (mc *MediaClient) testChannelSequentially(deviceAddr [6]byte, channel uint8) uint8 {
	log.Printf("Testing RFCOMM channel %d...", channel)

	// Create test socket
	fd, err := syscall.Socket(AF_BLUETOOTH, SOCK_STREAM, BTPROTO_RFCOMM)
	if err != nil {
		log.Printf("Channel %d: Failed to create socket: %v", channel, err)
		return 0
	}

	defer func() {
		syscall.Close(fd)
		log.Printf("Channel %d: Test connection closed, waiting for cleanup...", channel)
		time.Sleep(DISCOVERY_CLEANUP_DELAY)
	}()

	// Setup socket address
	sockaddr := sockaddr_rc{
		family:  AF_BLUETOOTH,
		bdaddr:  deviceAddr,
		channel: channel,
	}

	// Try to connect
	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&sockaddr)),
		uintptr(unsafe.Sizeof(sockaddr)),
	)

	if errno != 0 {
		log.Printf("Channel %d: Connection failed (%v)", channel, errno)
		return 0
	}

	log.Printf("Channel %d: Connection established, waiting for NocturneCompanion state updates...", channel)

	// Set non-blocking mode to avoid hanging
	if err := syscall.SetNonblock(fd, true); err != nil {
		log.Printf("Channel %d: Failed to set non-blocking: %v", channel, err)
		return 0
	}

	// Wait for NocturneCompanion to send automatic state updates
	buffer := make([]byte, 2048)
	endTime := time.Now().Add(DISCOVERY_WAIT_TIME)

	for time.Now().Before(endTime) {
		n, err := syscall.Read(fd, buffer)
		if err != nil && err != syscall.EAGAIN {
			log.Printf("Channel %d: Read error: %v", channel, err)
			return 0
		}

		if n > 0 {
			dataStr := string(buffer[:n])
			log.Printf("Channel %d: Received data (%d bytes): %s", channel, n, dataStr)

			// Try to parse as NocturneCompanion state update
			if mc.isValidNocturneCompanionData(dataStr) {
				log.Printf("Channel %d: ✅ CONFIRMED - This is NocturneCompanion!", channel)
				return channel
			} else {
				log.Printf("Channel %d: ❌ Invalid format - Not NocturneCompanion", channel)
				return 0
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	log.Printf("Channel %d: ⏱️ Timeout - No state updates received", channel)
	return 0
}

// validateNocturneCompanionChannel quickly validates a cached channel
func (mc *MediaClient) validateNocturneCompanionChannel(deviceAddr [6]byte, channel uint8) bool {
	log.Printf("Validating cached channel %d...", channel)

	// Quick connection test
	fd, err := syscall.Socket(AF_BLUETOOTH, SOCK_STREAM, BTPROTO_RFCOMM)
	if err != nil {
		return false
	}
	defer syscall.Close(fd)

	sockaddr := sockaddr_rc{
		family:  AF_BLUETOOTH,
		bdaddr:  deviceAddr,
		channel: channel,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&sockaddr)),
		uintptr(unsafe.Sizeof(sockaddr)),
	)

	if errno != 0 {
		log.Printf("Cached channel %d validation failed: %v", channel, errno)
		return false
	}

	log.Printf("Cached channel %d validation successful", channel)
	return true
}

// cacheSuccessfulChannel caches a working channel for faster future connections
func (mc *MediaClient) cacheSuccessfulChannel(channel uint8) {
	mc.cachedChannel = channel
	mc.lastDiscoveryTime = time.Now()
	log.Printf("Cached successful channel %d for future connections", channel)
}

// isValidNocturneCompanionData checks if received data looks like NocturneCompanion state updates
func (mc *MediaClient) isValidNocturneCompanionData(data string) bool {
	// Split multiple JSON objects if concatenated
	lines := []string{data}
	if strings.Contains(data, "}\n{") {
		lines = strings.Split(data, "\n")
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Try to parse as MediaStateUpdate
		var stateUpdate utils.MediaStateUpdate
		if err := json.Unmarshal([]byte(line), &stateUpdate); err == nil {
			if stateUpdate.Type == "stateUpdate" {
				log.Printf("Found valid NocturneCompanion state update with track: %v",
					stringOrDefault(stateUpdate.Track, "Unknown"))
				return true
			}
		}
	}

	return false
}

// connectRFCOMM establishes an RFCOMM socket connection
func (mc *MediaClient) connectRFCOMM(deviceAddr [6]byte, channel uint8) (int, error) {
	log.Printf("SPP_LOG: connectRFCOMM called for channel %d", channel)
	// Create RFCOMM socket
	fd, err := syscall.Socket(AF_BLUETOOTH, SOCK_STREAM, BTPROTO_RFCOMM)
	if err != nil {
		log.Printf("SPP_LOG: connectRFCOMM failed to create socket: %v", err)
		return -1, fmt.Errorf("failed to create RFCOMM socket: %v", err)
	}

	// Setup socket address
	sockaddr := sockaddr_rc{
		family:  AF_BLUETOOTH,
		bdaddr:  deviceAddr,
		channel: channel,
	}

	// Connect to the device
	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&sockaddr)),
		uintptr(unsafe.Sizeof(sockaddr)),
	)

	if errno != 0 {
		syscall.Close(fd)
		log.Printf("SPP_LOG: connectRFCOMM failed to connect: %v", errno)
		return -1, fmt.Errorf("failed to connect to RFCOMM channel %d: %v", channel, errno)
	}
	log.Printf("SPP_LOG: connectRFCOMM succeeded for channel %d, fd: %d", channel, fd)
	return fd, nil
}

// monitorConnection monitors the RFCOMM connection and handles reconnection
func (mc *MediaClient) monitorConnection() {
	mc.heartbeatTicker = time.NewTicker(HEARTBEAT_INTERVAL)
	defer mc.heartbeatTicker.Stop()

	for {
		select {
		case <-mc.stopChan:
			return
		case <-mc.heartbeatTicker.C:
			mc.mu.RLock()
			timeSinceActivity := time.Since(mc.lastActivity)
			connected := mc.sppConnected
			mc.mu.RUnlock()

			if connected && timeSinceActivity > 2*HEARTBEAT_INTERVAL {
				log.Printf("No activity for %v - connection appears idle but stable", timeSinceActivity)
				// Note: Not sending ping commands to avoid interfering with the connection
				// NocturneCompanion will send state updates when media is active
			}

			// Clean up stale album art transfers
			mc.cleanupStaleTransfers()
		}
	}
}

// handleConnectionLoss handles connection loss and triggers reconnection
func (mc *MediaClient) handleConnectionLoss() {
	log.Println("SPP_LOG: handleConnectionLoss called")
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if !mc.sppConnected {
		log.Println("SPP_LOG: handleConnectionLoss: already handling disconnection")
		return // Already handling disconnection
	}

	log.Printf("Connection lost to NocturneCompanion, cleaning up...")
	mc.sppConnected = false

	// Clean up resources
	if mc.rfcommFd > 0 {
		log.Printf("SPP_LOG: Closing rfcommFd: %d", mc.rfcommFd)
		syscall.Close(mc.rfcommFd)
		mc.rfcommFd = -1
	}

	if mc.sppConn != nil {
		log.Println("SPP_LOG: Closing sppConn")
		mc.sppConn.Close()
		mc.sppConn = nil
	}

	if mc.heartbeatTicker != nil {
		log.Println("SPP_LOG: Stopping heartbeatTicker")
		mc.heartbeatTicker.Stop()
	}

	// Broadcast disconnection
	if mc.wsHub != nil {
		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/disconnected",
			Payload: map[string]string{
				"address": mc.targetAddress,
				"status":  "disconnected",
				"reason":  "connection_lost",
			},
		})
	}

	// Schedule reconnection attempt
	go mc.attemptReconnect()
}

func (mc *MediaClient) handleAlbumArtCommand(command *utils.MediaCommand) {
	mc.chunkMutex.Lock()
	defer mc.chunkMutex.Unlock()

	log.Printf("Received album art chunk: hash=%s, chunk=%d/%d, size=%d", 
		command.Hash, command.ChunkIndex+1, command.TotalChunks, command.ChunkSize)
	
	// Check if file already exists (for efficiency)
	albumArtDir := "/data/etc/nocturne/albumart"
	filename := fmt.Sprintf("%s.jpg", command.Hash)
	filePath := filepath.Join(albumArtDir, filename)
	
	if _, err := os.Stat(filePath); err == nil {
		log.Printf("Album art already exists: %s", filename)
		if mc.wsHub != nil {
			mc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "album_art_exists",
				Payload: map[string]interface{}{
					"hash":     command.Hash,
					"filename": filename,
					"path":     filePath,
				},
			})
		}
		// Clean up any partial transfer
		delete(mc.chunkBuffer, command.Hash)
		return
	}

	// Initialize transfer if this is the first chunk
	transfer, exists := mc.chunkBuffer[command.Hash]
	if !exists {
		transfer = &AlbumArtTransfer{
			Hash:         command.Hash,
			TotalSize:    command.Size,
			TotalChunks:  command.TotalChunks,
			ReceivedData: make(map[int]string),
			StartTime:    time.Now(),
		}
		mc.chunkBuffer[command.Hash] = transfer
		log.Printf("Started new album art transfer: %s (%d chunks expected)", command.Hash, command.TotalChunks)
	}

	// Store the chunk data
	transfer.ReceivedData[command.ChunkIndex] = command.Data
	log.Printf("Stored chunk %d/%d for %s", command.ChunkIndex+1, command.TotalChunks, command.Hash)

	// Check if we have all chunks
	if len(transfer.ReceivedData) == transfer.TotalChunks {
		log.Printf("All chunks received for %s, assembling image...", command.Hash)
		mc.assembleAndSaveAlbumArt(transfer, albumArtDir, filename, filePath)
		delete(mc.chunkBuffer, command.Hash)
	} else {
		// Broadcast progress
		if mc.wsHub != nil {
			mc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "album_art_progress",
				Payload: map[string]interface{}{
					"hash":              command.Hash,
					"chunks_received":   len(transfer.ReceivedData),
					"total_chunks":      transfer.TotalChunks,
					"progress_percent":  float64(len(transfer.ReceivedData)) / float64(transfer.TotalChunks) * 100,
				},
			})
		}
	}
}

func (mc *MediaClient) assembleAndSaveAlbumArt(transfer *AlbumArtTransfer, albumArtDir, filename, filePath string) {
	// Create album art directory if it doesn't exist
	if err := os.MkdirAll(albumArtDir, 0755); err != nil {
		log.Printf("Failed to create album art directory: %v", err)
		return
	}

	// Assemble chunks in order
	var base64Data strings.Builder
	for i := 0; i < transfer.TotalChunks; i++ {
		chunkData, exists := transfer.ReceivedData[i]
		if !exists {
			log.Printf("Missing chunk %d for %s", i, transfer.Hash)
			return
		}
		base64Data.WriteString(chunkData)
	}

	// Decode base64 data
	imageData, err := base64.StdEncoding.DecodeString(base64Data.String())
	if err != nil {
		log.Printf("Failed to decode album art data for %s: %v", transfer.Hash, err)
		return
	}

	// Verify size matches
	if len(imageData) != transfer.TotalSize {
		log.Printf("Album art size mismatch for %s: expected %d, got %d", transfer.Hash, transfer.TotalSize, len(imageData))
		return
	}

	// Save the file
	if err := ioutil.WriteFile(filePath, imageData, 0644); err != nil {
		log.Printf("Failed to save album art %s: %v", filename, err)
		return
	}

	duration := time.Since(transfer.StartTime)
	log.Printf("Album art saved successfully: %s (%d bytes, %d chunks, took %v)", 
		filename, len(imageData), transfer.TotalChunks, duration)

	// Broadcast successful album art save
	if mc.wsHub != nil {
		mc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "album_art_received",
			Payload: map[string]interface{}{
				"hash":              transfer.Hash,
				"filename":          filename,
				"path":              filePath,
				"size":              len(imageData),
				"chunks":            transfer.TotalChunks,
				"transfer_duration": duration.Milliseconds(),
			},
		})
	}
}

// cleanupStaleTransfers removes album art transfers that have timed out
func (mc *MediaClient) cleanupStaleTransfers() {
	mc.chunkMutex.Lock()
	defer mc.chunkMutex.Unlock()

	now := time.Now()
	for hash, transfer := range mc.chunkBuffer {
		if now.Sub(transfer.StartTime) > CHUNK_TIMEOUT {
			log.Printf("Cleaning up stale album art transfer: %s (started %v ago)", hash, now.Sub(transfer.StartTime))
			delete(mc.chunkBuffer, hash)
		}
	}
}

// rfcommConn wraps a raw RFCOMM file descriptor to implement net.Conn interface
type rfcommConn struct {
	fd int
}

func (rc *rfcommConn) Read(b []byte) (int, error) {
	n, err := syscall.Read(rc.fd, b)
	if err != nil {
		return n, err
	}
	return n, nil
}

func (rc *rfcommConn) Write(b []byte) (int, error) {
	n, err := syscall.Write(rc.fd, b)
	if err != nil {
		return n, err
	}
	return n, nil
}

func (rc *rfcommConn) Close() error {
	return syscall.Close(rc.fd)
}

func (rc *rfcommConn) LocalAddr() net.Addr  { return nil }
func (rc *rfcommConn) RemoteAddr() net.Addr { return nil }

func (rc *rfcommConn) SetDeadline(t time.Time) error      { return nil }
func (rc *rfcommConn) SetReadDeadline(t time.Time) error  { return nil }
func (rc *rfcommConn) SetWriteDeadline(t time.Time) error { return nil }