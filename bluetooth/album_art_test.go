package bluetooth

import (
	"crypto/md5"
	"fmt"
	"testing"
	"time"
)

func TestAlbumArtHandler(t *testing.T) {
	handler := NewAlbumArtHandler()
	
	// Test basic cache operations
	testData := []byte("test album art data")
	testHash := fmt.Sprintf("%x", md5.Sum(testData))
	
	// Store in cache
	handler.StoreInCache(testHash, testData)
	
	// Retrieve from cache
	retrieved, exists := handler.GetFromCache(testHash)
	if !exists {
		t.Error("Expected album art to exist in cache")
	}
	
	if string(retrieved) != string(testData) {
		t.Errorf("Expected data '%s', got '%s'", string(testData), string(retrieved))
	}
	
	// Test cache stats
	entries, totalSize := handler.GetCacheStats()
	if entries != 1 {
		t.Errorf("Expected 1 cache entry, got %d", entries)
	}
	
	if totalSize != len(testData) {
		t.Errorf("Expected cache size %d, got %d", len(testData), totalSize)
	}
	
	// Test cache clearing
	handler.ClearCache()
	
	entries, totalSize = handler.GetCacheStats()
	if entries != 0 || totalSize != 0 {
		t.Error("Expected cache to be empty after clearing")
	}
}

func TestAlbumArtTransfer(t *testing.T) {
	handler := NewAlbumArtHandler()
	
	// Test data
	testData := []byte("This is test album art data for chunked transfer testing")
	testHash := fmt.Sprintf("%x", md5.Sum(testData))
	
	// Set up callback to capture received data
	var receivedData []byte
	handler.SetCallback(func(data []byte) {
		receivedData = data
	})
	
	// Simulate chunked transfer
	chunkSize := 10
	totalChunks := (len(testData) + chunkSize - 1) / chunkSize
	
	// Start transfer
	err := handler.StartTransfer(testHash, totalChunks, len(testData))
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}
	
	// Send chunks
	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(testData) {
			end = len(testData)
		}
		
		chunk := testData[start:end]
		err := handler.ReceiveChunk(testHash, i, chunk)
		if err != nil {
			t.Fatalf("Failed to receive chunk %d: %v", i, err)
		}
	}
	
	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)
	
	// Check if data was received correctly
	if receivedData == nil {
		t.Error("Expected callback to be called with received data")
	}
	
	if string(receivedData) != string(testData) {
		t.Errorf("Expected received data '%s', got '%s'", string(testData), string(receivedData))
	}
	
	// Check if data is in cache
	cached, exists := handler.GetFromCache(testHash)
	if !exists {
		t.Error("Expected album art to be in cache after transfer")
	}
	
	if string(cached) != string(testData) {
		t.Errorf("Expected cached data '%s', got '%s'", string(testData), string(cached))
	}
	
	// Check transfer progress (should be complete)
	_, _, transferExists := handler.GetTransferProgress(testHash)
	if transferExists {
		t.Error("Expected transfer to be cleaned up after completion")
	}
	
	// Before completion, progress should have been tracked
	activeTransfers := handler.GetActiveTransfers()
	if len(activeTransfers) != 0 {
		t.Errorf("Expected no active transfers, got %d", len(activeTransfers))
	}
}

func TestAlbumArtTransferProgress(t *testing.T) {
	handler := NewAlbumArtHandler()
	
	testHash := "testhash123"
	totalChunks := 5
	
	// Start transfer
	err := handler.StartTransfer(testHash, totalChunks, 100)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}
	
	// Send some chunks
	for i := 0; i < 3; i++ {
		chunk := []byte(fmt.Sprintf("chunk%d", i))
		err := handler.ReceiveChunk(testHash, i, chunk)
		if err != nil {
			t.Fatalf("Failed to receive chunk %d: %v", i, err)
		}
	}
	
	// Check progress
	received, total, exists := handler.GetTransferProgress(testHash)
	if !exists {
		t.Error("Expected transfer to exist")
	}
	
	if received != 3 {
		t.Errorf("Expected 3 chunks received, got %d", received)
	}
	
	if total != totalChunks {
		t.Errorf("Expected %d total chunks, got %d", totalChunks, total)
	}
	
	// Check active transfers
	activeTransfers := handler.GetActiveTransfers()
	if len(activeTransfers) != 1 {
		t.Errorf("Expected 1 active transfer, got %d", len(activeTransfers))
	}
	
	transfer, exists := activeTransfers[testHash]
	if !exists {
		t.Error("Expected transfer to exist in active transfers")
	}
	
	if !transfer.IsReceiving {
		t.Error("Expected transfer to be receiving")
	}
	
	if transfer.IsComplete {
		t.Error("Expected transfer to not be complete")
	}
}

