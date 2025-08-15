package bluetooth

import (
	"bytes"
	"compress/gzip"
	//"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	//"encoding/hex"
	"encoding/json" // Still needed for legacy fallback
	"fmt"
	"io"
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
	
	// Current track metadata - accessible from all functions
	currentArtist    string
	currentAlbum     string
	currentTrack     string
	
	// Track change detection
	previousTrackIdentifier string
	hasNewTimestamp         bool
	pendingTimestamp        int64
	
	// State deduplication
	lastStateHash           string
	lastStateTimestamp      time.Time

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
	
	// Weather data management
	weatherState      *utils.WeatherState
	weatherMutex      sync.RWMutex
	
	// Binary protocol support
	supportsBinaryProtocol bool
	binaryProtocolVersion  byte
	
	// Album art callback
	albumArtCallback func([]byte)
	
	// Album art request tracking
	pendingAlbumArtRequest  bool
	pendingAlbumArtHash     string
	albumArtRequestMutex    sync.Mutex
	lastAlbumArtRequestTime time.Time
	
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
	
	// Weather binary transfer management
	weatherBinaryReceiving     bool
	weatherBinaryBuffer        []byte
	weatherBinaryChunks        map[int][]byte
	weatherBinaryChecksum      string
	weatherBinaryTotalChunks   int
	weatherBinaryMode          string // "hourly" or "weekly"
	weatherBinaryLocation      string
	weatherBinaryStartTime     time.Time
	weatherBinaryMutex         sync.Mutex
	
	// PHY mode tracking
	currentPHYMode string // "1M PHY", "2M PHY", or "Unknown"
}

