package bluetooth

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// BinaryProtocolV2 constants compatible with NocturneCompanion
const (
	PROTOCOL_VERSION = 2
	HEADER_SIZE      = 16

	// System messages (0x00xx)
	MSG_CAPABILITIES                     = 0x0001
	MSG_TIME_SYNC                       = 0x0002
	MSG_PROTOCOL_ENABLE                 = 0x0003
	MSG_DEVICE_INFO                     = 0x0004
	MSG_CONNECTION_PARAMS               = 0x0005
	MSG_GET_CAPABILITIES                = 0x0006
	MSG_ENABLE_BINARY_INCREMENTAL       = 0x0007
	MSG_REQUEST_HIGH_PRIORITY_CONNECTION = 0x0008
	MSG_OPTIMIZE_CONNECTION_PARAMS      = 0x0009

	// Command messages (0x01xx)
	MSG_CMD_PLAY              = 0x0101
	MSG_CMD_PAUSE             = 0x0102
	MSG_CMD_NEXT              = 0x0103
	MSG_CMD_PREVIOUS          = 0x0104
	MSG_CMD_SEEK_TO           = 0x0105
	MSG_CMD_SET_VOLUME        = 0x0106
	MSG_CMD_REQUEST_STATE     = 0x0107
	MSG_CMD_REQUEST_TIMESTAMP = 0x0108
	MSG_CMD_ALBUM_ART_QUERY   = 0x0109
	MSG_CMD_TEST_ALBUM_ART    = 0x010A

	// State messages (0x02xx)
	MSG_STATE_FULL         = 0x0201
	MSG_STATE_ARTIST       = 0x0202
	MSG_STATE_ALBUM        = 0x0203
	MSG_STATE_TRACK        = 0x0204
	MSG_STATE_POSITION     = 0x0205
	MSG_STATE_DURATION     = 0x0206
	MSG_STATE_PLAY_STATUS  = 0x0207
	MSG_STATE_VOLUME       = 0x0208
	MSG_STATE_ARTIST_ALBUM = 0x0209

	// Album art messages (0x03xx)
	MSG_ALBUM_ART_START        = 0x0301
	MSG_ALBUM_ART_CHUNK        = 0x0302
	MSG_ALBUM_ART_END          = 0x0303
	MSG_ALBUM_ART_NOT_AVAILABLE = 0x0304

	// Test album art messages (0x031x)
	MSG_TEST_ALBUM_ART_START = 0x0310
	MSG_TEST_ALBUM_ART_CHUNK = 0x0311
	MSG_TEST_ALBUM_ART_END   = 0x0312

	// Error messages (0x04xx)
	MSG_ERROR                 = 0x0401
	MSG_ERROR_COMMAND_FAILED  = 0x0402
	MSG_ERROR_INVALID_MESSAGE = 0x0403

	// Weather messages (0x05xx) - Extended protocol from NocturneCompanion
	MSG_WEATHER_START = 0x0501
	MSG_WEATHER_CHUNK = 0x0502
	MSG_WEATHER_END   = 0x0503

	// Gradient messages (0x06xx)
	MSG_GRADIENT_COLORS = 0x0601
)

// MessageHeader represents the 16-byte binary protocol header
type MessageHeader struct {
	MessageType  uint16 // Protocol version + Message Type (version in high 4 bits, type in lower 12 bits)
	MessageID    uint16 // For request/response correlation
	PayloadSize  uint32 // Size of payload
	CRC32        uint32 // CRC32 of payload only
	Flags        uint16 // For future use
	Reserved     uint16 // Reserved
}

// EncodeMessage creates a complete binary message compatible with NocturneCompanion
func EncodeMessage(messageType uint16, payload []byte, messageID uint16) []byte {
	// Calculate CRC32 of payload
	crc := crc32.ChecksumIEEE(payload)

	// Combine protocol version with message type
	versionedType := (uint16(PROTOCOL_VERSION) << 12) | (messageType & 0x0FFF)

	// Create header
	header := MessageHeader{
		MessageType: versionedType,
		MessageID:   messageID,
		PayloadSize: uint32(len(payload)),
		CRC32:       crc,
		Flags:       0,
		Reserved:    0,
	}

	// Encode header
	headerBytes := make([]byte, HEADER_SIZE)
	binary.BigEndian.PutUint16(headerBytes[0:2], header.MessageType)
	binary.BigEndian.PutUint16(headerBytes[2:4], header.MessageID)
	binary.BigEndian.PutUint32(headerBytes[4:8], header.PayloadSize)
	binary.BigEndian.PutUint32(headerBytes[8:12], header.CRC32)
	binary.BigEndian.PutUint16(headerBytes[12:14], header.Flags)
	binary.BigEndian.PutUint16(headerBytes[14:16], header.Reserved)

	// Combine header and payload
	result := make([]byte, HEADER_SIZE+len(payload))
	copy(result[:HEADER_SIZE], headerBytes)
	copy(result[HEADER_SIZE:], payload)

	return result
}

