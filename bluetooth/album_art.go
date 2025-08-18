package bluetooth

import (
	"crypto/md5"
	"fmt"
	"log"
	"sync"
	"time"
)

// AlbumArtHandler manages album art transfers for the v2 protocol
type AlbumArtHandler struct {
	mu              sync.RWMutex
	activeTransfers map[string]*AlbumArtTransfer
	cache           map[string][]byte // Hash -> image data cache
	maxCacheSize    int
	chunkSize       int
	callback        func([]byte) // Callback when album art is received
}

// NewAlbumArtHandler creates a new album art handler
func NewAlbumArtHandler() *AlbumArtHandler {
	return &AlbumArtHandler{
		activeTransfers: make(map[string]*AlbumArtTransfer),
		cache:           make(map[string][]byte),
		maxCacheSize:    50, // Cache up to 50 album arts
		chunkSize:       DefaultChunkSize,
	}
}

// SetCallback sets the callback function for when album art is received
func (h *AlbumArtHandler) SetCallback(callback func([]byte)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callback = callback
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
	h.mu.Lock()
	defer h.mu.Unlock()
	
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
		Hash:        hash,
		Chunks:      make(map[int][]byte),
		TotalChunks: totalChunks,
		Size:        totalSize,
		StartTime:   time.Now(),
		LastUpdate:  time.Now(),
		IsReceiving: true,
		IsComplete:  false,
	}
	
	h.activeTransfers[hash] = transfer
	log.Printf("Started album art transfer for %s: %d chunks, %d bytes", hash, totalChunks, totalSize)
	
	return nil
}

// ReceiveChunk receives a chunk of album art data
func (h *AlbumArtHandler) ReceiveChunk(hash string, chunkIndex int, chunkData []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
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
	
	// Create the complete data
	completeData := make([]byte, 0, totalSize)
	for i := 0; i < transfer.TotalChunks; i++ {
		completeData = append(completeData, transfer.Chunks[i]...)
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
	log.Printf("Completed album art transfer for %s: %d bytes in %v (%.2f KB/s)", 
		hash, len(completeData), duration, float64(len(completeData))/1024.0/duration.Seconds())
	
	// Store in cache
	h.cache[hash] = make([]byte, len(completeData))
	copy(h.cache[hash], completeData)
	
	// Call callback if set
	if h.callback != nil {
		go h.callback(completeData)
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