func NewBleClient(btManager *BluetoothManager, wsHub *utils.WebSocketHub) *BleClient {
	return &BleClient{
		btManager:     btManager,
		wsHub:         wsHub,
		conn:          btManager.conn, // Reuse existing D-Bus connection
		stopChan:      make(chan struct{}),
		mtu:           DefaultMTU,
		commandQueue:  make(chan *CommandQueueItem, 100), // Buffer up to 100 commands
		currentPHYMode: "Unknown",
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
	
	// Reset connection state for clean start
	bc.capsReceived = false
	bc.fullyConnected = false
	bc.supportsBinaryProtocol = false

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
			var connVariant dbus.Variant
			if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, BLUEZ_DEVICE_INTERFACE, "Connected").Store(&connVariant); err == nil {
				if connVal, ok := connVariant.Value().(bool); ok && connVal {
					connected = true
					log.Println("BLE_LOG: Device connected successfully")
					break
				}
			}
			
			if i == maxAttempts-1 {
				return fmt.Errorf("timeout waiting for device connection")
			}
		}
	} else {
		log.Println("BLE_LOG: Device already connected")
	}

	bc.bleConnected = true

	// Perform sequential connection optimization for 2M PHY
	// BCM20703A2 requires sequential negotiation: MTU→Priority→PHY with delays
	if err := bc.negotiateOptimalConnectionParams(); err != nil {
		log.Printf("BLE_LOG: Warning - connection optimization incomplete: %v", err)
		// Continue with default parameters
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
		var srvVariant dbus.Variant
		if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0, BLUEZ_DEVICE_INTERFACE, "ServicesResolved").Store(&srvVariant); err == nil {
			if srvVal, ok := srvVariant.Value().(bool); ok && srvVal {
				servicesResolved = true
				log.Println("BLE_LOG: Services resolved")
				break
			}
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
	
	// Request capabilities immediately - Android sends time sync after notifications are enabled
	log.Println("BLE_LOG: Requesting device capabilities immediately...")
	go func() {
		// No delay - send immediately to be ready for Android's initial messages
		// Android sends time sync + state 50ms after notifications are enabled
		
		// Create binary message for get capabilities
		messageID := uint16(time.Now().Unix() & 0xFFFF)
		cmdData := CreateMessage(MsgGetCapabilities, []byte{}, messageID)
		
		charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
		options := make(map[string]interface{})
		
		if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdData, options).Err; err != nil {
			log.Printf("BLE_LOG: Failed to request capabilities: %v", err)
			
			// Retry after a short delay if initial request fails
			time.Sleep(100 * time.Millisecond)
			if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdData, options).Err; err != nil {
				log.Printf("BLE_LOG: Retry failed for capabilities request: %v", err)
			} else {
				log.Printf("BLE_LOG: Capabilities request sent on retry (binary, ID: %d)", messageID)
			}
		} else {
			log.Printf("BLE_LOG: Capabilities request sent (binary, ID: %d)", messageID)
		}
		
		// Set up retry timer for capabilities if not received
		go func() {
			time.Sleep(2 * time.Second)
			bc.mu.RLock()
			capsReceived := bc.capsReceived
			bc.mu.RUnlock()
			
			if !capsReceived {
				log.Println("BLE_LOG: Capabilities not received after 2s, retrying...")
				messageID := uint16(time.Now().Unix() & 0xFFFF)
				cmdData := CreateMessage(MsgGetCapabilities, []byte{}, messageID)
				
				charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
				options := make(map[string]interface{})
				
				if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdData, options).Err; err != nil {
					log.Printf("BLE_LOG: Failed to retry capabilities request: %v", err)
				} else {
					log.Printf("BLE_LOG: Capabilities retry sent (binary, ID: %d)", messageID)
				}
			}
		}()
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
	// Add panic recovery for this critical goroutine
	defer func() {
		if r := recover(); r != nil {
			log.Printf("BLE_LOG: PANIC in handleBleNotifications: %v - attempting to reconnect", r)
			bc.handleConnectionLoss()
		}
	}()
	
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
				var variant dbus.Variant
				if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0, BLUEZ_DEVICE_INTERFACE, "Connected").Store(&variant); err != nil {
					connectionCheckFailures++
					log.Printf("BLE_LOG: Error checking connection (failure %d/3): %v", connectionCheckFailures, err)
					// Only handle connection loss after 3 consecutive failures
					if connectionCheckFailures >= 3 {
						bc.handleConnectionLoss()
						return
					}
					continue
				}
				
				// Extract boolean value from variant
				if connectedVal, ok := variant.Value().(bool); ok {
					connected = connectedVal
				} else {
					log.Printf("BLE_LOG: Warning - could not extract Connected value from variant")
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
	// Add panic recovery for notification monitoring
	defer func() {
		if r := recover(); r != nil {
			log.Printf("BLE_LOG: PANIC in monitorCharacteristicNotifications: %v - restarting monitor", r)
			// Restart the monitor after a delay
			time.Sleep(1 * time.Second)
			go bc.monitorCharacteristicNotifications()
		}
	}()
	
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
									// Check for connection state changes
									if connectedProp, exists := changedProps["Connected"]; exists {
										if connectedVal, ok := connectedProp.Value().(bool); ok && !connectedVal {
											log.Printf("BLE_LOG: Device disconnected signal received")
											go bc.handleConnectionLoss()
											continue
										}
									}
									
									// Check for ServicesResolved changes
									if resolvedProp, exists := changedProps["ServicesResolved"]; exists {
										if resolvedVal, ok := resolvedProp.Value().(bool); ok && !resolvedVal {
											log.Printf("BLE_LOG: Services no longer resolved")
											go bc.handleConnectionLoss()
											continue
										}
									}
									
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

	// Always try binary protocol first (v2 has version in header)
	if len(data) >= BinaryHeaderSize {
		// Try to parse as enhanced binary protocol
		header, payload, err := ParseMessage(data)
		if err == nil {
			log.Printf("BLE_LOG: Received binary message type: 0x%04X (%s), size: %d", 
				header.MessageType, GetMessageTypeString(header.MessageType), header.PayloadSize)
			bc.handleBinaryMessageV2(header, payload)
			return
		} else {
			// Log why binary v2 parsing failed - show first 16 bytes in hex
			hexBytes := ""
			for i := 0; i < len(data) && i < 16; i++ {
				hexBytes += fmt.Sprintf("%02x ", data[i])
			}
			log.Printf("BLE_LOG: Binary v2 parse failed: %v (data len: %d, first bytes: %s)", err, len(data), hexBytes)
		}
		
		// Try legacy binary protocol for album art
		legacyHeader, legacyPayload, err := ParseBinaryMessage(data)
		if err == nil {
			bc.handleBinaryMessage(legacyHeader, legacyPayload)
			return
		} else {
			// Log why legacy binary parsing failed
			log.Printf("BLE_LOG: Legacy binary parse failed: %v", err)
		}
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
			IncrementalUpdates    bool     `json:"incremental_updates"`
		}
		if err := json.Unmarshal(data, &caps); err == nil {
			log.Printf("BLE Capabilities: Version %s, MTU %d, Features: %v, Binary Protocol: %v, Incremental: %v", 
				caps.Version, caps.MTU, caps.Features, caps.BinaryProtocol, caps.IncrementalUpdates)
			
			// Check if incremental updates are supported
			if caps.IncrementalUpdates {
				log.Printf("BLE_LOG: Device supports incremental updates, will enable binary protocol...")
				// Enable binary incremental updates with retry
				go func() {
					time.Sleep(100 * time.Millisecond) // Small delay to ensure connection is stable
					
					// Try up to 3 times to enable binary protocol
					for attempt := 1; attempt <= 3; attempt++ {
						log.Printf("BLE_LOG: Enabling binary protocol (attempt %d/3)...", attempt)
						if err := bc.enableBinaryProtocol(); err != nil {
							log.Printf("BLE_LOG: Failed to enable binary protocol (attempt %d): %v", attempt, err)
							if attempt < 3 {
								time.Sleep(time.Duration(attempt) * time.Second) // Exponential backoff
							}
						} else {
							log.Printf("BLE_LOG: Binary protocol enable command sent successfully")
							
							// Wait up to 3 seconds for confirmation
							go func() {
								time.Sleep(3 * time.Second)
								bc.mu.RLock()
								enabled := bc.supportsBinaryProtocol
								bc.mu.RUnlock()
								
								if !enabled {
									log.Printf("BLE_LOG: WARNING - Binary protocol not confirmed after 3 seconds")
									log.Printf("BLE_LOG: Connection will continue in JSON mode")
								} else {
									log.Printf("BLE_LOG: Binary protocol verified as ACTIVE")
								}
							}()
							break
						}
					}
				}()
			} else if caps.BinaryProtocol && caps.BinaryProtocolVersion >= 1 {
				log.Printf("BLE_LOG: Device supports binary protocol v%d but not incremental updates", caps.BinaryProtocolVersion)
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
		
	case "timestampResponse":
		// Timestamp response from request_timestamp command
		var timestampResp struct {
			Type       string `json:"type"`
			PositionMs int64  `json:"position_ms"`
		}
		if err := json.Unmarshal(data, &timestampResp); err == nil {
			log.Printf("BLE_LOG: Received timestamp response: %dms", timestampResp.PositionMs)
			bc.mu.Lock()
			bc.pendingTimestamp = timestampResp.PositionMs
			bc.hasNewTimestamp = true
			bc.mu.Unlock()
			
			// Broadcast the timestamp sync event
			bc.broadcastStateUpdate()
		}
		
	case "pong":
		// Speed test pong response
		var pongResp struct {
			Type       string `json:"type"`
			CommandId  string `json:"command_id"`
			Timestamp  int64  `json:"timestamp"`
			ReceivedAt int64  `json:"received_at"`
		}
		if err := json.Unmarshal(data, &pongResp); err == nil {
			speedTestState.mu.Lock()
			if startTime, exists := speedTestState.pingStartTimes[pongResp.CommandId]; exists {
				latency := time.Since(startTime).Milliseconds()
				speedTestState.latencies = append(speedTestState.latencies, latency)
				delete(speedTestState.pingStartTimes, pongResp.CommandId)
				log.Printf("BLE_LOG: Pong received for %s - latency: %dms", pongResp.CommandId, latency)
				
				// Broadcast latency result
				if bc.wsHub != nil {
					bc.wsHub.Broadcast(utils.WebSocketEvent{
						Type: "test/bt_speed_pong",
						Payload: map[string]interface{}{
							"latency_ms": latency,
							"command_id": pongResp.CommandId,
						},
					})
				}
			}
			speedTestState.mu.Unlock()
		}
		
	case "throughput_chunk_ack":
		// Throughput test acknowledgment
		var ackResp struct {
			Type        string `json:"type"`
			CommandId   string `json:"command_id"`
			ChunkNum    int    `json:"chunk_num"`
			TotalChunks int    `json:"total_chunks"`
			DataSize    int    `json:"data_size"`
			Timestamp   int64  `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &ackResp); err == nil {
			speedTestState.mu.Lock()
			speedTestState.chunksReceived++
			speedTestState.totalDataReceived += ackResp.DataSize
			speedTestState.mu.Unlock()
			
			log.Printf("BLE_LOG: Throughput chunk %d/%d acknowledged (%d bytes)", 
				ackResp.ChunkNum, ackResp.TotalChunks, ackResp.DataSize)
		}
		
	case "throughput_test_complete":
		// Throughput test completed on Android side
		log.Printf("BLE_LOG: Throughput test acknowledged by Android")
		
	case "2m_phy_response":
		// 2M PHY negotiation response from Android
		var phyResp struct {
			Type      string `json:"type"`
			CommandId string `json:"command_id"`
			Success   bool   `json:"success"`
			Timestamp int64  `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &phyResp); err == nil {
			log.Printf("BLE_LOG: 2M PHY response - success: %v", phyResp.Success)
			
			// Update PHY mode tracking
			if phyResp.Success {
				bc.currentPHYMode = "2M PHY"
			} else {
				// Try to check actual PHY from device properties
				if bc.devicePath != "" {
					deviceObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.devicePath)
					var txPhy uint8
					if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0, 
						BLUEZ_DEVICE_INTERFACE, "TxPhy").Store(&txPhy); err == nil {
						if txPhy == 2 {
							bc.currentPHYMode = "2M PHY"
						} else if txPhy == 1 {
							bc.currentPHYMode = "1M PHY"
						}
					}
				}
			}
			
			// Broadcast PHY status to UI
			if bc.wsHub != nil {
				bc.wsHub.Broadcast(utils.WebSocketEvent{
					Type: "test/2m_phy_response",
					Payload: map[string]interface{}{
						"success": phyResp.Success,
						"phy_mode": bc.currentPHYMode,
					},
				})
			}
		}
		
	case "weatherUpdate":
		// Weather update from Android
		var weatherUpdate utils.WeatherUpdate
		if err := json.Unmarshal(data, &weatherUpdate); err != nil {
			log.Printf("BLE_LOG: Failed to parse weather update: %v (data: %s)", err, string(data))
			return
		}
		
		log.Printf("BLE_LOG: Received weather update - Mode: %s, Location: %s", 
			weatherUpdate.Mode, weatherUpdate.Location.Name)
		
		// Store weather data and broadcast to WebSocket clients
		bc.handleWeatherUpdate(&weatherUpdate)
		
	case "stateUpdate":
		// Media state update
		var stateUpdate utils.MediaStateUpdate
		if err := json.Unmarshal(data, &stateUpdate); err != nil {
			log.Printf("BLE_LOG: Failed to parse media state update: %v (data: %s)", err, string(data))
			// Don't disconnect on parse errors - could be MTU truncation
			return
		}

		// Safe logging with nil checks
		trackName := ""
		artistName := ""
		albumName := ""
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
		// Check if track changed and is NOT longer than 10 minutes
		trackIdentifier := fmt.Sprintf("%s|%s|%s", artistName, albumName, trackName)
		trackChanged := bc.previousTrackIdentifier != "" && bc.previousTrackIdentifier != trackIdentifier
		isShortTrack := stateUpdate.DurationMs <= 600000 // 10 minutes or less in milliseconds
		
		if trackChanged && isShortTrack {
			log.Printf("BLE_LOG: Track changed to a short track (<=10min), resetting position to 0. Track: %s, Duration: %dms", 
				trackName, stateUpdate.DurationMs)
			stateUpdate.PositionMs = 0
		}
		
		bc.previousTrackIdentifier = trackIdentifier
		bc.currentState = &stateUpdate
		// Update the accessible track metadata fields
		bc.currentTrack = trackName
		bc.currentArtist = artistName
		bc.currentAlbum = albumName
		bc.mu.Unlock()
		
		// Check for track change and request timestamp if needed
		if bc.checkTrackChange() {
			go bc.SendTimestampRequest()
		}

		// Only request album art if we have valid metadata
		if bc.currentArtist != "" && bc.currentAlbum != "" {
			go bc.checkAndRequestAlbumArt(bc.currentArtist, bc.currentAlbum)
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
			log.Printf("BLE_LOG: ✓ Binary protocol CONFIRMED enabled, version %d", ack.Version)
			bc.mu.Lock()
			previousState := bc.supportsBinaryProtocol
			bc.supportsBinaryProtocol = true
			bc.binaryProtocolVersion = byte(ack.Version)
			bc.mu.Unlock()
			
			if !previousState {
				log.Printf("BLE_LOG: Binary protocol state changed from disabled to ENABLED")
			}
			
			// Broadcast binary protocol status
			if bc.wsHub != nil {
				bc.wsHub.Broadcast(utils.WebSocketEvent{
					Type: "media/binary_protocol_status",
					Payload: map[string]interface{}{
						"enabled": true,
						"version": ack.Version,
					},
				})
			}
		} else {
			log.Printf("BLE_LOG: Failed to parse binary_protocol_enabled acknowledgment: %v", err)
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

// handleWeatherUpdate processes incoming weather data from Android
func (bc *BleClient) handleWeatherUpdate(update *utils.WeatherUpdate) {
	bc.weatherMutex.Lock()
	
	// Initialize weather state if needed
	if bc.weatherState == nil {
		bc.weatherState = &utils.WeatherState{}
	}
	
	// Store weather data based on mode
	var shouldSaveData bool
	if update.Mode == "hourly" {
		bc.weatherState.HourlyData = update
		log.Printf("BLE_LOG: Stored hourly weather data for %s (%d hours)", 
			update.Location.Name, len(update.Hours))
		// Check if we have both hourly and weekly data to save
		shouldSaveData = bc.weatherState.WeeklyData != nil
	} else if update.Mode == "weekly" {
		bc.weatherState.WeeklyData = update
		log.Printf("BLE_LOG: Stored weekly weather data for %s (%d days)", 
			update.Location.Name, len(update.Days))
		// Check if we have both hourly and weekly data to save
		shouldSaveData = bc.weatherState.HourlyData != nil
	}
	
	bc.weatherState.LastUpdate = time.Now()
	
	// Save weather data to timestamped folder if we have both types
	if shouldSaveData {
		if err := utils.SaveWeatherData(bc.weatherState.HourlyData, bc.weatherState.WeeklyData); err != nil {
			log.Printf("BLE_LOG: Failed to save weather data: %v", err)
		} else {
			log.Printf("BLE_LOG: Weather data saved to timestamped folder for %s", update.Location.Name)
		}
	}
	
	bc.weatherMutex.Unlock()
	
	// Broadcast weather update to WebSocket clients
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "weather/update",
			Payload: update,
		})
	}
}

// GetWeatherState returns the current weather state (thread-safe)
func (bc *BleClient) GetWeatherState() *utils.WeatherState {
	bc.weatherMutex.RLock()
	defer bc.weatherMutex.RUnlock()
	
	if bc.weatherState == nil {
		return nil
	}
	
	// Return a copy to prevent external modifications
	state := &utils.WeatherState{
		LastUpdate: bc.weatherState.LastUpdate,
		HourlyData: bc.weatherState.HourlyData,
		WeeklyData: bc.weatherState.WeeklyData,
	}
	
	return state
}

// RequestWeatherRefresh sends a refresh request to the Android app
func (bc *BleClient) RequestWeatherRefresh() error {
	return bc.SendCommand("refresh_weather", nil, nil)
}