func TestAlbumArtSendFunction(t *testing.T) {
	handler := NewAlbumArtHandler()
	
	testData := []byte("Test album art data for sending")
	testHash := fmt.Sprintf("%x", md5.Sum(testData))
	
	// Track sent messages
	var sentMessages [][]byte
	sendFunc := func(msg []byte) error {
		sentMessages = append(sentMessages, msg)
		return nil
	}
	
	// Send album art
	err := handler.SendAlbumArt(testHash, testData, sendFunc)
	if err != nil {
		t.Fatalf("Failed to send album art: %v", err)
	}
	
	// Should have at least 2 messages (start + at least 1 chunk)
	if len(sentMessages) < 2 {
		t.Errorf("Expected at least 2 messages, got %d", len(sentMessages))
	}
	
	// First message should be album art start
	msgType, payload, err := DecodeMessage(sentMessages[0])
	if err != nil {
		t.Fatalf("Failed to decode start message: %v", err)
	}
	
	if msgType != MsgTypeAlbumArtStart {
		t.Errorf("Expected album art start message type 0x10, got 0x%02x", msgType)
	}
	
	totalChunks, hash, err := DecodeAlbumArtStart(payload)
	if err != nil {
		t.Fatalf("Failed to decode album art start: %v", err)
	}
	
	if hash != testHash {
		t.Errorf("Expected hash '%s', got '%s'", testHash, hash)
	}
	
	// Remaining messages should be chunks
	for i := 1; i < len(sentMessages); i++ {
		msgType, payload, err := DecodeMessage(sentMessages[i])
		if err != nil {
			t.Fatalf("Failed to decode chunk message %d: %v", i, err)
		}
		
		if msgType != MsgTypeAlbumArtChunk {
			t.Errorf("Expected album art chunk message type 0x11, got 0x%02x", msgType)
		}
		
		chunkIndex, chunkData, err := DecodeAlbumArtChunk(payload)
		if err != nil {
			t.Fatalf("Failed to decode album art chunk %d: %v", i, err)
		}
		
		if int(chunkIndex) != i-1 {
			t.Errorf("Expected chunk index %d, got %d", i-1, chunkIndex)
		}
		
		if len(chunkData) == 0 {
			t.Errorf("Expected non-empty chunk data for chunk %d", i-1)
		}
	}
	
	// Verify total chunks matches
	expectedChunks := (len(testData) + DefaultChunkSize - 1) / DefaultChunkSize
	if int(totalChunks) != expectedChunks {
		t.Errorf("Expected %d total chunks, got %d", expectedChunks, totalChunks)
	}
}

func TestAlbumArtCleanupStaleTransfers(t *testing.T) {
	handler := NewAlbumArtHandler()
	
	// Start a transfer
	testHash := "stalehash123"
	err := handler.StartTransfer(testHash, 5, 100)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}
	
	// Verify transfer exists
	activeTransfers := handler.GetActiveTransfers()
	if len(activeTransfers) != 1 {
		t.Errorf("Expected 1 active transfer, got %d", len(activeTransfers))
	}
	
	// Clean up with very short max age (should remove the transfer)
	handler.CleanupStaleTransfers(1 * time.Nanosecond)
	
	// Verify transfer was removed
	activeTransfers = handler.GetActiveTransfers()
	if len(activeTransfers) != 0 {
		t.Errorf("Expected 0 active transfers after cleanup, got %d", len(activeTransfers))
	}
}

func BenchmarkAlbumArtHandler(b *testing.B) {
	handler := NewAlbumArtHandler()
	testData := []byte("Benchmark test data for album art handler performance testing")
	testHash := fmt.Sprintf("%x", md5.Sum(testData))
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.StoreInCache(testHash, testData)
		_, exists := handler.GetFromCache(testHash)
		if !exists {
			b.Error("Expected data to be in cache")
		}
		handler.ClearCache()
	}
}