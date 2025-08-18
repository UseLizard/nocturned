package bluetooth

import (
	"log"

	"github.com/usenocturne/nocturned/utils"
)

// CommandHandler handles BLE commands received from NocturneCompanion
type CommandHandler struct {
	wsHub      *utils.WebSocketHub
	broadcaster *utils.WebSocketBroadcaster
	sendMessageFunc func(uint16, []byte) error
}

// NewCommandHandler creates a new command handler instance
func NewCommandHandler(wsHub *utils.WebSocketHub, sendMessageFunc func(uint16, []byte) error) *CommandHandler {
	return &CommandHandler{
		wsHub:           wsHub,
		broadcaster:     utils.NewWebSocketBroadcaster(wsHub),
		sendMessageFunc: sendMessageFunc,
	}
}

// Helper method to parse command payloads
func (h *CommandHandler) parseCommandPayload(payload []byte) (*int, *int) {
	if len(payload) == 0 {
		return nil, nil
	}
	
	// Try to parse as binary protocol command payload
	// Format: [flags:1][valueMs:8?][valuePercent:1?]
	if len(payload) < 1 {
		return nil, nil
	}
	
	flags := payload[0]
	offset := 1
	var valueMs *int
	var valuePercent *int
	
	// Check if valueMs is present (bit 0)
	if (flags & 0x01) != 0 && len(payload) >= offset+8 {
		ms := int(payload[offset]) << 56 |
			int(payload[offset+1]) << 48 |
			int(payload[offset+2]) << 40 |
			int(payload[offset+3]) << 32 |
			int(payload[offset+4]) << 24 |
			int(payload[offset+5]) << 16 |
			int(payload[offset+6]) << 8 |
			int(payload[offset+7])
		valueMs = &ms
		offset += 8
	}
	
	// Check if valuePercent is present (bit 1)
	if (flags & 0x02) != 0 && len(payload) >= offset+1 {
		percent := int(payload[offset])
		valuePercent = &percent
	}
	
	return valueMs, valuePercent
}

// Command handlers for BinaryProtocolV2 compatibility

func (h *CommandHandler) HandlePlayCommand(payload []byte) {
	log.Println("Received play command")
	
	// Parse command payload
	valueMs, valuePercent := h.parseCommandPayload(payload)
	
	// Create media command for WebSocket clients
	mediaCmd := utils.MediaCommand{
		Command:      "play",
		ValueMs:      valueMs,
		ValuePercent: valuePercent,
	}
	
	// Broadcast to WebSocket clients using broadcaster
	h.broadcaster.BroadcastMediaCommand(mediaCmd)
	
	log.Printf("✅ Play command forwarded to media system")
}

func (h *CommandHandler) HandlePauseCommand(payload []byte) {
	log.Println("Received pause command")
	
	// Parse command payload
	valueMs, valuePercent := h.parseCommandPayload(payload)
	
	// Create media command for WebSocket clients
	mediaCmd := utils.MediaCommand{
		Command:      "pause",
		ValueMs:      valueMs,
		ValuePercent: valuePercent,
	}
	
	// Broadcast to WebSocket clients using broadcaster
	h.broadcaster.BroadcastMediaCommand(mediaCmd)
	
	log.Printf("✅ Pause command forwarded to media system")
}

func (h *CommandHandler) HandleNextCommand(payload []byte) {
	log.Println("Received next command")
	
	// Parse command payload
	valueMs, valuePercent := h.parseCommandPayload(payload)
	
	// Create media command for WebSocket clients
	mediaCmd := utils.MediaCommand{
		Command:      "next",
		ValueMs:      valueMs,
		ValuePercent: valuePercent,
	}
	
	// Broadcast to WebSocket clients using broadcaster
	h.broadcaster.BroadcastMediaCommand(mediaCmd)
	
	log.Printf("✅ Next command forwarded to media system")
}