// handleWeatherBinaryStart handles the start of a weather binary transfer
func (bc *BleClient) handleWeatherBinaryStart(payload []byte) {
	// Parse weather start payload - match Android's WeatherBinaryEncoder structure
	if len(payload) < 56 { // originalSize(4) + compressedSize(4) + totalChunks(4) + reserved(4) + timestamp(8) + checksum(32) = 56 bytes minimum
		log.Printf("BLE_LOG: Weather start payload too small: %d bytes", len(payload))
		return
	}
	
	// Extract fields from binary payload (matching WeatherBinaryEncoder structure)
	originalSize := binary.BigEndian.Uint32(payload[0:4])
	compressedSize := binary.BigEndian.Uint32(payload[4:8]) 
	totalChunks := binary.BigEndian.Uint32(payload[8:12])
	_ = binary.BigEndian.Uint32(payload[12:16]) // reserved - unused
	timestampMs := binary.BigEndian.Uint64(payload[16:24])
	
	// Extract checksum (32 bytes SHA-256)
	checksum := payload[24:56]
	checksumHex := fmt.Sprintf("%x", checksum)
	
	// Extract mode and location (remaining bytes as strings)
	remainingData := payload[56:]
	parts := bytes.Split(remainingData, []byte{0}) // Null-terminated strings
	
	var mode, location string
	if len(parts) >= 2 {
		mode = string(parts[0])
		location = string(parts[1])
	}
	
	bc.weatherBinaryMutex.Lock()
	bc.weatherBinaryReceiving = true
	bc.weatherBinaryBuffer = nil
	bc.weatherBinaryChunks = make(map[int][]byte)
	bc.weatherBinaryChecksum = checksumHex
	bc.weatherBinaryTotalChunks = int(totalChunks)
	bc.weatherBinaryMode = mode
	bc.weatherBinaryLocation = location
	bc.weatherBinaryStartTime = time.Now()
	bc.weatherBinaryMutex.Unlock()
	
	log.Printf("BLE_LOG: Starting weather binary transfer - Mode: %s, Location: %s, Original: %d bytes, Compressed: %d bytes, Chunks: %d, Timestamp: %d",
		mode, location, originalSize, compressedSize, totalChunks, timestampMs)
}

// handleWeatherBinaryChunk handles a weather binary chunk
func (bc *BleClient) handleWeatherBinaryChunk(header *MessageHeader, payload []byte) {
	chunkIndex := int(header.MessageID) // Use MessageID as chunk index
	
	bc.weatherBinaryMutex.Lock()
	defer bc.weatherBinaryMutex.Unlock()
	
	if !bc.weatherBinaryReceiving {
		return
	}
	
	// Check for duplicate chunks
	if _, exists := bc.weatherBinaryChunks[chunkIndex]; exists {
		return
	}
	
	// Store chunk data
	bc.weatherBinaryChunks[chunkIndex] = payload
	
	log.Printf("BLE_LOG: Received weather chunk %d/%d (%d bytes)", 
		chunkIndex+1, bc.weatherBinaryTotalChunks, len(payload))
}

// handleWeatherBinaryEnd handles the end of a weather binary transfer
func (bc *BleClient) handleWeatherBinaryEnd(payload []byte) {
	if len(payload) < 33 { // 32 bytes checksum + 1 byte success flag
		log.Printf("BLE_LOG: Weather end payload too small: %d bytes", len(payload))
		return
	}
	
	checksum := fmt.Sprintf("%x", payload[0:32])
	success := payload[32] == 1
	
	bc.weatherBinaryMutex.Lock()
	defer bc.weatherBinaryMutex.Unlock()
	
	if !bc.weatherBinaryReceiving || bc.weatherBinaryChecksum != checksum {
		log.Printf("BLE_LOG: Weather transfer end - not receiving or checksum mismatch")
		return
	}
	
	if !success {
		log.Printf("BLE_LOG: Weather transfer failed according to sender")
		bc.resetWeatherBinaryTransfer()
		return
	}
	
	// Check if we have all chunks
	if len(bc.weatherBinaryChunks) != bc.weatherBinaryTotalChunks {
		log.Printf("BLE_LOG: Weather transfer incomplete - received %d/%d chunks", 
			len(bc.weatherBinaryChunks), bc.weatherBinaryTotalChunks)
		bc.resetWeatherBinaryTransfer()
		return
	}
	
	// Process the completed weather data
	bc.processWeatherBinaryData()
}

// resetWeatherBinaryTransfer clears the weather transfer state
func (bc *BleClient) resetWeatherBinaryTransfer() {
	bc.weatherBinaryReceiving = false
	bc.weatherBinaryBuffer = nil
	bc.weatherBinaryChunks = nil
	bc.weatherBinaryChecksum = ""
	bc.weatherBinaryTotalChunks = 0
	bc.weatherBinaryMode = ""
	bc.weatherBinaryLocation = ""
}

// processWeatherBinaryData assembles and processes the weather data
func (bc *BleClient) processWeatherBinaryData() {
	log.Printf("BLE_LOG: Processing weather binary data...")
	
	if len(bc.weatherBinaryChunks) != bc.weatherBinaryTotalChunks {
		log.Printf("BLE_LOG: Cannot process - missing chunks %d/%d", 
			len(bc.weatherBinaryChunks), bc.weatherBinaryTotalChunks)
		bc.resetWeatherBinaryTransfer()
		return
	}
	
	// Calculate total size
	totalSize := 0
	for i := 0; i < bc.weatherBinaryTotalChunks; i++ {
		chunk, exists := bc.weatherBinaryChunks[i]
		if !exists {
			log.Printf("BLE_LOG: Missing weather chunk %d", i)
			bc.resetWeatherBinaryTransfer()
			return
		}
		totalSize += len(chunk)
	}
	
	// Assemble chunks
	bc.weatherBinaryBuffer = make([]byte, totalSize)
	offset := 0
	for i := 0; i < bc.weatherBinaryTotalChunks; i++ {
		chunk := bc.weatherBinaryChunks[i]
		copy(bc.weatherBinaryBuffer[offset:], chunk)
		offset += len(chunk)
	}
	
	log.Printf("BLE_LOG: Assembled weather binary data - %d bytes from %d chunks", 
		len(bc.weatherBinaryBuffer), bc.weatherBinaryTotalChunks)
	
	// Decompress gzip data
	gzipReader, err := gzip.NewReader(bytes.NewReader(bc.weatherBinaryBuffer))
	if err != nil {
		log.Printf("BLE_LOG: Failed to create gzip reader: %v", err)
		bc.resetWeatherBinaryTransfer()
		return
	}
	defer gzipReader.Close()
	
	decompressedData, err := io.ReadAll(gzipReader)
	if err != nil {
		log.Printf("BLE_LOG: Failed to decompress weather data: %v", err)
		bc.resetWeatherBinaryTransfer()
		return
	}
	
	// Parse JSON
	var weatherUpdate utils.WeatherUpdate
	if err := json.Unmarshal(decompressedData, &weatherUpdate); err != nil {
		log.Printf("BLE_LOG: Failed to parse weather JSON: %v", err)
		bc.resetWeatherBinaryTransfer()
		return
	}
	
	log.Printf("BLE_LOG: Weather binary transfer complete - Mode: %s, Location: %s, Original: %d bytes, Compressed: %d bytes, Compression: %.1f%%", 
		bc.weatherBinaryMode, bc.weatherBinaryLocation, len(decompressedData), len(bc.weatherBinaryBuffer),
		(1.0 - float64(len(bc.weatherBinaryBuffer))/float64(len(decompressedData))) * 100.0)
	
	// Handle the weather update using existing logic
	bc.handleWeatherUpdate(&weatherUpdate)
	
	// Clean up
	bc.resetWeatherBinaryTransfer()
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

// sendCommandImmediate actually sends a command using binary protocol
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
	
	// Map command string to binary message type
	var msgType uint16
	switch cmdItem.command {
	case "play":
		msgType = MsgCmdPlay
	case "pause":
		msgType = MsgCmdPause
	case "next":
		msgType = MsgCmdNext
	case "previous":
		msgType = MsgCmdPrevious
	case "seek_to":
		msgType = MsgCmdSeekTo
	case "set_volume":
		msgType = MsgCmdSetVolume
	case "request_state":
		msgType = MsgCmdRequestState
	case "request_timestamp":
		msgType = MsgCmdRequestTimestamp
	case "album_art_query":
		msgType = MsgCmdAlbumArtQuery
	case "test_album_art_request":
		msgType = MsgCmdTestAlbumArt
	default:
		// Fall back to JSON format for unknown commands (including refresh_weather)
		log.Printf("BLE_LOG: Command %s not in binary protocol, sending as JSON", cmdItem.command)
		return bc.sendCommandJSON(cmdItem)
	}
	
	// Create command payload
	var valueMs *int64
	if cmdItem.valueMs != nil {
		ms := int64(*cmdItem.valueMs)
		valueMs = &ms
	}
	var payload []byte
	
	// Special handling for album art query
	if cmdItem.command == "album_art_query" && cmdItem.hash != "" {
		payload = CreateAlbumArtQueryPayload(cmdItem.hash)
	} else {
		payload = CreateCommandPayload(valueMs, cmdItem.valuePercent)
	}
	
	// Create binary message
	messageID := uint16(time.Now().Unix() & 0xFFFF)
	cmdData := CreateMessage(msgType, payload, messageID)

	// Check if command exceeds MTU
	bc.mu.RLock()
	currentMTU := bc.mtu
	bc.mu.RUnlock()
	
	effectiveMTU := int(currentMTU - MTUHeaderSize)
	if len(cmdData) > effectiveMTU {
		log.Printf("BLE_LOG: WARNING - Command size %d exceeds MTU %d, may fail", len(cmdData), effectiveMTU)
		// Binary protocol is more compact, but still check
	}

	log.Printf("BLE_LOG: Sending binary command: %s (0x%04X) with ID: %d (size: %d bytes)", 
		cmdItem.command, msgType, messageID, len(cmdData))

	// Send using D-Bus
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
	// BlueZ options for WriteValue - empty map uses default write with response
	options := make(map[string]interface{})
	
	// Try to send
	maxRetries := 3
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("BLE_LOG: Retry attempt %d for command %s", attempt, cmdItem.command)
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		
		err = charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdData, options).Err
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

