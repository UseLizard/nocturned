package bluetooth

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/usenocturne/nocturned/utils"
)



type BleClient struct {
	connected         bool
	targetAddress     string
	wsHub             *utils.WebSocketHub
	btManager         *BluetoothManager
	mu                sync.RWMutex
	reconnectAttempts int
	currentState      *utils.MediaStateUpdate
	stopChan          chan struct{}
	conn              *dbus.Conn
	devicePath        dbus.ObjectPath
	servicePath       dbus.ObjectPath
	controlChar       dbus.BusObject
	commandRxCharPath dbus.ObjectPath
	responseTxCharPath dbus.ObjectPath
	debugLogCharPath  dbus.ObjectPath
	deviceInfoCharPath dbus.ObjectPath
	albumArtCharPath  dbus.ObjectPath
	bleConnected      bool
	mtu               uint16
	lastCommandTime   time.Time
	commandMutex      sync.Mutex
	commandQueue      chan *Command
	commandQueueMu    sync.Mutex
	activeCommand     *Command
	lastCommandID     string
	fullyConnected    bool
	capsReceived      bool
	pollingTicker     *time.Ticker
	lastPolledValue   []byte
	albumArtBuffer    []byte
	albumArtChunks    map[int][]byte
	albumArtTrackID   string
	albumArtSize      int
	albumArtChecksum  string
	albumArtReceiving bool
	albumArtTotalChunks int
	albumArtStartTime time.Time
	albumArtMutex     sync.Mutex
	weatherState      *utils.WeatherState
	weatherMutex      sync.RWMutex
	supportsBinaryProtocol bool
	binaryProtocolVersion  byte
	albumArtCallback func([]byte)
	pendingAlbumArtRequest  bool
	pendingAlbumArtHash     string
	albumArtRequestMutex    sync.Mutex
	lastAlbumArtRequestTime time.Time
	testAlbumArtBuffer    []byte
	testAlbumArtChunks    map[int][]byte
	testAlbumArtSize      int
	testAlbumArtChecksum  string
	testAlbumArtReceiving bool
	testAlbumArtTotalChunks int
	testAlbumArtStartTime time.Time
	testAlbumArtMutex     sync.Mutex
	testAlbumArtLastProgressUpdate int
	weatherBinaryReceiving     bool
	weatherBinaryBuffer        []byte
	weatherBinaryChunks        map[int][]byte
	weatherBinaryChecksum      string
	weatherBinaryTotalChunks   int
	weatherBinaryMode          string
	weatherBinaryLocation      string
	weatherBinaryStartTime     time.Time
	weatherBinaryMutex         sync.Mutex
	currentPHYMode string
}

func NewBleClient(btManager *BluetoothManager, wsHub *utils.WebSocketHub) *BleClient {
	return &BleClient{
		btManager:     btManager,
		wsHub:         wsHub,
		conn:          btManager.conn,
		stopChan:      make(chan struct{}),
		mtu:           DefaultMTU,
		commandQueue:  make(chan *Command, 100),
		currentPHYMode: "Unknown",
	}
}

func (c *BleClient) SetAlbumArtCallback(callback func([]byte)) {
	c.albumArtCallback = callback
}

func (c *BleClient) DiscoverAndConnect() error {
	log.Println("BLE_LOG: DiscoverAndConnect called")
	time.Sleep(1 * time.Second)
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wsHub != nil {
		c.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/ble_scan_start",
			Payload: map[string]interface{}{
				"timestamp": time.Now().Unix(),
			},
		})
	}

	address, err := c.discoverNocturneCompanion()
	if err != nil {
		log.Printf("BLE_LOG: discoverNocturneCompanion failed: %v", err)
		return fmt.Errorf("failed to discover NocturneCompanion: %v", err)
	}
	log.Printf("BLE_LOG: discoverNocturneCompanion succeeded, address: %s", address)

	c.targetAddress = address
	return c.connectToDevice()
}