func (h *CommandHandler) HandlePreviousCommand(payload []byte) {
	log.Println("Received previous command")
	
	// Parse command payload
	valueMs, valuePercent := h.parseCommandPayload(payload)
	
	// Create media command for WebSocket clients
	mediaCmd := utils.MediaCommand{
		Command:      "previous",
		ValueMs:      valueMs,
		ValuePercent: valuePercent,
	}
	
	// Broadcast to WebSocket clients using broadcaster
	h.broadcaster.BroadcastMediaCommand(mediaCmd)
	
	log.Printf("✅ Previous command forwarded to media system")
}

func (h *CommandHandler) HandleSeekCommand(payload []byte) {
	log.Println("Received seek command")
	
	// Parse seek position from payload
	valueMs, valuePercent := h.parseCommandPayload(payload)
	
	if valueMs == nil {
		log.Printf("Warning: Seek command missing position value")
		return
	}
	
	// Create media command for WebSocket clients
	mediaCmd := utils.MediaCommand{
		Command:      "seek",
		ValueMs:      valueMs,
		ValuePercent: valuePercent,
	}
	
	// Broadcast to WebSocket clients using broadcaster
	h.broadcaster.BroadcastMediaCommand(mediaCmd)
	
	log.Printf("✅ Seek command forwarded to media system (position: %dms)", *valueMs)
}

func (h *CommandHandler) HandleVolumeCommand(payload []byte) {
	log.Println("Received volume command")
	
	// Parse volume from payload
	valueMs, valuePercent := h.parseCommandPayload(payload)
	
	if valuePercent == nil {
		log.Printf("Warning: Volume command missing volume value")
		return
	}
	
	// Create media command for WebSocket clients
	mediaCmd := utils.MediaCommand{
		Command:      "volume",
		ValueMs:      valueMs,
		ValuePercent: valuePercent,
	}
	
	// Broadcast to WebSocket clients using broadcaster
	h.broadcaster.BroadcastMediaCommand(mediaCmd)
	
	log.Printf("✅ Volume command forwarded to media system (volume: %d%%)", *valuePercent)
}

func (h *CommandHandler) HandleStateRequest(payload []byte) {
	log.Println("Received state request")
	
	// Request current media state from WebSocket clients
	stateRequest := utils.MediaCommand{
		Command: "request_state",
	}
	
	h.broadcaster.BroadcastMediaCommand(stateRequest)
	
	log.Printf("✅ State request forwarded to media system")
}

func (h *CommandHandler) HandleTimeSyncRequest() {
	log.Println("Received time sync request")
	
	// Send time sync response
	if err := h.sendTimeSyncResponse(); err != nil {
		log.Printf("Failed to send time sync response: %v", err)
	} else {
		log.Printf("✅ Time sync response sent")
	}
}

func (h *CommandHandler) HandleAlbumArtRequest(payload []byte) {
	// Parse album art query payload (should contain MD5 hash or track ID)
	query := string(payload)
	log.Printf("Received album art request for: %s", query)
	
	// Forward album art request to WebSocket clients
	albumArtRequest := utils.MediaCommand{
		Command: "album_art_request",
		Hash:    query,
	}
	
	h.broadcaster.BroadcastMediaCommand(albumArtRequest)
	
	log.Printf("✅ Album art request forwarded to media system")
}

func (h *CommandHandler) HandleCapabilitiesRequest() {
	log.Println("Received capabilities request")
	
	// Send capabilities response
	capabilities := []string{
		"binary_protocol_v2",
		"media_control",
		"album_art_transfer",
		"time_sync",
		"state_updates",
	}
	
	payload := CreateCapabilitiesPayload("nocturned-v2-1.0.0", capabilities, 517, true)
	if err := h.sendMessageFunc(MSG_CAPABILITIES, payload); err != nil {
		log.Printf("Failed to send capabilities response: %v", err)
	} else {
		log.Printf("✅ Capabilities response sent: %v", capabilities)
	}
}

func (h *CommandHandler) sendTimeSyncResponse() error {
	// Get current time and timezone
	timestampMs := utils.GetCurrentTimestampMs()
	timezone := utils.GetCurrentTimezone()
	
	payload := CreateTimeSyncPayload(timestampMs, timezone)
	return h.sendMessageFunc(MSG_TIME_SYNC, payload)
}