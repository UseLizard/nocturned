package bluetooth

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/usenocturne/nocturned/utils"
)

type ProfileManager struct {
	btManager     *BluetoothManager
	wsHub         *utils.WebSocketHub
	mu            sync.RWMutex
	configurations map[string]*utils.ProfileConfiguration // deviceAddress -> config
	profileLogs   []utils.ProfileLogEntry
	maxLogEntries int
}

func NewProfileManager(btManager *BluetoothManager, wsHub *utils.WebSocketHub) *ProfileManager {
	pm := &ProfileManager{
		btManager:     btManager,
		wsHub:         wsHub,
		configurations: make(map[string]*utils.ProfileConfiguration),
		profileLogs:   make([]utils.ProfileLogEntry, 0),
		maxLogEntries: 1000, // Keep last 1000 log entries
	}
	
	return pm
}

// GetDefaultProfiles returns the default profile configuration for a device
func (pm *ProfileManager) GetDefaultProfiles() map[string]utils.BluetoothProfile {
	return map[string]utils.BluetoothProfile{
		// Audio Profiles
		PROFILE_A2DP_UUID: {
			UUID:        PROFILE_A2DP_UUID,
			Name:        "A2DP",
			Description: "Advanced Audio Distribution Profile - High-quality stereo audio streaming",
			Category:    CATEGORY_AUDIO,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     true,
			AutoConnect: true,
			Priority:    90,
			Settings: map[string]interface{}{
				"codec_preference": "AAC", // SBC, AAC, aptX
				"bitrate_mode":     "quality", // quality, balanced, low_latency
			},
		},
		PROFILE_AVRCP_UUID: {
			UUID:        PROFILE_AVRCP_UUID,
			Name:        "AVRCP",
			Description: "Audio/Video Remote Control Profile - Media playback control",
			Category:    CATEGORY_AUDIO,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     true,
			AutoConnect: true,
			Priority:    85,
			Settings: map[string]interface{}{
				"version":          "1.6",
				"metadata_support": true,
				"volume_control":   true,
			},
		},
		PROFILE_HSP_UUID: {
			UUID:        PROFILE_HSP_UUID,
			Name:        "HSP",
			Description: "Headset Profile - Basic mono audio and call control",
			Category:    CATEGORY_AUDIO,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     false,
			AutoConnect: false,
			Priority:    40,
		},
		PROFILE_HFP_UUID: {
			UUID:        PROFILE_HFP_UUID,
			Name:        "HFP",
			Description: "Hands-Free Profile - Advanced hands-free calling features",
			Category:    CATEGORY_AUDIO,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     false,
			AutoConnect: false,
			Priority:    50,
		},
		// Data/Network Profiles
		PROFILE_PAN_UUID: {
			UUID:        PROFILE_PAN_UUID,
			Name:        "PAN",
			Description: "Personal Area Network - Bluetooth networking",
			Category:    CATEGORY_DATA,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     true,
			AutoConnect: false,
			Priority:    70,
		},
		PROFILE_NAP_UUID: {
			UUID:        PROFILE_NAP_UUID,
			Name:        "NAP",
			Description: "Network Access Point - Internet tethering via Bluetooth",
			Category:    CATEGORY_DATA,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     false,
			AutoConnect: false,
			Priority:    60,
		},
		// Phone Integration Profiles
		PROFILE_PBAP_UUID: {
			UUID:        PROFILE_PBAP_UUID,
			Name:        "PBAP",
			Description: "Phone Book Access Profile - Contact synchronization",
			Category:    CATEGORY_PHONE,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     false,
			AutoConnect: false,
			Priority:    30,
		},
		PROFILE_MAP_UUID: {
			UUID:        PROFILE_MAP_UUID,
			Name:        "MAP",
			Description: "Message Access Profile - SMS/messaging access",
			Category:    CATEGORY_PHONE,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     false,
			AutoConnect: false,
			Priority:    25,
		},
		// Device/Utility Profiles
		PROFILE_HID_UUID: {
			UUID:        PROFILE_HID_UUID,
			Name:        "HID",
			Description: "Human Interface Device - Keyboard/mouse input",
			Category:    CATEGORY_DEVICE,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     false,
			AutoConnect: false,
			Priority:    20,
		},
		PROFILE_OPP_UUID: {
			UUID:        PROFILE_OPP_UUID,
			Name:        "OPP",
			Description: "Object Push Profile - File transfer capabilities",
			Category:    CATEGORY_DEVICE,
			State:       PROFILE_STATE_DISABLED,
			Enabled:     false,
			AutoConnect: false,
			Priority:    15,
		},
	}
}

// GetProfileConfiguration returns the profile configuration for a device
func (pm *ProfileManager) GetProfileConfiguration(deviceAddress string) *utils.ProfileConfiguration {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	config, exists := pm.configurations[deviceAddress]
	if !exists {
		// Create default configuration
		config = &utils.ProfileConfiguration{
			DeviceAddress: deviceAddress,
			Profiles:      pm.GetDefaultProfiles(),
			GlobalSettings: map[string]interface{}{
				"connection_timeout": 30,
				"auto_retry_count":   3,
				"power_management":   "balanced",
			},
		}
		pm.configurations[deviceAddress] = config
	}
	
	return config
}