func (c *BleClient) discoverNocturneCompanion() (string, error) {
	log.Println("BLE_LOG: discoverNocturneCompanion called")

	devices, err := c.btManager.GetDevices()
	if err != nil {
		log.Printf("BLE_LOG: GetDevices failed: %v", err)
		return "", fmt.Errorf("failed to get bluetooth devices: %v", err)
	}

	log.Printf("🔍 BLE_LOG: Checking %d paired devices for NocturneCompanion...", len(devices))

	for _, device := range devices {
		log.Printf("BLE_LOG: Checking paired device: %s (%s)", device.Name, device.Address)
		if device.Name == DeviceName {
			log.Printf("BLE_LOG: Found paired device with matching name: %s (%s)",
				device.Name, device.Address)
			if c.wsHub != nil {
				c.wsHub.Broadcast(utils.WebSocketEvent{
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

	log.Println("BLE_LOG: No paired NocturneCompanion found, starting BLE scan...")

	if err := c.startDiscovery(); err != nil {
		log.Printf("BLE_LOG: Failed to start discovery: %v", err)
		return "", fmt.Errorf("failed to start discovery: %v", err)
	}

	foundDevice := make(chan string, 1)
	stopScan := make(chan struct{})

	go c.monitorDiscoveredDevices(foundDevice, stopScan)

	select {
	case address := <-foundDevice:
		log.Printf("BLE_LOG: Found NocturneCompanion during scan: %s", address)
		c.stopDiscovery()
		return address, nil
	case <-time.After(time.Duration(ScanTimeoutSec) * time.Second):
		close(stopScan)
		c.stopDiscovery()
		log.Println("BLE_LOG: Scan timeout - no NocturneCompanion device found")
		return "", fmt.Errorf("no NocturneCompanion device found during scan")
	}
}

func (c *BleClient) connectToDevice() error {
	log.Println("BLE_LOG: connectToDevice called")
	if c.targetAddress == "" {
		log.Println("BLE_LOG: connectToDevice failed: no target address")
		return fmt.Errorf("no target address set")
	}

	log.Printf("Connecting to BLE NocturneCompanion at %s", c.targetAddress)

	if err := c.connectBLE(); err != nil {
		log.Printf("BLE_LOG: connectBLE failed: %v", err)
		return fmt.Errorf("failed to establish BLE connection: %v", err)
	}

	c.connected = true
	c.reconnectAttempts = 0

	if c.wsHub != nil {
		c.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "media/ble_connected",
			Payload: map[string]string{
				"address": c.targetAddress,
				"status":  "connected",
			},
		})
	}

	log.Printf("Successfully connected to BLE NocturneCompanion")
	return nil
}

func (c *BleClient) connectBLE() error {
	log.Println("BLE_LOG: connectBLE called")

	c.stopChan = make(chan struct{})
	c.capsReceived = false
	c.fullyConnected = false
	c.supportsBinaryProtocol = false
	c.devicePath = formatDevicePath(c.btManager.adapter, c.targetAddress)

	obj := c.conn.Object(BLUEZ_BUS_NAME, c.devicePath)
	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, BLUEZ_DEVICE_INTERFACE).Store(&props); err != nil {
		return fmt.Errorf("failed to get device properties: %v", err)
	}

	connected := false
	if connectedProp, ok := props["Connected"]; ok {
		connected = connectedProp.Value().(bool)
	}

	if !connected {
		log.Println("BLE_LOG: Device not connected, attempting connection...")
		if err := obj.Call("org.bluez.Device1.Connect", 0).Err; err != nil {
			if strings.Contains(err.Error(), "InProgress") {
				log.Println("BLE_LOG: Connection already in progress, waiting...")
				time.Sleep(500 * time.Millisecond)
			} else {
				return fmt.Errorf("failed to connect to device: %v", err)
			}
		}

		maxAttempts := 10
		for i := 0; i < maxAttempts; i++ {
			time.Sleep(1 * time.Second)
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

	c.bleConnected = true

	if err := c.negotiateOptimalConnectionParams(); err != nil {
		log.Printf("BLE_LOG: Warning - connection optimization incomplete: %v", err)
	}

	log.Println("BLE_LOG: Waiting for GATT services to be resolved...")
	time.Sleep(500 * time.Millisecond)

	if err := c.discoverGattService(); err != nil {
		log.Printf("BLE_LOG: discoverGattService failed: %v", err)
		return fmt.Errorf("failed to discover GATT service: %v", err)
	}

	if err := c.setupCharacteristics(); err != nil {
		log.Printf("BLE_LOG: setupCharacteristics failed: %v", err)
		return fmt.Errorf("failed to setup characteristics: %v", err)
	}

	go c.handleBleNotifications()
	go c.processCommandQueue()

	log.Printf("BLE GATT connection established to %s", c.targetAddress)
	return nil
}

func (c *BleClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

func (c *BleClient) UpdateLocalPlayState(newState *utils.MediaStateUpdate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentState = newState
}

func (c *BleClient) GetCurrentState() *utils.MediaStateUpdate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentState
}

func (c *BleClient) startDiscovery() error {
	log.Println("BLE_LOG: Starting BLE discovery...")

	adapter := c.conn.Object(BLUEZ_BUS_NAME, c.btManager.adapter)

	filter := map[string]interface{}{
		"Transport":     "le",
		"DuplicateData": false,
	}

	if err := adapter.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, filter).Err; err != nil {
		log.Printf("BLE_LOG: Failed to set discovery filter: %v", err)
	}

	if err := adapter.Call("org.bluez.Adapter1.StartDiscovery", 0).Err; err != nil {
		return fmt.Errorf("failed to start discovery: %v", err)
	}

	log.Println("BLE_LOG: Discovery started successfully")
	return nil
}

func (c *BleClient) stopDiscovery() {
	log.Println("BLE_LOG: Stopping BLE discovery...")

	adapter := c.conn.Object(BLUEZ_BUS_NAME, c.btManager.adapter)
	if err := adapter.Call("org.bluez.Adapter1.StopDiscovery", 0).Err; err != nil {
		log.Printf("BLE_LOG: Failed to stop discovery: %v", err)
	}
}

func (c *BleClient) monitorDiscoveredDevices(foundDevice chan<- string, stopScan <-chan struct{}) {
	log.Println("BLE_LOG: Starting device discovery monitor...")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	checkedDevices := make(map[string]bool)

	for {
		select {
		case <-stopScan:
			log.Println("BLE_LOG: Stopping device monitor")
			return

		case <-ticker.C:
			objects, err := c.getManagedObjects()
			if err != nil {
				log.Printf("BLE_LOG: Failed to get managed objects during scan: %v", err)
				continue
			}

			for path, interfaces := range objects {
				pathStr := string(path)

				if !strings.HasPrefix(pathStr, string(c.btManager.adapter)+"/dev_") {
					continue
				}

				if deviceIface, hasDevice := interfaces[BLUEZ_DEVICE_INTERFACE]; hasDevice {
					if addrVariant, ok := deviceIface["Address"]; ok {
						address := addrVariant.Value().(string)

						if checkedDevices[address] {
							continue
						}
						checkedDevices[address] = true

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

						if name == DeviceName {
							foundDevice <- address
							return
						}

						for _, uuid := range uuids {
							if strings.EqualFold(uuid, NocturneServiceUUID) {
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

func (c *BleClient) negotiateOptimalConnectionParams() error {
	log.Println("BLE_LOG: Attempting MTU negotiation (target: 247)")
	c.mtu = DefaultMTU
	log.Printf("BLE_LOG: Using MTU: %d (will be updated if negotiation occurs)", c.mtu)
	return nil
}

func (c *BleClient) discoverGattService() error {
	log.Println("BLE_LOG: Discovering GATT services...")

	deviceObj := c.conn.Object(BLUEZ_BUS_NAME, c.devicePath)

	var servicesResolved bool
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		var srvVariant dbus.Variant
		if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0, BLUEZ_DEVICE_INTERFACE, "ServicesResolved").Store(&srvVariant); err == nil {
			if srvVal, ok := srvVariant.Value().(bool); ok && srvVal {
				servicesResolved = true
				break
			}
		}
		if i < maxRetries-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if !servicesResolved {
		log.Println("BLE_LOG: Warning - services may not be fully resolved")
	}

	objects, err := c.getManagedObjects()
	if err != nil {
		return fmt.Errorf("failed to get managed objects: %v", err)
	}

	devicePathStr := string(c.devicePath)
	for path, interfaces := range objects {
		pathStr := string(path)

		if !strings.HasPrefix(pathStr, devicePathStr+"/service") {
			continue
		}

		if svcIface, hasGattService := interfaces["org.bluez.GattService1"]; hasGattService {
			if uuidVariant, ok := svcIface["UUID"]; ok {
				uuid := uuidVariant.Value().(string)
				if strings.EqualFold(uuid, NocturneServiceUUID) {
					c.servicePath = path
					return nil
				}
			}
		}
	}

	return fmt.Errorf("Nocturne GATT service not found")
}

func (c *BleClient) setupCharacteristics() error {
	log.Println("BLE_LOG: Setting up characteristics...")

	objects, err := c.getManagedObjects()
	if err != nil {
		return fmt.Errorf("failed to get managed objects: %v", err)
	}

	servicePathStr := string(c.servicePath)
	for path, interfaces := range objects {
		pathStr := string(path)

		if !strings.HasPrefix(pathStr, servicePathStr+"/char") {
			continue
		}

		if charIface, hasChar := interfaces["org.bluez.GattCharacteristic1"]; hasChar {
			if uuidVariant, ok := charIface["UUID"]; ok {
				uuid := uuidVariant.Value().(string)
				switch strings.ToLower(uuid) {
				case strings.ToLower(CommandRxCharUUID):
					c.commandRxCharPath = path
				case strings.ToLower(ResponseTxCharUUID):
					c.responseTxCharPath = path
				case strings.ToLower(DebugLogCharUUID):
					c.debugLogCharPath = path
				case strings.ToLower(DeviceInfoCharUUID):
					c.deviceInfoCharPath = path
				case strings.ToLower(AlbumArtTxCharUUID):
					c.albumArtCharPath = path
				}
			}
		}
	}

	if c.commandRxCharPath == "" || c.responseTxCharPath == "" {
		return fmt.Errorf("required characteristics not found")
	}

	return nil
}

func (c *BleClient) handleConnectionLoss() {
	log.Println("BLE_LOG: Connection lost, attempting to reconnect...")
	c.mu.Lock()
	c.connected = false
	c.fullyConnected = false
	c.reconnectAttempts++
	c.mu.Unlock()

	// Stop any existing goroutines
	close(c.stopChan)

	// Attempt to reconnect with backoff
	time.Sleep(time.Duration(c.reconnectAttempts) * time.Second)
	go c.DiscoverAndConnect()
}

func (c *BleClient) getManagedObjects() (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	obj := c.conn.Object(BLUEZ_BUS_NAME, "/")
	var managedObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := obj.Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&managedObjects)
	if err != nil {
		return nil, fmt.Errorf("failed to get managed objects: %w", err)
	}
	return managedObjects, nil
}

func formatDevicePath(adapter dbus.ObjectPath, address string) dbus.ObjectPath {
	return dbus.ObjectPath(fmt.Sprintf("%s/dev_%s", adapter, strings.Replace(address, ":", "_", -1)))
}
