package bluetooth

import (
	//"crypto/sha256"
	"encoding/base64"
	//"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/usenocturne/nocturned/utils"
)

// CommandQueueItem represents a command in the queue
type CommandQueueItem struct {
	id           string
	command      string
	valueMs      *int
	valuePercent *int
	hash         string
	timestamp    time.Time
	retryCount   int
	callback     chan error
}


type BleClient struct {
	connected         bool
	targetAddress     string
	wsHub             *utils.WebSocketHub
	btManager         *BluetoothManager
	mu                sync.RWMutex
	reconnectAttempts int
	currentState      *utils.MediaStateUpdate
	stopChan          chan struct{}

	// BLE specific fields using D-Bus (matching existing codebase pattern)
	conn              *dbus.Conn
	devicePath        dbus.ObjectPath
	servicePath       dbus.ObjectPath
	commandRxCharPath dbus.ObjectPath
	responseTxCharPath dbus.ObjectPath
	debugLogCharPath  dbus.ObjectPath
	deviceInfoCharPath dbus.ObjectPath
	albumArtCharPath  dbus.ObjectPath
	bleConnected      bool
	mtu               uint16
	
	// Rate limiting for commands
	lastCommandTime   time.Time
	commandMutex      sync.Mutex
	
	// Command queue
	commandQueue      chan *CommandQueueItem
	commandQueueMu    sync.Mutex
	activeCommand     *CommandQueueItem
	lastCommandID     string
	
	// Connection state tracking
	fullyConnected    bool
	capsReceived      bool
	
	// Polling for state updates
	pollingTicker     *time.Ticker
	lastPolledValue   []byte
	
	// Album art transfer management
	albumArtBuffer    []byte
	albumArtChunks    map[int][]byte  // Track chunks by index
	albumArtTrackID   string
	albumArtSize      int
	albumArtChecksum  string
	albumArtReceiving bool
	albumArtTotalChunks int           // Total expected chunks
	albumArtStartTime time.Time      // Track transfer timing
	albumArtMutex     sync.Mutex
	
	// Binary protocol support
	supportsBinaryProtocol bool
	binaryProtocolVersion  byte
	
	// Album art callback
	albumArtCallback func([]byte)
	
	// Test mode album art management (separate from production)
	testAlbumArtBuffer    []byte
	testAlbumArtChunks    map[int][]byte
	testAlbumArtSize      int
	testAlbumArtChecksum  string
	testAlbumArtReceiving bool
	testAlbumArtTotalChunks int
	testAlbumArtStartTime time.Time
	testAlbumArtMutex     sync.Mutex
	testAlbumArtLastProgressUpdate int // Track last progress percentage for throttling
}

func NewBleClient(btManager *BluetoothManager, wsHub *utils.WebSocketHub) *BleClient {
	return &BleClient{
		btManager:     btManager,
		wsHub:         wsHub,
		conn:          btManager.conn, // Reuse existing D-Bus connection
		stopChan:      make(chan struct{}),
		mtu:           DefaultMTU,
		commandQueue:  make(chan *CommandQueueItem, 100), // Buffer up to 100 commands
	}
}

// SetAlbumArtCallback sets the callback function for when album art is received
func (bc *BleClient) SetAlbumArtCallback(callback func([]byte)) {
	bc.albumArtCallback = callback
}

func (bc *BleClient) DiscoverAndConnect() error {
	log.Println("BLE_LOG: DiscoverAndConnect called")
	// Reduced delay - adapter should already be initialized
	time.Sleep(1 * time.Second)
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Broadcast scan start
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/ble_scan_start",
			Payload: map[string]interface{}{
				"timestamp": time.Now().Unix(),
			},
		})
	}

	address, err := bc.discoverNocturneCompanion()
	if err != nil {
		log.Printf("BLE_LOG: discoverNocturneCompanion failed: %v", err)
		return fmt.Errorf("failed to discover NocturneCompanion: %v", err)
	}
	log.Printf("BLE_LOG: discoverNocturneCompanion succeeded, address: %s", address)

	bc.targetAddress = address
	return bc.connectToDevice()
}

func (bc *BleClient) discoverNocturneCompanion() (string, error) {
	log.Println("BLE_LOG: discoverNocturneCompanion called")

	// First check paired devices
	devices, err := bc.btManager.GetDevices()
	if err != nil {
		log.Printf("BLE_LOG: GetDevices failed: %v", err)
		return "", fmt.Errorf("failed to get bluetooth devices: %v", err)
	}

	log.Printf("🔍 BLE_LOG: Checking %d paired devices for NocturneCompanion...", len(devices))

	// Check paired devices first
	for _, device := range devices {
		log.Printf("BLE_LOG: Checking paired device: %s (%s)", device.Name, device.Address)
		
		// Check by name
		if device.Name == DeviceName {
			log.Printf("BLE_LOG: Found paired device with matching name: %s (%s)", 
				device.Name, device.Address)
			
			// Broadcast device found
			if bc.wsHub != nil {
				bc.wsHub.Broadcast(utils.WebSocketEvent{
					Type: "media/ble_device_found",
					Payload: map[string]interface{}{
						"name":      device.Name,
						"address":   device.Address,
						"paired":    true,
						"source":    "paired_devices",
						"timestamp": time.Now().Unix(),
					},
				})
			}
			
			return device.Address, nil
		}
	}

	// If not found in paired devices, start BLE scanning
	log.Println("BLE_LOG: No paired NocturneCompanion found, starting BLE scan...")
	
	// Start discovery
	if err := bc.startDiscovery(); err != nil {
		log.Printf("BLE_LOG: Failed to start discovery: %v", err)
		return "", fmt.Errorf("failed to start discovery: %v", err)
	}
	
	// Monitor for devices during scan
	foundDevice := make(chan string, 1)
	stopScan := make(chan struct{})
	
	go bc.monitorDiscoveredDevices(foundDevice, stopScan)
	
	// Wait for device discovery or timeout
	select {
	case address := <-foundDevice:
		log.Printf("BLE_LOG: Found NocturneCompanion during scan: %s", address)
		bc.stopDiscovery()
		return address, nil
		
	case <-time.After(time.Duration(ScanTimeoutSec) * time.Second):
		close(stopScan)
		bc.stopDiscovery()
		log.Println("BLE_LOG: Scan timeout - no NocturneCompanion device found")
		return "", fmt.Errorf("no NocturneCompanion device found during scan")
	}
}

func (bc *BleClient) deviceSupportsGatt(address string) bool {
	log.Printf("BLE_LOG: deviceSupportsGatt called for address: %s", address)
	devicePath := formatDevicePath(bc.btManager.adapter, address)
	
	// Check if device has GATT services by looking for our specific service
	objects := make(map[dbus.ObjectPath]map[string]map[string]dbus.Variant)
	obj := bc.conn.Object(BLUEZ_BUS_NAME, "/")
	if err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		log.Printf("Failed to get managed objects for GATT check: %v", err)
		return false
	}

	// Look for GATT services under this device
	devicePathStr := string(devicePath)
	for path, interfaces := range objects {
		pathStr := string(path)
		if strings.HasPrefix(pathStr, devicePathStr+"/service") {
			if _, hasGattService := interfaces["org.bluez.GattService1"]; hasGattService {
				log.Printf("BLE_LOG: Device %s supports GATT", address)
				return true
			}
		}
	}
	
	log.Printf("BLE_LOG: Device %s does not support GATT", address)
	return false
}

func (bc *BleClient) deviceHasNordicUartService(address string) bool {
	log.Printf("BLE_LOG: deviceHasNordicUartService called for address: %s", address)
	devicePath := formatDevicePath(bc.btManager.adapter, address)
	
	// Get all managed objects to find services
	objects := make(map[dbus.ObjectPath]map[string]map[string]dbus.Variant)
	obj := bc.conn.Object(BLUEZ_BUS_NAME, "/")
	if err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		log.Printf("Failed to get managed objects for Nordic UART service check: %v", err)
		return false
	}

	// Look for the Nordic UART Service UUID under this device
	devicePathStr := string(devicePath)
	for path, interfaces := range objects {
		pathStr := string(path)
		
		// Check if this is a service under our device
		if !strings.HasPrefix(pathStr, devicePathStr+"/service") {
			continue
		}
		
		// Check if it's a GATT service with our Nordic UART Service UUID
		if svcIface, hasGattService := interfaces["org.bluez.GattService1"]; hasGattService {
			if uuidVariant, ok := svcIface["UUID"]; ok {
				uuid := uuidVariant.Value().(string)
				if strings.EqualFold(uuid, NocturneServiceUUID) {
					log.Printf("BLE_LOG: Device %s has Nordic UART Service", address)
					return true
				}
			}
		}
	}
	
	log.Printf("BLE_LOG: Device %s does not have Nordic UART Service", address)
	return false
}

func (bc *BleClient) startDiscovery() error {
	log.Println("BLE_LOG: Starting BLE discovery...")
	
	// Set discovery filter for BLE devices
	adapter := bc.conn.Object(BLUEZ_BUS_NAME, bc.btManager.adapter)
	
	// Set discovery filter to scan for BLE devices only
	filter := map[string]interface{}{
		"Transport": "le",  // Low Energy only
		"DuplicateData": false,
	}
	
	if err := adapter.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, filter).Err; err != nil {
		log.Printf("BLE_LOG: Failed to set discovery filter: %v", err)
		// Continue anyway, some adapters don't support filters
	}
	
	// Start discovery
	if err := adapter.Call("org.bluez.Adapter1.StartDiscovery", 0).Err; err != nil {
		return fmt.Errorf("failed to start discovery: %v", err)
	}
	
	log.Println("BLE_LOG: Discovery started successfully")
	return nil
}

func (bc *BleClient) stopDiscovery() {
	log.Println("BLE_LOG: Stopping BLE discovery...")
	
	adapter := bc.conn.Object(BLUEZ_BUS_NAME, bc.btManager.adapter)
	if err := adapter.Call("org.bluez.Adapter1.StopDiscovery", 0).Err; err != nil {
		log.Printf("BLE_LOG: Failed to stop discovery: %v", err)
	}
}

func (bc *BleClient) monitorDiscoveredDevices(foundDevice chan<- string, stopScan <-chan struct{}) {
	log.Println("BLE_LOG: Starting device discovery monitor...")
	
	// Poll for discovered devices
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	
	checkedDevices := make(map[string]bool)
	
	for {
		select {
		case <-stopScan:
			log.Println("BLE_LOG: Stopping device monitor")
			return
			
		case <-ticker.C:
			// Get all current devices
			objects := make(map[dbus.ObjectPath]map[string]map[string]dbus.Variant)
			obj := bc.conn.Object(BLUEZ_BUS_NAME, "/")
			if err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
				log.Printf("BLE_LOG: Failed to get managed objects during scan: %v", err)
				continue
			}
			
			// Look for new devices
			for path, interfaces := range objects {
				pathStr := string(path)
				
				// Check if this is a device under our adapter
				if !strings.HasPrefix(pathStr, string(bc.btManager.adapter)+"/dev_") {
					continue
				}
				
				// Get device interface
				if deviceIface, hasDevice := interfaces[BLUEZ_DEVICE_INTERFACE]; hasDevice {
					// Get device address
					if addrVariant, ok := deviceIface["Address"]; ok {
						address := addrVariant.Value().(string)
						
						// Skip if already checked
						if checkedDevices[address] {
							continue
						}
						checkedDevices[address] = true
						
						// Get device properties
						var name string
						var uuids []string
						
						if nameVariant, ok := deviceIface["Name"]; ok {
							name = nameVariant.Value().(string)
						}
						
						if uuidsVariant, ok := deviceIface["UUIDs"]; ok {
							if uuidArray, ok := uuidsVariant.Value().([]string); ok {
								uuids = uuidArray
							}
						}
						
						log.Printf("BLE_LOG: Discovered device: %s (%s) with %d UUIDs", name, address, len(uuids))
						
						// Check if this is our device by name
						if name == DeviceName {
							log.Printf("BLE_LOG: Found NocturneCompanion by name: %s", address)
							
							// Broadcast device found
							if bc.wsHub != nil {
								bc.wsHub.Broadcast(utils.WebSocketEvent{
									Type: "media/ble_device_found",
									Payload: map[string]interface{}{
										"name":      name,
										"address":   address,
										"paired":    false,
										"source":    "ble_scan",
										"timestamp": time.Now().Unix(),
									},
								})
							}
							
							foundDevice <- address
							return
						}
						
						// Check if device advertises our service UUID
						for _, uuid := range uuids {
							if strings.EqualFold(uuid, NocturneServiceUUID) {
								log.Printf("BLE_LOG: Found device with Nordic UART Service: %s (%s)", name, address)
								
								// Broadcast device found
								if bc.wsHub != nil {
									bc.wsHub.Broadcast(utils.WebSocketEvent{
										Type: "media/ble_device_found",
										Payload: map[string]interface{}{
											"name":      name,
											"address":   address,
											"paired":    false,
											"source":    "ble_scan_service",
											"timestamp": time.Now().Unix(),
										},
									})
								}
								
								foundDevice <- address
								return
							}
						}
					}
				}
			}
		}
	}
}

