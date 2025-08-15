package bluetooth

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Binary protocol constants
const (
	BinaryProtocolVersion byte = 2
	BinaryHeaderSize      = 16
	
	// System messages (0x00xx)
	MsgCapabilities     uint16 = 0x0001
	MsgTimeSync        uint16 = 0x0002
	MsgProtocolEnable  uint16 = 0x0003
	MsgDeviceInfo      uint16 = 0x0004
	MsgConnectionParams uint16 = 0x0005
	MsgGetCapabilities  uint16 = 0x0006
	MsgEnableBinaryIncremental uint16 = 0x0007
	MsgRequestHighPriorityConnection uint16 = 0x0008
	MsgOptimizeConnectionParams uint16 = 0x0009
	
	// Command messages - must fit in 12 bits (0x000-0xFFF)
	// Using 0x01xx range for commands
	MsgCmdPlay            uint16 = 0x0101
	MsgCmdPause           uint16 = 0x0102
	MsgCmdNext            uint16 = 0x0103
	MsgCmdPrevious        uint16 = 0x0104
	MsgCmdSeekTo          uint16 = 0x0105
	MsgCmdSetVolume       uint16 = 0x0106
	MsgCmdRequestState    uint16 = 0x0107
	MsgCmdRequestTimestamp uint16 = 0x0108
	MsgCmdAlbumArtQuery   uint16 = 0x0109
	MsgCmdTestAlbumArt    uint16 = 0x010A
	
	// State messages - must fit in 12 bits (0x000-0xFFF)
	// Using 0x02xx range for states
	MsgStateFull        uint16 = 0x0201
	MsgStateArtist      uint16 = 0x0202
	MsgStateAlbum       uint16 = 0x0203
	MsgStateTrack       uint16 = 0x0204
	MsgStatePosition    uint16 = 0x0205
	MsgStateDuration    uint16 = 0x0206
	MsgStatePlayStatus  uint16 = 0x0207
	MsgStateVolume      uint16 = 0x0208
	MsgStateArtistAlbum uint16 = 0x0209
	
	// Album art messages - must fit in 12 bits (0x000-0xFFF)
	// Using 0x03xx range for album art
	MsgAlbumArtStart        uint16 = 0x0301
	MsgAlbumArtChunk        uint16 = 0x0302
	MsgAlbumArtEnd          uint16 = 0x0303
	MsgAlbumArtNotAvailable uint16 = 0x0304
	
	// Legacy album art messages (from BinaryProtocol v1)
	MsgLegacyAlbumArtStart uint16 = 0x0100
	MsgLegacyAlbumArtChunk uint16 = 0x0101
	MsgLegacyAlbumArtEnd   uint16 = 0x0102
	
	// Test album art messages
	MsgTestAlbumArtStart  uint16 = 0x0310
	MsgTestAlbumArtChunk  uint16 = 0x0311
	MsgTestAlbumArtEnd    uint16 = 0x0312
	
	// Error messages - must fit in 12 bits (0x000-0xFFF)
	// Using 0x04xx range for errors
	MsgError              uint16 = 0x0401
	MsgErrorCommandFailed uint16 = 0x0402
	MsgErrorInvalidMessage uint16 = 0x0403
	
	// Weather messages - must fit in 12 bits (0x000-0xFFF)
	// Using 0x05xx range for weather
	MsgWeatherRequest     uint16 = 0x050A  // Request weather refresh
	MsgWeatherStart       uint16 = 0x0501  // Weather transfer start
	MsgWeatherChunk       uint16 = 0x0502  // Weather data chunk
	MsgWeatherEnd         uint16 = 0x0503  // Weather transfer complete
)

// MessageHeader represents the enhanced 16-byte header for all binary messages
type MessageHeader struct {
	MessageType uint16 // Message type identifier (with version in high 4 bits)
	MessageID   uint16 // Message ID for correlation
	PayloadSize uint32 // Payload size
	CRC32       uint32 // CRC32 of payload
	Flags       uint16 // Flags for future use
	Reserved    uint16 // Reserved
}

// Legacy BinaryHeader for backward compatibility with album art
type BinaryHeader struct {
	MessageType uint16 // Message type identifier
	ChunkIndex  uint16 // Chunk index (0 for non-chunk messages)
	TotalSize   uint32 // Total payload size
	CRC32       uint32 // CRC32 of payload
	Reserved    uint32 // Reserved for future use
}

