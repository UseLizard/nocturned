package bluetooth

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/usenocturne/nocturned/utils"
)

// WeatherTransferState manages incoming weather data transfer
type WeatherTransferState struct {
	TotalChunks    uint32
	CompressedSize uint32
	Checksum       [32]byte
	ChunksReceived map[uint32][]byte
	StartTime      time.Time
}

// WeatherHandler manages weather data reception and storage
type WeatherHandler struct {
	broadcaster   *utils.WebSocketBroadcaster
	currentTransfer *WeatherTransferState
}

// NewWeatherHandler creates a new weather handler
func NewWeatherHandler(broadcaster *utils.WebSocketBroadcaster) *WeatherHandler {
	return &WeatherHandler{
		broadcaster: broadcaster,
	}
}

// HandleWeatherStart processes weather start messages
func (h *WeatherHandler) HandleWeatherStart(payload []byte) {
	log.Printf("🌤️ Received WeatherStart (%d bytes)", len(payload))
	
	if len(payload) < 40 { // 32 bytes checksum + 4 bytes totalChunks + 4 bytes compressedSize
		log.Printf("WeatherStart payload too short: %d bytes", len(payload))
		return
	}
	
	// Parse the start message payload (from NocturneCompanion WeatherBinaryEncoder)
	// Format: [originalSize:4][compressedSize:4][totalChunks:4][reserved:4][timestampMs:8][checksum:32][mode+location strings]
	if len(payload) < 56 { // 4+4+4+4+8+32 = 56 minimum bytes
		log.Printf("WeatherStart payload too short for full header: %d bytes", len(payload))
		return
	}
	
	// Parse exactly as NocturneCompanion WeatherBinaryEncoder sends it:
	// payloadBuffer.putInt(originalSize)     // bytes 0-3
	// payloadBuffer.putInt(compressedSize)   // bytes 4-7  
	// payloadBuffer.putInt(totalChunks)      // bytes 8-11
	// payloadBuffer.putInt(0)                // bytes 12-15 (reserved)
	// payloadBuffer.putLong(timestampMs)     // bytes 16-23
	// payloadBuffer.put(checksumBytes)       // bytes 24-55 (32 byte SHA-256)
	originalSize := uint32(payload[0])<<24 | uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
	compressedSize := uint32(payload[4])<<24 | uint32(payload[5])<<16 | uint32(payload[6])<<8 | uint32(payload[7])
	totalChunks := uint32(payload[8])<<24 | uint32(payload[9])<<16 | uint32(payload[10])<<8 | uint32(payload[11])
	reserved := uint32(payload[12])<<24 | uint32(payload[13])<<16 | uint32(payload[14])<<8 | uint32(payload[15])
	timestampMs := uint64(payload[16])<<56 | uint64(payload[17])<<48 | uint64(payload[18])<<40 | uint64(payload[19])<<32 | 
	               uint64(payload[20])<<24 | uint64(payload[21])<<16 | uint64(payload[22])<<8 | uint64(payload[23])
	
	var checksum [32]byte
	copy(checksum[:], payload[24:56]) // SHA-256 checksum at bytes 24-55
	
	// Reset transfer state
	h.currentTransfer = &WeatherTransferState{
		TotalChunks:    totalChunks,
		CompressedSize: compressedSize,
		Checksum:       checksum,
		ChunksReceived: make(map[uint32][]byte),
		StartTime:      time.Now(),
	}
	
	log.Printf("🌤️ Weather transfer started: %d chunks, %d->%d bytes compressed, reserved=%d, timestamp=%d, checksum: %x", 
		totalChunks, originalSize, compressedSize, reserved, timestampMs, checksum[:8])
}

// HandleWeatherChunk processes weather chunk messages  
func (h *WeatherHandler) HandleWeatherChunk(chunkIndex uint32, payload []byte) {
	if h.currentTransfer == nil {
		log.Printf("Received WeatherChunk without WeatherStart")
		return
	}
	
	// Store chunk data (payload is the actual chunk data, index comes from messageID)
	h.currentTransfer.ChunksReceived[chunkIndex] = payload
	
	log.Printf("🌤️ Received WeatherChunk %d/%d (%d bytes)", 
		chunkIndex+1, h.currentTransfer.TotalChunks, len(payload))
}

