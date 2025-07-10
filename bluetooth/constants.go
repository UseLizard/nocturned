package bluetooth

const (
	BLUEZ_BUS_NAME          = "org.bluez"
	BLUEZ_ADAPTER_INTERFACE = "org.bluez.Adapter1"
	BLUEZ_DEVICE_INTERFACE  = "org.bluez.Device1"
	BLUEZ_AGENT_INTERFACE   = "org.bluez.Agent1"
	BLUEZ_AGENT_MANAGER     = "org.bluez.AgentManager1"
	BLUEZ_BATTERY_INTERFACE = "org.bluez.Battery1"
	BLUEZ_OBJECT_PATH       = "/org/bluez"
	BLUEZ_AGENT_PATH        = "/org/bluez/agent"
)

// Bluetooth Profile UUIDs (standardized 16-bit UUIDs)
const (
	// Audio Profiles
	PROFILE_A2DP_UUID   = "0000110d-0000-1000-8000-00805f9b34fb" // Advanced Audio Distribution Profile
	PROFILE_AVRCP_UUID  = "0000110e-0000-1000-8000-00805f9b34fb" // Audio/Video Remote Control Profile
	PROFILE_HSP_UUID    = "00001108-0000-1000-8000-00805f9b34fb" // Headset Profile
	PROFILE_HFP_UUID    = "0000111e-0000-1000-8000-00805f9b34fb" // Hands-Free Profile
	
	// Data/Network Profiles  
	PROFILE_SPP_UUID    = "00001101-0000-1000-8000-00805f9b34fb" // Serial Port Profile
	PROFILE_PAN_UUID    = "00001115-0000-1000-8000-00805f9b34fb" // Personal Area Network
	PROFILE_NAP_UUID    = "00001116-0000-1000-8000-00805f9b34fb" // Network Access Point
	PROFILE_PBAP_UUID   = "0000112f-0000-1000-8000-00805f9b34fb" // Phone Book Access Profile
	PROFILE_MAP_UUID    = "00001134-0000-1000-8000-00805f9b34fb" // Message Access Profile
	
	// Device/Utility Profiles
	PROFILE_HID_UUID    = "00001124-0000-1000-8000-00805f9b34fb" // Human Interface Device
	PROFILE_OPP_UUID    = "00001105-0000-1000-8000-00805f9b34fb" // Object Push Profile
	PROFILE_DID_UUID    = "00001200-0000-1000-8000-00805f9b34fb" // Device Information
	PROFILE_GAP_UUID    = "00001800-0000-1000-8000-00805f9b34fb" // Generic Access Profile
)

// Profile Categories
const (
	CATEGORY_AUDIO = "audio"
	CATEGORY_DATA  = "data"
	CATEGORY_PHONE = "phone" 
	CATEGORY_DEVICE = "device"
)

// Profile States
const (
	PROFILE_STATE_DISABLED    = "disabled"
	PROFILE_STATE_ENABLED     = "enabled"
	PROFILE_STATE_CONNECTING  = "connecting"
	PROFILE_STATE_CONNECTED   = "connected"
	PROFILE_STATE_ERROR       = "error"
)