// DecodeMessage parses a binary message compatible with NocturneCompanion
func DecodeMessage(data []byte) (messageType uint16, payload []byte, messageID uint16, err error) {
	if len(data) < HEADER_SIZE {
		return 0, nil, 0, fmt.Errorf("message too short: %d bytes", len(data))
	}

	// Parse header
	versionedType := binary.BigEndian.Uint16(data[0:2])
	messageID = binary.BigEndian.Uint16(data[2:4])
	payloadSize := binary.BigEndian.Uint32(data[4:8])
	headerCRC32 := binary.BigEndian.Uint32(data[8:12])
	// Skip flags and reserved for now

	// Extract protocol version and message type
	version := (versionedType >> 12) & 0x0F
	messageType = versionedType & 0x0FFF

	// Verify protocol version
	if version != PROTOCOL_VERSION {
		return 0, nil, 0, fmt.Errorf("unsupported protocol version: %d", version)
	}

	// Check payload size
	if len(data) < int(HEADER_SIZE+payloadSize) {
		return 0, nil, 0, fmt.Errorf("incomplete message: expected %d bytes, got %d", HEADER_SIZE+payloadSize, len(data))
	}

	// Extract payload
	payload = data[HEADER_SIZE : HEADER_SIZE+payloadSize]

	// Verify CRC32
	calculatedCRC := crc32.ChecksumIEEE(payload)
	if calculatedCRC != headerCRC32 {
		return 0, nil, 0, fmt.Errorf("CRC32 mismatch: expected %08x, got %08x", headerCRC32, calculatedCRC)
	}

	return messageType, payload, messageID, nil
}

// CreateFullStatePayload creates a full state update payload compatible with NocturneCompanion
func CreateFullStatePayload(artist, album, track string, durationMs, positionMs int64, isPlaying bool, volumePercent int) []byte {
	artistBytes := []byte(artist)
	albumBytes := []byte(album)
	trackBytes := []byte(track)
	
	totalSize := 1 + 8 + 8 + 1 + 2 + len(artistBytes) + 2 + len(albumBytes) + 2 + len(trackBytes)
	payload := make([]byte, totalSize)
	
	offset := 0
	
	// State flags (1 byte)
	var flags byte
	if isPlaying {
		flags = 0x01
	}
	payload[offset] = flags
	offset++
	
	// Timing (16 bytes)
	binary.BigEndian.PutUint64(payload[offset:offset+8], uint64(durationMs))
	offset += 8
	binary.BigEndian.PutUint64(payload[offset:offset+8], uint64(positionMs))
	offset += 8
	
	// Volume (1 byte)
	payload[offset] = byte(volumePercent)
	offset++
	
	// Strings with length prefixes
	binary.BigEndian.PutUint16(payload[offset:offset+2], uint16(len(artistBytes)))
	offset += 2
	copy(payload[offset:offset+len(artistBytes)], artistBytes)
	offset += len(artistBytes)
	
	binary.BigEndian.PutUint16(payload[offset:offset+2], uint16(len(albumBytes)))
	offset += 2
	copy(payload[offset:offset+len(albumBytes)], albumBytes)
	offset += len(albumBytes)
	
	binary.BigEndian.PutUint16(payload[offset:offset+2], uint16(len(trackBytes)))
	offset += 2
	copy(payload[offset:], trackBytes)
	
	return payload
}