// sendCommandJSON sends a command using JSON format for commands not in binary protocol
func (bc *BleClient) sendCommandJSON(cmdItem *CommandQueueItem) error {
	// Create JSON command structure
	command := map[string]interface{}{
		"command":    cmdItem.command,
		"command_id": cmdItem.id,
		"timestamp":  time.Now().UnixMilli(),
	}
	
	// Add optional values
	if cmdItem.valueMs != nil {
		command["value_ms"] = *cmdItem.valueMs
	}
	if cmdItem.valuePercent != nil {
		command["value_percent"] = *cmdItem.valuePercent
	}
	if cmdItem.hash != "" {
		command["hash"] = cmdItem.hash
	}
	
	// Marshal to JSON
	jsonData, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON command: %v", err)
	}
	
	// Check MTU
	bc.mu.RLock()
	currentMTU := bc.mtu
	bc.mu.RUnlock()
	
	effectiveMTU := int(currentMTU - MTUHeaderSize)
	if len(jsonData) > effectiveMTU {
		log.Printf("BLE_LOG: WARNING - JSON command size %d exceeds MTU %d, may fail", len(jsonData), effectiveMTU)
	}
	
	log.Printf("BLE_LOG: Sending JSON command: %s (size: %d bytes)", cmdItem.command, len(jsonData))
	
	// Send using D-Bus
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
	options := make(map[string]interface{})
	
	// Try to send with retries
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("BLE_LOG: Retry attempt %d for JSON command %s", attempt, cmdItem.command)
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		
		err = charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, jsonData, options).Err
		if err == nil {
			bc.lastCommandTime = time.Now()
			log.Printf("BLE_LOG: JSON command sent successfully via %s", bc.commandRxCharPath)
			// Clear active command immediately after sending
			bc.commandQueueMu.Lock()
			bc.activeCommand = nil
			bc.commandQueueMu.Unlock()
			return nil
		}
		
		// Handle common BLE errors
		if strings.Contains(err.Error(), "ATT error: 0x0e") {
			log.Printf("BLE_LOG: ATT error 0x0e - characteristic busy, will retry")
			continue
		}
		
		if strings.Contains(err.Error(), "InProgress") || strings.Contains(err.Error(), "In Progress") {
			log.Printf("BLE_LOG: Operation in progress, clearing state and retrying...")
			bc.commandQueueMu.Lock()
			bc.activeCommand = nil
			bc.commandQueueMu.Unlock()
			time.Sleep(500 * time.Millisecond)
			continue
		}
		
		log.Printf("BLE_LOG: Failed to send JSON command: %v", err)
	}
	
	return fmt.Errorf("failed to send JSON command after %d attempts: %v", maxRetries, err)
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

// UpdateLocalPlayState updates the local play/pause state immediately
func (bc *BleClient) UpdateLocalPlayState(isPlaying bool) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	
	if bc.currentState != nil {
		bc.currentState.IsPlaying = isPlaying
		log.Printf("BLE_LOG: Updated local play state to %v", isPlaying)
		// Broadcast the state change immediately
		go bc.broadcastStateUpdate()
	}
}


