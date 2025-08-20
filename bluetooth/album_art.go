package bluetooth

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

// AlbumArtTransferStats contains statistics about a completed transfer
type AlbumArtTransferStats struct {
	Hash         string
	Size         int
	Chunks       int
	Duration     time.Duration
	SpeedKBps    float64
	SpeedMbps    float64
	StartTime    time.Time
	EndTime      time.Time
}

// AlbumArtHandler manages album art transfers for the v2 protocol
type AlbumArtHandler struct {
	mu              sync.RWMutex
	activeTransfers map[string]*AlbumArtTransfer
	cache           map[string][]byte // Hash -> image data cache
	maxCacheSize    int
	chunkSize       int
	callback        func([]byte, *AlbumArtTransferStats) // Callback when album art is received
	saveCallback    func(string, []byte) error // Callback to save album art to file (hash, data)
	enabled         bool // Whether album art transfers are enabled
}

// NewAlbumArtHandler creates a new album art handler
func NewAlbumArtHandler() *AlbumArtHandler {
	return &AlbumArtHandler{
		activeTransfers: make(map[string]*AlbumArtTransfer),
		cache:           make(map[string][]byte),
		maxCacheSize:    50, // Cache up to 50 album arts
		chunkSize:       DefaultChunkSize,
		enabled:         true, // Enabled by default for backwards compatibility
	}
}

// SetCallback sets the callback function for when album art is received
func (h *AlbumArtHandler) SetCallback(callback func([]byte, *AlbumArtTransferStats)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callback = callback
}

// SetSaveCallback sets the callback function to save album art to file
func (h *AlbumArtHandler) SetSaveCallback(saveCallback func(string, []byte) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.saveCallback = saveCallback
}

// SetEnabled enables or disables album art transfers
func (h *AlbumArtHandler) SetEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enabled = enabled
	if !enabled {
		log.Println("Album art transfers disabled - will ignore incoming transfers")
	} else {
		log.Println("Album art transfers enabled")
	}
}

// IsEnabled returns whether album art transfers are enabled
func (h *AlbumArtHandler) IsEnabled() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.enabled
}

// SetChunkSize sets the chunk size for album art transfers
func (h *AlbumArtHandler) SetChunkSize(size int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if size < MinChunkSize {
		size = MinChunkSize
	} else if size > MaxChunkSize {
		size = MaxChunkSize
	}
	
	h.chunkSize = size
}

// GetFromCache retrieves album art from cache by hash
func (h *AlbumArtHandler) GetFromCache(hash string) ([]byte, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	data, exists := h.cache[hash]
	if exists {
		// Return a copy to prevent modification
		result := make([]byte, len(data))
		copy(result, data)
		return result, true
	}
	
	return nil, false
}

// StoreInCache stores album art in cache
func (h *AlbumArtHandler) StoreInCache(hash string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	// Check if cache is full
	if len(h.cache) >= h.maxCacheSize {
		// Remove a random entry (simple eviction strategy)
		for k := range h.cache {
			delete(h.cache, k)
			break
		}
	}
	
	// Store a copy to prevent external modification
	cached := make([]byte, len(data))
	copy(cached, data)
	h.cache[hash] = cached
}

// StartTransfer starts an album art transfer
func (h *AlbumArtHandler) StartTransfer(hash string, totalChunks int, totalSize int) error {
	return h.StartTransferWithCompression(hash, totalChunks, totalSize, 0, false)
}

// StartTransferWithCompression starts an album art transfer with compression support
func (h *AlbumArtHandler) StartTransferWithCompression(hash string, totalChunks int, totalSize int, compressedSize int, isGzipCompressed bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	// Check if album art transfers are enabled
	if !h.enabled {
		log.Printf("Album art transfers disabled - ignoring transfer for %s", hash[:16]+"...")
		return nil
	}
	
	// Check if we already have this album art in cache
	if _, exists := h.cache[hash]; exists {
		log.Printf("Album art %s already in cache, skipping transfer", hash)
		return nil
	}
	
	// Check if transfer is already in progress
	if transfer, exists := h.activeTransfers[hash]; exists {
		if transfer.IsReceiving {
			log.Printf("Album art transfer %s already in progress", hash)
			return nil
		}
	}
	
	transfer := &AlbumArtTransfer{
		Hash:             hash,
		Chunks:           make(map[int][]byte),
		TotalChunks:      totalChunks,
		Size:             totalSize,
		CompressedSize:   compressedSize,
		IsGzipCompressed: isGzipCompressed,
		StartTime:        time.Now(),
		LastUpdate:       time.Now(),
		IsReceiving:      true,
		IsComplete:       false,
	}
	
	h.activeTransfers[hash] = transfer
	log.Printf("🎨 ═══════════════════════════════════════════════════════════════")
	log.Printf("🎨 ALBUM ART TRANSFER STARTED")
	log.Printf("🎨 ═══════════════════════════════════════════════════════════════")
	log.Printf("🎨 Hash: %s", hash[:16]+"...")
	log.Printf("🎨 Expected Size: %d bytes (%.2f KB)", totalSize, float64(totalSize)/1024.0)
	if isGzipCompressed && compressedSize > 0 {
		compressionRatio := float64(totalSize) / float64(compressedSize)
		log.Printf("🎨 Compressed Size: %d bytes (%.2f KB)", compressedSize, float64(compressedSize)/1024.0)
		log.Printf("🎨 Compression Ratio: %.2fx", compressionRatio)
		log.Printf("🎨 Compression: GZIP")
	} else {
		log.Printf("🎨 Compression: None")
	}
	log.Printf("🎨 Expected Chunks: %d", totalChunks)
	log.Printf("🎨 Start Time: %s", transfer.StartTime.Format("15:04:05.000"))
	log.Printf("🎨 ═══════════════════════════════════════════════════════════════")
	
	return nil
}

