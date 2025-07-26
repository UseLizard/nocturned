package bluetooth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	timestamp    time.Time
	retryCount   int
	callback     chan error
}

// CommandAck represents acknowledgment from the device
type CommandAck struct {
	CommandID string `json:"command_id"`
	Status    string `json:"status"` // "received" or "success"
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
	
	// Command queue with ACK handling
	commandQueue      chan *CommandQueueItem
	commandQueueMu    sync.Mutex
	activeCommand     *CommandQueueItem
	lastCommandID     string
	ackTimeout        time.Duration
	
	// Connection state tracking
	fullyConnected    bool
	capsReceived      bool
	
	// Polling for state updates
	pollingTicker     *time.Ticker
	lastPolledValue   []byte
	
	// Album art transfer management
	albumArtBuffer    []byte
	albumArtTrackID   string
	albumArtSize      int
	albumArtChecksum  string
	albumArtReceiving bool
	albumArtMutex     sync.Mutex
}

func NewBleClient(btManager *BluetoothManager, wsHub *utils.WebSocketHub) *BleClient {
	return &BleClient{
		btManager:     btManager,
		wsHub:         wsHub,
		conn:          btManager.conn, // Reuse existing D-Bus connection
		stopChan:      make(chan struct{}),
		mtu:           DefaultMTU,
		commandQueue:  make(chan *CommandQueueItem, 100), // Buffer up to 100 commands
		ackTimeout:    3 * time.Second,
	}
}

func (bc *BleClient) DiscoverAndConnect() error {
	log.Println("BLE_LOG: DiscoverAndConnect called, waiting 5 seconds for adapter to initialize...")
	time.Sleep(5 * time.Second)
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
				time.Sleep(3 * time.Second)
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

	// Wait a bit for services to be resolved
	log.Println("BLE_LOG: Waiting for GATT services to be resolved...")
	time.Sleep(2 * time.Second)

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
			time.Sleep(2 * time.Second)
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
	notificationMinInterval := 15 * time.Millisecond

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

	// Create a channel to receive signals
	sigChan := make(chan *dbus.Signal, 100)
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
					log.Printf("BLE_LOG: Received notification on Album Art TX characteristic")
				} else if bc.debugLogCharPath != "" && sig.Path == bc.debugLogCharPath {
					charType = "debug"
					log.Printf("BLE_LOG: Received notification on Debug Log characteristic")
				} else {
					// Log other signals for debugging
					if strings.Contains(string(sig.Path), string(bc.devicePath)) {
						log.Printf("BLE_LOG: Received signal on path %s (not our characteristic)", sig.Path)
					}
					continue // Not our characteristic
				}
				
				// The signal has format: interface_name, changed_properties, invalidated_properties
				if len(sig.Body) >= 2 {
					if changedProps, ok := sig.Body[1].(map[string]dbus.Variant); ok {
						if valueVariant, exists := changedProps["Value"]; exists {
							if value, ok := valueVariant.Value().([]byte); ok {
								// Check rate limiting
								now := time.Now()
								if lastTime, exists := lastNotificationTime[charType]; exists {
									if now.Sub(lastTime) < notificationMinInterval {
										log.Printf("BLE_LOG: Rate limiting notification on %s (too fast: %v)", charType, now.Sub(lastTime))
										continue
									}
								}
								lastNotificationTime[charType] = now
								
								// Broadcast notification received
								if bc.wsHub != nil {
									bc.wsHub.Broadcast(utils.WebSocketEvent{
										Type: "media/ble_notification_received",
										Payload: map[string]interface{}{
											"char_type": charType,
											"data":      string(value),
											"size":      len(value),
											"timestamp": time.Now().Unix(),
										},
									})
								}
								bc.handleNotificationData(value, charType)
							}
						}
					}
				}
			}
		}
	}
}

