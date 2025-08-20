package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/usenocturne/nocturned/utils"
)

// AlbumArtRequest represents an album art upload request
type AlbumArtRequest struct {
	Hash   string `json:"hash"`
	Data   string `json:"data"`   // Base64 encoded image data
	Format string `json:"format"` // Image format (webp, jpeg, png)
}

// AlbumArtUploadRequest represents an album art upload via multipart form
type AlbumArtUploadRequest struct {
	Hash   string
	Format string
	Data   []byte
}

// handleAlbumArt handles album art upload (both JSON and multipart)
func (s *ServerV2) handleAlbumArt(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	
	if contentType == "application/json" {
		s.handleAlbumArtJSON(w, r)
	} else {
		s.handleAlbumArtMultipart(w, r)
	}
}

// handleAlbumArtJSON handles JSON-based album art upload
func (s *ServerV2) handleAlbumArtJSON(w http.ResponseWriter, r *http.Request) {
	var req AlbumArtRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid album art data", err)
		return
	}
	
	// Decode base64 data
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Invalid base64 data", err)
		return
	}
	
	// Hash is required and should be the integer hash from companion
	if req.Hash == "" {
		writeErrorResponse(w, http.StatusBadRequest, "Hash is required (should be integer hash from companion)", nil)
		return
	}
	
	// No hash validation - we trust the companion-provided hash
	
	// Send album art to device
	if err := s.manager.SendAlbumArt(req.Hash, data); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send album art", err)
		return
	}
	
	// Store in cache
	s.manager.GetAlbumArtHandler().StoreInCache(req.Hash, data)
	
	log.Printf("Album art uploaded: %s (%d bytes, %s)", req.Hash, len(data), req.Format)
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":    "uploaded",
		"hash":      req.Hash,
		"size":      len(data),
		"format":    req.Format,
		"timestamp": time.Now().Unix(),
	})
}

// handleAlbumArtMultipart handles multipart form album art upload
func (s *ServerV2) handleAlbumArtMultipart(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form (max 10MB)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "Failed to parse multipart form", err)
		return
	}
	
	// Get the file
	file, header, err := r.FormFile("album_art")
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "No album_art file provided", err)
		return
	}
	defer file.Close()
	
	// Read file data
	data, err := io.ReadAll(file)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to read file data", err)
		return
	}
	
	// Get hash and format from form
	hash := r.FormValue("hash")
	format := r.FormValue("format")
	
	// Detect format from filename if not provided
	if format == "" {
		switch header.Header.Get("Content-Type") {
		case "image/webp":
			format = "webp"
		case "image/jpeg":
			format = "jpeg"
		case "image/png":
			format = "png"
		default:
			format = "unknown"
		}
	}
	
	// Hash is required and should be the integer hash from companion
	if hash == "" {
		writeErrorResponse(w, http.StatusBadRequest, "Hash is required (should be integer hash from companion)", nil)
		return
	}
	
	// No hash validation - we trust the companion-provided hash
	
	// Send album art to device
	if err := s.manager.SendAlbumArt(hash, data); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to send album art", err)
		return
	}
	
	// Store in cache
	s.manager.GetAlbumArtHandler().StoreInCache(hash, data)
	
	log.Printf("Album art uploaded via multipart: %s (%d bytes, %s)", hash, len(data), format)
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":    "uploaded",
		"hash":      hash,
		"size":      len(data),
		"format":    format,
		"filename":  header.Filename,
		"timestamp": time.Now().Unix(),
	})
}

// handleAlbumArtStatus returns status of album art transfers
func (s *ServerV2) handleAlbumArtStatus(w http.ResponseWriter, r *http.Request) {
	handler := s.manager.GetAlbumArtHandler()
	activeTransfers := handler.GetActiveTransfers()
	cacheEntries, cacheSize := handler.GetCacheStats()
	
	response := map[string]interface{}{
		"active_transfers": len(activeTransfers),
		"cache": map[string]interface{}{
			"entries":    cacheEntries,
			"total_size": cacheSize,
		},
		"transfers": make([]map[string]interface{}, 0),
		"timestamp": time.Now().Unix(),
	}
	
	// Add transfer details
	transfers := make([]map[string]interface{}, 0, len(activeTransfers))
	for hash, transfer := range activeTransfers {
		received, total, _ := handler.GetTransferProgress(hash)
		progress := float64(received) / float64(total) * 100
		
		transfers = append(transfers, map[string]interface{}{
			"hash":         hash,
			"progress":     progress,
			"received":     received,
			"total":        total,
			"is_complete":  transfer.IsComplete,
			"is_receiving": transfer.IsReceiving,
			"start_time":   transfer.StartTime.Unix(),
			"last_update":  transfer.LastUpdate.Unix(),
		})
	}
	response["transfers"] = transfers
	
	writeJSONResponse(w, http.StatusOK, response)
}

