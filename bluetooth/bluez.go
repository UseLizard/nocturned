package bluetooth

const (
	BLUEZ_BUS_NAME              = "org.bluez"
	BLUEZ_DEVICE_INTERFACE      = "org.bluez.Device1"
	// Nordic UART Service compatible UUIDs for better compatibility with NocturneCompanion
	NOCTURNE_SERVICE_UUID       = "6E400001-B5A3-F393-E0A9-E50E24DCCA9E"
	COMMAND_RX_CHARACTERISTIC_UUID = "6E400002-B5A3-F393-E0A9-E50E24DCCA9E" // Write - for receiving commands
	STATE_TX_CHARACTERISTIC_UUID   = "6E400003-B5A3-F393-E0A9-E50E24DCCA9E" // Notify - for sending state updates
	DEBUG_LOG_CHARACTERISTIC_UUID  = "6E400004-B5A3-F393-E0A9-E50E24DCCA9E" // Notify - for debug logs
	DEVICE_INFO_CHARACTERISTIC_UUID = "6E400005-B5A3-F393-E0A9-E50E24DCCA9E" // Read - device info
	ALBUM_ART_TX_CHARACTERISTIC_UUID = "6E400006-B5A3-F393-E0A9-E50E24DCCA9E" // Notify - for album art transfer
)
