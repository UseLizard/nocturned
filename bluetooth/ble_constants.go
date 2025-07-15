package bluetooth

import "time"

const (
	// Service and Characteristic UUIDs (Nordic UART Service compatible)
	NocturneServiceUUID    = "6e400001-b5a3-f393-e0a9-e50e24dcca9e"
	CommandRxCharUUID      = "6e400002-b5a3-f393-e0a9-e50e24dcca9e" // Write
	ResponseTxCharUUID     = "6e400003-b5a3-f393-e0a9-e50e24dcca9e" // Notify (State TX in Android)
	DebugLogCharUUID       = "6e400004-b5a3-f393-e0a9-e50e24dcca9e" // Notify
	DeviceInfoCharUUID     = "6e400005-b5a3-f393-e0a9-e50e24dcca9e" // Read
	
	// BLE Configuration
	TargetMTU         = uint16(512)
	DefaultMTU        = uint16(23)
	MTUHeaderSize     = uint16(3)
	ScanTimeoutSec    = 30
	ConnectTimeoutSec = 10
	
	// Device identification
	DeviceName = "NocturneCompanion"
	
	// Reconnection configuration
	MAX_RECONNECT_ATTEMPTS = 20
	RECONNECT_DELAY       = 5 * time.Second
)