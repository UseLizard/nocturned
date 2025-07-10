package utils

// Bluetooth
type BluetoothDeviceInfo struct {
	Address           string `json:"address"`
	Name              string `json:"name"`
	Alias             string `json:"alias"`
	Class             string `json:"class"`
	Icon              string `json:"icon"`
	Paired            bool   `json:"paired"`
	Trusted           bool   `json:"trusted"`
	Blocked           bool   `json:"blocked"`
	Connected         bool   `json:"connected"`
	LegacyPairing     bool   `json:"legacyPairing"`
	BatteryPercentage int    `json:"batteryPercentage,omitempty"`
}

type PairingRequest struct {
	Device      string
	Passkey     string
	RequestType string
}

// WebSocket
type WebSocketEvent struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type PairingStartedPayload struct {
	Address    string `json:"address"`
	PairingKey string `json:"pairingKey"`
}

type DeviceConnectedPayload struct {
	Address string               `json:"address"`
	Device  *BluetoothDeviceInfo `json:"device,omitempty"`
}

type DeviceDisconnectedPayload struct {
	Address string `json:"address"`
}

type DevicePairedPayload struct {
	Device *BluetoothDeviceInfo `json:"device"`
}

type NetworkConnectedPayload struct {
	Address string `json:"address"`
}

// Media Control (NocturneCompanion Integration)
type MediaCommand struct {
	Command      string `json:"command"`
	ValueMs      *int   `json:"value_ms,omitempty"`
	ValuePercent *int   `json:"value_percent,omitempty"`
}

type MediaStateUpdate struct {
	Type          string  `json:"type"`
	Artist        *string `json:"artist"`
	Album         *string `json:"album"`
	Track         *string `json:"track"`
	DurationMs    int64   `json:"duration_ms"`
	PositionMs    int64   `json:"position_ms"`
	IsPlaying     bool    `json:"is_playing"`
	VolumePercent int     `json:"volume_percent"`
}

type MediaStatePayload struct {
	Artist        *string `json:"artist"`
	Album         *string `json:"album"`
	Track         *string `json:"track"`
	DurationMs    int64   `json:"duration_ms"`
	PositionMs    int64   `json:"position_ms"`
	IsPlaying     bool    `json:"is_playing"`
	VolumePercent int     `json:"volume_percent"`
}

// Bluetooth Profile Management
type BluetoothProfile struct {
	UUID        string                 `json:"uuid"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Category    string                 `json:"category"`
	State       string                 `json:"state"`
	Enabled     bool                   `json:"enabled"`
	AutoConnect bool                   `json:"auto_connect"`
	Priority    int                    `json:"priority"`
	Settings    map[string]interface{} `json:"settings,omitempty"`
}

type ProfileConfiguration struct {
	DeviceAddress string                      `json:"device_address"`
	Profiles      map[string]BluetoothProfile `json:"profiles"`
	GlobalSettings map[string]interface{}     `json:"global_settings,omitempty"`
}

type ProfileUpdateRequest struct {
	DeviceAddress string `json:"device_address"`
	ProfileUUID   string `json:"profile_uuid"`
	Enabled       *bool  `json:"enabled,omitempty"`
	AutoConnect   *bool  `json:"auto_connect,omitempty"`
	Priority      *int   `json:"priority,omitempty"`
	Settings      map[string]interface{} `json:"settings,omitempty"`
}

type ProfileConnectionRequest struct {
	DeviceAddress string `json:"device_address"`
	ProfileUUID   string `json:"profile_uuid"`
	ForceReconnect bool  `json:"force_reconnect"`
}

type ProfileStatusResponse struct {
	DeviceAddress string                      `json:"device_address"`
	Profiles      map[string]BluetoothProfile `json:"profiles"`
	LastUpdated   int64                      `json:"last_updated"`
}

type ProfileLogEntry struct {
	Timestamp     int64  `json:"timestamp"`
	DeviceAddress string `json:"device_address"`
	ProfileUUID   string `json:"profile_uuid"`
	ProfileName   string `json:"profile_name"`
	Action        string `json:"action"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	Error         string `json:"error,omitempty"`
}

// WebSocket Events for Profile Management
type ProfileStateChangedPayload struct {
	DeviceAddress string           `json:"device_address"`
	Profile       BluetoothProfile `json:"profile"`
}

type ProfileLogPayload struct {
	Entry ProfileLogEntry `json:"entry"`
}
