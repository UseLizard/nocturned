package bluetooth

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/usenocturne/nocturned/utils"
)


// BleClientV2 is the v2 implementation of the BLE client compatible with NocturneCompanion.
type BleClientV2 struct {
	conn                *dbus.Conn
	wsHub               *utils.WebSocketHub
	broadcaster         *utils.WebSocketBroadcaster
	weatherHandler      *WeatherHandler
	albumArtHandler     *AlbumArtHandler // Reference to the album art handler
	mediaStateCallback  func(*MediaState)
	mu                  sync.RWMutex
	stopChan            chan struct{}
	devicePath          dbus.ObjectPath
	commandRxChar       dbus.BusObject // Write - for receiving commands from companion
	stateTxChar         dbus.BusObject // Notify - for sending state updates to companion
	debugLogChar        dbus.BusObject // Notify - for debug logs
	deviceInfoChar      dbus.BusObject // Read - for device info
	albumArtTxChar      dbus.BusObject // Notify - for album art transfer
	isConnected         bool
	commandQueue        chan *Command
	nextMessageID       uint16
	commandHandler      *CommandHandler
}

// NewBleClientV2 creates a new BleClientV2 instance.
func NewBleClientV2(conn *dbus.Conn, wsHub *utils.WebSocketHub) *BleClientV2 {
	broadcaster := utils.NewWebSocketBroadcaster(wsHub)
	
	client := &BleClientV2{
		conn:         conn,
		wsHub:        wsHub,
		broadcaster:  broadcaster,
		weatherHandler: NewWeatherHandler(broadcaster),
		stopChan:     make(chan struct{}),
		commandQueue: make(chan *Command, 100),
		nextMessageID: 1,
	}
	
	// Initialize command handler
	client.commandHandler = NewCommandHandler(wsHub, client.sendMessage)
	
	return client
}

// SetMediaStateCallback sets the callback for media state updates
func (c *BleClientV2) SetMediaStateCallback(callback func(*MediaState)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mediaStateCallback = callback
}

// SendPlayCommand sends a play command to the companion
func (c *BleClientV2) SendPlayCommand() error {
	return c.sendMessage(MSG_CMD_PLAY, []byte{})
}

// SendPauseCommand sends a pause command to the companion
func (c *BleClientV2) SendPauseCommand() error {
	return c.sendMessage(MSG_CMD_PAUSE, []byte{})
}

// SendNextCommand sends a next track command to the companion
func (c *BleClientV2) SendNextCommand() error {
	return c.sendMessage(MSG_CMD_NEXT, []byte{})
}

// SendPreviousCommand sends a previous track command to the companion
func (c *BleClientV2) SendPreviousCommand() error {
	return c.sendMessage(MSG_CMD_PREVIOUS, []byte{})
}

// SendVolumeCommand sends a volume command to the companion
func (c *BleClientV2) SendVolumeCommand(volumePercent int) error {
	payload := CreateCommandPayload(nil, &volumePercent)
	return c.sendMessage(MSG_CMD_SET_VOLUME, payload)
}

// SendSeekCommand sends a seek command to the companion
func (c *BleClientV2) SendSeekCommand(positionMs int64) error {
	payload := CreateCommandPayload(&positionMs, nil)
	return c.sendMessage(MSG_CMD_SEEK_TO, payload)
}

// getNextMessageID returns the next message ID for request/response correlation
func (c *BleClientV2) getNextMessageID() uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	id := c.nextMessageID
	c.nextMessageID++
	if c.nextMessageID == 0 {
		c.nextMessageID = 1 // Avoid 0 as message ID
	}
	return id
}

func (c *BleClientV2) sendCommand(cmd *Command) {
	c.commandQueue <- cmd
}


// Start starts the BLE client.
func (c *BleClientV2) Start() {
	log.Println("Starting Nocturned v2 BLE client")
	go c.run()
}

// Stop stops the BLE client.
func (c *BleClientV2) Stop() {
	log.Println("Stopping Nocturned v2 BLE client")
	close(c.stopChan)
}