// HandleWeatherEnd processes weather end messages and assembles the final data
func (h *WeatherHandler) HandleWeatherEnd(payload []byte) {
	if h.currentTransfer == nil {
		log.Printf("Received WeatherEnd without WeatherStart")
		return
	}
	
	log.Printf("🌤️ Received WeatherEnd, assembling weather data...")
	
	// Check if we have all chunks
	if len(h.currentTransfer.ChunksReceived) != int(h.currentTransfer.TotalChunks) {
		log.Printf("Missing weather chunks: got %d, expected %d", 
			len(h.currentTransfer.ChunksReceived), h.currentTransfer.TotalChunks)
		h.currentTransfer = nil
		return
	}
	
	// Assemble chunks in order
	var compressedData []byte
	for i := uint32(0); i < h.currentTransfer.TotalChunks; i++ {
		chunk, exists := h.currentTransfer.ChunksReceived[i]
		if !exists {
			log.Printf("Missing weather chunk %d", i)
			h.currentTransfer = nil
			return
		}
		compressedData = append(compressedData, chunk...)
		log.Printf("🔧 Assembled chunk %d: %d bytes (total so far: %d)", i, len(chunk), len(compressedData))
	}
	
	log.Printf("🔧 Final compressed data: %d bytes (expected: %d)", len(compressedData), h.currentTransfer.CompressedSize)
	
	// Verify checksum
	actualChecksum := sha256.Sum256(compressedData)
	if actualChecksum != h.currentTransfer.Checksum {
		log.Printf("Weather checksum mismatch: expected %x, got %x", 
			h.currentTransfer.Checksum[:8], actualChecksum[:8])
		h.currentTransfer = nil
		return
	}
	
	// Decompress data
	jsonData, err := h.decompressGzip(compressedData)
	if err != nil {
		log.Printf("Failed to decompress weather data: %v", err)
		h.currentTransfer = nil
		return
	}
	
	// Parse JSON weather data
	log.Printf("🔧 Attempting to parse JSON data (%d bytes)", len(jsonData))
	log.Printf("🔧 First 100 chars of JSON: %.100s", string(jsonData))
	
	var weatherData map[string]interface{}
	if err := json.Unmarshal(jsonData, &weatherData); err != nil {
		log.Printf("Failed to parse weather JSON: %v", err)
		log.Printf("🔧 Raw JSON data: %q", string(jsonData))
		h.currentTransfer = nil
		return
	}
	
	// Save weather data to storage
	if err := h.saveWeatherData(weatherData); err != nil {
		log.Printf("Failed to save weather data: %v", err)
	} else {
		log.Printf("✅ Weather data saved successfully")
	}
	
	// Broadcast weather update via WebSocket
	h.broadcaster.BroadcastWeatherUpdate(weatherData)
	
	elapsed := time.Since(h.currentTransfer.StartTime)
	log.Printf("🌤️ Weather transfer completed in %v: %d bytes decompressed", 
		elapsed, len(jsonData))
	
	// Clear transfer state
	h.currentTransfer = nil
}

// decompressGzip decompresses gzip-compressed data
func (h *WeatherHandler) decompressGzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer reader.Close()
	
	// Use ioutil.ReadAll for simpler and more reliable decompression
	result, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress gzip data: %v", err)
	}
	
	log.Printf("🌤️ GZIP decompressed: %d bytes -> %d bytes", len(data), len(result))
	return result, nil
}

// saveWeatherData saves weather data to the file system  
func (h *WeatherHandler) saveWeatherData(weatherData map[string]interface{}) error {
	// Determine the mode (hourly/weekly) and save appropriately
	mode, ok := weatherData["mode"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid mode in weather data")
	}
	
	weatherDir := "/var/nocturne/weather"
	var filename string
	
	switch mode {
	case "hourly":
		filename = "hourly_weather.json"
	case "weekly":
		filename = "weekly_weather.json"
	default:
		return fmt.Errorf("unknown weather mode: %s", mode)
	}
	
	filepath := filepath.Join(weatherDir, filename)
	
	// Add timestamp to the weather data
	weatherData["received_at"] = time.Now().Unix()
	
	// Convert back to JSON and save
	jsonData, err := json.MarshalIndent(weatherData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal weather data: %v", err)
	}
	
	// Create directory if it doesn't exist
	if err := os.MkdirAll(weatherDir, 0755); err != nil {
		return fmt.Errorf("failed to create weather directory: %v", err)
	}
	
	if err := os.WriteFile(filepath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write weather file: %v", err)
	}
	
	log.Printf("Weather data saved to %s (%d bytes)", filepath, len(jsonData))
	return nil
}