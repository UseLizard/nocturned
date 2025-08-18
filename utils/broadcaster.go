package utils

import (
	"log"
)

// WebSocketBroadcaster provides high-level broadcasting functions for different event types
type WebSocketBroadcaster struct {
	wsHub *WebSocketHub
}

// NewWebSocketBroadcaster creates a new broadcaster instance
func NewWebSocketBroadcaster(wsHub *WebSocketHub) *WebSocketBroadcaster {
	return &WebSocketBroadcaster{
		wsHub: wsHub,
	}
}

// BroadcastTimeSync broadcasts time synchronization data to all connected WebSocket clients
// and optionally sets the system time
func (b *WebSocketBroadcaster) BroadcastTimeSync(timestampMs int64, timezone string) {
	log.Printf("📅 Broadcasting time sync to WebSocket clients: timestamp=%d, timezone=%s", timestampMs, timezone)
	
	// Set system time if provided timestamp is valid
	if timestampMs > 0 {
		if err := SetSystemTime(timestampMs); err != nil {
			log.Printf("❌ Failed to set system time: %v", err)
		} else {
			log.Printf("✅ System time updated from companion: %d", timestampMs)
		}
	}
	
	// Set timezone if provided
	if timezone != "" && timezone != "Local" {
		if err := SetTimezone(timezone); err != nil {
			log.Printf("❌ Failed to set timezone: %v", err)
		} else {
			log.Printf("✅ Timezone updated from companion: %s", timezone)
		}
	}
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type: "timeSync",
		Payload: map[string]interface{}{
			"timestamp_ms": timestampMs,
			"timezone":     timezone,
			"source":       "companion",
		},
	})
}

// BroadcastMediaStateUpdate broadcasts full media state updates from companion
func (b *WebSocketBroadcaster) BroadcastMediaStateUpdate(payload []byte) {
	log.Printf("🎵 Broadcasting media state update to WebSocket clients (%d bytes)", len(payload))
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type:    "media_state_update",
		Payload: map[string]interface{}{
			"source": "companion",
			"type":   "full_state",
			"data":   payload,
		},
	})
}

// BroadcastAlbumArtUpdate broadcasts album art data from companion
func (b *WebSocketBroadcaster) BroadcastAlbumArtUpdate(messageType uint16, payload []byte) {
	log.Printf("🎨 Broadcasting album art update to WebSocket clients (%s)", messageTypeToString(messageType))
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type:    "album_art_update",
		Payload: map[string]interface{}{
			"message_type": messageType,
			"data":         payload,
		},
	})
}

// BroadcastGradientColors broadcasts gradient color data from companion
func (b *WebSocketBroadcaster) BroadcastGradientColors(payload []byte) {
	log.Printf("🌈 Broadcasting gradient colors to WebSocket clients (%d bytes)", len(payload))
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type:    "gradient_colors",
		Payload: map[string]interface{}{
			"source": "companion",
			"data":   payload,
		},
	})
}

// BroadcastWeatherUpdate broadcasts weather data updates from companion
func (b *WebSocketBroadcaster) BroadcastWeatherUpdate(weatherData map[string]interface{}) {
	mode, _ := weatherData["mode"].(string)
	log.Printf("🌤️ Broadcasting weather update to WebSocket clients: mode=%s", mode)
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type:    "weather_update",
		Payload: map[string]interface{}{
			"source": "companion",
			"mode":   mode,
			"data":   weatherData,
		},
	})
}

// BroadcastMediaCommand broadcasts media commands to WebSocket clients
func (b *WebSocketBroadcaster) BroadcastMediaCommand(command MediaCommand) {
	log.Printf("🎵 Broadcasting media command to WebSocket clients: %s", command.Command)
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type:    "media_command",
		Payload: command,
	})
}

// BroadcastDeviceConnected broadcasts device connection events
func (b *WebSocketBroadcaster) BroadcastDeviceConnected(address string, device *BluetoothDeviceInfo) {
	log.Printf("🔗 Broadcasting device connected: %s", address)
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type: "device_connected",
		Payload: DeviceConnectedPayload{
			Address: address,
			Device:  device,
		},
	})
}

// BroadcastDeviceDisconnected broadcasts device disconnection events
func (b *WebSocketBroadcaster) BroadcastDeviceDisconnected(address string) {
	log.Printf("🔌 Broadcasting device disconnected: %s", address)
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type: "device_disconnected",
		Payload: DeviceDisconnectedPayload{
			Address: address,
		},
	})
}

// BroadcastDevicePaired broadcasts device pairing events
func (b *WebSocketBroadcaster) BroadcastDevicePaired(device *BluetoothDeviceInfo) {
	log.Printf("📱 Broadcasting device paired: %s", device.Address)
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type: "device_paired",
		Payload: DevicePairedPayload{
			Device: device,
		},
	})
}

// BroadcastPairingStarted broadcasts pairing started events
func (b *WebSocketBroadcaster) BroadcastPairingStarted(address, pairingKey string) {
	log.Printf("🔐 Broadcasting pairing started: %s", address)
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type: "pairing_started",
		Payload: PairingStartedPayload{
			Address:    address,
			PairingKey: pairingKey,
		},
	})
}

// BroadcastNetworkConnected broadcasts network connection events
func (b *WebSocketBroadcaster) BroadcastNetworkConnected(address string) {
	log.Printf("🌐 Broadcasting network connected: %s", address)
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type: "network_connected",
		Payload: NetworkConnectedPayload{
			Address: address,
		},
	})
}

// BroadcastProfileStateChanged broadcasts profile state changes
func (b *WebSocketBroadcaster) BroadcastProfileStateChanged(deviceAddress string, profile BluetoothProfile) {
	log.Printf("⚙️  Broadcasting profile state changed: %s - %s", deviceAddress, profile.Name)
	
	b.wsHub.Broadcast(WebSocketEvent{
		Type: "profile_state_changed",
		Payload: ProfileStateChangedPayload{
			DeviceAddress: deviceAddress,
			Profile:       profile,
		},
	})
}

// BroadcastProfileLog broadcasts profile log entries
func (b *WebSocketBroadcaster) BroadcastProfileLog(entry ProfileLogEntry) {
	b.wsHub.Broadcast(WebSocketEvent{
		Type: "profile_log",
		Payload: ProfileLogPayload{
			Entry: entry,
		},
	})
}

// messageTypeToString converts message type to string for logging
func messageTypeToString(msgType uint16) string {
	switch msgType {
	case 0x0301:
		return "AlbumArtStart"
	case 0x0302:
		return "AlbumArtChunk"
	case 0x0303:
		return "AlbumArtEnd"
	case 0x0304:
		return "AlbumArtNotAvailable"
	case 0x0601:
		return "GradientColors"
	default:
		return "Unknown"
	}
}