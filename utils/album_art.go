package utils

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Global in-memory album art storage
var (
	currentAlbumArt      []byte
	currentAlbumArtMutex sync.RWMutex
)

// SetCurrentAlbumArt updates the global in-memory album art
func SetCurrentAlbumArt(data []byte) {
	currentAlbumArtMutex.Lock()
	// Make a copy to avoid external modifications
	if data != nil {
		currentAlbumArt = make([]byte, len(data))
		copy(currentAlbumArt, data)
	} else {
		currentAlbumArt = nil
	}
	currentAlbumArtMutex.Unlock()
}

// GetCurrentAlbumArt returns the current in-memory album art
func GetCurrentAlbumArt() []byte {
	currentAlbumArtMutex.RLock()
	defer currentAlbumArtMutex.RUnlock()
	// Return the actual data - the HTTP handler will write it directly
	// This avoids an unnecessary copy
	return currentAlbumArt
}

func CheckAlbumArtExists(checksum string) bool {
	albumArtDir := "/var/nocturne/albumart"
	filePath := filepath.Join(albumArtDir, checksum+".webp")
	if _, err := os.Stat(filePath); err == nil {
		return true
	}
	return false
}

func GetAlbumArtPath(checksum string) string {
	albumArtDir := "/var/nocturne/albumart"
	return filepath.Join(albumArtDir, checksum+".webp")
}

func GenerateAlbumArtHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// GenerateMetadataHash is DEPRECATED - album art now uses integer hashes from companion
// This function is kept for potential non-album-art use cases but should not be used for album art
func GenerateMetadataHash(artist, album string) string {
	// Normalize strings: lowercase, trim spaces (matching Android)
	normalizedArtist := strings.ToLower(strings.TrimSpace(artist))
	normalizedAlbum := strings.ToLower(strings.TrimSpace(album))
	
	// Combine artist and album with hyphen (matching Android)
	combined := fmt.Sprintf("%s-%s", normalizedArtist, normalizedAlbum)
	
	// Generate MD5 hash
	hash := md5.Sum([]byte(combined))
	return hex.EncodeToString(hash[:])
}

func SaveAlbumArt(checksum string, data []byte) (string, error) {
	albumArtDir := "/var/nocturne/albumart"
	if err := os.MkdirAll(albumArtDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create album art directory: %w", err)
	}
	filePath := GetAlbumArtPath(checksum)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to save album art: %w", err)
	}
	return filePath, nil
}