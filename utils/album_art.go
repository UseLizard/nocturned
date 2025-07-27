package utils

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// SaveAlbumArt saves album art data to a file
func SaveAlbumArt(data []byte, filename string) error {
	// Ensure directory exists
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}
	
	// Write file
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}
	
	return nil
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
	return filepath.Join("/data/etc/nocturne/albumart", hash + ".webp")
}

// CheckAlbumArtExists checks if album art is already cached
func CheckAlbumArtExists(artist, album string) bool {
	path := GetAlbumArtPath(artist, album)
	_, err := os.Stat(path)
	return err == nil
}