// UpdateProfile updates a specific profile configuration
func (pm *ProfileManager) UpdateProfile(req utils.ProfileUpdateRequest) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	config := pm.GetProfileConfiguration(req.DeviceAddress)
	profile, exists := config.Profiles[req.ProfileUUID]
	if !exists {
		return fmt.Errorf("profile %s not found", req.ProfileUUID)
	}
	
	// Update profile fields
	if req.Enabled != nil {
		profile.Enabled = *req.Enabled
		pm.logProfileAction(req.DeviceAddress, req.ProfileUUID, profile.Name, 
			"config_update", "success", fmt.Sprintf("Enabled: %v", *req.Enabled), "")
	}
	
	if req.AutoConnect != nil {
		profile.AutoConnect = *req.AutoConnect
		pm.logProfileAction(req.DeviceAddress, req.ProfileUUID, profile.Name,
			"config_update", "success", fmt.Sprintf("AutoConnect: %v", *req.AutoConnect), "")
	}
	
	if req.Priority != nil {
		profile.Priority = *req.Priority
		pm.logProfileAction(req.DeviceAddress, req.ProfileUUID, profile.Name,
			"config_update", "success", fmt.Sprintf("Priority: %d", *req.Priority), "")
	}
	
	if req.Settings != nil {
		for key, value := range req.Settings {
			if profile.Settings == nil {
				profile.Settings = make(map[string]interface{})
			}
			profile.Settings[key] = value
		}
		pm.logProfileAction(req.DeviceAddress, req.ProfileUUID, profile.Name,
			"settings_update", "success", "Profile settings updated", "")
	}
	
	config.Profiles[req.ProfileUUID] = profile
	
	// Broadcast profile state change
	if pm.wsHub != nil {
		pm.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "bluetooth/profile/state_changed",
			Payload: utils.ProfileStateChangedPayload{
				DeviceAddress: req.DeviceAddress,
				Profile:       profile,
			},
		})
	}
	
	return nil
}

// ConnectProfile attempts to connect a specific profile
func (pm *ProfileManager) ConnectProfile(req utils.ProfileConnectionRequest) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	config := pm.GetProfileConfiguration(req.DeviceAddress)
	profile, exists := config.Profiles[req.ProfileUUID]
	if !exists {
		return fmt.Errorf("profile %s not found", req.ProfileUUID)
	}
	
	if !profile.Enabled {
		err := fmt.Errorf("profile %s is disabled", profile.Name)
		pm.logProfileAction(req.DeviceAddress, req.ProfileUUID, profile.Name,
			"connect", "error", "", err.Error())
		return err
	}
	
	// Update profile state to connecting
	profile.State = PROFILE_STATE_CONNECTING
	config.Profiles[req.ProfileUUID] = profile
	
	pm.logProfileAction(req.DeviceAddress, req.ProfileUUID, profile.Name,
		"connect", "connecting", "Attempting to connect profile", "")
	
	// Broadcast state change
	if pm.wsHub != nil {
		pm.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "bluetooth/profile/state_changed",
			Payload: utils.ProfileStateChangedPayload{
				DeviceAddress: req.DeviceAddress,
				Profile:       profile,
			},
		})
	}
	
	// Handle profile-specific connection logic
	go pm.handleProfileConnection(req, profile)
	
	return nil
}

// handleProfileConnection handles the actual profile connection logic
func (pm *ProfileManager) handleProfileConnection(req utils.ProfileConnectionRequest, profile utils.BluetoothProfile) {
	success := false
	var err error
	
	switch req.ProfileUUID {
	case PROFILE_SPP_UUID:
		// SPP is no longer supported - use BLE instead
		err = fmt.Errorf("SPP profile no longer supported, please use BLE")
		success = false
		
	case PROFILE_NAP_UUID:
		// Network connection
		err = pm.btManager.ConnectNetwork(req.DeviceAddress)
		success = (err == nil)
		
	case PROFILE_A2DP_UUID, PROFILE_AVRCP_UUID:
		// Audio profiles - use standard device connection
		err = pm.btManager.ConnectDevice(req.DeviceAddress)
		success = (err == nil)
		
	default:
		// Generic profile connection attempt
		err = pm.connectGenericProfile(req.DeviceAddress, req.ProfileUUID)
		success = (err == nil)
	}
	
	// Update profile state
	pm.mu.Lock()
	config := pm.configurations[req.DeviceAddress]
	if config != nil {
		profile := config.Profiles[req.ProfileUUID]
		if success {
			profile.State = PROFILE_STATE_CONNECTED
			pm.logProfileAction(req.DeviceAddress, req.ProfileUUID, profile.Name,
				"connect", "connected", "Profile connected successfully", "")
		} else {
			profile.State = PROFILE_STATE_ERROR
			pm.logProfileAction(req.DeviceAddress, req.ProfileUUID, profile.Name,
				"connect", "error", "", err.Error())
		}
		config.Profiles[req.ProfileUUID] = profile
		
		// Broadcast state change
		if pm.wsHub != nil {
			pm.wsHub.Broadcast(utils.WebSocketEvent{
				Type: "bluetooth/profile/state_changed",
				Payload: utils.ProfileStateChangedPayload{
					DeviceAddress: req.DeviceAddress,
					Profile:       profile,
				},
			})
		}
	}
	pm.mu.Unlock()
}

