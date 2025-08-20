package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// V1 Compatibility Handlers for nocturne-ui integration

// handleV1MediaPlay handles v1 media play requests
func (s *ServerV2) handleV1MediaPlay(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.SendMediaCommand("play", nil); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send play command", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "play command sent",
	})
}

// handleV1MediaPause handles v1 media pause requests
func (s *ServerV2) handleV1MediaPause(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.SendMediaCommand("pause", nil); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send pause command", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "pause command sent",
	})
}

// handleV1MediaNext handles v1 media next requests
func (s *ServerV2) handleV1MediaNext(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.SendMediaCommand("next", nil); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send next command", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "next command sent",
	})
}

// handleV1MediaPrevious handles v1 media previous requests
func (s *ServerV2) handleV1MediaPrevious(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.SendMediaCommand("previous", nil); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send previous command", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "previous command sent",
	})
}

// handleV1MediaVolume handles v1 volume requests like /media/volume/75
func (s *ServerV2) handleV1MediaVolume(w http.ResponseWriter, r *http.Request) {
	// Extract volume from URL path
	path := strings.TrimPrefix(r.URL.Path, "/media/volume/")
	if path == "" {
		writeErrorResponse(w, http.StatusBadRequest, "Volume level required", nil)
		return
	}
	
	// Parse volume level
	volume, err := strconv.Atoi(path)
	if err != nil || volume < 0 || volume > 100 {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid volume level", err)
		return
	}
	
	// Send volume command
	if err := s.manager.SendMediaCommand("volume", map[string]interface{}{
		"volume": volume,
	}); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send volume command", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "volume set to " + path,
		"volume": volume,
	})
}

// handleV1MediaSeek handles v1 seek requests like /media/seek/60
func (s *ServerV2) handleV1MediaSeek(w http.ResponseWriter, r *http.Request) {
	// Extract seek position from URL path
	path := strings.TrimPrefix(r.URL.Path, "/media/seek/")
	if path == "" {
		writeErrorResponse(w, http.StatusBadRequest, "Seek position required", nil)
		return
	}
	
	// Parse seek position (assume it's in seconds, convert to milliseconds)
	positionSec, err := strconv.ParseInt(path, 10, 64)
	if err != nil || positionSec < 0 {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid seek position", err)
		return
	}
	
	positionMs := positionSec * 1000 // Convert to milliseconds
	
	// Send seek command
	if err := s.manager.SendMediaCommand("seek", map[string]interface{}{
		"position": positionMs,
	}); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send seek command", err)
		return
	}
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "seeked to " + path,
		"position": positionSec,
	})
}

// handleV1Info handles v1 info requests
func (s *ServerV2) handleV1Info(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"version":     "2.0.0-compat",
		"service":     "nocturned-v2",
		"bluetooth":   "active",
		"uptime":      time.Now().Unix(),
		"system_info": s.manager.GetCurrentState(),
	}
	
	writeJSONResponse(w, http.StatusOK, info)
}

// handleV1Date handles v1 date requests
func (s *ServerV2) handleV1Date(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	
	response := map[string]interface{}{
		"timestamp":    now.Unix(),
		"iso":         now.Format(time.RFC3339),
		"readable":    now.Format("2006-01-02 15:04:05"),
		"time":        now.Format("15:04"), // Add time field for UI compatibility
		"timezone":    now.Location().String(),
		"unix_millis": now.UnixMilli(),
	}
	
	writeJSONResponse(w, http.StatusOK, response)
}

// handleV1DateTimezone handles v1 timezone setting requests
func (s *ServerV2) handleV1DateTimezone(w http.ResponseWriter, r *http.Request) {
	// This endpoint is called by nocturne-ui but nocturned-v2 doesn't actually need to handle timezone changes
	// as the system manages its own time. We'll just acknowledge the request.
	
	response := map[string]interface{}{
		"status":    "acknowledged",
		"message":   "Timezone setting not required for nocturned-v2",
		"timestamp": time.Now().Unix(),
	}
	
	writeJSONResponse(w, http.StatusOK, response)
}

// handleV1MediaStatus handles v1 media status requests (backward compatibility)
func (s *ServerV2) handleV1MediaStatus(w http.ResponseWriter, r *http.Request) {
	mediaState := s.manager.GetCurrentMediaState()
	
	response := map[string]interface{}{
		"connected": mediaState != nil,
	}
	
	if mediaState != nil {
		response["state"] = map[string]interface{}{
			"artist":         mediaState.Artist,
			"album":          mediaState.Album,
			"track":          mediaState.Track,
			"duration_ms":    mediaState.DurationMs,
			"position_ms":    mediaState.PositionMs,
			"is_playing":     mediaState.IsPlaying,
			"volume_percent": mediaState.Volume,
			"album_hash":     mediaState.AlbumHash,
		}
	}
	
	writeJSONResponse(w, http.StatusOK, response)
}

// handleLegacyAlbumArt handles V1 album art requests for backward compatibility
func (s *ServerV2) handleLegacyAlbumArt(w http.ResponseWriter, r *http.Request) {
	// Get the current media state to find the current album hash
	mediaState := s.manager.GetCurrentMediaState()
	
	if mediaState == nil || mediaState.AlbumHash == 0 {
		// No current media or no album hash, return 404
		http.NotFound(w, r)
		return
	}
	
	// Convert album hash to string for cache lookup
	hashStr := fmt.Sprintf("%d", mediaState.AlbumHash)
	
	// Try to get album art from cache
	handler := s.manager.GetAlbumArtHandler()
	data, exists := handler.GetFromCache(hashStr)
	
	if !exists {
		// Try to load from file system
		albumArtPath := fmt.Sprintf("/var/album_art/%s.webp", hashStr)
		if fileData, err := os.ReadFile(albumArtPath); err == nil {
			// Found in file system, load into cache
			handler.StoreInCache(hashStr, fileData)
			data = fileData
			exists = true
		}
	}
	
	if !exists {
		// Album art not available
		http.NotFound(w, r)
		return
	}
	
	// Determine content type (assume WebP for now)
	contentType := "image/webp"
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xD8 {
			contentType = "image/jpeg"
		} else if len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
			contentType = "image/png"
		}
	}
	
	// Set headers
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour
	
	// Write image data
	if _, err := w.Write(data); err != nil {
		log.Printf("Failed to write legacy album art data: %v", err)
	}
}