// CreateTimeSyncPayload creates a time sync payload compatible with NocturneCompanion
func CreateTimeSyncPayload(timestampMs int64, timezone string) []byte {
	tzBytes := []byte(timezone)
	
	payload := make([]byte, 8+2+len(tzBytes))
	binary.BigEndian.PutUint64(payload[0:8], uint64(timestampMs))
	binary.BigEndian.PutUint16(payload[8:10], uint16(len(tzBytes)))
	copy(payload[10:], tzBytes)
	
	return payload
}

// ParseTimeSyncPayload parses a time sync payload from NocturneCompanion
func ParseTimeSyncPayload(payload []byte) (timestampMs int64, timezone string, err error) {
	if len(payload) < 10 {
		return 0, "", fmt.Errorf("time sync payload too short: %d bytes", len(payload))
	}
	
	// Parse timestamp (8 bytes, big-endian)
	timestampMs = int64(binary.BigEndian.Uint64(payload[0:8]))
	
	// Parse timezone length (2 bytes, big-endian)
	tzLength := binary.BigEndian.Uint16(payload[8:10])
	
	// Check if we have enough data for the timezone string
	if len(payload) < int(10+tzLength) {
		return 0, "", fmt.Errorf("time sync payload incomplete: expected %d bytes, got %d", 10+tzLength, len(payload))
	}
	
	// Parse timezone string
	timezone = string(payload[10 : 10+tzLength])
	
	return timestampMs, timezone, nil
}

// CreateCommandPayload creates a command payload with optional values
func CreateCommandPayload(valueMs *int64, valuePercent *int) []byte {
	payload := []byte{0} // Start with flags byte
	
	// Set flags and add values if present
	if valueMs != nil {
		payload[0] |= 0x01 // Set bit 0
		valueBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(valueBytes, uint64(*valueMs))
		payload = append(payload, valueBytes...)
	}
	
	if valuePercent != nil {
		payload[0] |= 0x02 // Set bit 1
		payload = append(payload, byte(*valuePercent))
	}
	
	return payload
}

// CreateStringPayload creates a simple string payload
func CreateStringPayload(value string) []byte {
	return []byte(value)
}

// CreateLongPayload creates a long value payload (8 bytes)
func CreateLongPayload(value int64) []byte {
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, uint64(value))
	return payload
}

// CreateBooleanPayload creates a boolean payload (1 byte)
func CreateBooleanPayload(value bool) []byte {
	if value {
		return []byte{1}
	}
	return []byte{0}
}

// CreateBytePayload creates a byte payload
func CreateBytePayload(value byte) []byte {
	return []byte{value}
}

// CreateArtistAlbumPayload creates combined artist+album payload
func CreateArtistAlbumPayload(artist, album string) []byte {
	artistBytes := []byte(artist)
	albumBytes := []byte(album)
	
	payload := make([]byte, 4+len(artistBytes)+len(albumBytes))
	binary.BigEndian.PutUint16(payload[0:2], uint16(len(artistBytes)))
	copy(payload[2:2+len(artistBytes)], artistBytes)
	binary.BigEndian.PutUint16(payload[2+len(artistBytes):4+len(artistBytes)], uint16(len(albumBytes)))
	copy(payload[4+len(artistBytes):], albumBytes)
	
	return payload
}

// Album art payload structures compatible with NocturneCompanion

// AlbumArtStartPayload represents album art start message with SHA-256 checksum
type AlbumArtStartPayload struct {
	Checksum    [32]byte // SHA-256 checksum
	TotalChunks uint32   // Total number of chunks
	ImageSize   uint32   // Total image size
	TrackID     string   // Track identifier
}

// CreateAlbumArtStartPayload creates album art start payload
func CreateAlbumArtStartPayload(checksum [32]byte, totalChunks, imageSize uint32, trackID string) []byte {
	trackIDBytes := []byte(trackID)
	payload := make([]byte, 32+4+4+len(trackIDBytes))
	
	copy(payload[0:32], checksum[:])
	binary.BigEndian.PutUint32(payload[32:36], totalChunks)
	binary.BigEndian.PutUint32(payload[36:40], imageSize)
	copy(payload[40:], trackIDBytes)
	
	return payload
}