// handleAlbumArtCache handles album art cache operations
func (s *ServerV2) handleAlbumArtCache(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.handleGetAlbumArtCache(w, r)
	case "DELETE":
		s.handleClearAlbumArtCache(w, r)
	default:
		writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
	}
}

// handleGetAlbumArtCache returns album art cache information
func (s *ServerV2) handleGetAlbumArtCache(w http.ResponseWriter, r *http.Request) {
	handler := s.manager.GetAlbumArtHandler()
	entries, totalSize := handler.GetCacheStats()
	
	// Get hash from query parameter if specified
	hash := r.URL.Query().Get("hash")
	
	response := map[string]interface{}{
		"entries":    entries,
		"total_size": totalSize,
		"timestamp":  time.Now().Unix(),
	}
	
	if hash != "" {
		// Return specific album art data
		if data, exists := handler.GetFromCache(hash); exists {
			// Return as base64 encoded data
			encodedData := base64.StdEncoding.EncodeToString(data)
			response["hash"] = hash
			response["data"] = encodedData
			response["size"] = len(data)
		} else {
			writeErrorResponse(w, http.StatusNotFound, "Album art not found in cache", nil)
			return
		}
	}
	
	writeJSONResponse(w, http.StatusOK, response)
}

// handleClearAlbumArtCache clears the album art cache
func (s *ServerV2) handleClearAlbumArtCache(w http.ResponseWriter, r *http.Request) {
	handler := s.manager.GetAlbumArtHandler()
	
	// Get entries count before clearing
	entriesBefore, sizeBefore := handler.GetCacheStats()
	
	handler.ClearCache()
	
	log.Printf("Album art cache cleared: %d entries, %d bytes", entriesBefore, sizeBefore)
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":          "cleared",
		"entries_removed": entriesBefore,
		"bytes_freed":     sizeBefore,
		"timestamp":       time.Now().Unix(),
	})
}

// handleAlbumArtByHash handles requests for specific album art by hash
func (s *ServerV2) handleAlbumArtByHash(w http.ResponseWriter, r *http.Request) {
	// Get hash from query parameter (set by handleAlbumArtByHashRoute)
	hash := r.URL.Query().Get("hash")
	
	if hash == "" {
		writeErrorResponse(w, http.StatusBadRequest, "Hash parameter required", nil)
		return
	}
	
	handler := s.manager.GetAlbumArtHandler()
	data, exists := handler.GetFromCache(hash)
	
	if !exists {
		writeErrorResponse(w, http.StatusNotFound, "Album art not found", nil)
		return
	}
	
	// Determine content type based on format query parameter or detect from data
	format := r.URL.Query().Get("format")
	contentType := "application/octet-stream"
	
	switch format {
	case "webp":
		contentType = "image/webp"
	case "jpeg", "jpg":
		contentType = "image/jpeg"
	case "png":
		contentType = "image/png"
	default:
		// Try to detect from data
		if len(data) >= 4 {
			if data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 {
				contentType = "image/webp"
			} else if data[0] == 0xFF && data[1] == 0xD8 {
				contentType = "image/jpeg"
			} else if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
				contentType = "image/png"
			}
		}
	}
	
	// Set headers
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour
	
	// Write image data
	if _, err := w.Write(data); err != nil {
		log.Printf("Failed to write album art data: %v", err)
	}
}