func (bc *BleClient) handleNotificationData(data []byte, charType string) {
	// Handle album art data differently (raw bytes, not JSON)
	if charType == "albumart" {
		bc.handleAlbumArtData(data)
		return
	}
	
	dataStr := string(data)
	if len(dataStr) == 0 {
		return
	}

	log.Printf("Received BLE %s data: %s", charType, dataStr)

	// Broadcast received data
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/ble_data_received",
			Payload: map[string]interface{}{
				"address":   bc.targetAddress,
				"data":      dataStr,
				"char_type": charType,
				"timestamp": time.Now().Unix(),
			},
		})
	}

	// Try to parse the message type first
	var msgType struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &msgType); err != nil {
		log.Printf("Failed to parse message type: %v", err)
		return
	}

	// Handle different message types
	switch msgType.Type {
	case "ack":
		// Command acknowledgment
		var ack CommandAck
		if err := json.Unmarshal(data, &ack); err == nil {
			log.Printf("BLE ACK: %s - %s", ack.CommandID, ack.Status)
			
			// Handle ACK for active command
			bc.commandQueueMu.Lock()
			if bc.activeCommand != nil && bc.activeCommand.id == ack.CommandID {
				if ack.Status == "success" {
					log.Printf("BLE_LOG: Command %s completed successfully", bc.activeCommand.command)
					bc.activeCommand = nil
				}
			}
			bc.commandQueueMu.Unlock()
		}
		
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
			Type         string   `json:"type"`
			Version      string   `json:"version"`
			Features     []string `json:"features"`
			MTU          int      `json:"mtu"`
			DebugEnabled bool     `json:"debug_enabled"`
		}
		if err := json.Unmarshal(data, &caps); err == nil {
			log.Printf("BLE Capabilities: Version %s, MTU %d, Features: %v", 
				caps.Version, caps.MTU, caps.Features)
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
			log.Printf("Failed to parse media state update: %v", err)
			return
		}

		log.Printf("BLE_LOG: Received state update - Track: %s, Playing: %v, Position: %d ms", 
			stateUpdate.Track, stateUpdate.IsPlaying, stateUpdate.PositionMs)

		bc.mu.Lock()
		bc.currentState = &stateUpdate
		bc.mu.Unlock()

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
		
	case "album_art_start":
		// Album art transfer starting
		var artStart struct {
			Type     string `json:"type"`
			Size     int    `json:"size"`
			TrackID  string `json:"track_id"`
			Checksum string `json:"checksum"`
		}
		if err := json.Unmarshal(data, &artStart); err == nil {
			bc.albumArtMutex.Lock()
			bc.albumArtReceiving = true
			bc.albumArtBuffer = make([]byte, 0, artStart.Size)
			bc.albumArtTrackID = artStart.TrackID
			bc.albumArtSize = artStart.Size
			bc.albumArtChecksum = artStart.Checksum
			bc.albumArtMutex.Unlock()
			
			log.Printf("BLE_LOG: Starting album art transfer - Track: %s, Size: %d bytes", 
				artStart.TrackID, artStart.Size)
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
				bc.albumArtBuffer = append(bc.albumArtBuffer, chunkData...)
				log.Printf("BLE_LOG: Received album art chunk %d - %d bytes (total: %d/%d)", 
					artChunk.ChunkIndex, len(chunkData), len(bc.albumArtBuffer), bc.albumArtSize)
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
			if bc.albumArtReceiving && bc.albumArtChecksum == artEnd.Checksum {
				if artEnd.Success {
					// Process the completed album art
					go bc.processAlbumArt()
				} else {
					log.Printf("BLE_LOG: Album art transfer failed for checksum: %s", artEnd.Checksum)
					bc.albumArtReceiving = false
					bc.albumArtBuffer = nil
				}
			}
			bc.albumArtMutex.Unlock()
			
			log.Printf("BLE_LOG: Album art transfer complete - Checksum: %s, Success: %v", 
				artEnd.Checksum, artEnd.Success)
		}
		
	default:
		log.Printf("Unknown BLE message type: %s", msgType.Type)
	}
}

