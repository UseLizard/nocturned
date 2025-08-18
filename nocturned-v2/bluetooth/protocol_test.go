package bluetooth

import (
	"testing"
)

func TestEncodeDecodeMessage(t *testing.T) {
	// Test basic message encoding/decoding
	testPayload := []byte("Hello World")
	encoded := EncodeMessage(0x01, testPayload)
	
	msgType, payload, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatalf("Failed to decode message: %v", err)
	}
	
	if msgType != 0x01 {
		t.Errorf("Expected message type 0x01, got 0x%02x", msgType)
	}
	
	if string(payload) != "Hello World" {
		t.Errorf("Expected payload 'Hello World', got '%s'", string(payload))
	}
}

func TestEncodeDecodePlayState(t *testing.T) {
	// Test play state encoding/decoding
	trackTitle := "Test Track"
	artist := "Test Artist"
	album := "Test Album"
	albumArtHash := "testhash123"
	duration := uint32(180)
	position := uint32(60)
	playState := byte(1)
	
	encoded := EncodePlayStateUpdate(trackTitle, artist, album, albumArtHash, duration, position, playState)
	
	// Decode the message
	msgType, payload, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatalf("Failed to decode message: %v", err)
	}
	
	if msgType != MsgTypePlayStateUpdate {
		t.Errorf("Expected message type 0x01, got 0x%02x", msgType)
	}
	
	// Decode the play state
	decodedTitle, decodedArtist, decodedAlbum, decodedHash, decodedDuration, decodedPosition, decodedState, err := DecodePlayStateUpdate(payload)
	if err != nil {
		t.Fatalf("Failed to decode play state: %v", err)
	}
	
	if decodedTitle != trackTitle {
		t.Errorf("Expected title '%s', got '%s'", trackTitle, decodedTitle)
	}
	
	if decodedArtist != artist {
		t.Errorf("Expected artist '%s', got '%s'", artist, decodedArtist)
	}
	
	if decodedAlbum != album {
		t.Errorf("Expected album '%s', got '%s'", album, decodedAlbum)
	}
	
	if decodedHash != albumArtHash {
		t.Errorf("Expected hash '%s', got '%s'", albumArtHash, decodedHash)
	}
	
	if decodedDuration != duration {
		t.Errorf("Expected duration %d, got %d", duration, decodedDuration)
	}
	
	if decodedPosition != position {
		t.Errorf("Expected position %d, got %d", position, decodedPosition)
	}
	
	if decodedState != playState {
		t.Errorf("Expected play state %d, got %d", playState, decodedState)
	}
}

func TestEncodeDecodeVolume(t *testing.T) {
	// Test volume encoding/decoding
	volume := byte(75)
	
	encoded := EncodeVolumeUpdate(volume)
	
	// Decode the message
	msgType, payload, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatalf("Failed to decode message: %v", err)
	}
	
	if msgType != MsgTypeVolumeUpdate {
		t.Errorf("Expected message type 0x02, got 0x%02x", msgType)
	}
	
	// Decode the volume
	decodedVolume, err := DecodeVolumeUpdate(payload)
	if err != nil {
		t.Fatalf("Failed to decode volume: %v", err)
	}
	
	if decodedVolume != volume {
		t.Errorf("Expected volume %d, got %d", volume, decodedVolume)
	}
}

func TestEncodeDecodeAlbumArtStart(t *testing.T) {
	// Test album art start encoding/decoding
	totalChunks := uint16(42)
	albumArtHash := "abc123def456"
	
	encoded := EncodeAlbumArtStart(totalChunks, albumArtHash)
	
	// Decode the message
	msgType, payload, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatalf("Failed to decode message: %v", err)
	}
	
	if msgType != MsgTypeAlbumArtStart {
		t.Errorf("Expected message type 0x10, got 0x%02x", msgType)
	}
	
	// Decode the album art start
	decodedChunks, decodedHash, err := DecodeAlbumArtStart(payload)
	if err != nil {
		t.Fatalf("Failed to decode album art start: %v", err)
	}
	
	if decodedChunks != totalChunks {
		t.Errorf("Expected total chunks %d, got %d", totalChunks, decodedChunks)
	}
	
	if decodedHash != albumArtHash {
		t.Errorf("Expected hash '%s', got '%s'", albumArtHash, decodedHash)
	}
}

func TestEncodeDecodeAlbumArtChunk(t *testing.T) {
	// Test album art chunk encoding/decoding
	chunkIndex := uint16(5)
	chunkData := []byte("chunk data here")
	
	encoded := EncodeAlbumArtChunk(chunkIndex, chunkData)
	
	// Decode the message
	msgType, payload, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatalf("Failed to decode message: %v", err)
	}
	
	if msgType != MsgTypeAlbumArtChunk {
		t.Errorf("Expected message type 0x11, got 0x%02x", msgType)
	}
	
	// Decode the album art chunk
	decodedIndex, decodedData, err := DecodeAlbumArtChunk(payload)
	if err != nil {
		t.Fatalf("Failed to decode album art chunk: %v", err)
	}
	
	if decodedIndex != chunkIndex {
		t.Errorf("Expected chunk index %d, got %d", chunkIndex, decodedIndex)
	}
	
	if string(decodedData) != string(chunkData) {
		t.Errorf("Expected chunk data '%s', got '%s'", string(chunkData), string(decodedData))
	}
}

func TestEncodeDecodeTimeSyncResponse(t *testing.T) {
	// Test time sync response encoding/decoding
	timestamp := uint64(1642680000000) // Example timestamp
	timezone := "America/New_York"
	
	encoded := EncodeTimeSyncResponse(timestamp, timezone)
	
	// Decode the message
	msgType, payload, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatalf("Failed to decode message: %v", err)
	}
	
	if msgType != MsgTypeTimeSyncResponse {
		t.Errorf("Expected message type 0x21, got 0x%02x", msgType)
	}
	
	// Decode the time sync response
	decodedTimestamp, decodedTimezone, err := DecodeTimeSyncResponse(payload)
	if err != nil {
		t.Fatalf("Failed to decode time sync response: %v", err)
	}
	
	if decodedTimestamp != timestamp {
		t.Errorf("Expected timestamp %d, got %d", timestamp, decodedTimestamp)
	}
	
	if decodedTimezone != timezone {
		t.Errorf("Expected timezone '%s', got '%s'", timezone, decodedTimezone)
	}
}

func TestInvalidMessages(t *testing.T) {
	// Test decoding invalid messages
	
	// Too short message
	_, _, err := DecodeMessage([]byte{0x01})
	if err == nil {
		t.Error("Expected error for too short message")
	}
	
	// Message with incorrect payload size
	_, _, err = DecodeMessage([]byte{0x01, 0x00, 0x05, 0x01, 0x02}) // Says 5 bytes, only has 2
	if err == nil {
		t.Error("Expected error for incorrect payload size")
	}
}

func BenchmarkEncodeDecodeMessage(b *testing.B) {
	testPayload := []byte("This is a test payload for benchmarking")
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded := EncodeMessage(0x01, testPayload)
		_, _, err := DecodeMessage(encoded)
		if err != nil {
			b.Fatalf("Decode error: %v", err)
		}
	}
}