// ParseAlbumArtStartPayload parses album art start payload
func ParseAlbumArtStartPayload(payload []byte) (*AlbumArtStartPayload, error) {
	if len(payload) < 40 {
		return nil, fmt.Errorf("album art start payload too short: %d bytes", len(payload))
	}
	
	result := &AlbumArtStartPayload{}
	copy(result.Checksum[:], payload[0:32])
	result.TotalChunks = binary.BigEndian.Uint32(payload[32:36])
	result.ImageSize = binary.BigEndian.Uint32(payload[36:40])
	result.TrackID = string(payload[40:])
	
	return result, nil
}

// AlbumArtChunkPayload represents album art chunk with index and data
type AlbumArtChunkPayload struct {
	ChunkIndex uint32
	Data       []byte
}

// CreateAlbumArtChunkPayload creates album art chunk payload
func CreateAlbumArtChunkPayload(chunkIndex uint32, data []byte) []byte {
	payload := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(payload[0:4], chunkIndex)
	copy(payload[4:], data)
	return payload
}

// ParseAlbumArtChunkPayload parses album art chunk payload
func ParseAlbumArtChunkPayload(payload []byte) (*AlbumArtChunkPayload, error) {
	if len(payload) < 4 {
		return nil, fmt.Errorf("album art chunk payload too short: %d bytes", len(payload))
	}
	
	return &AlbumArtChunkPayload{
		ChunkIndex: binary.BigEndian.Uint32(payload[0:4]),
		Data:       payload[4:],
	}, nil
}

// AlbumArtEndPayload represents album art end message
type AlbumArtEndPayload struct {
	Checksum [32]byte // SHA-256 checksum
	Success  bool     // Transfer success flag
}

// CreateAlbumArtEndPayload creates album art end payload
func CreateAlbumArtEndPayload(checksum [32]byte, success bool) []byte {
	payload := make([]byte, 33)
	copy(payload[0:32], checksum[:])
	if success {
		payload[32] = 1
	} else {
		payload[32] = 0
	}
	return payload
}

// ParseAlbumArtEndPayload parses album art end payload
func ParseAlbumArtEndPayload(payload []byte) (*AlbumArtEndPayload, error) {
	if len(payload) < 33 {
		return nil, fmt.Errorf("album art end payload too short: %d bytes", len(payload))
	}
	
	result := &AlbumArtEndPayload{}
	copy(result.Checksum[:], payload[0:32])
	result.Success = payload[32] != 0
	
	return result, nil
}

// CreateGradientColorsPayload creates gradient colors payload
func CreateGradientColorsPayload(colors []uint32) []byte {
	colorCount := len(colors)
	if colorCount > 255 {
		colorCount = 255 // Limit to 255 colors
	}
	
	payload := make([]byte, 1+colorCount*3)
	payload[0] = byte(colorCount)
	
	offset := 1
	for i := 0; i < colorCount; i++ {
		color := colors[i]
		payload[offset] = byte((color >> 16) & 0xFF) // Red
		payload[offset+1] = byte((color >> 8) & 0xFF)  // Green
		payload[offset+2] = byte(color & 0xFF)         // Blue
		offset += 3
	}
	
	return payload
}