// ReceiveChunk receives a chunk of album art data
func (h *AlbumArtHandler) ReceiveChunk(hash string, chunkIndex int, chunkData []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	// Check if album art transfers are enabled
	if !h.enabled {
		log.Printf("Album art transfers disabled - ignoring chunk for %s", hash[:16]+"...")
		return nil
	}
	
	transfer, exists := h.activeTransfers[hash]
	if !exists {
		return fmt.Errorf("no active transfer for hash %s", hash)
	}
	
	if !transfer.IsReceiving {
		return fmt.Errorf("transfer for hash %s is not active", hash)
	}
	
	// Store the chunk
	transfer.Chunks[chunkIndex] = make([]byte, len(chunkData))
	copy(transfer.Chunks[chunkIndex], chunkData)
	transfer.LastUpdate = time.Now()
	
	log.Printf("Received chunk %d/%d for album art %s (%d bytes)", 
		chunkIndex+1, transfer.TotalChunks, hash, len(chunkData))
	
	// Check if we have all chunks
	if len(transfer.Chunks) == transfer.TotalChunks {
		return h.completeTransfer(hash)
	}
	
	return nil
}

// completeTransfer assembles all chunks and completes the transfer
// Must be called with mutex locked
func (h *AlbumArtHandler) completeTransfer(hash string) error {
	transfer := h.activeTransfers[hash]
	
	// Assemble all chunks in order
	totalSize := 0
	for i := 0; i < transfer.TotalChunks; i++ {
		chunk, exists := transfer.Chunks[i]
		if !exists {
			return fmt.Errorf("missing chunk %d for hash %s", i, hash)
		}
		totalSize += len(chunk)
	}
	
	// Create the complete data (this is still compressed if compression was used)
	compressedData := make([]byte, 0, totalSize)
	for i := 0; i < transfer.TotalChunks; i++ {
		compressedData = append(compressedData, transfer.Chunks[i]...)
	}
	
	// Decompress if needed
	var completeData []byte
	var err error
	if transfer.IsGzipCompressed {
		log.Printf("🎨 Decompressing GZIP data (%d bytes -> expected %d bytes)", len(compressedData), transfer.Size)
		completeData, err = h.decompressGzip(compressedData)
		if err != nil {
			return fmt.Errorf("failed to decompress GZIP data: %w", err)
		}
		log.Printf("🎨 Decompression complete: %d bytes -> %d bytes", len(compressedData), len(completeData))
	} else {
		completeData = compressedData
	}
	
	// Verify hash
	actualHash := fmt.Sprintf("%x", md5.Sum(completeData))
	if actualHash != hash {
		log.Printf("Album art hash mismatch: expected %s, got %s", hash, actualHash)
		// Continue anyway, as the hash might be from a different algorithm
	}
	
	transfer.Data = completeData
	transfer.IsReceiving = false
	transfer.IsComplete = true
	
	duration := time.Since(transfer.StartTime)
	speedKBps := float64(len(completeData)) / 1024.0 / duration.Seconds()
	speedMbps := (float64(len(completeData)) * 8) / (1024.0 * 1024.0) / duration.Seconds()
	
	log.Printf("🎨 ═══════════════════════════════════════════════════════════════")
	log.Printf("🎨 ALBUM ART TRANSFER COMPLETED")
	log.Printf("🎨 ═══════════════════════════════════════════════════════════════")
	log.Printf("🎨 Hash: %s", hash[:16]+"...")
	log.Printf("🎨 Size: %d bytes (%.2f KB)", len(completeData), float64(len(completeData))/1024.0)
	log.Printf("🎨 Chunks: %d", transfer.TotalChunks)
	log.Printf("🎨 Duration: %v", duration)
	log.Printf("🎨 Speed: %.2f KB/s (%.2f Mbps)", speedKBps, speedMbps)
	log.Printf("🎨 ═══════════════════════════════════════════════════════════════")
	
	// Store in cache
	h.cache[hash] = make([]byte, len(completeData))
	copy(h.cache[hash], completeData)
	
	// Save to file if callback is set
	if h.saveCallback != nil {
		if err := h.saveCallback(hash, completeData); err != nil {
			log.Printf("Failed to save album art to file: %v", err)
		}
	}
	
	// Call callback if set
	if h.callback != nil {
		endTime := time.Now()
		stats := &AlbumArtTransferStats{
			Hash:      hash,
			Size:      len(completeData),
			Chunks:    transfer.TotalChunks,
			Duration:  duration,
			SpeedKBps: speedKBps,
			SpeedMbps: speedMbps,
			StartTime: transfer.StartTime,
			EndTime:   endTime,
		}
		h.callback(completeData, stats)
	}
	
	// Clean up transfer
	delete(h.activeTransfers, hash)
	
	return nil
}