// checkAndRequestAlbumArt checks if album art is cached and requests it if not
func (bc *BleClient) checkAndRequestAlbumArt(artist, album string) {
	// Validate input
	if artist == "" || album == "" || artist == "<nil>" || album == "<nil>" {
		log.Printf("BLE_LOG: Skipping album art request for invalid metadata: artist=%s, album=%s", artist, album)
		return
	}
	
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
	
	// Check if we already have a pending request
	bc.albumArtRequestMutex.Lock()
	defer bc.albumArtRequestMutex.Unlock()
	
	// If we have a pending request for the same hash, skip
	if bc.pendingAlbumArtRequest && bc.pendingAlbumArtHash == hash {
		log.Printf("BLE_LOG: Album art request already pending for hash: %s", hash)
		return
	}
	
	// If we have a pending request for a different hash, check timing
	if bc.pendingAlbumArtRequest && bc.pendingAlbumArtHash != hash {
		timeSinceLastRequest := time.Since(bc.lastAlbumArtRequestTime)
		if timeSinceLastRequest < 200*time.Millisecond {
			log.Printf("BLE_LOG: Deferring album art request for %s - %s, another request in progress", artist, album)
			// Schedule a retry after the current request completes
			go func() {
				time.Sleep(300 * time.Millisecond)
				bc.checkAndRequestAlbumArt(artist, album)
			}()
			return
		}
		// If request is old, assume it failed and proceed
		if timeSinceLastRequest > 2*time.Second {
			log.Printf("BLE_LOG: Previous album art request timed out, proceeding with new request")
			bc.pendingAlbumArtRequest = false
		}
	}
	
	log.Printf("BLE_LOG: Album art not cached for %s - %s (hash: %s), requesting from companion", artist, album, hash)
	
	// Mark request as pending
	bc.pendingAlbumArtRequest = true
	bc.pendingAlbumArtHash = hash
	bc.lastAlbumArtRequestTime = time.Now()
	
	// Request album art from companion app
	go func() {
		// Add panic recovery
		defer func() {
			if r := recover(); r != nil {
				log.Printf("BLE_LOG: PANIC in album art request goroutine: %v", r)
				// Clear pending status on panic
				bc.albumArtRequestMutex.Lock()
				if bc.pendingAlbumArtHash == hash {
					bc.pendingAlbumArtRequest = false
				}
				bc.albumArtRequestMutex.Unlock()
			}
		}()
		
		if err := bc.SendCommandWithHash("album_art_query", nil, nil, hash); err != nil {
			log.Printf("BLE_LOG: Failed to request album art: %v", err)
			
			// Clear pending status on error
			bc.albumArtRequestMutex.Lock()
			if bc.pendingAlbumArtHash == hash {
				bc.pendingAlbumArtRequest = false
			}
			bc.albumArtRequestMutex.Unlock()
			
			// Retry if it was an "In Progress" error
			if strings.Contains(err.Error(), "In Progress") {
				log.Printf("BLE_LOG: Retrying album art request after In Progress error")
				time.Sleep(500 * time.Millisecond)
				bc.checkAndRequestAlbumArt(artist, album)
			}
		} else {
			// Clear pending status after successful send (will be cleared when response arrives)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("BLE_LOG: PANIC in album art timeout goroutine: %v", r)
					}
				}()
				
				time.Sleep(2 * time.Second) // Timeout for response
				bc.albumArtRequestMutex.Lock()
				if bc.pendingAlbumArtHash == hash {
					bc.pendingAlbumArtRequest = false
					log.Printf("BLE_LOG: Cleared pending album art request after timeout")
				}
				bc.albumArtRequestMutex.Unlock()
			}()
		}
	}()
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
	log.Printf("BLE_LOG: Processing album art...")
	bc.albumArtMutex.Lock()
	
	if !bc.albumArtReceiving || bc.albumArtChunks == nil {
		log.Printf("BLE_LOG: Cannot process - receiving=%v, chunks=%v", bc.albumArtReceiving, bc.albumArtChunks != nil)
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
	
	// Calculate actual size from chunks since bc.albumArtSize might be wrong
	actualSize := 0
	for i := 0; i < bc.albumArtTotalChunks; i++ {
		chunk, exists := bc.albumArtChunks[i]
		if !exists {
			log.Printf("BLE_LOG: Missing album art chunk %d", i)
			bc.albumArtReceiving = false
			bc.albumArtChunks = nil
			bc.albumArtMutex.Unlock()
			return
		}
		actualSize += len(chunk)
	}
	
	log.Printf("BLE_LOG: Assembling %d chunks into %d bytes (declared size: %d)", 
		bc.albumArtTotalChunks, actualSize, bc.albumArtSize)
	
	// Pre-allocate buffer with actual size
	bc.albumArtBuffer = make([]byte, actualSize)
	offset := 0
	
	// Assemble chunks directly into pre-allocated buffer
	for i := 0; i < bc.albumArtTotalChunks; i++ {
		chunk := bc.albumArtChunks[i]
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
	
	// Clear pending album art request flag
	bc.albumArtRequestMutex.Lock()
	bc.pendingAlbumArtRequest = false
	log.Printf("BLE_LOG: Cleared pending album art request flag after successful transfer")
	bc.albumArtRequestMutex.Unlock()
	
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
	
	// Reset binary protocol state - will be re-enabled after reconnection
	bc.supportsBinaryProtocol = false
	bc.binaryProtocolVersion = 0
	bc.capsReceived = false
	bc.fullyConnected = false
	log.Println("BLE_LOG: Reset binary protocol and connection state for clean reconnection")

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

// handleBinaryMessageV2 processes enhanced binary protocol v2 messages
func (bc *BleClient) handleBinaryMessageV2(header *MessageHeader, payload []byte) {
	switch header.MessageType {
	// System messages
	case MsgCapabilities:
		// Debug: Log the raw payload bytes
		log.Printf("BLE_LOG: Capabilities payload bytes (%d): %x", len(payload), payload)
		caps, err := ParseCapabilitiesPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse capabilities: %v", err)
			return
		}
		log.Printf("BLE Capabilities: Version %s, MTU %d, Features: %v, Debug: %v", 
			caps.Version, caps.MTU, caps.Features, caps.DebugEnabled)
		
		bc.mu.Lock()
		bc.mtu = uint16(caps.MTU)
		bc.supportsBinaryProtocol = true
		bc.binaryProtocolVersion = BinaryProtocolVersion
		bc.fullyConnected = true
		bc.capsReceived = true
		bc.mu.Unlock()
		
		// Send initial state request
		go func() {
			time.Sleep(100 * time.Millisecond)
			bc.SendCommand("request_state", nil, nil)
		}()
		
	case MsgTimeSync:
		sync, err := ParseTimeSyncPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse time sync: %v", err)
			return
		}
		log.Printf("BLE_LOG: Received time sync - Timestamp: %d, Timezone: %s", sync.TimestampMs, sync.Timezone)
		
		// SET THE SYSTEM TIME - THIS IS THE MISSING PIECE
		if err := utils.SetSystemTime(sync.TimestampMs); err != nil {
			log.Printf("BLE_LOG: ERROR - Failed to set system time: %v", err)
		} else {
			log.Printf("BLE_LOG: Successfully set system time to %d", sync.TimestampMs)
		}
		
		// Optionally set timezone if provided
		if sync.Timezone != "" {
			if err := utils.SetTimezone(sync.Timezone); err != nil {
				log.Printf("BLE_LOG: Warning - Failed to set timezone: %v", err)
			} else {
				log.Printf("BLE_LOG: Successfully set timezone to %s", sync.Timezone)
			}
		}
		
		// Broadcast time sync event with the expected type for UI
		if bc.wsHub != nil {
			bc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "system/time_updated",  // Changed from "timeSync" to match UI expectations
				Payload: map[string]interface{}{
					"timestamp_ms": sync.TimestampMs,
					"timezone":    sync.Timezone,
				},
			})
		}
		
	// State messages
	case MsgStateFull:
		state, err := ParseFullStatePayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse full state: %v", err)
			return
		}
		
		// Create a hash of the state for deduplication
		stateHash := fmt.Sprintf("%s|%s|%s|%d|%d|%t|%d",
			state.Artist, state.Album, state.Track,
			state.DurationMs, state.PositionMs,
			state.IsPlaying, state.VolumePercent)
		
		bc.mu.Lock()
		
		// Check for duplicate state within 1 second
		now := time.Now()
		if stateHash == bc.lastStateHash && now.Sub(bc.lastStateTimestamp) < 1*time.Second {
			bc.mu.Unlock()
			log.Printf("BLE_LOG: Skipping duplicate FullState (received %dms after last identical state)", 
				now.Sub(bc.lastStateTimestamp).Milliseconds())
			return
		}
		
		// Update deduplication tracking
		bc.lastStateHash = stateHash
		bc.lastStateTimestamp = now
		
		// Check if track changed and is NOT longer than 10 minutes
		trackIdentifier := fmt.Sprintf("%s|%s|%s", state.Artist, state.Album, state.Track)
		trackChanged := bc.previousTrackIdentifier != "" && bc.previousTrackIdentifier != trackIdentifier
		isShortTrack := state.DurationMs <= 600000 // 10 minutes or less in milliseconds
		
		if trackChanged && isShortTrack {
			log.Printf("BLE_LOG: Track changed to a short track (<=10min), resetting position to 0. Track: %s, Duration: %dms", 
				state.Track, state.DurationMs)
			state.PositionMs = 0
		}
		
		bc.previousTrackIdentifier = trackIdentifier
		bc.currentArtist = state.Artist
		bc.currentAlbum = state.Album
		bc.currentTrack = state.Track
		
		stateUpdate := &utils.MediaStateUpdate{
			Artist:       &state.Artist,
			Album:        &state.Album,
			Track:        &state.Track,
			DurationMs:   state.DurationMs,
			PositionMs:   state.PositionMs,
			IsPlaying:    state.IsPlaying,
			VolumePercent: state.VolumePercent,
		}
		bc.currentState = stateUpdate
		bc.mu.Unlock()
		
		// Broadcast state update using the standard function
		bc.broadcastStateUpdate()
		
		// Check for album art if we have complete metadata
		if state.Artist != "" && state.Album != "" && state.Track != "" {
			log.Printf("BLE_LOG: Complete metadata in full state, requesting album art for: %s - %s", state.Artist, state.Album)
			go bc.checkAndRequestAlbumArt(state.Artist, state.Album)
		}
		
	case MsgStateArtist:
		artist := string(payload)
		bc.mu.Lock()
		bc.currentArtist = artist
		if bc.currentState != nil {
			bc.currentState.Artist = &artist
		}
		bc.mu.Unlock()
		log.Printf("BLE_LOG: Artist update: %s", artist)
		
	case MsgStateAlbum:
		album := string(payload)
		bc.mu.Lock()
		bc.currentAlbum = album
		if bc.currentState != nil {
			bc.currentState.Album = &album
		}
		bc.mu.Unlock()
		log.Printf("BLE_LOG: Album update: %s", album)
		
	case MsgStateTrack:
		track := string(payload)
		bc.mu.Lock()
		// Check if track changed and is NOT longer than 10 minutes
		trackIdentifier := fmt.Sprintf("%s|%s|%s", bc.currentArtist, bc.currentAlbum, track)
		trackChanged := bc.previousTrackIdentifier != "" && bc.previousTrackIdentifier != trackIdentifier
		
		bc.previousTrackIdentifier = trackIdentifier
		bc.currentTrack = track
		if bc.currentState != nil {
			bc.currentState.Track = &track
			
			// Reset position if track changed and is short
			if trackChanged && bc.currentState.DurationMs <= 600000 {
				log.Printf("BLE_LOG: Track changed to a short track (<=10min), resetting position to 0. Track: %s, Duration: %dms", 
					track, bc.currentState.DurationMs)
				bc.currentState.PositionMs = 0
			}
		}
		bc.mu.Unlock()
		log.Printf("BLE_LOG: Track update: %s", track)
		
	case MsgStatePosition:
		if len(payload) >= 8 {
			position := int64(binary.BigEndian.Uint64(payload[:8]))
			bc.mu.Lock()
			if bc.currentState != nil {
				bc.currentState.PositionMs = position
			}
			bc.mu.Unlock()
			log.Printf("BLE_LOG: Position update: %dms", position)
		}
		
	case MsgStateDuration:
		if len(payload) >= 8 {
			duration := int64(binary.BigEndian.Uint64(payload[:8]))
			bc.mu.Lock()
			if bc.currentState != nil {
				bc.currentState.DurationMs = duration
			}
			bc.mu.Unlock()
			log.Printf("BLE_LOG: Duration update: %dms", duration)
		}
		
	case MsgStatePlayStatus:
		if len(payload) >= 1 {
			isPlaying := payload[0] != 0
			bc.mu.Lock()
			if bc.currentState != nil {
				bc.currentState.IsPlaying = isPlaying
			}
			bc.mu.Unlock()
			log.Printf("BLE_LOG: Play status update: %v", isPlaying)
		}
		
	case MsgStateVolume:
		if len(payload) >= 1 {
			volume := int(payload[0])
			bc.mu.Lock()
			if bc.currentState != nil {
				bc.currentState.VolumePercent = volume
			}
			bc.mu.Unlock()
			log.Printf("BLE_LOG: Volume update: %d%%", volume)
		}
		
	case MsgStateArtistAlbum:
		artist, album, err := ParseArtistAlbumPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse artist+album: %v", err)
			return
		}
		bc.mu.Lock()
		bc.currentArtist = artist
		bc.currentAlbum = album
		if bc.currentState != nil {
			bc.currentState.Artist = &artist
			bc.currentState.Album = &album
		}
		bc.mu.Unlock()
		log.Printf("BLE_LOG: Artist+Album update: %s / %s", artist, album)
		
	// Album art messages
	case MsgAlbumArtStart:
		artStart, err := UnmarshalAlbumArtStartPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse album art start: %v", err)
			return
		}
		bc.albumArtMutex.Lock()
		bc.albumArtReceiving = true
		bc.albumArtTrackID = artStart.TrackID
		bc.albumArtSize = int(artStart.ImageSize)
		bc.albumArtChecksum = BytesToHex(artStart.Checksum[:])
		bc.albumArtTotalChunks = int(artStart.TotalChunks)
		bc.albumArtChunks = make(map[int][]byte)
		bc.albumArtStartTime = time.Now()
		bc.albumArtMutex.Unlock()
		log.Printf("BLE_LOG: Album art transfer started - Track: %s, Size: %d, Chunks: %d", 
			artStart.TrackID, artStart.ImageSize, artStart.TotalChunks)
		
	case MsgAlbumArtChunk:
		// Like legacy protocol, chunk index is in header.MessageID
		bc.albumArtMutex.Lock()
		if bc.albumArtReceiving {
			// Get chunk index from header
			chunkIndex := int(header.MessageID)
			
			// Check for duplicate chunks
			if _, exists := bc.albumArtChunks[chunkIndex]; exists {
				log.Printf("BLE_LOG: Duplicate chunk %d received, ignoring", chunkIndex)
				bc.albumArtMutex.Unlock()
				return
			}
			
			// Store chunk data (entire payload is image data)
			bc.albumArtChunks[chunkIndex] = payload
			receivedCount := len(bc.albumArtChunks)
			log.Printf("BLE_LOG: Received album art chunk %d/%d (%d bytes) - Total received: %d",
				chunkIndex+1, bc.albumArtTotalChunks, len(payload), receivedCount)
		}
		bc.albumArtMutex.Unlock()
		
	case MsgAlbumArtEnd:
		artEnd, err := UnmarshalAlbumArtEndPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse album art end: %v", err)
			return
		}
		
		bc.albumArtMutex.Lock()
		if bc.albumArtReceiving && BytesToHex(artEnd.Checksum[:]) == bc.albumArtChecksum {
			if artEnd.Success {
				// Process the completed album art
				bc.albumArtMutex.Unlock()
				bc.processAlbumArt()
				log.Printf("BLE_LOG: Album art transfer completed successfully")
			} else {
				log.Printf("BLE_LOG: Album art transfer failed")
				bc.albumArtReceiving = false
				bc.albumArtBuffer = nil
				bc.albumArtChunks = nil
				bc.albumArtMutex.Unlock()
			}
		} else {
			bc.albumArtMutex.Unlock()
			log.Printf("BLE_LOG: Album art end checksum mismatch or not receiving")
		}
		
	// Error messages
	case MsgError, MsgErrorCommandFailed:
		errData, err := ParseErrorPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse error: %v", err)
			return
		}
		log.Printf("BLE ERROR: %s - %s", errData.Code, errData.Message)
		
	case MsgEnableBinaryIncremental:
		// Android app confirms binary incremental mode is enabled
		enabled := false
		if len(payload) > 0 {
			enabled = payload[0] == 1
		}
		log.Printf("BLE_LOG: Binary incremental updates %s", map[bool]string{true: "enabled", false: "disabled"}[enabled])
		
	// Weather binary transfer messages
	case MsgWeatherStart:
		bc.handleWeatherBinaryStart(payload)
		
	case MsgWeatherChunk:
		bc.handleWeatherBinaryChunk(header, payload)
		
	case MsgWeatherEnd:
		bc.handleWeatherBinaryEnd(payload)
		
	default:
		log.Printf("BLE_LOG: Unknown binary message type: 0x%04X", header.MessageType)
	}
}