// Marshal serializes the header to bytes
func (h *BinaryHeader) Marshal() []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, h)
	return buf.Bytes()
}

// UnmarshalBinaryHeader deserializes a header from bytes
func UnmarshalBinaryHeader(data []byte) (*BinaryHeader, error) {
	if len(data) < BinaryHeaderSize {
		return nil, fmt.Errorf("insufficient data for header: %d bytes", len(data))
	}
	
	h := &BinaryHeader{}
	buf := bytes.NewReader(data[:BinaryHeaderSize])
	if err := binary.Read(buf, binary.BigEndian, h); err != nil {
		return nil, err
	}
	
	return h, nil
}

// AlbumArtStartPayload represents the album art start message payload
type AlbumArtStartPayload struct {
	Checksum    [32]byte // SHA-256 checksum
	TotalChunks uint32   // Total number of chunks
	ImageSize   uint32   // Total image size in bytes
	TrackID     string   // Track identifier
}

// Marshal serializes the payload to bytes
func (p *AlbumArtStartPayload) Marshal() []byte {
	buf := new(bytes.Buffer)
	buf.Write(p.Checksum[:])
	binary.Write(buf, binary.BigEndian, p.TotalChunks)
	binary.Write(buf, binary.BigEndian, p.ImageSize)
	buf.WriteString(p.TrackID)
	return buf.Bytes()
}

// UnmarshalAlbumArtStartPayload deserializes the payload from bytes
func UnmarshalAlbumArtStartPayload(data []byte) (*AlbumArtStartPayload, error) {
	if len(data) < 40 {
		return nil, fmt.Errorf("insufficient data for start payload: %d bytes", len(data))
	}
	
	p := &AlbumArtStartPayload{}
	buf := bytes.NewReader(data)
	
	// Read checksum
	if _, err := buf.Read(p.Checksum[:]); err != nil {
		return nil, err
	}
	
	// Read total chunks
	if err := binary.Read(buf, binary.BigEndian, &p.TotalChunks); err != nil {
		return nil, err
	}
	
	// Read image size
	if err := binary.Read(buf, binary.BigEndian, &p.ImageSize); err != nil {
		return nil, err
	}
	
	// Read track ID (remaining bytes)
	trackIDBytes := make([]byte, buf.Len())
	if _, err := buf.Read(trackIDBytes); err != nil {
		return nil, err
	}
	p.TrackID = string(trackIDBytes)
	
	return p, nil
}

// AlbumArtEndPayload represents the album art end message payload
type AlbumArtEndPayload struct {
	Checksum [32]byte // SHA-256 checksum
	Success  bool     // Transfer success flag
}