// SendAlbumArt sends album art data in chunks
func (h *AlbumArtHandler) SendAlbumArt(hash string, data []byte, sendFunc func([]byte) error) error {
	if len(data) == 0 {
		return fmt.Errorf("no data to send")
	}
	
	// Calculate number of chunks
	totalChunks := (len(data) + h.chunkSize - 1) / h.chunkSize
	
	// Send start message
	startMsg := EncodeAlbumArtStart(uint16(totalChunks), hash)
	if err := sendFunc(startMsg); err != nil {
		return fmt.Errorf("failed to send album art start: %w", err)
	}
	
	log.Printf("Sending album art %s: %d bytes in %d chunks", hash, len(data), totalChunks)
	
	// Send chunks with rate limiting
	for i := 0; i < totalChunks; i++ {
		start := i * h.chunkSize
		end := start + h.chunkSize
		if end > len(data) {
			end = len(data)
		}
		
		chunkData := data[start:end]
		chunkMsg := EncodeAlbumArtChunk(uint16(i), chunkData)
		
		if err := sendFunc(chunkMsg); err != nil {
			return fmt.Errorf("failed to send chunk %d: %w", i, err)
		}
		
		log.Printf("Sent album art chunk %d/%d (%d bytes)", i+1, totalChunks, len(chunkData))
		
		// Rate limiting delay between chunks
		if i < totalChunks-1 {
			time.Sleep(DefaultRateLimitConfig().ChunkDelay)
		}
	}
	
	log.Printf("Album art transfer complete for %s", hash)
	return nil
}

// GetActiveTransfers returns information about active transfers
func (h *AlbumArtHandler) GetActiveTransfers() map[string]*AlbumArtTransfer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	result := make(map[string]*AlbumArtTransfer)
	for hash, transfer := range h.activeTransfers {
		// Create a copy to prevent external modification
		transferCopy := &AlbumArtTransfer{
			Hash:        transfer.Hash,
			TotalChunks: transfer.TotalChunks,
			Size:        transfer.Size,
			StartTime:   transfer.StartTime,
			LastUpdate:  transfer.LastUpdate,
			IsComplete:  transfer.IsComplete,
			IsReceiving: transfer.IsReceiving,
		}
		transferCopy.Chunks = make(map[int][]byte)
		for k, v := range transfer.Chunks {
			transferCopy.Chunks[k] = make([]byte, len(v))
			copy(transferCopy.Chunks[k], v)
		}
		result[hash] = transferCopy
	}
	
	return result
}

// CleanupStaleTransfers removes transfers that have been inactive for too long
func (h *AlbumArtHandler) CleanupStaleTransfers(maxAge time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	now := time.Now()
	for hash, transfer := range h.activeTransfers {
		if now.Sub(transfer.LastUpdate) > maxAge {
			log.Printf("Cleaning up stale album art transfer for %s (inactive for %v)", 
				hash, now.Sub(transfer.LastUpdate))
			delete(h.activeTransfers, hash)
		}
	}
}

// GetCacheStats returns cache statistics
func (h *AlbumArtHandler) GetCacheStats() (entries int, totalSize int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	entries = len(h.cache)
	for _, data := range h.cache {
		totalSize += len(data)
	}
	
	return entries, totalSize
}

// ClearCache clears all cached album art
func (h *AlbumArtHandler) ClearCache() {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	h.cache = make(map[string][]byte)
	log.Println("Album art cache cleared")
}

// GetTransferProgress returns the progress of an active transfer
func (h *AlbumArtHandler) GetTransferProgress(hash string) (received int, total int, exists bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	transfer, exists := h.activeTransfers[hash]
	if !exists {
		return 0, 0, false
	}
	
	return len(transfer.Chunks), transfer.TotalChunks, true
}

// decompressGzip decompresses GZIP-compressed data
func (h *AlbumArtHandler) decompressGzip(compressedData []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressedData))
	if err != nil {
		return nil, fmt.Errorf("failed to create GZIP reader: %w", err)
	}
	defer reader.Close()
	
	decompressedData, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read decompressed data: %w", err)
	}
	
	return decompressedData, nil
}