// handleLatestAlbum handles requests for the latest saved album art
func (s *ServerV2) handleLatestAlbum(w http.ResponseWriter, r *http.Request) {
	albumArtDir := "/var/album_art"
	
	// Check if directory exists
	if _, err := os.Stat(albumArtDir); os.IsNotExist(err) {
		writeErrorResponse(w, http.StatusNotFound, "Album art directory not found", nil)
		return
	}
	
	// Read directory contents
	files, err := os.ReadDir(albumArtDir)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to read album art directory", err)
		return
	}
	
	// Filter and sort files by modification time
	type fileInfo struct {
		name    string
		modTime time.Time
		size    int64
	}
	
	var albumFiles []fileInfo
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		
		// Get full file info
		fullPath := filepath.Join(albumArtDir, file.Name())
		stat, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		
		// Skip empty files
		if stat.Size() == 0 {
			continue
		}
		
		albumFiles = append(albumFiles, fileInfo{
			name:    file.Name(),
			modTime: stat.ModTime(),
			size:    stat.Size(),
		})
	}
	
	// Check if any files found
	if len(albumFiles) == 0 {
		writeErrorResponse(w, http.StatusNotFound, "No album art files found", nil)
		return
	}
	
	// Sort by modification time (newest first)
	sort.Slice(albumFiles, func(i, j int) bool {
		return albumFiles[i].modTime.After(albumFiles[j].modTime)
	})
	
	// Get the latest file
	latestFile := albumFiles[0]
	latestPath := filepath.Join(albumArtDir, latestFile.name)
	
	// Read the file
	data, err := os.ReadFile(latestPath)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "Failed to read latest album art file", err)
		return
	}
	
	// Determine content type from file extension or data
	contentType := "application/octet-stream"
	ext := filepath.Ext(latestFile.name)
	
	switch ext {
	case ".webp":
		contentType = "image/webp"
	case ".jpg", ".jpeg":
		contentType = "image/jpeg"
	case ".png":
		contentType = "image/png"
	default:
		// Try to detect from data
		if len(data) >= 4 {
			if data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 {
				contentType = "image/webp"
			} else if data[0] == 0xFF && data[1] == 0xD8 {
				contentType = "image/jpeg"
			} else if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
				contentType = "image/png"
			}
		}
	}
	
	// Set headers
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(latestFile.size, 10))
	w.Header().Set("Cache-Control", "public, max-age=60") // Cache for 1 minute
	w.Header().Set("X-Album-Art-Hash", filepath.Base(latestFile.name)) // Include hash in header
	w.Header().Set("X-Album-Art-Modified", latestFile.modTime.Format(time.RFC3339))
	
	log.Printf("Serving latest album art: %s (%d bytes, %s)", 
		latestFile.name, latestFile.size, latestFile.modTime.Format("2006-01-02 15:04:05"))
	
	// Write image data
	if _, err := w.Write(data); err != nil {
		log.Printf("Failed to write latest album art data: %v", err)
	}
}

// handleCurrentAlbumArt handles requests for the current album art
func (s *ServerV2) handleCurrentAlbumArt(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}

	// Get current album art from global storage
	data := utils.GetCurrentAlbumArt()
	
	if data == nil {
		writeErrorResponse(w, http.StatusNotFound, "No current album art available", nil)
		return
	}
	
	// Determine content type from data
	contentType := "application/octet-stream"
	if len(data) >= 4 {
		if data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 {
			contentType = "image/webp"
		} else if data[0] == 0xFF && data[1] == 0xD8 {
			contentType = "image/jpeg"
		} else if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
			contentType = "image/png"
		}
	}
	
	// Set headers
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=60") // Cache for 1 minute
	
	log.Printf("Serving current album art: %d bytes (%s)", len(data), contentType)
	
	// Write image data
	if _, err := w.Write(data); err != nil {
		log.Printf("Failed to write current album art data: %v", err)
	}
}

// handleAlbumArtEnabled handles getting and setting album art transfer enabled state
func (s *ServerV2) handleAlbumArtEnabled(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		enabled := s.manager.IsAlbumArtEnabled()
		writeJSONResponse(w, http.StatusOK, map[string]interface{}{
			"enabled":   enabled,
			"timestamp": time.Now().Unix(),
		})
		
	case "POST":
		var request struct {
			Enabled bool `json:"enabled"`
		}
		
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeErrorResponse(w, http.StatusBadRequest, "Invalid JSON in request body", err)
			return
		}
		
		s.manager.SetAlbumArtEnabled(request.Enabled)
		
		log.Printf("Album art transfers %s via API", map[bool]string{true: "enabled", false: "disabled"}[request.Enabled])
		
		writeJSONResponse(w, http.StatusOK, map[string]interface{}{
			"enabled":   request.Enabled,
			"timestamp": time.Now().Unix(),
		})
	}
}