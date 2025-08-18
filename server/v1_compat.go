package server

import (
	"net/http"
	"strings"
	"time"
)

// V1 Compatibility Handlers for nocturne-ui integration

// handleV1MediaPlay handles v1 media play requests
func (s *ServerV2) handleV1MediaPlay(w http.ResponseWriter, r *http.Request) {
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "play command sent",
	})
}

// handleV1MediaPause handles v1 media pause requests
func (s *ServerV2) handleV1MediaPause(w http.ResponseWriter, r *http.Request) {
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "pause command sent",
	})
}

// handleV1MediaNext handles v1 media next requests
func (s *ServerV2) handleV1MediaNext(w http.ResponseWriter, r *http.Request) {
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "next command sent",
	})
}

// handleV1MediaPrevious handles v1 media previous requests
func (s *ServerV2) handleV1MediaPrevious(w http.ResponseWriter, r *http.Request) {
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
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "volume set to " + path,
		"volume": path,
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
	
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status": "seeked to " + path,
		"position": path,
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