// CalculateSHA256 calculates SHA-256 hash of data
func CalculateSHA256(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// CreateCapabilitiesPayload creates a capabilities response payload
func CreateCapabilitiesPayload(version string, features []string, mtu int, debugEnabled bool) []byte {
	versionBytes := []byte(version)
	featuresStr := ""
	if len(features) > 0 {
		featuresStr = fmt.Sprintf("%v", features) // Simple string representation
	}
	featuresBytes := []byte(featuresStr)
	
	payload := make([]byte, 0, 1+2+1+len(versionBytes)+2+len(featuresBytes))
	
	// Flags
	flags := byte(0)
	if debugEnabled {
		flags |= 0x01
	}
	payload = append(payload, flags)
	
	// MTU (2 bytes, big-endian)
	payload = append(payload, byte(mtu>>8), byte(mtu&0xFF))
	
	// Version string length + data
	payload = append(payload, byte(len(versionBytes)))
	payload = append(payload, versionBytes...)
	
	// Features string length + data (2 bytes length for future expansion)
	payload = append(payload, byte(len(featuresBytes)>>8), byte(len(featuresBytes)&0xFF))
	payload = append(payload, featuresBytes...)
	
	return payload
}

// GetMessageTypeString returns human-readable string for message type
func GetMessageTypeString(msgType uint16) string {
	switch msgType {
	// System messages
	case MSG_CAPABILITIES:
		return "Capabilities"
	case MSG_TIME_SYNC:
		return "TimeSync"
	case MSG_PROTOCOL_ENABLE:
		return "ProtocolEnable"
	case MSG_GET_CAPABILITIES:
		return "GetCapabilities"
	case MSG_ENABLE_BINARY_INCREMENTAL:
		return "EnableBinaryIncremental"
	
	// Command messages
	case MSG_CMD_PLAY:
		return "Play"
	case MSG_CMD_PAUSE:
		return "Pause"
	case MSG_CMD_NEXT:
		return "Next"
	case MSG_CMD_PREVIOUS:
		return "Previous"
	case MSG_CMD_SEEK_TO:
		return "SeekTo"
	case MSG_CMD_SET_VOLUME:
		return "SetVolume"
	case MSG_CMD_REQUEST_STATE:
		return "RequestState"
	case MSG_CMD_ALBUM_ART_QUERY:
		return "AlbumArtQuery"
	
	// State messages
	case MSG_STATE_FULL:
		return "FullState"
	case MSG_STATE_ARTIST:
		return "Artist"
	case MSG_STATE_ALBUM:
		return "Album"
	case MSG_STATE_TRACK:
		return "Track"
	case MSG_STATE_POSITION:
		return "Position"
	case MSG_STATE_DURATION:
		return "Duration"
	case MSG_STATE_PLAY_STATUS:
		return "PlayStatus"
	case MSG_STATE_VOLUME:
		return "Volume"
	case MSG_STATE_ARTIST_ALBUM:
		return "ArtistAlbum"
	
	// Album art messages
	case MSG_ALBUM_ART_START:
		return "AlbumArtStart"
	case MSG_ALBUM_ART_CHUNK:
		return "AlbumArtChunk"
	case MSG_ALBUM_ART_END:
		return "AlbumArtEnd"
	case MSG_ALBUM_ART_NOT_AVAILABLE:
		return "AlbumArtNotAvailable"
	
	// Test album art messages
	case MSG_TEST_ALBUM_ART_START:
		return "TestAlbumArtStart"
	case MSG_TEST_ALBUM_ART_CHUNK:
		return "TestAlbumArtChunk"
	case MSG_TEST_ALBUM_ART_END:
		return "TestAlbumArtEnd"
	
	// Error messages
	case MSG_ERROR:
		return "Error"
	
	// Weather messages
	case MSG_WEATHER_START:
		return "WeatherStart"
	case MSG_WEATHER_CHUNK:
		return "WeatherChunk"
	case MSG_WEATHER_END:
		return "WeatherEnd"

	// Gradient messages
	case MSG_GRADIENT_COLORS:
		return "GradientColors"
	
	default:
		return fmt.Sprintf("Unknown(0x%04x)", msgType)
	}
}

// Legacy compatibility functions for existing code

// EncodePlayStateUpdate creates a legacy play state update - converts to new format
func EncodePlayStateUpdate(trackTitle, artist, album, albumArtHash string, duration, position uint32, playState byte) []byte {
	isPlaying := playState == 1
	payload := CreateFullStatePayload(artist, album, trackTitle, int64(duration)*1000, int64(position)*1000, isPlaying, 75)
	return EncodeMessage(MSG_STATE_FULL, payload, 0)
}

// EncodeVolumeUpdate creates a volume update message
func EncodeVolumeUpdate(volume byte) []byte {
	payload := CreateBytePayload(volume)
	return EncodeMessage(MSG_STATE_VOLUME, payload, 0)
}

// EncodeAlbumArtStart creates album art start message
func EncodeAlbumArtStart(totalChunks uint16, albumArtHash string) []byte {
	// Create a dummy checksum for compatibility
	checksum := CalculateSHA256([]byte(albumArtHash))
	payload := CreateAlbumArtStartPayload(checksum, uint32(totalChunks), 0, albumArtHash)
	return EncodeMessage(MSG_ALBUM_ART_START, payload, 0)
}

// EncodeAlbumArtChunk creates album art chunk message
func EncodeAlbumArtChunk(chunkIndex uint16, chunkData []byte) []byte {
	payload := CreateAlbumArtChunkPayload(uint32(chunkIndex), chunkData)
	return EncodeMessage(MSG_ALBUM_ART_CHUNK, payload, 0)
}