// SendCommand queues a command for sending with ACK handling
func (bc *BleClient) SendCommand(command string, valueMs *int, valuePercent *int) error {
	bc.mu.RLock()
	if !bc.connected || bc.targetAddress == "" || !bc.bleConnected || bc.commandRxCharPath == "" {
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
	
	// Rate limit commands
	const minCommandInterval = 50 * time.Millisecond
	timeSinceLastCommand := time.Since(bc.lastCommandTime)
	if timeSinceLastCommand < minCommandInterval {
		waitTime := minCommandInterval - timeSinceLastCommand
		log.Printf("BLE_LOG: Rate limiting - waiting %v before sending command", waitTime)
		time.Sleep(waitTime)
	}
	
	cmd := utils.MediaCommand{
		Command:      cmdItem.command,
		ValueMs:      cmdItem.valueMs,
		ValuePercent: cmdItem.valuePercent,
		CommandID:    cmdItem.id, // Include command ID for ACK tracking
	}

	// Convert command to JSON
	cmdJson, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %v", err)
	}

	log.Printf("BLE_LOG: Sending command: %s with ID: %s (JSON: %s)", cmdItem.command, cmdItem.id, string(cmdJson))

	// Send using D-Bus
	charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
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
			log.Printf("BLE_LOG: Command sent successfully, waiting for ACK...")
			
			// For now, we'll proceed without waiting for ACK
			// TODO: Implement proper ACK waiting with channel communication
			// This would involve waiting for the ACK to be received in handleNotificationData
			// and signaling completion via a channel
			return nil
		}
		
		// Check if it's an ATT error
		if strings.Contains(err.Error(), "ATT error: 0x0e") {
			log.Printf("BLE_LOG: ATT error 0x0e - characteristic busy, will retry")
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

func (bc *BleClient) SendAlbumArtRequest(trackID string, checksum string) error {
    payload := map[string]string{
        "track_id": trackID,
        "checksum": checksum,
    }
    // This is a special command that doesn't go through the normal queue
    // because it's a request from nocturned to the companion app.
    cmd := utils.MediaCommand{
        Command: "nack_album_art_needed",
        Payload: payload,
    }
    cmdJson, err := json.Marshal(cmd)
    if err != nil {
        return fmt.Errorf("failed to marshal album art request: %v", err)
    }

    charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.commandRxCharPath)
    options := make(map[string]interface{})
    return charObj.Call("org.bluez.GattCharacteristic1.WriteValue", 0, cmdJson, options).Err
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

func (bc *BleClient) handleAlbumArtData(data []byte) {
	bc.albumArtMutex.Lock()
	defer bc.albumArtMutex.Unlock()
	
	if !bc.albumArtReceiving {
		log.Printf("BLE_LOG: Received album art data but not expecting it")
		return
	}
	
	// Append data to buffer
	bc.albumArtBuffer = append(bc.albumArtBuffer, data...)
	log.Printf("BLE_LOG: Received album art chunk - %d bytes (total: %d/%d)", 
		len(data), len(bc.albumArtBuffer), bc.albumArtSize)
}

func (bc *BleClient) processAlbumArt() {
	bc.albumArtMutex.Lock()
	defer bc.albumArtMutex.Unlock()
	
	if !bc.albumArtReceiving || len(bc.albumArtBuffer) == 0 {
		return
	}
	
	// Verify checksum (using SHA-256 to match Android app)
	    checksum := utils.CalculateSHA256(bc.albumArtBuffer)
	if checksum != bc.albumArtChecksum {
		log.Printf("BLE_LOG: Album art checksum mismatch - expected: %s, got: %s", 
			bc.albumArtChecksum, checksum)
		bc.albumArtReceiving = false
		return
	}
	
	// Save album art to temporary location for current playback
	tempFilename := "/tmp/album_art.webp"
	if err := utils.SaveAlbumArt(bc.albumArtBuffer, tempFilename); err != nil {
		log.Printf("BLE_LOG: Failed to save album art to temp: %v", err)
		bc.albumArtReceiving = false
		return
	}
	
	// Also save to persistent gallery storage
	galleryDir := "/data/etc/nocturne/albumart"
	if err := os.MkdirAll(galleryDir, 0755); err != nil {
		log.Printf("BLE_LOG: Failed to create gallery directory: %v", err)
	} else {
		// Use checksum as filename for deduplication
		galleryFilename := filepath.Join(galleryDir, bc.albumArtChecksum + ".webp")
		if err := utils.SaveAlbumArt(bc.albumArtBuffer, galleryFilename); err != nil {
			log.Printf("BLE_LOG: Failed to save album art to gallery: %v", err)
		} else {
			// Extract artist and album from current state
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
			
			// Save metadata
			metadata := map[string]interface{}{
				"artist": artist,
				"album": album,
				"added": time.Now().Format(time.RFC3339),
			}
			metadataFile := filepath.Join(galleryDir, bc.albumArtChecksum + ".json")
			if metadataJSON, err := json.MarshalIndent(metadata, "", "  "); err == nil {
				if err := os.WriteFile(metadataFile, metadataJSON, 0644); err != nil {
					log.Printf("BLE_LOG: Failed to save metadata: %v", err)
				}
			}
			log.Printf("BLE_LOG: Album art saved to gallery - %s", galleryFilename)
		}
	}
	
	log.Printf("BLE_LOG: Album art saved successfully - %s (%d bytes)", tempFilename, len(bc.albumArtBuffer))
	
	// Broadcast album art update
	if bc.wsHub != nil {
		bc.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/album_art_updated",
			Payload: map[string]interface{}{
				"track_id": bc.albumArtTrackID,
				"filename": tempFilename,
				"size":     len(bc.albumArtBuffer),
			},
		})
	}
	
	// Reset state
	bc.albumArtReceiving = false
	bc.albumArtBuffer = nil
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
			close(bc.stopChan)
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