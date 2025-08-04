package utils

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CalculateMD5 calculates the MD5 checksum of the given data
func CalculateMD5(data []byte) string {
	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:])
}

// CalculateSHA256 calculates the SHA-256 checksum of the given data
func CalculateSHA256(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// SaveAlbumArt saves album art data to a file with retry logic and graceful degradation
func SaveAlbumArt(data []byte, filename string) error {
	// For /tmp files, use simpler logic as /tmp is always available
	if strings.HasPrefix(filename, "/tmp/") {
		return saveWithRetry(data, filename, 3)
	}
	
	// For /var files, check if directory is writable first
	dir := filepath.Dir(filename)
	
	// Test if parent directory exists and is writable
	testFile := filepath.Join(dir, ".test_write")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		// Directory not ready, try to create it with retries
		for attempt := 0; attempt < 3; attempt++ {
			if err := os.MkdirAll(dir, 0755); err == nil {
				break
			}
			if attempt < 2 {
				time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
			}
		}
		
		// Test again
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			// Still can't write - gracefully degrade
			return nil // Don't fail the operation
		}
	}
	os.Remove(testFile)
	
	// Directory is writable, save with retry
	return saveWithRetry(data, filename, 3)
}

// saveWithRetry attempts to save data with retries
func saveWithRetry(data []byte, filename string, maxRetries int) error {
	var lastErr error
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Write to temporary file first
		tmpFile := filename + ".tmp"
		if err := os.WriteFile(tmpFile, data, 0644); err == nil {
			// Atomic rename
			if err := os.Rename(tmpFile, filename); err == nil {
				return nil
			} else {
				os.Remove(tmpFile)
				lastErr = err
			}
		} else {
			lastErr = err
		}
		
		if attempt < maxRetries-1 {
			time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
		}
	}
	
	return fmt.Errorf("failed after %d attempts: %v", maxRetries, lastErr)
}

// GenerateAlbumArtHash generates a hash from artist and album names
func GenerateAlbumArtHash(artist, album string) string {
	// Normalize strings: lowercase, trim spaces
	artist = strings.TrimSpace(strings.ToLower(artist))
	album = strings.TrimSpace(strings.ToLower(album))
	
	// Combine artist and album
	combined := artist + "-" + album
	
	// Generate MD5 hash
	hash := md5.Sum([]byte(combined))
	return hex.EncodeToString(hash[:])
}

// GetAlbumArtPath returns the path for cached album art
func GetAlbumArtPath(artist, album string) string {
	hash := GenerateAlbumArtHash(artist, album)
	return filepath.Join("/var/nocturne/albumart", hash + ".jpg")
}

// CheckAlbumArtExists checks if album art is already cached
func CheckAlbumArtExists(artist, album string) bool {
	path := GetAlbumArtPath(artist, album)
	_, err := os.Stat(path)
	return err == nil
}