// handleBinaryMessage processes binary protocol messages
func (bc *BleClient) handleBinaryMessage(header *BinaryHeader, payload []byte) {
	switch header.MessageType {
	case MsgStateArtistAlbum:
		// Handle combined artist+album incremental update
		artist, album, err := ParseArtistAlbumPayload(payload)
		if err != nil {
			log.Printf("BLE_LOG: Failed to parse artist+album payload: %v", err)
			return
		}
		
		log.Printf("BLE_LOG: Received incremental artist+album update: %s / %s", artist, album)
		
		// Update current state
		bc.mu.Lock()
		if bc.currentState == nil {
			bc.currentState = &utils.MediaStateUpdate{}
		}
		bc.currentState.Artist = &artist
		bc.currentState.Album = &album
		// Update the accessible track metadata fields
		bc.currentArtist = artist
		bc.currentAlbum = album
		hadTrack := bc.currentTrack != ""
		bc.mu.Unlock()
		
		// Check for album art if we have complete metadata
		// Request album art if:
		// 1. We have artist and album
		// 2. We already had a track (metadata is now complete)
		if artist != "" && album != "" {
			if hadTrack {
				// We have complete metadata, request album art
				log.Printf("BLE_LOG: Complete metadata available (track was already set), requesting album art")
				go bc.checkAndRequestAlbumArt(artist, album)
			}
			// If no track yet, wait for track update to trigger the request
		}
		
		// Broadcast the state update
		bc.broadcastStateUpdate()
		
	case MsgStateTrack:
		// Handle track title incremental update
		track := string(payload)
		log.Printf("BLE_LOG: Received incremental track update: %s", track)
		
		// Update current state
		bc.mu.Lock()
		if bc.currentState == nil {
			bc.currentState = &utils.MediaStateUpdate{}
		}
		
		// Check if track changed and is NOT longer than 10 minutes
		trackIdentifier := fmt.Sprintf("%s|%s|%s", bc.currentArtist, bc.currentAlbum, track)
		trackChanged := bc.previousTrackIdentifier != "" && bc.previousTrackIdentifier != trackIdentifier
		
		bc.previousTrackIdentifier = trackIdentifier
		bc.currentState.Track = &track
		// Update the accessible track metadata field
		bc.currentTrack = track
		
		// Reset position if track changed and is short
		if trackChanged && bc.currentState.DurationMs <= 600000 {
			log.Printf("BLE_LOG: Track changed to a short track (<=10min), resetting position to 0. Track: %s, Duration: %dms", 
				track, bc.currentState.DurationMs)
			bc.currentState.PositionMs = 0
		}
		
		currentArtist := bc.currentArtist
		currentAlbum := bc.currentAlbum
		bc.mu.Unlock()
		
		// Check for track change and request timestamp if needed
		if bc.checkTrackChange() {
			go bc.SendTimestampRequest()
		}
		
		// Check for album art if we have complete metadata
		if currentArtist != "" && currentAlbum != "" && track != "" {
			// We have complete metadata, request album art
			log.Printf("BLE_LOG: Complete metadata after track update, requesting album art for: %s - %s", currentArtist, currentAlbum)
			go bc.checkAndRequestAlbumArt(currentArtist, currentAlbum)
		} else if track != "" {
			// We have track but missing artist/album
			log.Printf("BLE_LOG: Track received but missing artist/album, deferring album art request")
		}
		
		// Broadcast the state update
		bc.broadcastStateUpdate()
		
	case MsgStatePosition:
		// Handle position incremental update
		if len(payload) >= 8 {
			position := int64(binary.BigEndian.Uint64(payload))
			log.Printf("BLE_LOG: Received incremental position update: %dms", position)
			
			bc.mu.Lock()
			if bc.currentState == nil {
				bc.currentState = &utils.MediaStateUpdate{}
			}
			bc.currentState.PositionMs = position
			bc.mu.Unlock()
			
			// Broadcast the state update
			bc.broadcastStateUpdate()
		}
		
	case MsgStateDuration:
		// Handle duration incremental update
		if len(payload) >= 8 {
			duration := int64(binary.BigEndian.Uint64(payload))
			log.Printf("BLE_LOG: Received incremental duration update: %dms", duration)
			
			bc.mu.Lock()
			if bc.currentState == nil {
				bc.currentState = &utils.MediaStateUpdate{}
			}
			bc.currentState.DurationMs = duration
			currentArtist := bc.currentArtist
			currentAlbum := bc.currentAlbum
			currentTrack := bc.currentTrack
			bc.mu.Unlock()
			
			// Check for album art if we have complete metadata
			// Duration often arrives last in the sequence
			if currentArtist != "" && currentAlbum != "" && currentTrack != "" {
				log.Printf("BLE_LOG: Complete metadata after duration update, requesting album art")
				go bc.checkAndRequestAlbumArt(currentArtist, currentAlbum)
			}
			
			// Broadcast the state update
			bc.broadcastStateUpdate()
		}
		
	case MsgStatePlayStatus:
		// Handle play state incremental update
		if len(payload) >= 1 {
			isPlaying := payload[0] != 0
			log.Printf("BLE_LOG: Received incremental play state update: %v", isPlaying)
			
			bc.mu.Lock()
			if bc.currentState == nil {
				bc.currentState = &utils.MediaStateUpdate{}
			}
			bc.currentState.IsPlaying = isPlaying
			bc.mu.Unlock()
			
			// Broadcast the state update
			bc.broadcastStateUpdate()
		}
		
	case MsgStateVolume:
		// Handle volume incremental update
		if len(payload) >= 1 {
			volume := int(payload[0])
			log.Printf("BLE_LOG: Received incremental volume update: %d%%", volume)
			
			bc.mu.Lock()
			if bc.currentState == nil {
				bc.currentState = &utils.MediaStateUpdate{}
			}
			bc.currentState.VolumePercent = volume
			bc.mu.Unlock()
			
			// Broadcast the state update
			bc.broadcastStateUpdate()
		}
		
	// Legacy album art messages (0x0100-0x0102)
	case MsgLegacyAlbumArtStart:
		log.Printf("BLE_LOG: Received legacy album art start message")
		// The legacy protocol stores chunk index in the header, not payload
		// Payload structure: [32 bytes SHA256][4 bytes total chunks][4 bytes image size][variable track ID]
		if len(payload) < 40 {
			log.Printf("BLE_LOG: Legacy album art start payload too small: %d bytes", len(payload))
			return
		}
		
		checksum := payload[0:32]
		totalChunks := binary.BigEndian.Uint32(payload[32:36])
		imageSize := binary.BigEndian.Uint32(payload[36:40])
		trackID := string(payload[40:])
		
		bc.albumArtMutex.Lock()
		bc.albumArtReceiving = true
		bc.albumArtBuffer = nil
		bc.albumArtChunks = make(map[int][]byte, int(totalChunks))
		bc.albumArtTrackID = trackID
		bc.albumArtSize = int(imageSize)
		bc.albumArtChecksum = BytesToHex(checksum)
		bc.albumArtTotalChunks = int(totalChunks)
		bc.albumArtStartTime = time.Now()
		bc.albumArtMutex.Unlock()
		
		log.Printf("BLE_LOG: Legacy album art transfer starting - Track: %s, Size: %d bytes, Chunks: %d",
			trackID, imageSize, totalChunks)
		
	case MsgLegacyAlbumArtChunk:
		bc.albumArtMutex.Lock()
		if bc.albumArtReceiving {
			// Legacy protocol uses header.ChunkIndex for the chunk index
			chunkIndex := int(header.ChunkIndex)
			
			// Check for duplicate chunks
			if _, exists := bc.albumArtChunks[chunkIndex]; exists {
				log.Printf("BLE_LOG: Duplicate legacy chunk %d received, ignoring", chunkIndex)
				bc.albumArtMutex.Unlock()
				return
			}
			
			// Store chunk data (entire payload is image data)
			bc.albumArtChunks[chunkIndex] = payload
			receivedCount := len(bc.albumArtChunks)
			log.Printf("BLE_LOG: Received legacy album art chunk %d/%d (%d bytes) - Total received: %d", 
				chunkIndex+1, bc.albumArtTotalChunks, len(payload), receivedCount)
		}
		bc.albumArtMutex.Unlock()
		
	case MsgLegacyAlbumArtEnd:
		// Legacy end message payload: [32 bytes SHA256][1 byte success]
		if len(payload) < 33 {
			log.Printf("BLE_LOG: Legacy album art end payload too small: %d bytes", len(payload))
			return
		}
		
		checksum := BytesToHex(payload[0:32])
		success := payload[32] != 0
		
		bc.albumArtMutex.Lock()
		if bc.albumArtReceiving && checksum == bc.albumArtChecksum {
			if success {
				// Process the completed album art
				bc.albumArtMutex.Unlock()
				bc.processAlbumArt()
				log.Printf("BLE_LOG: Legacy album art transfer completed successfully")
			} else {
				log.Printf("BLE_LOG: Legacy album art transfer failed")
				bc.albumArtReceiving = false
				bc.albumArtBuffer = nil
				bc.albumArtChunks = nil
				bc.albumArtMutex.Unlock()
			}
		} else {
			bc.albumArtMutex.Unlock()
			log.Printf("BLE_LOG: Legacy album art end checksum mismatch - expected: %s, got: %s", bc.albumArtChecksum, checksum)
		}
		
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
			// Like legacy protocol, chunk index is in header.ChunkIndex
			chunkIndex := int(header.ChunkIndex)
			
			// Check for duplicate chunks
			if _, exists := bc.albumArtChunks[chunkIndex]; exists {
				log.Printf("BLE_LOG: Duplicate chunk %d received, ignoring", chunkIndex)
				bc.albumArtMutex.Unlock()
				return
			}
			
			// Store chunk data (entire payload is image data)
			bc.albumArtChunks[chunkIndex] = payload
			receivedCount := len(bc.albumArtChunks)
			log.Printf("BLE_LOG: Received album art chunk %d/%d (%d bytes) - Total received: %d", 
				chunkIndex+1, bc.albumArtTotalChunks, len(payload), receivedCount)
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
			// Like legacy protocol, chunk index is in header.ChunkIndex
			chunkIndex := int(header.ChunkIndex)
			
			// Check for duplicate chunks
			if _, exists := bc.testAlbumArtChunks[chunkIndex]; exists {
				bc.testAlbumArtMutex.Unlock()
				return
			}
			
			// Store chunk data (entire payload is image data)
			bc.testAlbumArtChunks[chunkIndex] = payload
			bc.testAlbumArtMutex.Unlock()
			
			// Handle the chunk with the actual data
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
		
	// Incremental state update messages
	case MsgStateArtist:
		// Deprecated - kept for backward compatibility
		artist := ParseStringPayload(payload)
		log.Printf("BLE_LOG: Incremental artist update (deprecated): %s", artist)
		bc.mu.Lock()
		if bc.currentState == nil {
			bc.currentState = &utils.MediaStateUpdate{Type: "stateUpdate"}
		}
		bc.currentState.Artist = &artist
		bc.currentArtist = artist
		bc.mu.Unlock()
		bc.broadcastStateUpdate()
		
	case MsgStateAlbum:
		// Deprecated - kept for backward compatibility
		album := ParseStringPayload(payload)
		log.Printf("BLE_LOG: Incremental album update (deprecated): %s", album)
		bc.mu.Lock()
		if bc.currentState == nil {
			bc.currentState = &utils.MediaStateUpdate{Type: "stateUpdate"}
		}
		bc.currentState.Album = &album
		bc.currentAlbum = album
		bc.mu.Unlock()
		bc.broadcastStateUpdate()
		
	default:
		log.Printf("BLE_LOG: Unknown binary message type: 0x%04x", header.MessageType)
	}
}

