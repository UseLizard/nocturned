package bluetooth

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Binary protocol constants
const (
	ProtocolVersion    = 1
	BinaryHeaderSize   = 16
	
	// Message types
	MsgAlbumArtStart  uint16 = 0x0100
	MsgAlbumArtChunk  uint16 = 0x0101
	MsgAlbumArtEnd    uint16 = 0x0102
	MsgProtocolInfo   uint16 = 0x0001
	
	// Test message types
	MsgTestAlbumArtStart  uint16 = 0x0200
	MsgTestAlbumArtChunk  uint16 = 0x0201
	MsgTestAlbumArtEnd    uint16 = 0x0202
	
	// Incremental state update message types
	MsgStateArtist      uint16 = 0x0300 // Deprecated - use MsgStateArtistAlbum
	MsgStateAlbum       uint16 = 0x0301 // Deprecated - use MsgStateArtistAlbum
	MsgStateTrack       uint16 = 0x0302
	MsgStatePosition    uint16 = 0x0303
	MsgStateDuration    uint16 = 0x0304
	MsgStatePlayState   uint16 = 0x0305
	MsgStateVolume      uint16 = 0x0306
	MsgStateFull        uint16 = 0x0307
	MsgStateArtistAlbum uint16 = 0x0308 // Combined artist+album update
)

// BinaryHeader represents the 16-byte header for all binary messages
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