func (bc *BleClient) connectToDevice() error {
	log.Println("BLE_LOG: connectToDevice called")
	if bc.targetAddress == "" {
		log.Println("BLE_LOG: connectToDevice failed: no target address")
		return fmt.Errorf("no target address set")
	}

	log.Printf("Connecting to BLE NocturneCompanion at %s", bc.targetAddress)

	// Establish BLE GATT connection
	if err := bc.connectBLE(); err != nil {
		log.Printf("BLE_LOG: connectBLE failed: %v", err)
		return fmt.Errorf("failed to establish BLE connection: %v", err)
	}

	bc.connected = true
	bc.reconnectAttempts = 0

	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/ble_connected",
			Payload: map[string]string{
				"address": bc.targetAddress,
				"status":  "connected",
			},
		})
	}

	log.Printf("Successfully connected to BLE NocturneCompanion")
	return nil
}

func (bc *BleClient) connectBLE() error {
	log.Println("BLE_LOG: connectBLE called")

	// Re-initialize stopChan for new connection
	bc.stopChan = make(chan struct{})

	// Set device path
	bc.devicePath = formatDevicePath(bc.btManager.adapter, bc.targetAddress)
	
	// Get device object
	obj := bc.conn.Object(BLUEZ_BUS_NAME, bc.devicePath)
	
	// Check if device exists
	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, BLUEZ_DEVICE_INTERFACE).Store(&props); err != nil {
		return fmt.Errorf("failed to get device properties: %v", err)
	}

	// Check connection status
	connected := false
	if connectedProp, ok := props["Connected"]; ok {
		connected = connectedProp.Value().(bool)
	}

	if !connected {
		log.Println("BLE_LOG: Device not connected, attempting connection...")
		
		// For BLE devices, we need to connect first to discover GATT services
		if err := obj.Call("org.bluez.Device1.Connect", 0).Err; err != nil {
			// If already connecting, wait a bit
			if strings.Contains(err.Error(), "InProgress") {
				log.Println("BLE_LOG: Connection already in progress, waiting...")
				time.Sleep(500 * time.Millisecond)
			} else {
				return fmt.Errorf("failed to connect to device: %v", err)
			}
		}
		
		// Wait for connection to establish
		maxAttempts := 10
		for i := 0; i < maxAttempts; i++ {
			time.Sleep(1 * time.Second)
			
			// Check connection status again
			if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, BLUEZ_DEVICE_INTERFACE, "Connected").Store(&connected); err == nil && connected {
				log.Println("BLE_LOG: Device connected successfully")
				break
			}
			
			if i == maxAttempts-1 {
				return fmt.Errorf("timeout waiting for device connection")
			}
		}
	} else {
		log.Println("BLE_LOG: Device already connected")
	}

	bc.bleConnected = true

	// Try to optimize connection parameters for lower latency
	if err := bc.optimizeConnectionParameters(); err != nil {
		log.Printf("BLE_LOG: Warning - failed to optimize connection parameters: %v", err)
		// Continue anyway - this is not critical
	}

	// Wait briefly for services to be resolved
	log.Println("BLE_LOG: Waiting for GATT services to be resolved...")
	time.Sleep(500 * time.Millisecond)

	// Discover and connect to GATT service
	if err := bc.discoverGattService(); err != nil {
		log.Printf("BLE_LOG: discoverGattService failed: %v", err)
		return fmt.Errorf("failed to discover GATT service: %v", err)
	}

	// Setup characteristics
	if err := bc.setupCharacteristics(); err != nil {
		log.Printf("BLE_LOG: setupCharacteristics failed: %v", err)
		return fmt.Errorf("failed to setup characteristics: %v", err)
	}

	// Start monitoring notifications
	go bc.handleBleNotifications()
	
	// Start command queue processor
	go bc.processCommandQueue()
	
	// Disabled polling - it causes notification floods
	// The polling by reading characteristics triggers the Android app to send notifications
	// which creates an infinite loop. We'll rely on D-Bus notifications instead.
	// go bc.startStatePolling()

	log.Printf("BLE GATT connection established to %s", bc.targetAddress)
	return nil
}

func (bc *BleClient) findDeviceByAddress(address string) (string, error) {
	// This function is not needed as we already have the device path from discovery
	return "", fmt.Errorf("findDeviceByAddress not implemented - use devicePath from discovery")
}

func (bc *BleClient) discoverGattService() error {
	log.Println("BLE_LOG: Discovering GATT services...")

	// Ensure services are resolved
	deviceObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.devicePath)
	
	// Check if services are resolved
	var servicesResolved bool
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0, BLUEZ_DEVICE_INTERFACE, "ServicesResolved").Store(&servicesResolved); err == nil && servicesResolved {
			log.Println("BLE_LOG: Services resolved")
			break
		}
		
		if i < maxRetries-1 {
			log.Printf("BLE_LOG: Waiting for services to be resolved (attempt %d/%d)...", i+1, maxRetries)
			time.Sleep(500 * time.Millisecond)
		}
	}
	
	if !servicesResolved {
		log.Println("BLE_LOG: Warning - services may not be fully resolved")
	}

	// Get all managed objects to find services
	objects := make(map[dbus.ObjectPath]map[string]map[string]dbus.Variant)
	obj := bc.conn.Object(BLUEZ_BUS_NAME, "/")
	if err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return fmt.Errorf("failed to get managed objects: %v", err)
	}

	// Look for GATT services under this device
	devicePathStr := string(bc.devicePath)
	serviceCount := 0
	
	for path, interfaces := range objects {
		pathStr := string(path)
		
		// Check if this is a service under our device
		if !strings.HasPrefix(pathStr, devicePathStr+"/service") {
			continue
		}
		
		// Check if it's a GATT service
		if svcIface, hasGattService := interfaces["org.bluez.GattService1"]; hasGattService {
			serviceCount++
			
			// Get the UUID
			if uuidVariant, ok := svcIface["UUID"]; ok {
				uuid := uuidVariant.Value().(string)
				log.Printf("BLE_LOG: Found service %d: %s", serviceCount, uuid)
				
				if strings.EqualFold(uuid, NocturneServiceUUID) {
					log.Printf("BLE_LOG: Found Nocturne service at: %s", path)
					bc.servicePath = path
					
					// Broadcast service discovered
					if bc.wsHub != nil {
						bc.wsHub.Broadcast(utils.WebSocketEvent{
							Type: "media/ble_service_discovered",
							Payload: map[string]interface{}{
								"uuid":      uuid,
								"path":      string(path),
								"timestamp": time.Now().Unix(),
							},
						})
					}
					
					return nil
				}
			}
		}
	}

	if serviceCount == 0 {
		return fmt.Errorf("no GATT services found - device may not be properly connected")
	}

	return fmt.Errorf("Nocturne GATT service not found among %d services", serviceCount)
}

func (bc *BleClient) setupCharacteristics() error {
	log.Println("BLE_LOG: Setting up characteristics...")

	// Get all managed objects to find characteristics
	objects := make(map[dbus.ObjectPath]map[string]map[string]dbus.Variant)
	obj := bc.conn.Object(BLUEZ_BUS_NAME, "/")
	if err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return fmt.Errorf("failed to get managed objects: %v", err)
	}

	// Look for characteristics under our service
	servicePathStr := string(bc.servicePath)
	for path, interfaces := range objects {
		pathStr := string(path)
		
		// Check if this is a characteristic under our service
		if !strings.HasPrefix(pathStr, servicePathStr+"/char") {
			continue
		}
		
		// Check if it's a GATT characteristic
		if charIface, hasChar := interfaces["org.bluez.GattCharacteristic1"]; hasChar {
			// Get the UUID
			if uuidVariant, ok := charIface["UUID"]; ok {
				uuid := uuidVariant.Value().(string)
				log.Printf("BLE_LOG: Found characteristic: %s at %s", uuid, path)
				
				// Store characteristic paths based on UUID
				var charType string
				switch strings.ToLower(uuid) {
				case strings.ToLower(CommandRxCharUUID):
					bc.commandRxCharPath = path
					log.Println("BLE_LOG: Found Command RX characteristic")
					charType = "Command RX"
				case strings.ToLower(ResponseTxCharUUID):
					bc.responseTxCharPath = path
					log.Println("BLE_LOG: Found Response TX characteristic (State TX)")
					charType = "Response TX"
				case strings.ToLower(DebugLogCharUUID):
					bc.debugLogCharPath = path
					log.Println("BLE_LOG: Found Debug Log characteristic")
					charType = "Debug Log"
				case strings.ToLower(DeviceInfoCharUUID):
					bc.deviceInfoCharPath = path
					log.Println("BLE_LOG: Found Device Info characteristic")
					charType = "Device Info"
				case strings.ToLower(AlbumArtTxCharUUID):
					bc.albumArtCharPath = path
					log.Println("BLE_LOG: Found Album Art TX characteristic")
					charType = "Album Art TX"
				default:
					charType = "Unknown"
				}
				
				// Broadcast characteristic found
				if bc.wsHub != nil && charType != "Unknown" {
					bc.wsHub.Broadcast(utils.WebSocketEvent{
						Type: "media/ble_characteristic_found",
						Payload: map[string]interface{}{
							"uuid":      uuid,
							"type":      charType,
							"path":      string(path),
							"timestamp": time.Now().Unix(),
						},
					})
				}
			}
		}
	}

	// Verify all required characteristics found
	if bc.commandRxCharPath == "" {
		return fmt.Errorf("Command RX characteristic not found")
	}
	if bc.responseTxCharPath == "" {
		return fmt.Errorf("Response TX characteristic not found")
	}
	// Optional characteristics: debugLogCharPath, deviceInfoCharPath
	
	log.Printf("BLE_LOG: Characteristics discovered - CommandRx: %s, ResponseTx: %s", 
		bc.commandRxCharPath, bc.responseTxCharPath)
	
	// Read device info if available
	if bc.deviceInfoCharPath != "" {
		go bc.readDeviceInfo()
	}

	// Enable notifications for response characteristic
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.responseTxCharPath)
	if err := charObj.Call("org.bluez.GattCharacteristic1.StartNotify", 0).Err; err != nil {
		log.Printf("BLE_LOG: ERROR - failed to enable notifications on Response TX: %v", err)
		// Try to continue anyway, but this is likely to cause issues
	} else {
		log.Println("BLE_LOG: Successfully enabled notifications on Response TX characteristic")
	}
	
	// Enable notifications for album art characteristic if available
	if bc.albumArtCharPath != "" {
		albumArtCharObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.albumArtCharPath)
		if err := albumArtCharObj.Call("org.bluez.GattCharacteristic1.StartNotify", 0).Err; err != nil {
			log.Printf("BLE_LOG: Warning - failed to enable notifications on Album Art TX: %v", err)
		} else {
			log.Println("BLE_LOG: Successfully enabled notifications on Album Art TX characteristic")
		}
	}
	
	// DISABLED: Debug log notifications cause feedback loop
	// The Android app logs "Notification sent" for every notification,
	// which triggers a debug log notification, which logs "Notification sent",
	// creating an infinite loop that floods the connection.
	// 
	// To enable debug logs safely, the Android app needs to be fixed to not log
	// debug messages when sending debug log notifications.
	/*
	if bc.debugLogCharPath != "" {
		debugCharObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.debugLogCharPath)
		if err := debugCharObj.Call("org.bluez.GattCharacteristic1.StartNotify", 0).Err; err != nil {
			log.Printf("BLE_LOG: Warning - failed to enable notifications on Debug Log: %v", err)
		} else {
			log.Println("BLE_LOG: Enabled notifications on Debug Log characteristic")
		}
	}
	*/
	
	// Request capabilities to check for binary protocol support
	log.Println("BLE_LOG: Requesting device capabilities...")
	go func() {
		// Small delay to ensure notifications are ready
		time.Sleep(500 * time.Millisecond)
		
		cmd := map[string]interface{}{
			"command": "get_capabilities",
			"command_id": fmt.Sprintf("cap_%d", time.Now().Unix()),
		}
		
		cmdJson, err := json.Marshal(cmd)
		if err != nil {
			log.Printf("BLE_LOG: Failed to marshal capabilities request: %v", err)
			return
		}
		
		charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
		options := make(map[string]interface{})
		
		if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJson, options).Err; err != nil {
			log.Printf("BLE_LOG: Failed to request capabilities: %v", err)
		} else {
			log.Println("BLE_LOG: Capabilities request sent")
		}
	}()

	return nil
}