// broadcastStateUpdate sends the current state to WebSocket clients
func (bc *BleClient) broadcastStateUpdate() {
	bc.mu.RLock()
	state := bc.currentState
	hasNewTimestamp := bc.hasNewTimestamp
	pendingTimestamp := bc.pendingTimestamp
	bc.mu.RUnlock()
	
	if state != nil && bc.wsHub != nil {
		// Log what we're broadcasting
		artist := ""
		album := ""
		track := ""
		if state.Artist != nil {
			artist = *state.Artist
		}
		if state.Album != nil {
			album = *state.Album
		}
		if state.Track != nil {
			track = *state.Track
		}
		log.Printf("BLE_LOG: Broadcasting state update - Artist: %s, Album: %s, Track: %s", artist, album, track)
		
		// Send regular state update
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type:    "media/state_update",
			Payload: state,
		})
		
		// If we have a new timestamp, send timestamp sync event
		if hasNewTimestamp {
			bc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "media/timestamp_sync",
				Payload: map[string]interface{}{
					"position_ms": pendingTimestamp,
					"new_timestamp_available": true,
				},
			})
			
			// Clear the flag after broadcasting
			bc.mu.Lock()
			bc.hasNewTimestamp = false
			bc.mu.Unlock()
		}
	}
}

// checkTrackChange detects if track/artist/album has changed
func (bc *BleClient) checkTrackChange() bool {
	bc.mu.RLock()
	state := bc.currentState
	previousID := bc.previousTrackIdentifier
	bc.mu.RUnlock()
	
	if state == nil {
		return false
	}
	
	// Build current track identifier
	track := ""
	artist := ""
	album := ""
	
	if state.Track != nil {
		track = *state.Track
	}
	if state.Artist != nil {
		artist = *state.Artist
	}
	if state.Album != nil {
		album = *state.Album
	}
	
	currentID := fmt.Sprintf("%s|%s|%s", track, artist, album)
	
	// Check if changed
	if currentID != previousID && previousID != "" {
		bc.mu.Lock()
		bc.previousTrackIdentifier = currentID
		bc.mu.Unlock()
		return true
	}
	
	// Update stored identifier if first time
	if previousID == "" {
		bc.mu.Lock()
		bc.previousTrackIdentifier = currentID
		bc.mu.Unlock()
	}
	
	return false
}

// GetCurrentTrackMetadata returns the current track, artist, and album
func (bc *BleClient) GetCurrentTrackMetadata() (track, artist, album string) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.currentTrack, bc.currentArtist, bc.currentAlbum
}

// GetCurrentTrack returns the current track name
func (bc *BleClient) GetCurrentTrack() string {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.currentTrack
}

// GetCurrentArtist returns the current artist name
func (bc *BleClient) GetCurrentArtist() string {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.currentArtist
}

// GetCurrentAlbum returns the current album name
func (bc *BleClient) GetCurrentAlbum() string {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.currentAlbum
}

// SendTimestampRequest sends a request for current timestamp
func (bc *BleClient) SendTimestampRequest() error {
	log.Printf("BLE_LOG: Requesting timestamp due to track change")
	return bc.SendCommand("request_timestamp", nil, nil)
}

// enableBinaryProtocol sends command to enable binary protocol
func (bc *BleClient) enableBinaryProtocol() error {
	// Create binary message for enable binary incremental
	messageID := uint16(time.Now().Unix() & 0xFFFF)
	cmdData := CreateMessage(MsgEnableBinaryIncremental, []byte{}, messageID)
	
	log.Printf("BLE_LOG: Sending enable_binary_incremental command (binary, ID: %d)...", messageID)
	
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
	options := make(map[string]interface{})
	
	if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdData, options).Err; err != nil {
		return fmt.Errorf("failed to enable binary protocol: %v", err)
	}
	
	bc.mu.Lock()
	bc.supportsBinaryProtocol = true
	bc.binaryProtocolVersion = BinaryProtocolVersion
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
	
	// Create binary message for high priority connection request
	messageID := uint16(time.Now().Unix() & 0xFFFF)
	payload := CreateHighPriorityConnectionPayload("test_album_art_transfer")
	cmdData := CreateMessage(MsgRequestHighPriorityConnection, payload, messageID)
	
	bc.mu.RLock()
	if bc.conn != nil && bc.commandRxCharPath != "" {
		charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
		options := make(map[string]interface{})
		charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdData, options)
	}
	bc.mu.RUnlock()
	
	// Give it a moment to apply
	time.Sleep(100 * time.Millisecond)
	
	// Also try to re-optimize connection parameters
	go bc.optimizeConnectionParameters()
	
	// Send test album art request using binary protocol
	log.Printf("BLE_LOG: Sending test album art request")
	
	// Check connection state
	bc.mu.RLock()
	if bc.conn == nil || bc.commandRxCharPath == "" {
		bc.mu.RUnlock()
		return fmt.Errorf("BLE connection not established")
	}
	bc.mu.RUnlock()
	
	bc.testAlbumArtMutex.Lock()
	bc.testAlbumArtStartTime = time.Now()
	bc.testAlbumArtReceiving = false
	bc.testAlbumArtBuffer = nil
	bc.testAlbumArtChunks = make(map[int][]byte)
	bc.testAlbumArtTotalChunks = 0
	bc.testAlbumArtMutex.Unlock()
	
	// Use SendCommand to send the test album art request through the proper binary protocol
	return bc.SendCommand("test_album_art_request", nil, nil)
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
// negotiateOptimalConnectionParams performs sequential negotiation for optimal BLE performance
// BCM20703A2 on Car Thing supports 2M PHY for 2x throughput
// Sequence: MTU (517) → Priority (HIGH) → PHY (2M) with 250ms delays
func (bc *BleClient) negotiateOptimalConnectionParams() error {
	log.Println("BLE_LOG: Starting sequential connection parameter negotiation for 2M PHY")
	
	deviceObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.devicePath)
	
	// Step 1: Request MTU 517 (maximum for BLE 5.0)
	log.Println("BLE_LOG: Step 1/3: Requesting MTU 517...")
	
	// BlueZ handles MTU negotiation automatically, but we can try to set preferred MTU
	// through the kernel interface if available
	if connHandle := bc.getConnectionHandle(); connHandle != "" {
		// Note: Direct MTU control may not be available, BlueZ negotiates it
		log.Printf("BLE_LOG: Connection handle %s found for MTU optimization", connHandle)
	}
	
	// The MTU is negotiated at GATT level when we discover services
	// Android app should handle MTU request in onConnectionStateChange
	
	// Delay 250ms before next step
	time.Sleep(250 * time.Millisecond)
	
	// Step 2: Request HIGH connection priority
	log.Println("BLE_LOG: Step 2/3: Requesting HIGH connection priority...")
	
	// Try to set connection parameters for low latency
	if err := bc.setHighPriorityConnection(); err != nil {
		log.Printf("BLE_LOG: Warning - could not set high priority: %v", err)
	}
	
	// Delay 250ms before PHY negotiation
	time.Sleep(250 * time.Millisecond)
	
	// Step 3: Request 2M PHY
	log.Println("BLE_LOG: Step 3/3: Requesting 2M PHY...")
	
	// Request PHY update through BlueZ (requires BlueZ 5.50+)
	if err := bc.request2MPHY(deviceObj); err != nil {
		log.Printf("BLE_LOG: Warning - could not request 2M PHY: %v", err)
		// Continue with 1M PHY
	}
	
	// Verify final parameters
	time.Sleep(500 * time.Millisecond)
	bc.logFinalConnectionParams()
	
	return nil
}

// setHighPriorityConnection attempts to set connection parameters for low latency
func (bc *BleClient) setHighPriorityConnection() error {
	// Connection interval parameters for HIGH priority:
	// Min interval: 7.5ms (6 * 1.25ms)
	// Max interval: 15ms (12 * 1.25ms)
	// Latency: 0
	// Timeout: 5000ms (500 * 10ms)
	
	connHandle := bc.getConnectionHandle()
	if connHandle == "" {
		// Try global parameters if no specific handle
		bc.updateGlobalConnectionParams()
		return nil
	}
	
	debugPath := fmt.Sprintf("/sys/kernel/debug/bluetooth/hci0/%s", connHandle)
	
	params := map[string]string{
		"conn_min_interval": "6",    // 7.5ms
		"conn_max_interval": "12",   // 15ms
		"conn_latency": "0",         // No latency
		"supervision_timeout": "500", // 5 seconds
	}
	
	for param, value := range params {
		path := fmt.Sprintf("%s/%s", debugPath, param)
		if err := os.WriteFile(path, []byte(value+"\n"), 0644); err != nil {
			log.Printf("BLE_LOG: Could not set %s: %v", param, err)
		} else {
			log.Printf("BLE_LOG: Set %s to %s", param, value)
		}
	}
	
	return nil
}

// request2MPHY requests 2M PHY through BlueZ D-Bus interface
func (bc *BleClient) request2MPHY(deviceObj dbus.BusObject) error {
	// Check BlueZ version and PHY support
	// PHY selection requires BlueZ 5.50+ and kernel 4.14+
	
	// Try to set PHY through D-Bus properties if available
	// Note: PHY control through D-Bus is limited, kernel/driver support varies
	
	log.Println("BLE_LOG: Attempting to request 2M PHY...")
	
	// Log current PHY if available
	var txPhy, rxPhy uint8
	if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0, 
		BLUEZ_DEVICE_INTERFACE, "TxPhy").Store(&txPhy); err == nil {
		log.Printf("BLE_LOG: Current TX PHY: %d", txPhy)
		// PHY values: 1=1M, 2=2M, 3=Coded
		if txPhy == 2 {
			bc.currentPHYMode = "2M PHY"
		} else if txPhy == 1 {
			bc.currentPHYMode = "1M PHY"
		}
	}
	if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0,
		BLUEZ_DEVICE_INTERFACE, "RxPhy").Store(&rxPhy); err == nil {
		log.Printf("BLE_LOG: Current RX PHY: %d", rxPhy)
	}
	
	// PHY values: 1=1M, 2=2M, 3=Coded
	// Most BlueZ versions don't expose PHY control through D-Bus
	// The negotiation typically happens at the HCI level
	
	// Send command to Android to initiate PHY update from peripheral side
	go func() {
		time.Sleep(100 * time.Millisecond)
		if bc.commandRxCharPath != "" {
			// Create request for Android to initiate 2M PHY
			cmdMap := map[string]interface{}{
				"command": "request_2m_phy",
				"command_id": fmt.Sprintf("phy_%d", time.Now().Unix()),
			}
			cmdJSON, _ := json.Marshal(cmdMap)
			
			charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
			options := make(map[string]interface{})
			if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJSON, options).Err; err == nil {
				log.Println("BLE_LOG: Requested Android to initiate 2M PHY update")
			}
		}
	}()
	
	return nil
}