func (c *BleClientV2) run() {
	log.Printf("DEBUG: BLE client run() goroutine started")
	go c.processCommandQueue()

	for {
		select {
		case <-c.stopChan:
			log.Printf("DEBUG: BLE client run() received stop signal")
			return
		default:
			if !c.isConnected {
				log.Printf("DEBUG: Not connected, attempting to find device...")
				log.Println("Scanning for Nocturne device...")
				devicePath, err := c.findDevice()
				if err != nil {
					log.Printf("Failed to find device: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}

				log.Printf("Found device: %s", devicePath)
				c.devicePath = devicePath

				log.Printf("🔗 Attempting to connect to device...")
				log.Printf("DEBUG: About to call connect() method")
				if err := c.connect(); err != nil {
					log.Printf("❌ Failed to connect to device: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				log.Printf("DEBUG: connect() method completed successfully")

				c.isConnected = true
				log.Println("Connected to Nocturne device")
				log.Printf("DEBUG: Connection established, entering monitoring mode")
			} else {
				log.Printf("DEBUG: Already connected, checking health...")
				// Check connection health periodically
				if !c.checkConnectionHealth() {
					log.Println("Connection lost, attempting to reconnect...")
					c.isConnected = false
					continue
				}
				
				log.Printf("DEBUG: Connection healthy, sleeping 15 seconds")
				// Sleep to avoid tight loop when connected
				time.Sleep(15 * time.Second)
			}
		}
	}
}

func (c *BleClientV2) processCommandQueue() {
	for {
		select {
		case <-c.stopChan:
			return
		case cmd := <-c.commandQueue:
			if err := c.sendMessage(uint16(cmd.Type), cmd.Payload); err != nil {
				log.Printf("Failed to send command: %v", err)
			}
		}
	}
}

func (c *BleClientV2) sendMessage(msgType uint16, payload []byte) error {
	// TODO: Implement rate limiting

	messageID := c.getNextMessageID()
	msg := EncodeMessage(msgType, payload, messageID)

	// Send responses via the COMMAND_RX characteristic (write back to companion)
	if c.commandRxChar == nil {
		return fmt.Errorf("commandRxChar is not initialized")
	}
	
	log.Printf("📤 Sending message type %s (%d bytes) to companion", GetMessageTypeString(msgType), len(msg))
	return c.commandRxChar.Call("org.bluez.GattCharacteristic1.WriteValue", 0, msg, map[string]interface{}{}).Store()
}

func (c *BleClientV2) sendBinaryMessage(data []byte) error {
	// Send binary data directly via COMMAND_RX characteristic
	if c.commandRxChar == nil {
		return fmt.Errorf("commandRxChar is not initialized")
	}
	
	log.Printf("📤 Sending binary data (%d bytes) to companion", len(data))
	return c.commandRxChar.Call("org.bluez.GattCharacteristic1.WriteValue", 0, data, map[string]interface{}{}).Store()
}

func (c *BleClientV2) findDevice() (dbus.ObjectPath, error) {
	adapterPath, err := c.findAdapter()
	if err != nil {
		return "", err
	}

	// Start BLE discovery if not already running
	log.Println("Starting BLE discovery scan...")
	adapter := c.conn.Object(BLUEZ_BUS_NAME, adapterPath)
	if err := adapter.Call("org.bluez.Adapter1.StartDiscovery", 0).Store(); err != nil {
		log.Printf("Warning: Could not start discovery: %v", err)
	}

	// Wait a moment for devices to be discovered
	time.Sleep(2 * time.Second)

	managedObjects, err := c.getManagedObjects()
	if err != nil {
		return "", err
	}

	log.Printf("Scanning for devices... (looking for service UUID: %s)", NOCTURNE_SERVICE_UUID)
	
	var foundDevices []string
	for path, object := range managedObjects {
		if _, ok := object[BLUEZ_DEVICE_INTERFACE]; ok {
			if adapter, ok := object[BLUEZ_DEVICE_INTERFACE]["Adapter"].Value().(dbus.ObjectPath); ok && adapter == adapterPath {
				// Get device name for debugging
				deviceName := "Unknown"
				if name, ok := object[BLUEZ_DEVICE_INTERFACE]["Name"].Value().(string); ok {
					deviceName = name
				}
				
				// Get device address for debugging
				deviceAddress := "Unknown"
				if addr, ok := object[BLUEZ_DEVICE_INTERFACE]["Address"].Value().(string); ok {
					deviceAddress = addr
				}
				
				foundDevices = append(foundDevices, fmt.Sprintf("%s (%s)", deviceName, deviceAddress))
				
				// Check for NocturneCompanion by name as fallback
				if deviceName == "NocturneCompanion" {
					log.Printf("Found NocturneCompanion device by name: %s (%s)", deviceName, deviceAddress)
					return path, nil
				}
				
				// Check service UUIDs
				if uuids, ok := object[BLUEZ_DEVICE_INTERFACE]["UUIDs"].Value().([]string); ok {
					log.Printf("Device %s (%s) advertises UUIDs: %v", deviceName, deviceAddress, uuids)
					for _, uuid := range uuids {
						// Case-insensitive UUID matching
						if strings.ToLower(uuid) == strings.ToLower(NOCTURNE_SERVICE_UUID) {
							log.Printf("✅ FOUND NOCTURNE DEVICE! %s (%s) with service UUID: %s", deviceName, deviceAddress, uuid)
							return path, nil
						}
					}
				} else {
					log.Printf("Device %s (%s) - no UUIDs available", deviceName, deviceAddress)
				}
			}
		}
	}
	
	if len(foundDevices) > 0 {
		log.Printf("Found %d BLE devices, but none match NocturneCompanion: %v", len(foundDevices), foundDevices)
	} else {
		log.Println("No BLE devices found during scan")
	}

	return "", fmt.Errorf("nocturne device not found")
}

func (c *BleClientV2) findAdapter() (dbus.ObjectPath, error) {
	managedObjects, err := c.getManagedObjects()
	if err != nil {
		return "", err
	}

	for path, object := range managedObjects {
		if _, ok := object["org.bluez.Adapter1"]; ok {
			return path, nil
		}
	}

	return "", fmt.Errorf("bluetooth adapter not found")
}

func (c *BleClientV2) connect() error {
	log.Printf("🔗 Starting BLE connection process to device: %s", c.devicePath)
	
	// Check if D-Bus connection is still valid
	if c.conn == nil {
		return fmt.Errorf("D-Bus connection is nil")
	}
	
	device := c.conn.Object(BLUEZ_BUS_NAME, c.devicePath)
	if device == nil {
		return fmt.Errorf("failed to get D-Bus object for device")
	}
	
	log.Printf("🔗 Calling BlueZ Device1.Connect...")
	if err := device.Call("org.bluez.Device1.Connect", 0).Store(); err != nil {
		return fmt.Errorf("failed to connect to device: %w", err)
	}
	
	log.Printf("✅ BlueZ Device1.Connect completed successfully")

	// Wait for services to be discovered
	log.Println("Waiting for service discovery...")
	time.Sleep(5 * time.Second)
	log.Println("Service discovery wait complete, proceeding with characteristic discovery...")

	// Discover services and characteristics
	managedObjects, err := c.getManagedObjects()
	if err != nil {
		return err
	}

	log.Printf("📋 Discovering characteristics for device: %s", c.devicePath)
	
	// First find the Nocturne service
	var nocturneServicePath dbus.ObjectPath
	for path, object := range managedObjects {
		if serviceInterface, ok := object["org.bluez.GattService1"]; ok {
			if devicePath, ok := serviceInterface["Device"].Value().(dbus.ObjectPath); ok && devicePath == c.devicePath {
				if serviceUUID, ok := serviceInterface["UUID"].Value().(string); ok {
					if strings.ToLower(serviceUUID) == strings.ToLower(NOCTURNE_SERVICE_UUID) {
						nocturneServicePath = path
						log.Printf("✅ Found Nocturne service at path: %s", path)
						break
					}
				}
			}
		}
	}
	
	if nocturneServicePath == "" {
		return fmt.Errorf("Nocturne service not found")
	}
	
	// Now find characteristics within the Nocturne service
	for path, object := range managedObjects {
		if _, ok := object["org.bluez.GattCharacteristic1"]; ok {
			if service, ok := object["org.bluez.GattCharacteristic1"]["Service"].Value().(dbus.ObjectPath); ok && service == nocturneServicePath {
				uuid, _ := object["org.bluez.GattCharacteristic1"]["UUID"].Value().(string)
				log.Printf("🔍 Found characteristic: %s at path %s", uuid, path)
						
				switch strings.ToLower(uuid) {
				case strings.ToLower(COMMAND_RX_CHARACTERISTIC_UUID):
					c.commandRxChar = c.conn.Object(BLUEZ_BUS_NAME, path)
					log.Printf("✅ Set commandRxChar: %s", uuid)
				case strings.ToLower(STATE_TX_CHARACTERISTIC_UUID):
					c.stateTxChar = c.conn.Object(BLUEZ_BUS_NAME, path)
					log.Printf("✅ Set stateTxChar: %s", uuid)
				case strings.ToLower(DEBUG_LOG_CHARACTERISTIC_UUID):
					c.debugLogChar = c.conn.Object(BLUEZ_BUS_NAME, path)
					log.Printf("✅ Set debugLogChar: %s", uuid)
				case strings.ToLower(DEVICE_INFO_CHARACTERISTIC_UUID):
					c.deviceInfoChar = c.conn.Object(BLUEZ_BUS_NAME, path)
					log.Printf("✅ Set deviceInfoChar: %s", uuid)
				case strings.ToLower(ALBUM_ART_TX_CHARACTERISTIC_UUID):
					c.albumArtTxChar = c.conn.Object(BLUEZ_BUS_NAME, path)
					log.Printf("✅ Set albumArtTxChar: %s", uuid)
				default:
					log.Printf("❓ Unknown characteristic: %s", uuid)
				}
			}
		}
	}

	// Log what we found
	log.Printf("📊 Characteristic discovery results:")
	log.Printf("   📥 commandRxChar: %v", c.commandRxChar != nil)
	log.Printf("   📤 stateTxChar: %v", c.stateTxChar != nil)
	log.Printf("   🎨 albumArtTxChar: %v", c.albumArtTxChar != nil)
	log.Printf("   🐛 debugLogChar: %v", c.debugLogChar != nil)
	log.Printf("   ℹ️  deviceInfoChar: %v", c.deviceInfoChar != nil)

	// Only require the essential characteristics for basic communication
	if c.commandRxChar == nil || c.stateTxChar == nil {
		log.Printf("❌ Missing essential characteristics - commandRx:%v, stateTx:%v", 
			c.commandRxChar != nil, c.stateTxChar != nil)
		return fmt.Errorf("failed to discover essential characteristics (commandRx and stateTx)")
	}
	
	// Album art is optional but log if missing
	if c.albumArtTxChar == nil {
		log.Printf("⚠️  Album art characteristic not found - album art transfer will not work")
	}
	
	log.Printf("✅ Essential characteristics discovered successfully")

	log.Println("🔧 Setting up notifications and listeners...")
	
	// Enable notifications on characteristics that support it
	if err := c.enableNotifications(); err != nil {
		log.Printf("⚠️ Failed to enable notifications: %v", err)
		// Don't fail connection for notification issues
	} else {
		log.Println("✅ Notifications enabled successfully")
	}

	// Start listening for notifications
	if err := c.startNotifications(); err != nil {
		log.Printf("❌ Failed to start notification listeners: %v", err)
		return err
	}
	
	log.Println("✅ BLE connection setup complete")
	return nil
}

func (c *BleClientV2) enableNotifications() error {
	log.Println("Setting up BLE notifications...")
	
	// Use BlueZ's StartNotify method instead of writing CCCD directly
	// BlueZ will handle the CCCD write automatically with proper permissions
	
	// NocturneCompanion requires BOTH subscriptions to consider device "ready"
	subscriptionsEnabled := 0
	
	// Subscribe to STATE_TX notifications (commands, state updates, time sync)
	if c.stateTxChar != nil {
		log.Printf("🔧 Enabling notifications on STATE_TX characteristic...")
		err := c.stateTxChar.Call("org.bluez.GattCharacteristic1.StartNotify", 0).Store()
		if err != nil {
			log.Printf("Warning: Failed to enable notifications on stateTx: %v", err)
		} else {
			log.Printf("✅ Enabled notifications on STATE_TX characteristic")
			subscriptionsEnabled++
		}
	}
	
	// Subscribe to ALBUM_ART_TX notifications (album art transfer)
	if c.albumArtTxChar != nil {
		log.Printf("🔧 Enabling notifications on ALBUM_ART_TX characteristic...")
		err := c.albumArtTxChar.Call("org.bluez.GattCharacteristic1.StartNotify", 0).Store()
		if err != nil {
			log.Printf("Warning: Failed to enable notifications on albumArtTx: %v", err)
		} else {
			log.Printf("✅ Enabled notifications on ALBUM_ART_TX characteristic")
			subscriptionsEnabled++
		}
	}
	
	log.Printf("📊 Notification setup complete: %d/%d subscriptions enabled", subscriptionsEnabled, 2)
	if subscriptionsEnabled == 2 {
		log.Printf("✅ All required subscriptions active - device should be considered 'ready' by companion")
	} else {
		log.Printf("⚠️ Missing subscriptions - companion may not send regular updates")
	}
	
	return nil
}


func (c *BleClientV2) startNotifications() error {
	log.Println("Setting up notification listeners...")
	
	// Listen for property changes on GATT characteristics
	rule := "type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',arg0='org.bluez.GattCharacteristic1'"
	call := c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule)
	if call.Err != nil {
		return fmt.Errorf("failed to add match signal: %w", call.Err)
	}

	go c.handleNotifications()

	return nil
}

func (c *BleClientV2) handleNotifications() {
	sigChan := make(chan *dbus.Signal, 10)
	c.conn.Signal(sigChan)

	for sig := range sigChan {
		select {
		case <-c.stopChan:
			return
		default:
		}
		
		if sig.Name == "org.freedesktop.DBus.Properties.PropertiesChanged" {
			// Check if this is a GATT characteristic property change
			if len(sig.Body) >= 1 {
				if interfaceName, ok := sig.Body[0].(string); ok && interfaceName == "org.bluez.GattCharacteristic1" {
					// Check if this signal is from our STATE_TX or ALBUM_ART_TX characteristic
					charPath := string(sig.Path)
					isOurCharacteristic := false
					
					if c.stateTxChar != nil && string(c.stateTxChar.Path()) == charPath {
						isOurCharacteristic = true
						log.Printf("📨 Received data on STATE_TX characteristic")
					} else if c.albumArtTxChar != nil && string(c.albumArtTxChar.Path()) == charPath {
						isOurCharacteristic = true
						log.Printf("📨 Received data on ALBUM_ART_TX characteristic")
					}
					
					if !isOurCharacteristic {
						continue
					}
					
					if len(sig.Body) >= 2 {
						if changedProps, ok := sig.Body[1].(map[string]dbus.Variant); ok {
							if valueVariant, exists := changedProps["Value"]; exists {
								if value, ok := valueVariant.Value().([]byte); ok {
									log.Printf("📨 Received notification from %s: %d bytes", charPath, len(value))
									
									// Debug: Log first 16 bytes for header analysis
									if len(value) >= 16 {
										log.Printf("📨 First 16 bytes: %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x %02x",
											value[0], value[1], value[2], value[3], value[4], value[5], value[6], value[7],
											value[8], value[9], value[10], value[11], value[12], value[13], value[14], value[15])
									}
									
									c.handleNotification(value)
								}
							}
						}
					}
				}
			}
		}
	}
}

func (c *BleClientV2) handleNotification(data []byte) {
	// Check if this is a JSON message (starts with '{')
	if len(data) > 0 && data[0] == '{' {
		log.Printf("📨 Received JSON message instead of binary protocol: %s", string(data))
		c.handleJSONNotification(data)
		return
	}

	if len(data) < HEADER_SIZE {
		log.Printf("Received invalid notification data: too short (%d bytes)", len(data))
		return
	}

	// Decode the binary protocol v2 message
	messageType, payload, messageID, err := DecodeMessage(data)
	if err != nil {
		log.Printf("Failed to decode notification: %v", err)
		return
	}

	log.Printf("Received message: type=%s (0x%04x), msgID=%d, payload=%d bytes", 
		GetMessageTypeString(messageType), messageType, messageID, len(payload))

	switch messageType {
	// System messages (0x00xx) - usually from companion to client
	case MSG_TIME_SYNC:
		log.Printf("📅 Received TimeSync from companion (%d bytes)", len(payload))
		
		// Parse the time sync payload
		timestampMs, timezone, err := ParseTimeSyncPayload(payload)
		if err != nil {
			log.Printf("Error parsing time sync payload: %v", err)
			break
		}
		
		// Forward time sync to WebSocket clients using broadcaster
		c.broadcaster.BroadcastTimeSync(timestampMs, timezone)
		
	case MSG_GET_CAPABILITIES:
		c.commandHandler.HandleCapabilitiesRequest()
		
	// Command messages (0x01xx) - requests for actions
	case MSG_CMD_PLAY:
		c.commandHandler.HandlePlayCommand(payload)
	case MSG_CMD_PAUSE:
		c.commandHandler.HandlePauseCommand(payload)
	case MSG_CMD_NEXT:
		c.commandHandler.HandleNextCommand(payload)
	case MSG_CMD_PREVIOUS:
		c.commandHandler.HandlePreviousCommand(payload)
	case MSG_CMD_SEEK_TO:
		c.commandHandler.HandleSeekCommand(payload)
	case MSG_CMD_SET_VOLUME:
		c.commandHandler.HandleVolumeCommand(payload)
	case MSG_CMD_REQUEST_STATE:
		c.commandHandler.HandleStateRequest(payload)
	case MSG_CMD_REQUEST_TIMESTAMP:
		c.commandHandler.HandleTimeSyncRequest()
	case MSG_CMD_ALBUM_ART_QUERY:
		c.commandHandler.HandleAlbumArtRequest(payload)
		
	// State messages (0x02xx) - media state from companion
	case MSG_STATE_FULL:
		// Parse the FullState payload
		if mediaState, err := ParseFullStatePayload(payload); err != nil {
			log.Printf("❌ Failed to parse FullState payload: %v", err)
		} else {
			// Update manager state through callback
			c.mu.RLock()
			callback := c.mediaStateCallback
			c.mu.RUnlock()
			
			if callback != nil {
				callback(mediaState)
			}
		}
		
		// Continue to forward state updates to WebSocket clients using broadcaster
		c.broadcaster.BroadcastMediaStateUpdate(payload)
		
	// Album art messages (0x03xx) - album art data from companion
	case MSG_ALBUM_ART_START:
		c.handleAlbumArtStart(payload)
		// Broadcast start event (but not the raw data)
		c.broadcaster.BroadcastAlbumArtUpdate(messageType, nil)
	case MSG_ALBUM_ART_CHUNK:
		c.handleAlbumArtChunk(uint16(messageID), payload)
		// Don't broadcast individual chunks to avoid overwhelming WebSocket
	case MSG_ALBUM_ART_END:
		c.handleAlbumArtEnd(payload)
		// Broadcast completion event (but not the raw data)
		c.broadcaster.BroadcastAlbumArtUpdate(messageType, nil)
	case MSG_ALBUM_ART_AVAILABLE_QUERY:
		c.handleAlbumArtAvailabilityQuery(payload)
		
	// Weather messages (0x05xx) - Extended protocol from NocturneCompanion
	case MSG_WEATHER_START:
		c.weatherHandler.HandleWeatherStart(payload)
	case MSG_WEATHER_CHUNK:
		c.weatherHandler.HandleWeatherChunk(uint32(messageID), payload)
	case MSG_WEATHER_END:
		c.weatherHandler.HandleWeatherEnd(payload)
		
	default:
		log.Printf("Received unhandled message type: 0x%04x (%s)", messageType, GetMessageTypeString(messageType))
	}
}

// checkConnectionHealth verifies the BLE connection is still active
func (c *BleClientV2) checkConnectionHealth() bool {
	if c.devicePath == "" {
		return false
	}
	
	// Check if device is still connected via D-Bus
	device := c.conn.Object(BLUEZ_BUS_NAME, c.devicePath)
	var connected bool
	err := device.Call("org.freedesktop.DBus.Properties.Get", 0, "org.bluez.Device1", "Connected").Store(&connected)
	if err != nil {
		log.Printf("Failed to check connection status: %v", err)
		return false
	}
	
	return connected
}

// sendPlayStateUpdate sends a play state update - this will be refactored in next step









func (c *BleClientV2) sendPlayStateUpdate() error {
	// This method is deprecated and unused - nocturned-v2 only forwards commands, not state
	// State updates are handled by nocturne-ui via WebSocket communication
	// This method is kept for potential future use but should not be called
	log.Printf("⚠️ sendPlayStateUpdate called - this method is deprecated in nocturned-v2")
	log.Printf("⚠️ State updates should be handled by nocturne-ui via WebSocket")
	return fmt.Errorf("sendPlayStateUpdate is deprecated - use WebSocket for state updates")
}

func (c *BleClientV2) sendAlbumArt(trackID string, data []byte) error {
	chunkSize := 400 // 400 bytes per chunk
	totalChunks := (len(data) + chunkSize - 1) / chunkSize
	
	// Calculate SHA-256 checksum
	checksum := CalculateSHA256(data)

	// Send start message with SHA-256 checksum
	startPayload := CreateAlbumArtStartPayload(checksum, uint32(totalChunks), uint32(len(data)), trackID)
	if err := c.sendMessage(MSG_ALBUM_ART_START, startPayload); err != nil {
		return fmt.Errorf("failed to send album art start message: %w", err)
	}

	// Send chunks
	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}

		chunkPayload := CreateAlbumArtChunkPayload(uint32(i), data[start:end])
		if err := c.sendMessage(MSG_ALBUM_ART_CHUNK, chunkPayload); err != nil {
			return fmt.Errorf("failed to send album art chunk: %w", err)
		}

		// Small delay between chunks to avoid overwhelming the receiver
		time.Sleep(10 * time.Millisecond)
	}

	// Send end message
	endPayload := CreateAlbumArtEndPayload(checksum, true)
	if err := c.sendMessage(MSG_ALBUM_ART_END, endPayload); err != nil {
		return fmt.Errorf("failed to send album art end message: %w", err)
	}

	return nil
}

// handleAlbumArtStart handles album art start messages
func (c *BleClientV2) handleAlbumArtStart(payload []byte) {
	startPayload, err := ParseAlbumArtStartPayload(payload)
	if err != nil {
		log.Printf("Failed to parse album art start payload: %v", err)
		return
	}
	
	// Convert checksum to hex string
	hash := fmt.Sprintf("%x", startPayload.Checksum)
	
	log.Printf("🎨 Starting album art transfer: hash=%s, chunks=%d, size=%d", 
		hash, startPayload.TotalChunks, startPayload.ImageSize)
	
	// Start the transfer in the album art handler with compression support
	if c.albumArtHandler != nil {
		if startPayload.IsGzipCompressed && startPayload.CompressedSize > 0 {
			err := c.albumArtHandler.StartTransferWithCompression(
				hash, 
				int(startPayload.TotalChunks), 
				int(startPayload.ImageSize),
				int(startPayload.CompressedSize),
				startPayload.IsGzipCompressed,
			)
			if err != nil {
				log.Printf("Failed to start compressed album art transfer: %v", err)
			}
		} else {
			err := c.albumArtHandler.StartTransfer(hash, int(startPayload.TotalChunks), int(startPayload.ImageSize))
			if err != nil {
				log.Printf("Failed to start album art transfer: %v", err)
			}
		}
	}
}

// handleAlbumArtChunk handles album art chunk messages
func (c *BleClientV2) handleAlbumArtChunk(chunkIndex uint16, payload []byte) {
	log.Printf("🎨 Received album art chunk %d (%d bytes)", chunkIndex, len(payload))
	
	// Handle the album art chunk
	if c.albumArtHandler != nil {
		activeTransfers := c.albumArtHandler.GetActiveTransfers()
		
		// Find the active transfer (assuming only one at a time for simplicity)
		for hash, _ := range activeTransfers {
			err := c.albumArtHandler.ReceiveChunk(hash, int(chunkIndex), payload)
			if err != nil {
				log.Printf("Failed to receive album art chunk: %v", err)
			}
			break
		}
	}
}

// handleAlbumArtEnd handles album art end messages
func (c *BleClientV2) handleAlbumArtEnd(payload []byte) {
	endPayload, err := ParseAlbumArtEndPayload(payload)
	if err != nil {
		log.Printf("Failed to parse album art end payload: %v", err)
		return
	}
	
	// Convert checksum to hex string
	hash := fmt.Sprintf("%x", endPayload.Checksum)
	
	log.Printf("🎨 Album art transfer ended: hash=%s, success=%t", hash, endPayload.Success)
	
	// The album art handler will automatically complete the transfer when all chunks are received
}

// handleAlbumArtAvailabilityQuery handles album art availability queries from companion
func (c *BleClientV2) handleAlbumArtAvailabilityQuery(payload []byte) {
	queryData, err := ParseAlbumArtAvailabilityQuery(payload)
	if err != nil {
		log.Printf("❌ Failed to parse album art availability query: %v", err)
		return
	}
	
	log.Printf("🎨 Album art availability query: checksum=%s, trackID=%s, compressed=%v, size=%d", 
		queryData.Checksum, queryData.TrackID, queryData.IsGzipCompressed, queryData.CompressedSize)
	
	// Check if album art file exists locally
	albumArtPath := fmt.Sprintf("/var/album_art/%s.webp", queryData.Checksum)
	needed := true
	
	if _, err := os.Stat(albumArtPath); err == nil {
		log.Printf("🎨 Album art already exists at %s - not needed", albumArtPath)
		needed = false
	} else {
		log.Printf("🎨 Album art not found at %s - needed", albumArtPath)
	}
	
	// Send y/n response
	responsePayload := CreateAlbumArtNeededResponse(needed)
	if err := c.sendMessage(MSG_ALBUM_ART_NEEDED_RESPONSE, responsePayload); err != nil {
		log.Printf("❌ Failed to send album art needed response: %v", err)
		return
	}
	
	log.Printf("📤 Sent album art needed response: %s", string(responsePayload))
}

func (c *BleClientV2) getManagedObjects() (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	obj := c.conn.Object(BLUEZ_BUS_NAME, "/")
	var managedObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&managedObjects)
	if err != nil {
		return nil, fmt.Errorf("failed to get managed objects: %w", err)
	}
	return managedObjects, nil
}

// handleJSONNotification handles JSON messages from companion (legacy/fallback)
func (c *BleClientV2) handleJSONNotification(data []byte) {
	var jsonMsg map[string]interface{}
	if err := json.Unmarshal(data, &jsonMsg); err != nil {
		log.Printf("❌ Failed to parse JSON notification: %v", err)
		return
	}
	
	msgType, ok := jsonMsg["type"].(string)
	if !ok {
		log.Printf("❌ JSON message missing 'type' field")
		return
	}
	
	log.Printf("📨 Processing JSON message type: %s", msgType)
	
	switch msgType {
	case "album_art_not_available":
		// Handle album art not available response
		trackID, _ := jsonMsg["track_id"].(string)
		reason, _ := jsonMsg["reason"].(string)
		log.Printf("🎨 Album art not available for track %s: %s", trackID, reason)
		
		// This means the companion doesn't have the requested album art
		// We could implement retry logic or fallback behavior here
		
	case "album_art_error":
		// Handle album art error response
		reason, _ := jsonMsg["reason"].(string)
		log.Printf("🎨 Album art error: %s", reason)
		
	default:
		log.Printf("📨 Unhandled JSON message type: %s", msgType)
	}
}