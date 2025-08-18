package bluetooth

import (
	"time"
)

// Message Types
const (
	// Control Messages (0x01-0x0F)
	MsgTypePlayStateUpdate byte = 0x01
	MsgTypeVolumeUpdate    byte = 0x02
	MsgTypeRequest         byte = 0x03
	MsgTypeWeatherUpdate   byte = 0x04

	// Album Art Messages (0x10-0x1F)
	MsgTypeAlbumArtStart     byte = 0x10
	MsgTypeAlbumArtChunk     byte = 0x11
	MsgTypeAlbumArtComplete  byte = 0x12
	MsgTypeAlbumArtNotFound  byte = 0x13

	// Time Sync Messages (0x20-0x2F)
	MsgTypeTimeSyncRequest  byte = 0x20
	MsgTypeTimeSyncResponse byte = 0x21

	// Status Messages (0x30-0x3F)
	MsgTypeWeatherInfo      byte = 0x30
	MsgTypeConnectionStatus byte = 0x31
	MsgTypeDeviceInfo       byte = 0x32
)

// Request Types
const (
	RequestTypeTimeSync    byte = 0x01
	RequestTypeAlbumArt    byte = 0x02
	RequestTypePlayState   byte = 0x03
	RequestTypeWeatherInfo byte = 0x04
	RequestTypeDeviceInfo  byte = 0x05
)

// Play States
const (
	PlayStatePaused  byte = 0x00
	PlayStatePlaying byte = 0x01
	PlayStateStopped byte = 0x02
)

// Connection States
const (
	ConnectionStateDisconnected byte = 0x00
	ConnectionStateConnecting   byte = 0x01
	ConnectionStateConnected    byte = 0x02
	ConnectionStateError        byte = 0xFF
)

// PlayStateMessage represents a play state update
type PlayStateMessage struct {
	TrackTitle    string
	Artist        string
	Album         string
	AlbumArtHash  string
	Duration      uint32 // Duration in seconds
	Position      uint32 // Position in seconds
	PlayState     byte   // PlayStatePaused, PlayStatePlaying, PlayStateStopped
}

// VolumeMessage represents a volume update
type VolumeMessage struct {
	Volume byte // Volume level 0-100
}

// RequestMessage represents a request from the device
type RequestMessage struct {
	RequestType byte
	Data        []byte // Optional additional data
}

// AlbumArtStartMessage represents the start of album art transfer
type AlbumArtStartMessage struct {
	TotalChunks  uint16
	AlbumArtHash string
	TotalSize    uint32 // Optional: total size in bytes
}

// AlbumArtChunkMessage represents a chunk of album art data
type AlbumArtChunkMessage struct {
	ChunkIndex uint16
	ChunkData  []byte
}

// AlbumArtCompleteMessage represents successful album art transfer completion
type AlbumArtCompleteMessage struct {
	AlbumArtHash string
	Success      bool
	ErrorMessage string // Only present if Success is false
}

// TimeSyncMessage represents time synchronization data
type TimeSyncMessage struct {
	Timestamp uint64 // Unix timestamp in milliseconds
	Timezone  string // Timezone identifier (e.g., "America/New_York")
}

// WeatherMessage represents weather information
type WeatherMessage struct {
	Temperature int16  // Temperature in Celsius * 10 (e.g., 250 = 25.0°C)
	Humidity    byte   // Humidity percentage 0-100
	Condition   string // Weather condition (e.g., "Sunny", "Cloudy", "Rain")
	Location    string // Location name
}

// DeviceInfoMessage represents device information
type DeviceInfoMessage struct {
	DeviceName       string
	BatteryLevel     byte   // Battery percentage 0-100, 255 for unknown
	SignalStrength   byte   // Signal strength 0-100, 255 for unknown
	FirmwareVersion  string
	HardwareVersion  string
}

// ConnectionStatusMessage represents connection status
type ConnectionStatusMessage struct {
	Status        byte   // ConnectionState constants
	LastSeen      uint64 // Unix timestamp of last successful communication
	ErrorMessage  string // Error description if Status is ConnectionStateError
}

// Command represents a command to be sent to the device
type Command struct {
	Type      byte
	Payload   []byte
	Timestamp time.Time
	Retries   int
	ID        string // Unique identifier for the command
}

// DeviceCapabilities represents the capabilities of a connected device
type DeviceCapabilities struct {
	SupportsBinaryProtocol bool
	BinaryProtocolVersion  byte
	MaxMTU                 uint16
	SupportsAlbumArt       bool
	SupportsWeather        bool
	SupportsTimeSync       bool
	MaxChunkSize           uint16
}

// AlbumArtTransfer represents an ongoing album art transfer
type AlbumArtTransfer struct {
	Hash         string
	Data         []byte
	Chunks       map[int][]byte
	TotalChunks  int
	Size         int
	Checksum     string
	StartTime    time.Time
	LastUpdate   time.Time
	IsComplete   bool
	IsReceiving  bool
}

// WeatherTransfer represents an ongoing weather data transfer
type WeatherTransfer struct {
	Buffer      []byte
	Chunks      map[int][]byte
	TotalChunks int
	Checksum    string
	Mode        string
	Location    string
	StartTime   time.Time
	IsComplete  bool
	IsReceiving bool
}

// RateLimitConfig represents rate limiting configuration
type RateLimitConfig struct {
	MaxCommandsPerSecond int
	BurstSize            int
	ChunkDelay           time.Duration
}

// DefaultRateLimitConfig returns a sensible default rate limiting configuration
func DefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		MaxCommandsPerSecond: 10,
		BurstSize:           5,
		ChunkDelay:          50 * time.Millisecond,
	}
}

// ChunkSize constants
const (
	DefaultChunkSize = 400 // bytes
	MaxChunkSize     = 512 // bytes
	MinChunkSize     = 64  // bytes
)

// Timeout constants
const (
	DefaultConnectionTimeout = 30 * time.Second
	DefaultCommandTimeout    = 5 * time.Second
	DefaultChunkTimeout      = 2 * time.Second
	DefaultDiscoveryTimeout  = 10 * time.Second
)

// MTU constants
const (
	DefaultMTU = 517 // Default BLE MTU
	MinMTU     = 23  // Minimum BLE MTU
	MaxMTU     = 517 // Maximum standard BLE MTU
)