// logFinalConnectionParams logs the final negotiated connection parameters
func (bc *BleClient) logFinalConnectionParams() {
	log.Println("BLE_LOG: Final connection parameters:")
	
	// Log MTU
	log.Printf("BLE_LOG:   MTU: %d", bc.mtu)
	
	// Log connection interval if available
	if connHandle := bc.getConnectionHandle(); connHandle != "" {
		bc.logKernelConnectionParams(connHandle)
	}
	
	// Check PHY through btmon or kernel debug
	log.Println("BLE_LOG: To verify PHY: SSH root@172.16.42.2 'btmon | grep -E \"PHY|MTU\"'")
	log.Println("BLE_LOG: Success shows: LE PHY Update Complete TX:LE 2M RX:LE 2M")
}

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
		
		// Send a notification about connection optimization using binary protocol
		messageID := uint16(time.Now().Unix() & 0xFFFF)
		payload := CreateOptimizeConnectionParamsPayload(7.5, "client_optimization")
		cmdData := CreateMessage(MsgOptimizeConnectionParams, payload, messageID)
		
		if bc.commandRxCharPath != "" {
			charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
			options := make(map[string]interface{})
			if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdData, options).Err; err == nil {
				log.Printf("BLE_LOG: Notified Android app of connection parameter optimization (binary, ID: %d)", messageID)
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

// Speed test tracking
type SpeedTestState struct {
	mu sync.Mutex
	pingStartTimes map[string]time.Time
	latencies []int64
	throughputStartTime time.Time
	chunksReceived int
	totalDataReceived int
}

var speedTestState = &SpeedTestState{
	pingStartTimes: make(map[string]time.Time),
	latencies: []int64{},
}

// SendSpeedTestStart sends a speed test start command
func (bc *BleClient) SendSpeedTestStart() error {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	
	if !bc.connected {
		return fmt.Errorf("not connected")
	}
	
	// Reset speed test state
	speedTestState.mu.Lock()
	speedTestState.pingStartTimes = make(map[string]time.Time)
	speedTestState.latencies = []int64{}
	speedTestState.chunksReceived = 0
	speedTestState.totalDataReceived = 0
	speedTestState.mu.Unlock()
	
	// Send speed test command via WebSocket to UI
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "test/bt_speed_start",
			Payload: map[string]interface{}{
				"timestamp": time.Now().UnixMilli(),
			},
		})
	}
	
	// Send binary command to Android to start speed test
	log.Printf("BLE_LOG: Starting BT speed test")
	
	// Create command for speed test
	cmdMap := map[string]interface{}{
		"command":    "bt_speed_test",
		"command_id": fmt.Sprintf("speed_test_%d", time.Now().Unix()),
		"timestamp":  time.Now().UnixMilli(),
		"payload": map[string]interface{}{
			"command_id": fmt.Sprintf("speed_test_%d", time.Now().Unix()),
		},
	}
	
	cmdJSON, _ := json.Marshal(cmdMap)
	
	// Send command
	if bc.conn != nil && bc.commandRxCharPath != "" {
		charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
		options := make(map[string]interface{})
		charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJSON, options)
	}
	
	// Start the speed test sequence
	go bc.runSpeedTestSequence()
	
	return nil
}

// runSpeedTestSequence runs the speed test sequence
func (bc *BleClient) runSpeedTestSequence() {
	
	// Phase 1: Latency test (10 pings)
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "test/bt_speed_progress",
			Payload: map[string]interface{}{
				"current_test":     "Latency Test",
				"progress_percent": 10,
			},
		})
	}
	
	for i := 0; i < 10; i++ {
		pingStart := time.Now()
		pingId := fmt.Sprintf("ping_%d", i)
		
		// Store the ping start time
		speedTestState.mu.Lock()
		speedTestState.pingStartTimes[pingId] = pingStart
		speedTestState.mu.Unlock()
		
		// Send ping
		cmdMap := map[string]interface{}{
			"command":    "ping",
			"command_id": pingId,
			"payload": map[string]interface{}{
				"command_id": pingId,
				"timestamp": pingStart.UnixMilli(),
			},
		}
		cmdJSON, _ := json.Marshal(cmdMap)
		
		bc.mu.RLock()
		if bc.conn != nil && bc.commandRxCharPath != "" {
			charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
			options := make(map[string]interface{})
			charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJSON, options)
		}
		bc.mu.RUnlock()
		
		// Wait for pong response before sending next ping
		time.Sleep(100 * time.Millisecond)
		
		// The actual latency will be calculated when we receive the pong response
		
		progress := 10 + (i+1)*4
		if bc.wsHub != nil {
			bc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "test/bt_speed_progress",
				Payload: map[string]interface{}{
					"current_test":     "Latency Test",
					"progress_percent": progress,
				},
			})
		}
	}
	
	// Wait a bit for all pongs to come back
	time.Sleep(500 * time.Millisecond)
	
	// Phase 2: Throughput test
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "test/bt_speed_progress",
			Payload: map[string]interface{}{
				"current_test":     "Throughput Test",
				"progress_percent": 50,
			},
		})
	}
	
	// Send data chunks and measure throughput
	// Calculate safe chunk size considering JSON overhead
	// JSON wrapper adds about 200 bytes, base64 encoding increases size by ~33%
	maxDataSize := 200 // Start conservative
	if bc.mtu > 0 {
		// MTU - JSON overhead (200) / base64 expansion (1.33)
		maxDataSize = (int(bc.mtu) - 250) * 3 / 4
		if maxDataSize > 300 {
			maxDataSize = 300 // Cap at 300 bytes to be safe
		}
		if maxDataSize < 50 {
			maxDataSize = 50 // Minimum chunk size
		}
	}
	
	chunkSize := maxDataSize
	numChunks := 20
	
	log.Printf("BLE_LOG: Throughput test using chunk size %d bytes (MTU: %d)", chunkSize, bc.mtu)
	
	speedTestState.mu.Lock()
	speedTestState.throughputStartTime = time.Now()
	speedTestState.chunksReceived = 0
	speedTestState.totalDataReceived = 0
	speedTestState.mu.Unlock()
	
	for i := 0; i < numChunks; i++ {
		// Create test data but don't send it - just send size
		// This tests the command/response latency without data payload issues
		
		cmdMap := map[string]interface{}{
			"command":    "throughput_test",
			"command_id": fmt.Sprintf("throughput_%d", i),
			"payload": map[string]interface{}{
				"chunk_num":  i,
				"total":      numChunks,
				"size":       chunkSize,
				"command_id": fmt.Sprintf("throughput_%d", i),
			},
		}
		cmdJSON, _ := json.Marshal(cmdMap)
		
		bc.mu.RLock()
		if bc.conn != nil && bc.commandRxCharPath != "" {
			charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
			options := make(map[string]interface{})
			charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJSON, options)
		}
		bc.mu.RUnlock()
		
		progress := 50 + (i+1)*2
		if bc.wsHub != nil {
			bc.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "test/bt_speed_progress",
				Payload: map[string]interface{}{
					"current_test":     "Throughput Test",
					"progress_percent": progress,
				},
			})
		}
		
		time.Sleep(10 * time.Millisecond)
	}
	
	// Wait for all chunks to be acknowledged
	time.Sleep(500 * time.Millisecond)
	
	// Phase 3: Real data throughput test (send actual data)
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "test/bt_speed_progress",
			Payload: map[string]interface{}{
				"current_test":     "Data Transfer Test",
				"progress_percent": 80,
			},
		})
	}
	
	// Send a burst of actual data to measure real throughput
	realDataSize := 1024 // 1KB chunks
	realNumChunks := 10
	realStartTime := time.Now()
	totalBytesSent := 0
	
	for i := 0; i < realNumChunks; i++ {
		// Create actual test data
		testData := make([]byte, realDataSize)
		for j := range testData {
			testData[j] = byte((i + j) % 256)
		}
		
		// Send as binary data directly (not JSON)
		bc.mu.RLock()
		if bc.conn != nil && bc.commandRxCharPath != "" {
			charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
			options := make(map[string]interface{})
			if err := charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, testData, options).Err; err == nil {
				totalBytesSent += len(testData)
			}
		}
		bc.mu.RUnlock()
		
		// Small delay between chunks
		time.Sleep(5 * time.Millisecond)
	}
	
	realElapsed := time.Since(realStartTime).Seconds()
	realThroughputKbps := float64(totalBytesSent*8) / realElapsed / 1000
	
	log.Printf("BLE_LOG: Real data transfer: %d bytes in %.2f seconds = %.2f kbps", 
		totalBytesSent, realElapsed, realThroughputKbps)
	
	// Combine results
	speedTestState.mu.Lock()
	var avgLatency int64
	if len(speedTestState.latencies) > 0 {
		var totalLatency int64
		for _, l := range speedTestState.latencies {
			totalLatency += l
		}
		avgLatency = totalLatency / int64(len(speedTestState.latencies))
	}
	
	// Use the real throughput measurement
	finalThroughput := realThroughputKbps
	if speedTestState.chunksReceived > 0 {
		// Average with command/response throughput if available
		cmdThroughput := float64(speedTestState.totalDataReceived*8) / time.Since(speedTestState.throughputStartTime).Seconds() / 1000
		finalThroughput = (realThroughputKbps + cmdThroughput) / 2
	}
	speedTestState.mu.Unlock()
	
	// Broadcast final results
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "test/bt_speed_complete",
			Payload: map[string]interface{}{
				"avg_latency_ms":      avgLatency,
				"throughput_kbps":     finalThroughput,
				"real_throughput_kbps": realThroughputKbps,
				"packet_loss_percent": 0,
				"mtu":                 bc.mtu,
				"phy_status":          bc.currentPHYMode,
				"total_bytes_sent":    totalBytesSent,
			},
		})
	}
}