func (bc *BleClient) negotiateMTU() error {
	log.Printf("BLE_LOG: Attempting MTU negotiation (target: %d)", TargetMTU)
	
	// Note: MTU negotiation in BlueZ is typically handled automatically
	// The actual negotiated MTU would be available through device properties
	// For now, we'll use the default MTU and log when actual MTU is available
	
	bc.mtu = DefaultMTU
	log.Printf("BLE_LOG: Using MTU: %d (will be updated if negotiation occurs)", bc.mtu)
	return nil
}

func (bc *BleClient) handleBleNotifications() {
	log.Println("BLE_LOG: handleBleNotifications goroutine started")

	// Start monitoring notifications
	go bc.monitorCharacteristicNotifications()

	// Keep the goroutine alive and monitor connection
	ticker := time.NewTicker(60 * time.Second)  // Increased from 30s to reduce connection checks
	defer ticker.Stop()
	
	connectionCheckFailures := 0

	for {
		select {
		case <-bc.stopChan:
			log.Println("BLE_LOG: handleBleNotifications stopping")
			return
		case <-ticker.C:
			// Check connection health using D-Bus
			if bc.devicePath != "" {
				deviceObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.devicePath)
				var connected bool
				if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0, BLUEZ_DEVICE_INTERFACE, "Connected").Store(&connected); err != nil {
					connectionCheckFailures++
					log.Printf("BLE_LOG: Error checking connection (failure %d/3): %v", connectionCheckFailures, err)
					// Only handle connection loss after 3 consecutive failures
					if connectionCheckFailures >= 3 {
						bc.handleConnectionLoss()
						return
					}
					continue
				}
				
				if !connected {
					log.Printf("BLE_LOG: Connection lost")
					bc.handleConnectionLoss()
					return
				}
				
				// Reset failure count on successful check
				connectionCheckFailures = 0
			}
		}
	}
}

func (bc *BleClient) monitorCharacteristicNotifications() {
	log.Printf("BLE_LOG: Setting up D-Bus signal monitoring for notifications")
	
	// Add rate limiting to prevent notification floods
	lastNotificationTime := make(map[string]time.Time)
	notificationMinInterval := 2 * time.Millisecond

	// Subscribe to PropertiesChanged signals for the response characteristic
	rule := fmt.Sprintf("type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='%s'", bc.responseTxCharPath)
	
	if err := bc.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule).Err; err != nil {
		log.Printf("BLE_LOG: Failed to add match rule for response: %v", err)
		return
	}
	
	// Add match rule for album art notifications if available
	var albumArtRule string
	if bc.albumArtCharPath != "" {
		albumArtRule = fmt.Sprintf("type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='%s'", bc.albumArtCharPath)
		if err := bc.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, albumArtRule).Err; err != nil {
			log.Printf("BLE_LOG: Failed to add match rule for album art: %v", err)
		}
	}
	
	// DISABLED: Debug log monitoring to prevent feedback loop
	// See comment above about debug log notifications causing infinite loops
	/*
	var debugRule string
	if bc.debugLogCharPath != "" {
		debugRule = fmt.Sprintf("type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='%s'", bc.debugLogCharPath)
		if err := bc.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, debugRule).Err; err != nil {
			log.Printf("BLE_LOG: Failed to add match rule for debug: %v", err)
		}
	}
	*/

	// Create a channel to receive signals with smaller buffer to prevent queue buildup
	sigChan := make(chan *dbus.Signal, 10)
	bc.conn.Signal(sigChan)

	log.Println("BLE_LOG: Monitoring notifications...")

	for {
		select {
		case <-bc.stopChan:
			log.Println("BLE_LOG: Stopping notification monitor")
			bc.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, rule)
			if albumArtRule != "" {
				bc.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, albumArtRule)
			}
			// Debug rule removal disabled since we're not adding it anymore
			return
			
		case sig := <-sigChan:
			if sig == nil {
				continue
			}

			// Check if this is a PropertiesChanged signal for our characteristics
			if sig.Name == "org.freedesktop.DBus.Properties.PropertiesChanged" {
				var charType string
				if sig.Path == bc.responseTxCharPath {
					charType = "response"
					log.Printf("BLE_LOG: Received notification on Response TX characteristic")
				} else if bc.albumArtCharPath != "" && sig.Path == bc.albumArtCharPath {
					charType = "albumart"
					// Don't log every album art notification - too spammy during transfers
				} else if bc.debugLogCharPath != "" && sig.Path == bc.debugLogCharPath {
					charType = "debug"
					log.Printf("BLE_LOG: Received notification on Debug Log characteristic")
				} else {
					// It's not a notification on a characteristic we're watching.
					// Check if it's a UUIDs property change on the device itself, which can cause connection instability.
					if sig.Path == bc.devicePath {
						if len(sig.Body) >= 2 {
							// sig.Body[0] is the interface name (string)
							// sig.Body[1] is the changed properties (map[string]dbus.Variant)
							if interfaceName, ok := sig.Body[0].(string); ok && interfaceName == BLUEZ_DEVICE_INTERFACE {
								if changedProps, ok := sig.Body[1].(map[string]dbus.Variant); ok {
									if _, exists := changedProps["UUIDs"]; exists {
										log.Printf("BLE_LOG: Device services changed (UUIDs property). This can disrupt the connection. Re-validating GATT profile.")
										
										// This needs to be handled carefully to avoid race conditions.
										// We'll trigger a soft-reconnect in a separate goroutine.
										go func() {
											bc.mu.Lock()
											defer bc.mu.Unlock()

											log.Println("BLE_LOG: Attempting to re-validate GATT services and characteristics...")
											if err := bc.discoverGattService(); err != nil {
												log.Printf("BLE_LOG: Failed to re-discover GATT service after UUID change: %v. Triggering full reconnect.", err)
												// Use a goroutine to avoid deadlock on the lock
												go bc.handleConnectionLoss()
												return
											}
											if err := bc.setupCharacteristics(); err != nil {
												log.Printf("BLE_LOG: Failed to re-setup characteristics after UUID change: %v. Triggering full reconnect.", err)
												go bc.handleConnectionLoss()
												return
											}
											log.Printf("BLE_LOG: Successfully re-validated GATT profile after UUID change.")
										}()
										
										// We've handled this signal, so we can continue the loop.
										continue
									}
								}
							}
						}
					}

					// Log other unhandled signals for debugging if they are on our device path
					if strings.Contains(string(sig.Path), string(bc.devicePath)) {
						log.Printf("BLE_LOG: Received unhandled signal on path %s: Name=%s, Body=%v", sig.Path, sig.Name, sig.Body)
					}
					continue
				}
				
				// The signal has format: interface_name, changed_properties, invalidated_properties
				if len(sig.Body) >= 2 {
					if changedProps, ok := sig.Body[1].(map[string]dbus.Variant); ok {
						if valueVariant, exists := changedProps["Value"]; exists {
							if value, ok := valueVariant.Value().([]byte); ok {
								// Check rate limiting (skip for album art during transfers)
								now := time.Now()
								skipRateLimit := false
								
								// For album art, check if we're in any transfer
								if charType == "albumart" {
									bc.albumArtMutex.Lock()
									skipRateLimit = bc.albumArtReceiving
									bc.albumArtMutex.Unlock()
									
									if !skipRateLimit {
										bc.testAlbumArtMutex.Lock()
										skipRateLimit = bc.testAlbumArtReceiving
										bc.testAlbumArtMutex.Unlock()
									}
								}
								
								if !skipRateLimit {
									if lastTime, exists := lastNotificationTime[charType]; exists {
										if now.Sub(lastTime) < notificationMinInterval {
											log.Printf("BLE_LOG: Rate limiting notification on %s (too fast: %v)", charType, now.Sub(lastTime))
											continue
										}
									}
									lastNotificationTime[charType] = now
								}
								
								// Only broadcast notification metadata, not the full data
								if bc.wsHub != nil && charType == "response" {
									// Only broadcast state update notifications, not album art
									bc.wsHub.Broadcast(utils.WebSocketEvent{
										Type: "media/ble_notification_received",
										Payload: map[string]interface{}{
											"char_type": charType,
											"size":      len(value),
											"timestamp": time.Now().Unix(),
										},
									})
								}
								// Process state updates synchronously, album art async
								if charType == "response" {
									// State updates must be processed in order
									bc.handleNotificationData(value, charType)
								} else {
									// Album art can be processed asynchronously
									valueCopy := make([]byte, len(value))
									copy(valueCopy, value)
									go bc.handleNotificationData(valueCopy, charType)
								}
							}
						}
					}
				}
			}
		}
	}
}

