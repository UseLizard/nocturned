package bluetooth

import "time"

const (
	// Service and Characteristic UUIDs (must match Android)
	NocturneServiceUUID    = "12345678-1234-5678-9abc-def012345678"
	CommandRxCharUUID      = "12345678-1234-5678-9abc-def012345679"
	ResponseTxCharUUID     = "12345678-1234-5678-9abc-def01234567a"
	AlbumArtRxCharUUID     = "12345678-1234-5678-9abc-def01234567b"
	
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