// connectGenericProfile attempts to connect a generic Bluetooth profile via D-Bus
func (pm *ProfileManager) connectGenericProfile(deviceAddress, profileUUID string) error {
	devicePath := formatDevicePath(pm.btManager.adapter, deviceAddress)
	obj := pm.btManager.conn.Object(BLUEZ_BUS_NAME, devicePath)
	
	// Try to connect the specific profile UUID
	return obj.Call("org.bluez.Device1.ConnectProfile", 0, profileUUID).Err
}

// DisconnectProfile disconnects a specific profile
func (pm *ProfileManager) DisconnectProfile(deviceAddress, profileUUID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	config := pm.GetProfileConfiguration(deviceAddress)
	profile, exists := config.Profiles[profileUUID]
	if !exists {
		return fmt.Errorf("profile %s not found", profileUUID)
	}
	
	// Update state
	profile.State = PROFILE_STATE_ENABLED
	config.Profiles[profileUUID] = profile
	
	pm.logProfileAction(deviceAddress, profileUUID, profile.Name,
		"disconnect", "success", "Profile disconnected", "")
	
	// Broadcast state change
	if pm.wsHub != nil {
		pm.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "bluetooth/profile/state_changed",
			Payload: utils.ProfileStateChangedPayload{
				DeviceAddress: deviceAddress,
				Profile:       profile,
			},
		})
	}
	
	return nil
}

// GetProfileLogs returns the recent profile logs
func (pm *ProfileManager) GetProfileLogs(limit int) []utils.ProfileLogEntry {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	if limit <= 0 || limit > len(pm.profileLogs) {
		limit = len(pm.profileLogs)
	}
	
	// Return most recent entries
	start := len(pm.profileLogs) - limit
	if start < 0 {
		start = 0
	}
	
	return pm.profileLogs[start:]
}

// logProfileAction adds a new log entry
func (pm *ProfileManager) logProfileAction(deviceAddress, profileUUID, profileName, action, status, message, errorMsg string) {
	entry := utils.ProfileLogEntry{
		Timestamp:     time.Now().Unix(),
		DeviceAddress: deviceAddress,
		ProfileUUID:   profileUUID,
		ProfileName:   profileName,
		Action:        action,
		Status:        status,
		Message:       message,
		Error:         errorMsg,
	}
	
	pm.profileLogs = append(pm.profileLogs, entry)
	
	// Trim logs if exceeding max entries
	if len(pm.profileLogs) > pm.maxLogEntries {
		pm.profileLogs = pm.profileLogs[1:]
	}
	
	log.Printf("Profile %s (%s): %s - %s", profileName, deviceAddress, action, status)
	
	// Broadcast log entry
	if pm.wsHub != nil {
		pm.wsHub.Broadcast(utils.WebSocketEvent{
			Type: "bluetooth/profile/log",
			Payload: utils.ProfileLogPayload{
				Entry: entry,
			},
		})
	}
}

// DetectDeviceProfiles detects which profiles a device supports by checking its UUIDs
func (pm *ProfileManager) DetectDeviceProfiles(deviceAddress string) ([]string, error) {
	devicePath := formatDevicePath(pm.btManager.adapter, deviceAddress)
	obj := pm.btManager.conn.Object(BLUEZ_BUS_NAME, devicePath)
	
	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, BLUEZ_DEVICE_INTERFACE).Store(&props); err != nil {
		return nil, fmt.Errorf("failed to get device properties: %v", err)
	}
	
	supportedProfiles := []string{}
	
	if uuids, ok := props["UUIDs"]; ok {
		if uuidList, ok := uuids.Value().([]string); ok {
			defaultProfiles := pm.GetDefaultProfiles()
			
			for _, uuid := range uuidList {
				// Normalize UUID format
				normalizedUUID := normalizeUUID(uuid)
				if _, exists := defaultProfiles[normalizedUUID]; exists {
					supportedProfiles = append(supportedProfiles, normalizedUUID)
				}
			}
		}
	}
	
	pm.logProfileAction(deviceAddress, "", "", "detection", "success", 
		fmt.Sprintf("Detected %d supported profiles", len(supportedProfiles)), "")
	
	return supportedProfiles, nil
}

// normalizeUUID converts short UUIDs to full format
func normalizeUUID(uuid string) string {
	if len(uuid) == 8 {
		// Convert 16-bit UUID to full format
		return fmt.Sprintf("%s-0000-1000-8000-00805f9b34fb", uuid)
	}
	return uuid
}