func (bc *BleClient) handleNotificationData(data []byte, charType string) {
	if len(data) == 0 {
		return
	}

	// Check if this is binary protocol data (album art characteristic)
	if charType == "albumart" && len(data) >= BinaryHeaderSize {
		// Try to parse as binary protocol
		header, payload, err := ParseBinaryMessage(data)
		if err == nil {
			bc.handleBinaryMessage(header, payload)
			return
		}
		// Fall through to JSON parsing if binary parse fails
	}
	
	// Handle as JSON
	dataStr := string(data)
	log.Printf("Received BLE %s data: %s", charType, dataStr)

	// Skip broadcasting raw data to prevent flooding WebSocket

	// Log raw notification data for debugging ACK flow
	log.Printf("BLE_LOG: Processing notification data: %s", string(data))
	
	// Try to parse the message type first
	var msgType struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &msgType); err != nil {
		log.Printf("Failed to parse message type: %v", err)
		return
	}

	log.Printf("BLE_LOG: Notification type: %s", msgType.Type)
	
	// Handle different message types
	switch msgType.Type {
	case "error":
		// Error message
		var errMsg struct {
			Type      string `json:"type"`
			Code      string `json:"code"`
			Message   string `json:"message"`
			Timestamp int64  `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &errMsg); err == nil {
			log.Printf("BLE ERROR: %s - %s", errMsg.Code, errMsg.Message)
		}
		
	case "capabilities":
		// Device capabilities
		var caps struct {
			Type                  string   `json:"type"`
			Version               string   `json:"version"`
			Features              []string `json:"features"`
			MTU                   int      `json:"mtu"`
			DebugEnabled          bool     `json:"debug_enabled"`
			BinaryProtocol        bool     `json:"binary_protocol"`
			BinaryProtocolVersion int      `json:"binary_protocol_version"`
		}
		if err := json.Unmarshal(data, &caps); err == nil {
			log.Printf("BLE Capabilities: Version %s, MTU %d, Features: %v, Binary Protocol: %v", 
				caps.Version, caps.MTU, caps.Features, caps.BinaryProtocol)
			
			// Check if binary protocol is supported
			if caps.BinaryProtocol && caps.BinaryProtocolVersion >= 1 {
				log.Printf("BLE_LOG: Device supports binary protocol v%d, enabling...", caps.BinaryProtocolVersion)
				// Enable binary protocol
				go func() {
					time.Sleep(100 * time.Millisecond) // Small delay to ensure connection is stable
					if err := bc.enableBinaryProtocol(); err != nil {
						log.Printf("BLE_LOG: Failed to enable binary protocol: %v", err)
					}
				}()
			}
			// Update our MTU if provided
			if caps.MTU > 0 {
				bc.mu.Lock()
				bc.mtu = uint16(caps.MTU)
				bc.capsReceived = true
				bc.fullyConnected = true
				bc.mu.Unlock()
			}
			
			// Broadcast ready state
			if bc.wsHub != nil {
				bc.wsHub.Broadcast(utils.WebSocketEvent{
					Type: "media/ready",
					Payload: map[string]interface{}{
						"mtu":     caps.MTU,
						"version": caps.Version,
					},
				})
			}
		}
		
	case "stateUpdate":
		// Media state update
		var stateUpdate utils.MediaStateUpdate
		if err := json.Unmarshal(data, &stateUpdate); err != nil {
			log.Printf("BLE_LOG: Failed to parse media state update: %v (data: %s)", err, string(data))
			// Don't disconnect on parse errors - could be MTU truncation
			return
		}

		// Safe logging with nil checks
		trackName := "<nil>"
		artistName := "<nil>"
		albumName := "<nil>"
		if stateUpdate.Track != nil {
			trackName = *stateUpdate.Track
		}
		if stateUpdate.Artist != nil {
			artistName = *stateUpdate.Artist
		}
		if stateUpdate.Album != nil {
			albumName = *stateUpdate.Album
		}
		log.Printf("BLE_LOG: Received state update - Track: %s, Artist: %s, Album: %s, Playing: %v, Position: %d ms", 
			trackName, artistName, albumName, stateUpdate.IsPlaying, stateUpdate.PositionMs)

		bc.mu.Lock()
		bc.currentState = &stateUpdate
		bc.mu.Unlock()

		// Check for cached album art
		if stateUpdate.Artist != nil && stateUpdate.Album != nil && 
		   *stateUpdate.Artist != "" && *stateUpdate.Album != "" {
			go bc.checkAndRequestAlbumArt(*stateUpdate.Artist, *stateUpdate.Album)
		}

		// Broadcast to WebSocket clients
		if bc.wsHub != nil {
			log.Printf("BLE_LOG: Broadcasting state update to WebSocket clients")
			bc.wsHub.Broadcast(utils.WebSocketEvent{
				Type:    "media/state_update",
				Payload: stateUpdate,
			})
		}
		
	case "debugLog":
		// Debug log entry
		var debugLog struct {
			Type      string                 `json:"type"`
			Timestamp int64                  `json:"timestamp"`
			Level     string                 `json:"level"`
			LogType   string                 `json:"type"`
			Message   string                 `json:"message"`
			Data      map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(data, &debugLog); err == nil {
			log.Printf("BLE DEBUG [%s] %s: %s", debugLog.Level, debugLog.LogType, debugLog.Message)
		}
		
	case "timeSync":
		// Time synchronization from Android device
		var timeSync struct {
			Type        string `json:"type"`
			TimestampMs int64  `json:"timestamp_ms"`
			Timezone    string `json:"timezone,omitempty"`
		}
		if err := json.Unmarshal(data, &timeSync); err == nil {
			log.Printf("BLE_LOG: Received time sync - Timestamp: %d, Timezone: %s", timeSync.TimestampMs, timeSync.Timezone)
			
			// Set system time
			if err := utils.SetSystemTime(timeSync.TimestampMs); err != nil {
				log.Printf("BLE_LOG: ERROR - Failed to set system time: %v", err)
			} else {
				log.Printf("BLE_LOG: System time updated successfully")
			}
			
			// Set timezone if provided
			if timeSync.Timezone != "" {
				if err := utils.SetTimezone(timeSync.Timezone); err != nil {
					log.Printf("BLE_LOG: ERROR - Failed to set timezone: %v", err)
				} else {
					log.Printf("BLE_LOG: Timezone updated to: %s", timeSync.Timezone)
				}
			}
			
			// Broadcast time update event
			if bc.wsHub != nil {
				bc.wsHub.Broadcast(utils.WebSocketEvent{
					Type: "system/time_updated",
					Payload: map[string]interface{}{
						"timestamp_ms": timeSync.TimestampMs,
						"timezone":     timeSync.Timezone,
					},
				})
			}
		}
		
	case "binary_protocol_enabled":
		// Binary protocol acknowledgment
		var ack struct {
			Type    string `json:"type"`
			Version int    `json:"version"`
		}
		if err := json.Unmarshal(data, &ack); err == nil {
			log.Printf("BLE_LOG: Binary protocol enabled successfully, version %d", ack.Version)
			bc.mu.Lock()
			bc.supportsBinaryProtocol = true
			bc.binaryProtocolVersion = byte(ack.Version)
			bc.mu.Unlock()
		}
		
	case "album_art_start":
		// Album art transfer starting
		var artStart struct {
			Type        string `json:"type"`
			Size        int    `json:"size"`
			TrackID     string `json:"track_id"`
			Checksum    string `json:"checksum"`
			TotalChunks int    `json:"total_chunks"`
		}
		if err := json.Unmarshal(data, &artStart); err == nil {
			bc.albumArtMutex.Lock()
			bc.albumArtReceiving = true
			bc.albumArtBuffer = nil // Will be assembled from chunks
			bc.albumArtChunks = make(map[int][]byte) // Initialize chunk map
			bc.albumArtTrackID = artStart.TrackID
			bc.albumArtSize = artStart.Size
			bc.albumArtChecksum = artStart.Checksum
			bc.albumArtTotalChunks = artStart.TotalChunks
			bc.albumArtStartTime = time.Now()
			bc.albumArtMutex.Unlock()
			
			log.Printf("BLE_LOG: Starting album art transfer - Track: %s, Size: %d bytes, Chunks: %d", 
				artStart.TrackID, artStart.Size, artStart.TotalChunks)
		}
		
	case "album_art_chunk":
		// Album art chunk data
		var artChunk struct {
			Type       string `json:"type"`
			Checksum   string `json:"checksum"`
			ChunkIndex int    `json:"chunk_index"`
			Data       string `json:"data"` // Base64 encoded
		}
		if err := json.Unmarshal(data, &artChunk); err == nil {
			// Decode base64 data
			chunkData, err := base64.StdEncoding.DecodeString(artChunk.Data)
			if err != nil {
				log.Printf("BLE_LOG: Failed to decode album art chunk: %v", err)
				return
			}
			
			bc.albumArtMutex.Lock()
			if bc.albumArtReceiving && bc.albumArtChecksum == artChunk.Checksum {
				// Check if we already have this chunk (duplicate prevention)
				if _, exists := bc.albumArtChunks[artChunk.ChunkIndex]; exists {
					bc.albumArtMutex.Unlock()
					return
				}
				
				// Store chunk by index
				bc.albumArtChunks[artChunk.ChunkIndex] = chunkData
			}
			bc.albumArtMutex.Unlock()
		}
		
	case "album_art_end":
		// Album art transfer complete
		var artEnd struct {
			Type        string `json:"type"`
			Checksum    string `json:"checksum"`
			Success     bool   `json:"success"`
		}
		if err := json.Unmarshal(data, &artEnd); err == nil {
			bc.albumArtMutex.Lock()
			shouldProcess := false
			if bc.albumArtReceiving && bc.albumArtChecksum == artEnd.Checksum {
				if artEnd.Success {
					shouldProcess = true
				} else {
					log.Printf("BLE_LOG: Album art transfer failed for checksum: %s", artEnd.Checksum)
					bc.albumArtReceiving = false
					bc.albumArtBuffer = nil
					bc.albumArtChunks = nil
				}
			}
			bc.albumArtMutex.Unlock()
			
			if shouldProcess {
				// Process the completed album art synchronously for faster display
				bc.processAlbumArt()
				
				// Notify WebSocket clients that transfer is complete
				if bc.wsHub != nil {
					// Get metadata for test completion message
					bc.albumArtMutex.Lock()
					testPayload := map[string]interface{}{
						"timestamp": time.Now().UnixMilli(),
						"checksum":  artEnd.Checksum,
						"success":   artEnd.Success,
					}
					
					if bc.albumArtBuffer != nil && len(bc.albumArtBuffer) > 0 {
						imageFormat := detectImageFormatFromBytes(bc.albumArtBuffer)
						imageWidth, imageHeight := getImageDimensions(bc.albumArtBuffer, imageFormat)
						testPayload["size"] = len(bc.albumArtBuffer)
						testPayload["format"] = imageFormat
						testPayload["width"] = imageWidth
						testPayload["height"] = imageHeight
						testPayload["total_chunks"] = bc.albumArtTotalChunks
					}
					bc.albumArtMutex.Unlock()
					
					bc.wsHub.Broadcast(utils.WebSocketEvent{
						Type:    "test/album_art_transfer_complete",
						Payload: testPayload,
					})
				}
			}
			
			log.Printf("BLE_LOG: Album art transfer complete - Checksum: %s, Success: %v", 
				artEnd.Checksum, artEnd.Success)
		}
		
	// Test album art message types
	case "test_album_art_start":
		// Test album art transfer starting
		var testStart struct {
			Type        string `json:"type"`
			Size        int    `json:"size"`
			Checksum    string `json:"checksum"`
			TotalChunks int    `json:"total_chunks"`
		}
		if err := json.Unmarshal(data, &testStart); err == nil {
			bc.handleTestAlbumArtStart(map[string]interface{}{
				"size":         testStart.Size,
				"checksum":     testStart.Checksum,
				"total_chunks": testStart.TotalChunks,
			})
		}
		
	case "test_album_art_chunk":
		// Test album art chunk data
		var testChunk struct {
			Type       string `json:"type"`
			Checksum   string `json:"checksum"`
			ChunkIndex int    `json:"chunk_index"`
			Data       string `json:"data"` // Base64 encoded
		}
		if err := json.Unmarshal(data, &testChunk); err == nil {
			// Decode base64 data
			chunkData, err := base64.StdEncoding.DecodeString(testChunk.Data)
			if err != nil {
				log.Printf("BLE_LOG: Failed to decode test album art chunk: %v", err)
				return
			}
			
			bc.testAlbumArtMutex.Lock()
			checksumMatch := bc.testAlbumArtChecksum == testChunk.Checksum
			bc.testAlbumArtMutex.Unlock()
			
			if checksumMatch {
				bc.handleTestAlbumArtChunk(testChunk.ChunkIndex, chunkData)
			}
		}
		
	case "test_album_art_end":
		// Test album art transfer complete
		var testEnd struct {
			Type     string `json:"type"`
			Checksum string `json:"checksum"`
			Success  bool   `json:"success"`
		}
		if err := json.Unmarshal(data, &testEnd); err == nil {
			bc.testAlbumArtMutex.Lock()
			if !testEnd.Success {
				bc.handleTestAlbumArtError("transfer failed")
			}
			bc.testAlbumArtMutex.Unlock()
		}
		
	default:
		log.Printf("Unknown BLE message type: %s", msgType.Type)
	}
}

// SendCommand queues a command for sending with ACK handling
func (bc *BleClient) SendCommand(command string, valueMs *int, valuePercent *int) error {
	return bc.SendCommandWithHash(command, valueMs, valuePercent, "")
}

// SendCommandWithHash queues a command for sending with optional hash
func (bc *BleClient) SendCommandWithHash(command string, valueMs *int, valuePercent *int, hash string) error {
	bc.mu.RLock()
	if !bc.connected || bc.targetAddress == "" || !bc.bleConnected || bc.commandRxCharPath == "" {
		log.Printf("BLE_LOG: Cannot send command - connected:%v, address:%s, bleConnected:%v, cmdPath:%s",
			bc.connected, bc.targetAddress, bc.bleConnected, bc.commandRxCharPath)
		bc.mu.RUnlock()
		return fmt.Errorf("not connected to BLE media device")
	}
	bc.mu.RUnlock()

	// Generate command ID
	cmdID := fmt.Sprintf("cmd_%d_%d", time.Now().UnixNano(), len(command))
	
	// Create command item
	cmdItem := &CommandQueueItem{
		id:           cmdID,
		command:      command,
		valueMs:      valueMs,
		valuePercent: valuePercent,
		hash:         hash,
		timestamp:    time.Now(),
		callback:     make(chan error, 1),
	}
	
	// Add to queue (non-blocking)
	select {
	case bc.commandQueue <- cmdItem:
		log.Printf("BLE_LOG: Queued command %s with ID %s", command, cmdID)
	default:
		log.Printf("BLE_LOG: Command queue full, dropping command %s", command)
		return fmt.Errorf("command queue full")
	}
	
	// Wait for command to be processed
	select {
	case err := <-cmdItem.callback:
		return err
	case <-time.After(10 * time.Second):
		return fmt.Errorf("command timeout after 10 seconds")
	}
}

// processCommandQueue handles sending commands from the queue
func (bc *BleClient) processCommandQueue() {
	for {
		select {
		case <-bc.stopChan:
			return
		case cmd := <-bc.commandQueue:
			if cmd == nil {
				continue
			}
			
			// Send the command
			err := bc.sendCommandImmediate(cmd)
			
			// Notify callback
			if cmd.callback != nil {
				cmd.callback <- err
			}
		}
	}
}

// sendCommandImmediate actually sends a command and waits for ACK
func (bc *BleClient) sendCommandImmediate(cmdItem *CommandQueueItem) error {
	bc.commandQueueMu.Lock()
	bc.activeCommand = cmdItem
	bc.lastCommandID = cmdItem.id
	bc.commandQueueMu.Unlock()
	
	// Rate limit commands (reduced for faster response)
	const minCommandInterval = 10 * time.Millisecond
	timeSinceLastCommand := time.Since(bc.lastCommandTime)
	if timeSinceLastCommand < minCommandInterval {
		waitTime := minCommandInterval - timeSinceLastCommand
		time.Sleep(waitTime)
	}
	
	cmd := utils.MediaCommand{
		Command:      cmdItem.command,
		ValueMs:      cmdItem.valueMs,
		ValuePercent: cmdItem.valuePercent,
		// No command ID needed without ACK system
	}
	
	// For album_art_query, add checksum to payload
	if cmdItem.command == "album_art_query" {
		cmd.Payload = map[string]string{
			"checksum": cmdItem.hash,
			"track_id": "test",
		}
	}

	// Convert command to JSON
	cmdJson, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %v", err)
	}

	// Check if command exceeds MTU
	bc.mu.RLock()
	currentMTU := bc.mtu
	bc.mu.RUnlock()
	
	effectiveMTU := int(currentMTU - MTUHeaderSize)
	if len(cmdJson) > effectiveMTU {
		log.Printf("BLE_LOG: WARNING - Command size %d exceeds MTU %d, may fail", len(cmdJson), effectiveMTU)
		// For now, still try to send but log the warning
		// In the future, implement command chunking or compression
	}

	log.Printf("BLE_LOG: Sending command: %s with ID: %s (size: %d bytes)", cmdItem.command, cmdItem.id, len(cmdJson))

	// Send using D-Bus
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
	// BlueZ options for WriteValue - empty map uses default write with response
	options := make(map[string]interface{})
	
	// Try to send
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("BLE_LOG: Retry attempt %d for command %s", attempt, cmdItem.command)
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		
		err = charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJson, options).Err
		if err == nil {
			bc.lastCommandTime = time.Now()
			log.Printf("BLE_LOG: Command sent successfully via %s", bc.commandRxCharPath)
			// Clear active command immediately after sending
			bc.commandQueueMu.Lock()
			bc.activeCommand = nil
			bc.commandQueueMu.Unlock()
			return nil
		}
		
		// Check if it's an ATT error
		if strings.Contains(err.Error(), "ATT error: 0x0e") {
			log.Printf("BLE_LOG: ATT error 0x0e - characteristic busy, will retry")
			continue
		}
		
		// Check for "In Progress" error
		if strings.Contains(err.Error(), "InProgress") || strings.Contains(err.Error(), "In Progress") {
			log.Printf("BLE_LOG: Operation in progress, clearing state and retrying...")
			// Clear the active command to allow recovery
			bc.commandQueueMu.Lock()
			bc.activeCommand = nil
			bc.commandQueueMu.Unlock()
			// Add a longer delay before retry
			time.Sleep(500 * time.Millisecond)
			continue
		}
		
		// For other errors, log and continue retrying
		log.Printf("BLE_LOG: Failed to send command: %v", err)
	}
	
	return fmt.Errorf("failed to send command after %d attempts: %v", maxRetries, err)
}

// startStatePolling polls the state characteristic for faster updates
func (bc *BleClient) startStatePolling() {
	bc.pollingTicker = time.NewTicker(500 * time.Millisecond)
	
	for {
		select {
		case <-bc.stopChan:
			if bc.pollingTicker != nil {
				bc.pollingTicker.Stop()
			}
			return
		case <-bc.pollingTicker.C:
			bc.mu.RLock()
			charPath := bc.responseTxCharPath
			bc.mu.RUnlock()
			
			if charPath != "" {
				bc.pollCharacteristicValue()
			}
		}
	}
}

// pollCharacteristicValue reads the current value of the response characteristic
func (bc *BleClient) pollCharacteristicValue() {
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.responseTxCharPath)
	options := make(map[string]interface{})
	
	var value []byte
	if err := charObj.Call("org.bluez.GattCharacteristic1.ReadValue", 0, options).Store(&value); err == nil && len(value) > 0 {
		// Check if value changed since last poll
		if len(bc.lastPolledValue) == 0 || !bytesEqual(value, bc.lastPolledValue) {
			bc.lastPolledValue = make([]byte, len(value))
			copy(bc.lastPolledValue, value)
			// Process the notification data
			bc.handleNotificationData(value, "response")
		}
	}
}

// bytesEqual compares two byte slices
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Media control methods (same as SPP client)
func (bc *BleClient) Play() error {
	return bc.SendCommand("play", nil, nil)
}

func (bc *BleClient) Pause() error {
	return bc.SendCommand("pause", nil, nil)
}

func (bc *BleClient) Next() error {
	return bc.SendCommand("next", nil, nil)
}

func (bc *BleClient) Previous() error {
	return bc.SendCommand("previous", nil, nil)
}

func (bc *BleClient) SeekTo(positionMs int) error {
	return bc.SendCommand("seek_to", &positionMs, nil)
}

func (bc *BleClient) SetVolume(volumePercent int) error {
	if volumePercent < 0 || volumePercent > 100 {
		return fmt.Errorf("volume must be between 0 and 100")
	}
	return bc.SendCommand("set_volume", nil, &volumePercent)
}

// UpdateLocalVolume updates the local state volume without sending to device
func (bc *BleClient) UpdateLocalVolume(volumePercent int) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	
	if bc.currentState != nil {
		bc.currentState.VolumePercent = volumePercent
		log.Printf("BLE_LOG: Updated local volume state to %d%%", volumePercent)
	}
}

func (bc *BleClient) SendAlbumArtRequest(trackID string, checksum string) error {
    payload := map[string]string{
        "track_id": trackID,
        "checksum": checksum,
    }
    // This is a special command that doesn't go through the normal queue
    // because it's a request from nocturned to the companion app.
    cmd := utils.MediaCommand{
        Command: "album_art_query",
        Payload: payload,
    }
    cmdJson, err := json.Marshal(cmd)
    if err != nil {
        return fmt.Errorf("failed to marshal album art request: %v", err)
    }

    log.Printf("BLE_LOG: Sending album art request for checksum: %s", checksum)
    
    charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
    options := make(map[string]interface{})
    return charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJson, options).Err
}

// checkAndRequestAlbumArt checks if album art is cached and requests it if not
func (bc *BleClient) checkAndRequestAlbumArt(artist, album string) {
	// Check if album art is already cached
	if utils.CheckAlbumArtExists(artist, album) {
		log.Printf("BLE_LOG: Album art already cached for %s - %s", artist, album)
		
		// Load cached album art
		artPath := utils.GetAlbumArtPath(artist, album)
		if cachedData, err := os.ReadFile(artPath); err == nil {
			// Store in memory immediately if callback is set
			if bc.albumArtCallback != nil {
				bc.albumArtCallback(cachedData)
				log.Printf("BLE_LOG: Cached album art stored in memory (%d bytes)", len(cachedData))
			}
			
			// Also copy to temp location in background
			go func() {
				tempPath := "/tmp/album_art.jpg"
				if err := os.WriteFile(tempPath, cachedData, 0644); err != nil {
					log.Printf("BLE_LOG: Failed to copy cached album art to temp: %v", err)
				} else {
					log.Printf("BLE_LOG: Copied cached album art to %s", tempPath)
				}
			}()
		}
		
		// Broadcast that album art is available
		if bc.wsHub != nil {
			bc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/album_art_cached",
				Payload: map[string]interface{}{
					"artist": artist,
					"album":  album,
					"path":   artPath,
					"cached": true,
				},
			})
		}
		return
	}
	
	// Generate hash for this artist/album combination
	hash := utils.GenerateAlbumArtHash(artist, album)
	log.Printf("BLE_LOG: Album art not cached for %s - %s (hash: %s), requesting from companion", artist, album, hash)
	
	// Create a track ID from artist and album
	trackID := fmt.Sprintf("%s - %s", artist, album)
	
	// Request album art from companion app
	if err := bc.SendAlbumArtRequest(trackID, hash); err != nil {
		log.Printf("BLE_LOG: Failed to request album art: %v", err)
	}
}

func (bc *BleClient) GetCurrentState() *utils.MediaStateUpdate {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.currentState
}

func (bc *BleClient) IsConnected() bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.connected && bc.targetAddress != "" && bc.bleConnected
}


func (bc *BleClient) processAlbumArt() {
	bc.albumArtMutex.Lock()
	
	if !bc.albumArtReceiving || bc.albumArtChunks == nil {
		bc.albumArtMutex.Unlock()
		return
	}
	
	// Check if we have all chunks
	if len(bc.albumArtChunks) != bc.albumArtTotalChunks {
		log.Printf("BLE_LOG: Missing album art chunks - received %d/%d", 
			len(bc.albumArtChunks), bc.albumArtTotalChunks)
		bc.albumArtReceiving = false
		bc.albumArtChunks = nil
		bc.albumArtMutex.Unlock()
		return
	}
	
	// Pre-allocate buffer with exact size to avoid reallocations
	bc.albumArtBuffer = make([]byte, bc.albumArtSize)
	offset := 0
	
	// Assemble chunks directly into pre-allocated buffer
	for i := 0; i < bc.albumArtTotalChunks; i++ {
		chunk, exists := bc.albumArtChunks[i]
		if !exists {
			log.Printf("BLE_LOG: Missing album art chunk %d", i)
			bc.albumArtReceiving = false
			bc.albumArtChunks = nil
			bc.albumArtMutex.Unlock()
			return
		}
		copy(bc.albumArtBuffer[offset:], chunk)
		offset += len(chunk)
	}
	
	// Copy data for checksum verification (if needed)
	var bufferCopy []byte
	needsChecksum := !bc.supportsBinaryProtocol
	if needsChecksum {
		bufferCopy = make([]byte, len(bc.albumArtBuffer))
		copy(bufferCopy, bc.albumArtBuffer)
	}
	// expectedChecksum := bc.albumArtChecksum // Temporarily disabled
	
	// Extract artist and album while we have the lock
	artist := ""
	album := ""
	if bc.currentState != nil {
		if bc.currentState.Artist != nil {
			artist = *bc.currentState.Artist
		}
		if bc.currentState.Album != nil {
			album = *bc.currentState.Album
		}
	}
	
	// Release lock before checksum verification
	bc.albumArtMutex.Unlock()
	
	// TEMPORARILY DISABLED checksum verification to test performance
	log.Printf("BLE_LOG: Skipping checksum verification (temporarily disabled for testing)")
	// if needsChecksum {
	// 	checksum := utils.CalculateSHA256(bufferCopy)
	// 	if checksum != expectedChecksum {
	// 		log.Printf("BLE_LOG: Album art checksum mismatch - expected: %s, got: %s", 
	// 			expectedChecksum, checksum)
	// 		// Clean up state
	// 		bc.albumArtMutex.Lock()
	// 		bc.albumArtReceiving = false
	// 		bc.albumArtChunks = nil
	// 		bc.albumArtBuffer = nil
	// 		bc.albumArtMutex.Unlock()
	// 		return
	// 	}
	// } else {
	// 	log.Printf("BLE_LOG: Skipping checksum for binary protocol (CRC already verified)")
	// }
	
	// Re-acquire lock for final operations
	bc.albumArtMutex.Lock()
	
	// Store in memory FIRST before any other operations
	if bc.albumArtCallback != nil {
		bc.albumArtCallback(bc.albumArtBuffer)
		log.Printf("BLE_LOG: Album art stored in memory at %s (%d bytes)", 
			time.Now().Format("15:04:05.000"), len(bc.albumArtBuffer))
	}
	
	// Add a small delay to ensure memory storage is complete
	time.Sleep(10 * time.Millisecond)
	
	// Detect image format and dimensions
	imageFormat := "unknown"
	imageWidth := 0
	imageHeight := 0
	
	if len(bc.albumArtBuffer) > 0 {
		imageFormat = detectImageFormatFromBytes(bc.albumArtBuffer)
		imageWidth, imageHeight = getImageDimensions(bc.albumArtBuffer, imageFormat)
	}
	
	// NOW broadcast album art update - UI will fetch from memory
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/album_art_updated",
			Payload: map[string]interface{}{
				"track_id":    bc.albumArtTrackID,
				"artist":      artist,
				"album":       album,
				"filename":    "memory", // Indicate it's in memory
				"size":        len(bc.albumArtBuffer),
				"format":      imageFormat,
				"width":       imageWidth,
				"height":      imageHeight,
				"cached":      artist != "" && album != "",
				"checksum":    bc.albumArtChecksum,
				"total_chunks": bc.albumArtTotalChunks,
			},
		})
		log.Printf("BLE_LOG: Album art update broadcast sent at %s", time.Now().Format("15:04:05.000"))
	}
	
	// Log transfer completion with timing
	if !bc.albumArtStartTime.IsZero() {
		transferTime := time.Since(bc.albumArtStartTime).Milliseconds()
		log.Printf("BLE_LOG: Album art transfer complete - %d bytes in %dms", 
			len(bc.albumArtBuffer), transferTime)
	}
	
	// Make a copy for background save before clearing buffer
	albumArtCopy := make([]byte, len(bc.albumArtBuffer))
	copy(albumArtCopy, bc.albumArtBuffer)
	trackID := bc.albumArtTrackID
	
	// Reset state and unlock
	bc.albumArtReceiving = false
	bc.albumArtBuffer = nil
	bc.albumArtChunks = nil
	bc.albumArtMutex.Unlock()
	
	// Save to disk in background
	go func() {
		// Skip temp file save - we're serving from memory now
		// Just save to cache for future use
		if artist != "" && album != "" {
			cacheFilename := utils.GetAlbumArtPath(artist, album)
			if err := utils.SaveAlbumArt(albumArtCopy, cacheFilename); err != nil {
				log.Printf("BLE_LOG: Failed to save album art to cache: %v", err)
			} else {
				log.Printf("BLE_LOG: Album art cached at: %s", cacheFilename)
				
				// Save metadata
				hash := utils.GenerateAlbumArtHash(artist, album)
				metadataFile := filepath.Join("/var/nocturne/albumart", hash + ".json")
				metadata := map[string]interface{}{
					"artist": artist,
					"album": album,
					"track": trackID,
					"sha256": bc.albumArtChecksum, // Use the stored checksum
					"added": time.Now().Format(time.RFC3339),
				}
				if metadataJSON, err := json.MarshalIndent(metadata, "", "  "); err == nil {
					if err := os.WriteFile(metadataFile, metadataJSON, 0644); err != nil {
						log.Printf("BLE_LOG: Failed to save metadata: %v", err)
					}
				}
			}
		}
	}()
	
	return // Early return since we already unlocked
}

func (bc *BleClient) readDeviceInfo() {
	log.Println("BLE_LOG: Reading device info characteristic")
	
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.deviceInfoCharPath)
	options := make(map[string]interface{})
	
	var value []byte
	if err := charObj.Call("org.bluez.GattCharacteristic1.ReadValue", 0, options).Store(&value); err != nil {
		log.Printf("BLE_LOG: Failed to read device info: %v", err)
		return
	}
	
	log.Printf("BLE_LOG: Device info: %s", string(value))
	
	// Parse and handle capabilities
	var caps struct {
		Type         string   `json:"type"`
		Version      string   `json:"version"`
		Features     []string `json:"features"`
		MTU          int      `json:"mtu"`
		DebugEnabled bool     `json:"debug_enabled"`
	}
	if err := json.Unmarshal(value, &caps); err == nil {
		log.Printf("BLE Device Capabilities: Version %s, MTU %d, Features: %v", 
			caps.Version, caps.MTU, caps.Features)
		// Update our MTU if provided
		if caps.MTU > 0 {
			bc.mu.Lock()
			bc.mtu = uint16(caps.MTU)
			bc.mu.Unlock()
		}
	}
}

func (bc *BleClient) handleConnectionLoss() {
	log.Println("BLE_LOG: handleConnectionLoss called")
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if !bc.bleConnected {
		log.Println("BLE_LOG: handleConnectionLoss: already handling disconnection")
		return
	}

	log.Printf("BLE connection lost to NocturneCompanion, cleaning up...")
	bc.bleConnected = false
	bc.connected = false

	// Clean up BLE resources
	if bc.devicePath != "" {
		deviceObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.devicePath)
		deviceObj.Call("org.bluez.Device1.Disconnect", 0)
	}

	// Broadcast disconnection
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/ble_disconnected",
			Payload: map[string]string{
				"address": bc.targetAddress,
				"status":  "disconnected",
				"reason":  "connection_lost",
			},
		})
	}

	// Schedule reconnection attempt
	go bc.attemptReconnect()
}

func (bc *BleClient) attemptReconnect() {
	if bc.reconnectAttempts >= MAX_RECONNECT_ATTEMPTS {
		log.Printf("Max BLE reconnection attempts reached for media client")
		return
	}

	bc.reconnectAttempts++
	log.Printf("Attempting to reconnect BLE media client (attempt %d/%d)", bc.reconnectAttempts, MAX_RECONNECT_ATTEMPTS)
	
	// Broadcast reconnection attempt
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/ble_reconnect_attempt",
			Payload: map[string]interface{}{
				"attempt":      bc.reconnectAttempts,
				"max_attempts": MAX_RECONNECT_ATTEMPTS,
				"timestamp":    time.Now().Unix(),
			},
		})
	}

	time.Sleep(RECONNECT_DELAY)

	if err := bc.DiscoverAndConnect(); err != nil {
		log.Printf("BLE media client reconnection failed: %v", err)
		bc.attemptReconnect()
	}
}

// handleBinaryMessage processes binary protocol messages
func (bc *BleClient) handleBinaryMessage(header *BinaryHeader, payload []byte) {
	switch header.MessageType {
	case MsgAlbumArtStart:
		startPayload, err := UnmarshalAlbumArtStartPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse album art start payload: %v", err)
			return
		}
		
		bc.albumArtMutex.Lock()
		bc.albumArtReceiving = true
		bc.albumArtBuffer = nil
		// Pre-size map to avoid dynamic growth
		bc.albumArtChunks = make(map[int][]byte, int(startPayload.TotalChunks))
		bc.albumArtTrackID = startPayload.TrackID
		bc.albumArtSize = int(startPayload.ImageSize)
		bc.albumArtChecksum = BytesToHex(startPayload.Checksum[:])
		bc.albumArtTotalChunks = int(startPayload.TotalChunks)
		bc.albumArtStartTime = time.Now()
		bc.albumArtMutex.Unlock()
		
		log.Printf("BLE_LOG: Binary album art transfer starting - Track: %s, Size: %d bytes, Chunks: %d",
			startPayload.TrackID, startPayload.ImageSize, startPayload.TotalChunks)
		
	case MsgAlbumArtChunk:
		bc.albumArtMutex.Lock()
		if bc.albumArtReceiving {
			chunkIndex := int(header.ChunkIndex)
			
			// Check for duplicate chunks
			if _, exists := bc.albumArtChunks[chunkIndex]; exists {
				bc.albumArtMutex.Unlock()
				return
			}
			
			// Store chunk (payload is raw image data, no decoding needed)
			bc.albumArtChunks[chunkIndex] = payload
		}
		bc.albumArtMutex.Unlock()
		
	case MsgAlbumArtEnd:
		endPayload, err := UnmarshalAlbumArtEndPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse album art end payload: %v", err)
			return
		}
		
		bc.albumArtMutex.Lock()
		if bc.albumArtReceiving && BytesToHex(endPayload.Checksum[:]) == bc.albumArtChecksum {
			if endPayload.Success {
				// Process the completed album art
				bc.albumArtMutex.Unlock()
				bc.processAlbumArt()
				log.Printf("BLE_LOG: Binary album art transfer completed successfully")
			} else {
				log.Printf("BLE_LOG: Binary album art transfer failed")
				bc.albumArtReceiving = false
				bc.albumArtBuffer = nil
				bc.albumArtChunks = nil
				bc.albumArtMutex.Unlock()
			}
		} else {
			bc.albumArtMutex.Unlock()
		}
		
	// Test album art binary messages
	case MsgTestAlbumArtStart:
		startPayload, err := UnmarshalAlbumArtStartPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse test album art start payload: %v", err)
			return
		}
		
		bc.handleTestAlbumArtStart(map[string]interface{}{
			"size":         startPayload.ImageSize,
			"checksum":     BytesToHex(startPayload.Checksum[:]),
			"total_chunks": startPayload.TotalChunks,
			"binary":       true,
		})
		
	case MsgTestAlbumArtChunk:
		bc.testAlbumArtMutex.Lock()
		if bc.testAlbumArtReceiving {
			chunkIndex := int(header.ChunkIndex)
			
			// Check for duplicate chunks
			if _, exists := bc.testAlbumArtChunks[chunkIndex]; exists {
				bc.testAlbumArtMutex.Unlock()
				return
			}
			
			// Store chunk (payload is raw image data, no decoding needed)
			bc.testAlbumArtChunks[chunkIndex] = payload
			bc.testAlbumArtMutex.Unlock()
			
			// Handle the chunk
			bc.handleTestAlbumArtChunk(chunkIndex, payload)
		} else {
			bc.testAlbumArtMutex.Unlock()
		}
		
	case MsgTestAlbumArtEnd:
		endPayload, err := UnmarshalAlbumArtEndPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse test album art end payload: %v", err)
			return
		}
		
		bc.testAlbumArtMutex.Lock()
		if bc.testAlbumArtReceiving && BytesToHex(endPayload.Checksum[:]) == bc.testAlbumArtChecksum {
			if endPayload.Success {
				// Process the completed test album art
				bc.testAlbumArtMutex.Unlock()
				bc.assembleTestAlbumArt()
			} else {
				log.Printf("BLE_LOG: Binary test album art transfer failed")
				bc.testAlbumArtReceiving = false
				bc.testAlbumArtBuffer = nil
				bc.testAlbumArtChunks = nil
				bc.testAlbumArtMutex.Unlock()
				bc.handleTestAlbumArtError("binary transfer failed")
			}
		} else {
			bc.testAlbumArtMutex.Unlock()
			log.Printf("BLE_LOG: Test album art end checksum mismatch")
		}
		
	default:
		log.Printf("BLE_LOG: Unknown binary message type: 0x%04x", header.MessageType)
	}
}

// enableBinaryProtocol sends command to enable binary protocol
func (bc *BleClient) enableBinaryProtocol() error {
	cmd := map[string]interface{}{
		"command": "enable_binary_protocol",
		"command_id": fmt.Sprintf("binary_%d", time.Now().Unix()),
	}
	
	cmdJson, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal binary protocol command: %v", err)
	}
	
	log.Printf("BLE_LOG: Enabling binary protocol support")
	
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
	options := make(map[string]interface{})
	
	if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJson, options).Err; err != nil {
		return fmt.Errorf("failed to enable binary protocol: %v", err)
	}
	
	bc.mu.Lock()
	bc.supportsBinaryProtocol = true
	bc.binaryProtocolVersion = ProtocolVersion
	bc.mu.Unlock()
	
	return nil
}

func (bc *BleClient) Disconnect() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.connected {
		bc.connected = false
		bc.bleConnected = false
		bc.fullyConnected = false
		bc.capsReceived = false

		// Stop polling ticker
		if bc.pollingTicker != nil {
			bc.pollingTicker.Stop()
			bc.pollingTicker = nil
		}

		// Stop monitoring
		if bc.stopChan != nil {
			select {
			case <-bc.stopChan:
				// Channel already closed
			default:
				close(bc.stopChan)
			}
		}

		// Stop notifications if enabled
		if bc.responseTxCharPath != "" {
			charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.responseTxCharPath)
			charObj.Call("org.bluez.GattCharacteristic1.StopNotify", 0)
		}

		// Disconnect from device
		if bc.devicePath != "" {
			deviceObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.devicePath)
			deviceObj.Call("org.bluez.Device1.Disconnect", 0)
		}

		if bc.wsHub != nil {
			bc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/ble_disconnected",
				Payload: map[string]string{
					"address": bc.targetAddress,
					"status":  "disconnected",
				},
			})
		}
	}
}

// detectImageFormatFromBytes detects the image format from the data bytes
func detectImageFormatFromBytes(data []byte) string {
	if len(data) < 12 {
		return "unknown"
	}
	
	// Check for JPEG (starts with FF D8 FF)
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "JPEG"
	}
	
	// Check for PNG (starts with 89 50 4E 47 0D 0A 1A 0A)
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 &&
		data[4] == 0x0D && data[5] == 0x0A && data[6] == 0x1A && data[7] == 0x0A {
		return "PNG"
	}
	
	// Check for WebP (starts with RIFF....WEBP)
	if data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
		return "WebP"
	}
	
	return "unknown"
}

// getImageDimensions extracts image dimensions from the data
func getImageDimensions(data []byte, format string) (width, height int) {
	switch format {
	case "JPEG":
		return getJPEGDimensions(data)
	case "PNG":
		return getPNGDimensions(data)
	case "WebP":
		return getWebPDimensions(data)
	default:
		return 0, 0
	}
}

// getJPEGDimensions extracts dimensions from JPEG data
func getJPEGDimensions(data []byte) (width, height int) {
	// JPEG dimensions are in the SOF0 segment
	for i := 2; i < len(data)-9; i++ {
		if data[i] == 0xFF && (data[i+1] == 0xC0 || data[i+1] == 0xC2) {
			// SOF0 or SOF2 marker found
			height = int(data[i+5])<<8 | int(data[i+6])
			width = int(data[i+7])<<8 | int(data[i+8])
			return width, height
		}
	}
	return 0, 0
}

// getPNGDimensions extracts dimensions from PNG data
func getPNGDimensions(data []byte) (width, height int) {
	if len(data) < 24 {
		return 0, 0
	}
	// PNG dimensions are in the IHDR chunk (bytes 16-23)
	width = int(data[16])<<24 | int(data[17])<<16 | int(data[18])<<8 | int(data[19])
	height = int(data[20])<<24 | int(data[21])<<16 | int(data[22])<<8 | int(data[23])
	return width, height
}

// getWebPDimensions extracts dimensions from WebP data
func getWebPDimensions(data []byte) (width, height int) {
	if len(data) < 30 {
		return 0, 0
	}
	// Simple WebP (VP8)
	if data[12] == 'V' && data[13] == 'P' && data[14] == '8' && data[15] == ' ' {
		// VP8 bitstream - dimensions at offset 26-29
		width = int(data[26]) | int(data[27])<<8
		height = int(data[28]) | int(data[29])<<8
		return (width & 0x3fff) + 1, (height & 0x3fff) + 1
	}
	// VP8L
	if data[12] == 'V' && data[13] == 'P' && data[14] == '8' && data[15] == 'L' {
		// VP8L - dimensions encoded differently, simplified extraction
		if len(data) > 25 {
			bits := uint32(data[21]) | uint32(data[22])<<8 | uint32(data[23])<<16 | uint32(data[24])<<24
			width = int(bits&0x3FFF) + 1
			height = int((bits>>14)&0x3FFF) + 1
			return width, height
		}
	}
	return 0, 0
}

// Test mode methods - separate from production album art flow

// SendTestAlbumArtRequest sends a test album art request command
func (bc *BleClient) SendTestAlbumArtRequest() error {
	// First request high priority connection
	log.Printf("BLE_LOG: Requesting high priority connection for test transfer")
	highPriorityCmd := map[string]interface{}{
		"command": "request_high_priority_connection",
		"reason": "test_album_art_transfer",
	}
	if cmdJson, err := json.Marshal(highPriorityCmd); err == nil {
		bc.mu.RLock()
		if bc.conn != nil && bc.commandRxCharPath != "" {
			charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
			options := make(map[string]interface{})
			charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJson, options)
		}
		bc.mu.RUnlock()
		
		// Give it a moment to apply
		time.Sleep(100 * time.Millisecond)
		
		// Also try to re-optimize connection parameters
		go bc.optimizeConnectionParameters()
	}
	
	// Send a special test command that the companion app will recognize
	cmd := utils.MediaCommand{
		Command: "test_album_art_request",
	}
	cmdJson, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal test album art request: %v", err)
	}

	log.Printf("BLE_LOG: Sending test album art request")
	
	// Check connection state
	bc.mu.RLock()
	if bc.conn == nil || bc.commandRxCharPath == "" {
		bc.mu.RUnlock()
		return fmt.Errorf("BLE connection not established")
	}
	conn := bc.conn
	charPath := bc.commandRxCharPath
	bc.mu.RUnlock()
	
	bc.testAlbumArtMutex.Lock()
	bc.testAlbumArtStartTime = time.Now()
	bc.testAlbumArtReceiving = false
	bc.testAlbumArtBuffer = nil
	bc.testAlbumArtChunks = make(map[int][]byte)
	bc.testAlbumArtTotalChunks = 0
	bc.testAlbumArtMutex.Unlock()
	
	charObj := conn.Object(BLUEZ_BUS_NAME, charPath)
	options := make(map[string]interface{})
	return charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJson, options).Err
}

// GetTestTransferStatus returns the current test transfer status
func (bc *BleClient) GetTestTransferStatus() map[string]interface{} {
	bc.testAlbumArtMutex.Lock()
	defer bc.testAlbumArtMutex.Unlock()
	
	status := map[string]interface{}{
		"active": bc.testAlbumArtReceiving,
		"chunks_received": len(bc.testAlbumArtChunks),
		"total_chunks": bc.testAlbumArtTotalChunks,
		"size": bc.testAlbumArtSize,
		"checksum": bc.testAlbumArtChecksum,
	}
	
	if !bc.testAlbumArtStartTime.IsZero() {
		status["elapsed_ms"] = time.Since(bc.testAlbumArtStartTime).Milliseconds()
	}
	
	if bc.testAlbumArtTotalChunks > 0 {
		status["progress_percent"] = (len(bc.testAlbumArtChunks) * 100) / bc.testAlbumArtTotalChunks
	}
	
	return status
}

// handleTestAlbumArtStart handles the start of a test album art transfer
func (bc *BleClient) handleTestAlbumArtStart(payload map[string]interface{}) {
	bc.testAlbumArtMutex.Lock()
	defer bc.testAlbumArtMutex.Unlock()
	
	// Extract metadata - handle both float64 (from JSON) and uint32 (from binary)
	switch v := payload["size"].(type) {
	case float64:
		bc.testAlbumArtSize = int(v)
	case uint32:
		bc.testAlbumArtSize = int(v)
	case int:
		bc.testAlbumArtSize = v
	}
	
	if checksum, ok := payload["checksum"].(string); ok {
		bc.testAlbumArtChecksum = checksum
	}
	
	switch v := payload["total_chunks"].(type) {
	case float64:
		bc.testAlbumArtTotalChunks = int(v)
	case uint32:
		bc.testAlbumArtTotalChunks = int(v)
	case int:
		bc.testAlbumArtTotalChunks = v
	}
	
	bc.testAlbumArtReceiving = true
	bc.testAlbumArtBuffer = make([]byte, 0, bc.testAlbumArtSize)
	bc.testAlbumArtChunks = make(map[int][]byte)
	bc.testAlbumArtStartTime = time.Now()
	bc.testAlbumArtLastProgressUpdate = 0
	
	log.Printf("BLE_LOG: Test album art transfer started - size: %d, chunks: %d", 
		bc.testAlbumArtSize, bc.testAlbumArtTotalChunks)
	
	// Broadcast test transfer start
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "test/album_art_transfer_start",
			Payload: map[string]interface{}{
				"size": bc.testAlbumArtSize,
				"total_chunks": bc.testAlbumArtTotalChunks,
				"checksum": bc.testAlbumArtChecksum,
				"timestamp": time.Now().UnixMilli(),
			},
		})
	}
}

// handleTestAlbumArtChunk handles a test album art chunk
func (bc *BleClient) handleTestAlbumArtChunk(chunkIndex int, data []byte) {
	bc.testAlbumArtMutex.Lock()
	defer bc.testAlbumArtMutex.Unlock()
	
	if !bc.testAlbumArtReceiving {
		return
	}
	
	// Store chunk
	bc.testAlbumArtChunks[chunkIndex] = data
	
	// Only send WebSocket updates at 25% intervals to reduce overhead
	/*if bc.wsHub != nil && bc.testAlbumArtTotalChunks > 0 {
		progressPercent := (len(bc.testAlbumArtChunks) * 100) / bc.testAlbumArtTotalChunks
		
		// Send updates at 25%, 50%, 75% progress
		if progressPercent >= bc.testAlbumArtLastProgressUpdate + 25 {
			bc.testAlbumArtLastProgressUpdate = (progressPercent / 25) * 25
			
			bc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "test/album_art_progress",
				Payload: map[string]interface{}{
					"chunks_received": len(bc.testAlbumArtChunks),
					"total_chunks": bc.testAlbumArtTotalChunks,
					"progress_percent": progressPercent,
				},
			})
		}
	}*/
	
	// Check if all chunks received
	if len(bc.testAlbumArtChunks) == bc.testAlbumArtTotalChunks {
		bc.assembleTestAlbumArt()
	}
}

// assembleTestAlbumArt assembles the test album art from chunks
func (bc *BleClient) assembleTestAlbumArt() {
	// Assemble in order
	bc.testAlbumArtBuffer = make([]byte, 0, bc.testAlbumArtSize)
	for i := 0; i < bc.testAlbumArtTotalChunks; i++ {
		if chunk, ok := bc.testAlbumArtChunks[i]; ok {
			bc.testAlbumArtBuffer = append(bc.testAlbumArtBuffer, chunk...)
		} else {
			log.Printf("BLE_LOG: Missing test chunk %d", i)
			bc.handleTestAlbumArtError("missing chunk")
			return
		}
	}
	
	// Verify checksum
	/*if bc.testAlbumArtChecksum != "" {
		hash := sha256.Sum256(bc.testAlbumArtBuffer)
		checksum := hex.EncodeToString(hash[:])
		if checksum != bc.testAlbumArtChecksum {
			log.Printf("BLE_LOG: Test album art checksum mismatch")
			bc.handleTestAlbumArtError("checksum mismatch")
			return
		}
	}*/
	// Broadcast completion
	transferTime := time.Since(bc.testAlbumArtStartTime).Milliseconds()
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "test/album_art_transfer_complete",
			Payload: map[string]interface{}{
				"size": len(bc.testAlbumArtBuffer),
				"format": "Webp",
				"width": 300,
				"height": 300,
				"checksum": "sds",//bc.testAlbumArtChecksum,
				"total_chunks": "2323",//bc.testAlbumArtTotalChunks,
				"transfer_time_ms": transferTime,
				"timestamp": time.Now().UnixMilli(),
			},
		})
	}
	
	// Save to test location
	testPath := "/tmp/test_album_art.jpg"
	if err := os.WriteFile(testPath, bc.testAlbumArtBuffer, 0644); err != nil {
		log.Printf("BLE_LOG: Failed to save test album art: %v", err)
		bc.handleTestAlbumArtError("save failed")
		return
	}
	
	// Detect format and dimensions
	//imageFormat := detectImageFormatFromBytes(bc.testAlbumArtBuffer)
	//imageWidth, imageHeight := getImageDimensions(bc.testAlbumArtBuffer, imageFormat)
	
	
	
	
	
	log.Printf("BLE_LOG: Test album art transfer complete - %d bytes in %dms", 
		len(bc.testAlbumArtBuffer), transferTime)
	
	// Reset state
	bc.testAlbumArtReceiving = false
	bc.testAlbumArtBuffer = nil
	bc.testAlbumArtChunks = nil
}

// handleTestAlbumArtError handles test album art transfer errors
func (bc *BleClient) handleTestAlbumArtError(reason string) {
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "test/album_art_transfer_failed",
			Payload: map[string]interface{}{
				"reason": reason,
				"timestamp": time.Now().UnixMilli(),
			},
		})
	}
	
	bc.testAlbumArtReceiving = false
	bc.testAlbumArtBuffer = nil
	bc.testAlbumArtChunks = nil
}

// optimizeConnectionParameters attempts to set optimal BLE connection parameters
func (bc *BleClient) optimizeConnectionParameters() error {
	log.Println("BLE_LOG: Attempting to optimize connection parameters for low latency")
	
	// Get device object for D-Bus operations
	deviceObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.devicePath)
	
	// BlueZ doesn't expose connection interval parameters directly through D-Bus for connected devices
	// However, we can try to request connection parameter update through the kernel
	// The Android device (peripheral) can also request parameter updates from its side
	
	// Log current connection properties available through D-Bus
	var connectionInfo map[string]dbus.Variant
	if err := deviceObj.Call("org.freedesktop.DBus.Properties.GetAll", 0, BLUEZ_DEVICE_INTERFACE).Store(&connectionInfo); err == nil {
		// Log all available properties for debugging
		log.Println("BLE_LOG: Current device properties:")
		for key, value := range connectionInfo {
			if key == "RSSI" || key == "TxPower" || key == "Connected" || key == "ServicesResolved" {
				log.Printf("BLE_LOG:   %s: %v", key, value.Value())
			}
		}
	}
	
	// Try to read connection parameters from kernel debug filesystem if available
	// This helps us understand the current connection state
	connHandle := bc.getConnectionHandle()
	if connHandle != "" {
		bc.logKernelConnectionParams(connHandle)
	}
	
	// As the GATT client (central), we have control over connection parameters
	// Try to update them through the kernel interface
	if connHandle != "" {
		bc.updateKernelConnectionParams(connHandle)
	} else {
		// Try to update global default parameters
		log.Println("BLE_LOG: No per-connection handle found, trying global parameters")
		bc.updateGlobalConnectionParams()
	}
	
	// Also notify the Android app about our optimization attempt
	go func() {
		time.Sleep(500 * time.Millisecond) // Wait for connection to stabilize
		
		// Send a notification about connection optimization
		cmd := map[string]interface{}{
			"command": "connection_parameters_updated",
			"reason": "client_optimization",
			"target_interval_ms": 7.5,
			"note": "Car Thing (client) has requested fast connection parameters",
		}
		
		cmdJson, err := json.Marshal(cmd)
		if err == nil && bc.commandRxCharPath != "" {
			charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
			options := make(map[string]interface{})
			if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJson, options).Err; err == nil {
				log.Println("BLE_LOG: Notified Android app of connection parameter optimization")
			}
		}
	}()
	
	log.Println("BLE_LOG: Connection optimization complete")
	log.Println("BLE_LOG:   - As GATT client, we control connection parameters")
	log.Println("BLE_LOG:   - Target intervals: 7.5-15ms for fast transfers")
	log.Println("BLE_LOG:   - Check logs above for actual parameter values")
	
	return nil
}

// getConnectionHandle tries to find the kernel connection handle for our device
func (bc *BleClient) getConnectionHandle() string {
	// Try to find the connection handle from /sys/kernel/debug/bluetooth/hci0/
	debugPath := "/sys/kernel/debug/bluetooth/hci0"
	
	// First check if debugfs is mounted
	if _, err := os.Stat(debugPath); os.IsNotExist(err) {
		log.Println("BLE_LOG: Debug filesystem not available at", debugPath)
		return ""
	}
	
	entries, err := os.ReadDir(debugPath)
	if err != nil {
		log.Printf("BLE_LOG: Cannot read debug path: %v", err)
		return ""
	}
	
	// Look for directories that match our device address pattern
	addressLower := strings.ToLower(strings.Replace(bc.targetAddress, ":", "", -1))
	for _, entry := range entries {
		if entry.IsDir() {
			// Check if it's a numeric handle (connection ID)
			if _, err := strconv.Atoi(entry.Name()); err == nil {
				// This could be a connection handle, check if it has conn_info
				connInfoPath := fmt.Sprintf("%s/%s/conn_info", debugPath, entry.Name())
				if _, err := os.Stat(connInfoPath); err == nil {
					log.Printf("BLE_LOG: Found potential connection handle: %s", entry.Name())
					// Read conn_info to verify it's our device
					if data, err := os.ReadFile(connInfoPath); err == nil {
						if strings.Contains(string(data), bc.targetAddress) {
							log.Printf("BLE_LOG: Confirmed connection handle %s for device %s", entry.Name(), bc.targetAddress)
							return entry.Name()
						}
					}
				}
			}
			
			// Also check for address-based directories
			if strings.Contains(entry.Name(), addressLower) {
				log.Printf("BLE_LOG: Found connection handle by address: %s", entry.Name())
				return entry.Name()
			}
		}
	}
	
	log.Printf("BLE_LOG: No connection handle found for device %s", bc.targetAddress)
	return ""
}

// logKernelConnectionParams logs connection parameters from kernel debug filesystem
func (bc *BleClient) logKernelConnectionParams(handle string) {
	debugPath := fmt.Sprintf("/sys/kernel/debug/bluetooth/hci0/%s", handle)
	
	// Read various connection parameter files
	params := map[string]string{
		"conn_info": "Connection info",
		"conn_min_interval": "Min interval", 
		"conn_max_interval": "Max interval",
		"conn_latency": "Slave latency",
		"supervision_timeout": "Supervision timeout",
	}
	
	for file, desc := range params {
		path := fmt.Sprintf("%s/%s", debugPath, file)
		if data, err := os.ReadFile(path); err == nil {
			value := strings.TrimSpace(string(data))
			log.Printf("BLE_LOG: %s: %s", desc, value)
			
			// Parse and show human-readable values
			if intVal, err := strconv.Atoi(value); err == nil {
				switch file {
				case "conn_min_interval", "conn_max_interval":
					log.Printf("BLE_LOG:   = %.1f ms", float64(intVal)*1.25)
				case "supervision_timeout":
					log.Printf("BLE_LOG:   = %d ms", intVal*10)
				}
			}
		}
	}
}

// updateKernelConnectionParams attempts to update connection parameters via sysfs
func (bc *BleClient) updateKernelConnectionParams(handle string) {
	// Connection parameters in kernel units
	minInterval := 6   // 7.5ms (6 * 1.25ms)
	maxInterval := 12  // 15ms (12 * 1.25ms) 
	latency := 0       // No slave latency for lowest latency
	timeout := 500     // 5000ms supervision timeout (500 * 10ms)
	
	log.Printf("BLE_LOG: Attempting to update kernel connection parameters")
	log.Printf("BLE_LOG:   Target min interval: %d units (%.1fms)", minInterval, float64(minInterval)*1.25)
	log.Printf("BLE_LOG:   Target max interval: %d units (%.1fms)", maxInterval, float64(maxInterval)*1.25)
	log.Printf("BLE_LOG:   Target latency: %d events", latency)
	log.Printf("BLE_LOG:   Target timeout: %d units (%dms)", timeout, timeout*10)
	
	// Try to write to sysfs files (requires root and may not be available)
	basePath := fmt.Sprintf("/sys/kernel/debug/bluetooth/hci0/%s", handle)
	
	// These files might be writable on some systems
	params := map[string]string{
		"conn_min_interval": fmt.Sprintf("%d\n", minInterval),
		"conn_max_interval": fmt.Sprintf("%d\n", maxInterval),
		"conn_latency": fmt.Sprintf("%d\n", latency),
		"supervision_timeout": fmt.Sprintf("%d\n", timeout),
	}
	
	successCount := 0
	for param, value := range params {
		path := fmt.Sprintf("%s/%s", basePath, param)
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			log.Printf("BLE_LOG: Unable to write %s: %v", param, err)
		} else {
			log.Printf("BLE_LOG: Successfully updated %s", param)
			successCount++
		}
	}
	
	if successCount > 0 {
		log.Printf("BLE_LOG: Updated %d connection parameters", successCount)
		// Read back the values to confirm
		time.Sleep(100 * time.Millisecond)
		log.Println("BLE_LOG: Reading back connection parameters:")
		bc.logKernelConnectionParams(handle)
	} else {
		log.Println("BLE_LOG: Unable to update connection parameters (requires root)")
	}
}

// updateGlobalConnectionParams attempts to update global default connection parameters
func (bc *BleClient) updateGlobalConnectionParams() {
	debugPath := "/sys/kernel/debug/bluetooth/hci0"
	
	// First, log current global parameters
	log.Println("BLE_LOG: Current global connection parameters:")
	globalParams := map[string]string{
		"conn_min_interval": "Min interval", 
		"conn_max_interval": "Max interval",
		"conn_latency": "Latency",
		"supervision_timeout": "Supervision timeout",
	}
	
	for file, desc := range globalParams {
		path := fmt.Sprintf("%s/%s", debugPath, file)
		if data, err := os.ReadFile(path); err == nil {
			value := strings.TrimSpace(string(data))
			log.Printf("BLE_LOG: Global %s: %s", desc, value)
			if intVal, err := strconv.Atoi(value); err == nil {
				switch file {
				case "conn_min_interval", "conn_max_interval":
					log.Printf("BLE_LOG:   = %.1f ms", float64(intVal)*1.25)
				case "supervision_timeout":
					log.Printf("BLE_LOG:   = %d ms", intVal*10)
				}
			}
		}
	}
	
	// Try to update global parameters
	log.Println("BLE_LOG: Attempting to update global connection parameters...")
	
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
			log.Printf("BLE_LOG: Cannot update global %s: %v", param, err)
		} else {
			log.Printf("BLE_LOG: Successfully updated global %s to %s", param, value)
			successCount++
		}
	}
	
	if successCount > 0 {
		log.Printf("BLE_LOG: Updated %d global parameters", successCount)
		// Read back the values
		time.Sleep(100 * time.Millisecond)
		log.Println("BLE_LOG: New global connection parameters:")
		for file, desc := range globalParams {
			path := fmt.Sprintf("%s/%s", debugPath, file)
			if data, err := os.ReadFile(path); err == nil {
				value := strings.TrimSpace(string(data))
				log.Printf("BLE_LOG: Global %s: %s", desc, value)
			}
		}
		
		// IMPORTANT: Global parameters only apply to NEW connections
		// We need to disconnect and reconnect for them to take effect
		log.Println("BLE_LOG: Global parameters updated - these will apply to new connections")
		log.Println("BLE_LOG: Note: Current connection may still use old parameters")
	}
}