// Marshal serializes the payload to bytes
func (p *AlbumArtEndPayload) Marshal() []byte {
	buf := new(bytes.Buffer)
	buf.Write(p.Checksum[:])
	if p.Success {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// UnmarshalAlbumArtEndPayload deserializes the payload from bytes
func UnmarshalAlbumArtEndPayload(data []byte) (*AlbumArtEndPayload, error) {
	if len(data) < 33 {
		return nil, fmt.Errorf("insufficient data for end payload: %d bytes", len(data))
	}
	
	p := &AlbumArtEndPayload{}
	copy(p.Checksum[:], data[:32])
	p.Success = data[32] != 0
	
	return p, nil
}

// ParseBinaryMessage parses a complete binary message
func ParseBinaryMessage(data []byte) (*BinaryHeader, []byte, error) {
	if len(data) < BinaryHeaderSize {
		return nil, nil, fmt.Errorf("message too short: %d bytes", len(data))
	}
	
	// Parse header
	header, err := UnmarshalBinaryHeader(data)
	if err != nil {
		return nil, nil, err
	}
	
	// Extract payload
	payload := data[BinaryHeaderSize:]
	
	// Verify CRC
	crc := crc32.ChecksumIEEE(payload)
	if crc != header.CRC32 {
		return nil, nil, fmt.Errorf("CRC mismatch: expected %x, got %x", header.CRC32, crc)
	}
	
	// Verify payload size
	if uint32(len(payload)) != header.TotalSize {
		return nil, nil, fmt.Errorf("payload size mismatch: expected %d, got %d", header.TotalSize, len(payload))
	}
	
	return header, payload, nil
}

// CreateBinaryMessage creates a complete binary message with header
func CreateBinaryMessage(messageType uint16, chunkIndex uint16, payload []byte) []byte {
	header := &BinaryHeader{
		MessageType: messageType,
		ChunkIndex:  chunkIndex,
		TotalSize:   uint32(len(payload)),
		CRC32:       crc32.ChecksumIEEE(payload),
		Reserved:    0,
	}
	
	buf := new(bytes.Buffer)
	buf.Write(header.Marshal())
	buf.Write(payload)
	
	return buf.Bytes()
}

// HexToBytes converts a hex string to byte array
func HexToBytes(hex string) ([]byte, error) {
	if len(hex)%2 != 0 {
		return nil, fmt.Errorf("hex string must have even length")
	}
	
	bytes := make([]byte, len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		var b byte
		_, err := fmt.Sscanf(hex[i:i+2], "%02x", &b)
		if err != nil {
			return nil, err
		}
		bytes[i/2] = b
	}
	
	return bytes, nil
}

// BytesToHex converts a byte array to hex string
func BytesToHex(bytes []byte) string {
	hex := ""
	for _, b := range bytes {
		hex += fmt.Sprintf("%02x", b)
	}
	return hex
}

// ParseStringPayload extracts a UTF-8 string from binary payload
func ParseStringPayload(data []byte) string {
	return string(data)
}

// ParseLongPayload extracts an int64 from binary payload (8 bytes, big-endian)
func ParseLongPayload(data []byte) (int64, error) {
	if len(data) < 8 {
		return 0, fmt.Errorf("insufficient data for long: %d bytes", len(data))
	}
	return int64(binary.BigEndian.Uint64(data[:8])), nil
}

// ParseBooleanPayload extracts a boolean from binary payload (1 byte)
func ParseBooleanPayload(data []byte) (bool, error) {
	if len(data) < 1 {
		return false, fmt.Errorf("insufficient data for boolean")
	}
	return data[0] != 0, nil
}

// ParseBytePayload extracts a byte from binary payload
func ParseBytePayload(data []byte) (byte, error) {
	if len(data) < 1 {
		return 0, fmt.Errorf("insufficient data for byte")
	}
	return data[0], nil
}

// ParseArtistAlbumPayload extracts artist and album from combined binary payload
// Format: [artist_length:2][artist:N][album_length:2][album:M]
func ParseArtistAlbumPayload(data []byte) (string, string, error) {
	if len(data) < 4 {
		return "", "", fmt.Errorf("insufficient data for artist+album: %d bytes", len(data))
	}
	
	buf := bytes.NewReader(data)
	
	// Read artist length
	var artistLen uint16
	if err := binary.Read(buf, binary.BigEndian, &artistLen); err != nil {
		return "", "", fmt.Errorf("failed to read artist length: %v", err)
	}
	
	// Read artist
	artistBytes := make([]byte, artistLen)
	if _, err := buf.Read(artistBytes); err != nil {
		return "", "", fmt.Errorf("failed to read artist data: %v", err)
	}
	artist := string(artistBytes)
	
	// Read album length
	var albumLen uint16
	if err := binary.Read(buf, binary.BigEndian, &albumLen); err != nil {
		return "", "", fmt.Errorf("failed to read album length: %v", err)
	}
	
	// Read album
	albumBytes := make([]byte, albumLen)
	if _, err := buf.Read(albumBytes); err != nil {
		return "", "", fmt.Errorf("failed to read album data: %v", err)
	}
	album := string(albumBytes)
	
	return artist, album, nil
}

// ============ Enhanced Binary Protocol V2 Functions ============

// EncodeMessageHeader encodes the enhanced header to bytes
func (h *MessageHeader) Encode() []byte {
	buf := new(bytes.Buffer)
	
	// Combine protocol version with message type
	versionedType := (uint16(BinaryProtocolVersion) << 12) | (h.MessageType & 0x0FFF)
	binary.Write(buf, binary.BigEndian, versionedType)
	binary.Write(buf, binary.BigEndian, h.MessageID)
	binary.Write(buf, binary.BigEndian, h.PayloadSize)
	binary.Write(buf, binary.BigEndian, h.CRC32)
	binary.Write(buf, binary.BigEndian, h.Flags)
	binary.Write(buf, binary.BigEndian, h.Reserved)
	
	return buf.Bytes()
}

// DecodeMessageHeader decodes an enhanced header from bytes
func DecodeMessageHeader(data []byte) (*MessageHeader, error) {
	if len(data) < BinaryHeaderSize {
		return nil, fmt.Errorf("insufficient data for header: %d bytes", len(data))
	}
	
	buf := bytes.NewReader(data)
	h := &MessageHeader{}
	
	var versionedType uint16
	binary.Read(buf, binary.BigEndian, &versionedType)
	
	// Extract version and message type
	version := byte((versionedType >> 12) & 0x0F)
	if version != BinaryProtocolVersion {
		// Try legacy format
		return nil, fmt.Errorf("unsupported protocol version: %d", version)
	}
	
	h.MessageType = versionedType & 0x0FFF
	binary.Read(buf, binary.BigEndian, &h.MessageID)
	binary.Read(buf, binary.BigEndian, &h.PayloadSize)
	binary.Read(buf, binary.BigEndian, &h.CRC32)
	binary.Read(buf, binary.BigEndian, &h.Flags)
	binary.Read(buf, binary.BigEndian, &h.Reserved)
	
	return h, nil
}

// CreateMessage creates a complete binary message with enhanced header
func CreateMessage(messageType uint16, payload []byte, messageID uint16) []byte {
	// Calculate CRC32 of payload
	crc := crc32.ChecksumIEEE(payload)
	
	header := &MessageHeader{
		MessageType: messageType,
		MessageID:   messageID,
		PayloadSize: uint32(len(payload)),
		CRC32:       crc,
		Flags:       0,
		Reserved:    0,
	}
	
	result := header.Encode()
	result = append(result, payload...)
	return result
}

// ParseMessage parses a binary message with enhanced header
func ParseMessage(data []byte) (*MessageHeader, []byte, error) {
	header, err := DecodeMessageHeader(data)
	if err != nil {
		return nil, nil, err
	}
	
	totalSize := BinaryHeaderSize + int(header.PayloadSize)
	if len(data) < totalSize {
		return header, nil, fmt.Errorf("incomplete message: need %d bytes, have %d", totalSize, len(data))
	}
	
	payload := data[BinaryHeaderSize:totalSize]
	
	// Verify CRC
	crc := crc32.ChecksumIEEE(payload)
	if crc != header.CRC32 {
		return nil, nil, fmt.Errorf("CRC mismatch: expected %x, got %x", header.CRC32, crc)
	}
	
	return header, payload, nil
}

// CreateCommandPayload creates a command payload with optional values
func CreateCommandPayload(valueMs *int64, valuePercent *int) []byte {
	buf := new(bytes.Buffer)
	
	// Flags byte: bit 0 = has valueMs, bit 1 = has valuePercent
	var flags byte
	if valueMs != nil {
		flags |= 0x01
	}
	if valuePercent != nil {
		flags |= 0x02
	}
	buf.WriteByte(flags)
	
	// Add values if present
	if valueMs != nil {
		binary.Write(buf, binary.BigEndian, *valueMs)
	}
	if valuePercent != nil {
		buf.WriteByte(byte(*valuePercent))
	}
	
	return buf.Bytes()
}

// ParseCommandPayload parses a command payload
func ParseCommandPayload(data []byte) (*int64, *int, error) {
	if len(data) == 0 {
		return nil, nil, nil
	}
	
	buf := bytes.NewReader(data)
	
	var flags byte
	binary.Read(buf, binary.BigEndian, &flags)
	
	var valueMs *int64
	var valuePercent *int
	
	if flags&0x01 != 0 {
		var ms int64
		binary.Read(buf, binary.BigEndian, &ms)
		valueMs = &ms
	}
	
	if flags&0x02 != 0 {
		var pct byte
		binary.Read(buf, binary.BigEndian, &pct)
		percent := int(pct)
		valuePercent = &percent
	}
	
	return valueMs, valuePercent, nil
}

// StateData represents a full state update
type StateData struct {
	Artist        string
	Album         string
	Track         string
	DurationMs    int64
	PositionMs    int64
	IsPlaying     bool
	VolumePercent int
}

// CreateFullStatePayload creates a full state payload
func CreateFullStatePayload(state *StateData) []byte {
	buf := new(bytes.Buffer)
	
	// State flags (1 byte)
	var flags byte
	if state.IsPlaying {
		flags |= 0x01
	}
	buf.WriteByte(flags)
	
	// Timing (16 bytes)
	binary.Write(buf, binary.BigEndian, state.DurationMs)
	binary.Write(buf, binary.BigEndian, state.PositionMs)
	
	// Volume (1 byte)
	buf.WriteByte(byte(state.VolumePercent))
	
	// Strings with length prefixes
	artistBytes := []byte(state.Artist)
	binary.Write(buf, binary.BigEndian, uint16(len(artistBytes)))
	buf.Write(artistBytes)
	
	albumBytes := []byte(state.Album)
	binary.Write(buf, binary.BigEndian, uint16(len(albumBytes)))
	buf.Write(albumBytes)
	
	trackBytes := []byte(state.Track)
	binary.Write(buf, binary.BigEndian, uint16(len(trackBytes)))
	buf.Write(trackBytes)
	
	return buf.Bytes()
}

// ParseFullStatePayload parses a full state payload
func ParseFullStatePayload(data []byte) (*StateData, error) {
	if len(data) < 20 {
		return nil, fmt.Errorf("payload too small for full state")
	}
	
	buf := bytes.NewReader(data)
	state := &StateData{}
	
	// Flags
	var flags byte
	binary.Read(buf, binary.BigEndian, &flags)
	state.IsPlaying = (flags & 0x01) != 0
	
	// Timing
	binary.Read(buf, binary.BigEndian, &state.DurationMs)
	binary.Read(buf, binary.BigEndian, &state.PositionMs)
	
	// Volume
	var vol byte
	binary.Read(buf, binary.BigEndian, &vol)
	state.VolumePercent = int(vol)
	
	// Artist
	var artistLen uint16
	binary.Read(buf, binary.BigEndian, &artistLen)
	artistBytes := make([]byte, artistLen)
	buf.Read(artistBytes)
	state.Artist = string(artistBytes)
	
	// Album
	var albumLen uint16
	binary.Read(buf, binary.BigEndian, &albumLen)
	albumBytes := make([]byte, albumLen)
	buf.Read(albumBytes)
	state.Album = string(albumBytes)
	
	// Track
	var trackLen uint16
	binary.Read(buf, binary.BigEndian, &trackLen)
	trackBytes := make([]byte, trackLen)
	buf.Read(trackBytes)
	state.Track = string(trackBytes)
	
	return state, nil
}

// TimeSyncData represents time sync information
type TimeSyncData struct {
	TimestampMs int64
	Timezone    string
}

// ParseTimeSyncPayload parses a time sync payload
func ParseTimeSyncPayload(data []byte) (*TimeSyncData, error) {
	if len(data) < 10 {
		return nil, fmt.Errorf("payload too small for time sync")
	}
	
	buf := bytes.NewReader(data)
	sync := &TimeSyncData{}
	
	binary.Read(buf, binary.BigEndian, &sync.TimestampMs)
	
	var tzLen uint16
	binary.Read(buf, binary.BigEndian, &tzLen)
	tzBytes := make([]byte, tzLen)
	buf.Read(tzBytes)
	sync.Timezone = string(tzBytes)
	
	return sync, nil
}

// CapabilitiesData represents device capabilities
type CapabilitiesData struct {
	Version      string
	Features     []string
	MTU          int
	DebugEnabled bool
}

// ParseCapabilitiesPayload parses capabilities payload
func ParseCapabilitiesPayload(data []byte) (*CapabilitiesData, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("payload too small for capabilities")
	}
	
	buf := bytes.NewReader(data)
	caps := &CapabilitiesData{}
	
	// Flags
	var flags byte
	binary.Read(buf, binary.BigEndian, &flags)
	caps.DebugEnabled = (flags & 0x01) != 0
	
	// MTU
	var mtu uint16
	binary.Read(buf, binary.BigEndian, &mtu)
	caps.MTU = int(mtu)
	
	// Version string
	var versionLen byte
	binary.Read(buf, binary.BigEndian, &versionLen)
	versionBytes := make([]byte, versionLen)
	buf.Read(versionBytes)
	caps.Version = string(versionBytes)
	
	// Features string
	var featuresLen uint16
	binary.Read(buf, binary.BigEndian, &featuresLen)
	featuresBytes := make([]byte, featuresLen)
	buf.Read(featuresBytes)
	
	// Parse comma-separated features
	if len(featuresBytes) > 0 {
		featuresStr := string(featuresBytes)
		for _, f := range bytes.Split([]byte(featuresStr), []byte(",")) {
			if len(f) > 0 {
				caps.Features = append(caps.Features, string(f))
			}
		}
	}
	
	return caps, nil
}

// CreateAlbumArtQueryPayload creates an album art query payload
func CreateAlbumArtQueryPayload(hash string) []byte {
	return []byte(hash)
}

// CreateHighPriorityConnectionPayload creates a high priority connection request payload
func CreateHighPriorityConnectionPayload(reason string) []byte {
	return []byte(reason)
}

// CreateOptimizeConnectionParamsPayload creates a connection parameters optimization payload
func CreateOptimizeConnectionParamsPayload(intervalMs float32, reason string) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, intervalMs)
	reasonBytes := []byte(reason)
	binary.Write(buf, binary.BigEndian, uint16(len(reasonBytes)))
	buf.Write(reasonBytes)
	return buf.Bytes()
}

// ParseAlbumArtQueryPayload parses an album art query payload
func ParseAlbumArtQueryPayload(data []byte) string {
	return string(data)
}

// ErrorData represents an error message
type ErrorData struct {
	Code    string
	Message string
}

// ParseErrorPayload parses an error payload
func ParseErrorPayload(data []byte) (*ErrorData, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("payload too small for error")
	}
	
	buf := bytes.NewReader(data)
	err := &ErrorData{}
	
	var codeLen byte
	binary.Read(buf, binary.BigEndian, &codeLen)
	codeBytes := make([]byte, codeLen)
	buf.Read(codeBytes)
	err.Code = string(codeBytes)
	
	var msgLen uint16
	binary.Read(buf, binary.BigEndian, &msgLen)
	msgBytes := make([]byte, msgLen)
	buf.Read(msgBytes)
	err.Message = string(msgBytes)
	
	return err, nil
}

// GetMessageTypeString returns a human-readable string for message type
func GetMessageTypeString(msgType uint16) string {
	switch msgType {
	// System messages
	case MsgCapabilities:
		return "Capabilities"
	case MsgTimeSync:
		return "TimeSync"
	case MsgProtocolEnable:
		return "ProtocolEnable"
	case MsgGetCapabilities:
		return "GetCapabilities"
	case MsgEnableBinaryIncremental:
		return "EnableBinaryIncremental"
	case MsgRequestHighPriorityConnection:
		return "RequestHighPriorityConnection"
	case MsgOptimizeConnectionParams:
		return "OptimizeConnectionParams"
		
	// Command messages
	case MsgCmdPlay:
		return "Play"
	case MsgCmdPause:
		return "Pause"
	case MsgCmdNext:
		return "Next"
	case MsgCmdPrevious:
		return "Previous"
	case MsgCmdSeekTo:
		return "SeekTo"
	case MsgCmdSetVolume:
		return "SetVolume"
	case MsgCmdRequestState:
		return "RequestState"
	case MsgCmdAlbumArtQuery:
		return "AlbumArtQuery"
		
	// State messages
	case MsgStateFull:
		return "FullState"
	case MsgStateArtist:
		return "Artist"
	case MsgStateAlbum:
		return "Album"
	case MsgStateTrack:
		return "Track"
	case MsgStatePosition:
		return "Position"
	case MsgStateDuration:
		return "Duration"
	case MsgStatePlayStatus:
		return "PlayStatus"
	case MsgStateVolume:
		return "Volume"
		
	// Album art messages
	case MsgAlbumArtStart:
		return "AlbumArtStart"
	case MsgAlbumArtChunk:
		return "AlbumArtChunk"
	case MsgAlbumArtEnd:
		return "AlbumArtEnd"
		
	// Weather messages
	case MsgWeatherRequest:
		return "WeatherRequest"
	case MsgWeatherStart:
		return "WeatherStart"
	case MsgWeatherChunk:
		return "WeatherChunk"
	case MsgWeatherEnd:
		return "WeatherEnd"
		
	// Error messages
	case MsgError:
		return "Error"
		
	default:
		return fmt.Sprintf("Unknown(0x%04X)", msgType)
	}
}