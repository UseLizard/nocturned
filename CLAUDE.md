# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

nocturned is a Go-based host communication daemon for the Nocturne project - a Linux-based system that runs on repurposed Spotify Car Thing devices. It provides a REST API and WebSocket interface for managing Bluetooth connections, device settings, system updates, and network connectivity for the nocturne-ui frontend.

## Build and Development Commands

### Building the Project
```bash
# Build using Nix (recommended)
nix develop  # Enter development shell
nix build    # Build the application

# Build using Go directly
go build -o nocturned .

# Run the application
./nocturned
# or
go run .
```

### Development
```bash
# Run tests
go test ./...

# Format code
go fmt ./...

# Tidy dependencies
go mod tidy

# Update dependencies in Nix
gomod2nix

# Check for issues
go vet ./...
```

### Testing
```bash
# Run all tests
go test -v ./...

# Test specific package
go test -v ./bluetooth
go test -v ./utils

# Test with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Architecture Overview

### Core Components

1. **Main Server** (`main.go`): HTTP server with REST API endpoints and WebSocket support. Handles all device management, Bluetooth operations, and system control. Default port: 5000 (configurable via PORT env var).

2. **Bluetooth Manager** (`bluetooth/manager.go`): Manages Bluetooth adapter operations via D-Bus, including device discovery, pairing, connections, and network profiles. Integrates with BlueZ via D-Bus API.

3. **WebSocket Hub** (`utils/websocket.go`): Real-time communication system for broadcasting events to connected clients, including Bluetooth connection status, network changes, and system updates.

4. **Device Utilities** (`utils/device.go`): Hardware-specific functions for Car Thing device management including brightness control, system operations (shutdown/reboot), factory reset, and timezone management.

5. **Network Monitoring** (`main.go`): Continuous network connectivity monitoring using ping to 1.1.1.1, broadcasts status changes via WebSocket.

6. **Media Client** (`bluetooth/media_client.go`): Bluetooth SPP client for communicating with NocturneCompanion Android app. Discovers paired devices, establishes connections, sends media control commands, and broadcasts state updates.

### Key Architecture Patterns

- **D-Bus Integration**: Heavy use of D-Bus for Bluetooth operations via BlueZ
- **Concurrent Processing**: Goroutines for network monitoring, WebSocket broadcasting, and Bluetooth event handling
- **RESTful API**: Clean HTTP endpoints with JSON responses and CORS support
- **Real-time Updates**: WebSocket broadcasting for live status updates
- **Hardware Abstraction**: Device-specific operations abstracted into utility functions

### API Structure

**Bluetooth Operations**:
- `/bluetooth/discover/{on,off}` - Control Bluetooth discoverability
- `/bluetooth/devices` - List paired/available devices
- `/bluetooth/connect/{address}` - Connect to device
- `/bluetooth/network/{address}` - Connect Bluetooth network profile
- `/bluetooth/pairing/{accept,deny}` - Handle pairing requests

**Device Management**:
- `/device/brightness/{value}` - Control screen brightness
- `/device/power/{shutdown,reboot}` - System power control
- `/device/factoryreset` - Factory reset device
- `/device/date/settimezone` - Set timezone from API

**System Operations**:
- `/info` - Get system version from `/etc/nocturne/version.txt`
- `/network/status` - Get network connectivity status
- `/update` - Handle OTA system updates
- `/fetchjson` - Proxy for fetching external JSON APIs

**Media Control** (NocturneCompanion Integration):
- `/media/status` - Get media connection status and current state
- `/media/play` - Send play command to Android device
- `/media/pause` - Send pause command to Android device
- `/media/next` - Send next track command
- `/media/previous` - Send previous track command
- `/media/seek/{position_ms}` - Seek to specific position
- `/media/volume/{percent}` - Set volume (0-100)
- `/media/simulate` - Simulate media state updates (testing)

### Communication Flow

1. **REST API**: Frontend communicates via HTTP for device control and configuration
2. **WebSocket Events**: Real-time updates for Bluetooth status, network changes, update progress
3. **D-Bus Events**: Bluetooth events from BlueZ are monitored and forwarded to WebSocket clients
4. **Network Monitoring**: Continuous ping monitoring broadcasts connectivity status
5. **Media Integration**: Auto-discovery and connection to NocturneCompanion Android app for media control bridging

## Development Notes

- Go version: 1.23+ (specified in go.mod and flake.nix)
- Uses Nix for reproducible development environment
- SystemD service integration via NixOS module (flake.nix)
- Runs as root for hardware access and Bluetooth management
- Designed specifically for Amlogic-based Car Thing hardware
- Network interface monitoring focuses on `bnep0` for Bluetooth networking
- OTA updates write to `/data/tmp` and flash system images
- Brightness control via `/sys/class/backlight/aml-bl/brightness`
- Configuration stored in `/data/etc/nocturne/` and `/etc/nocturne/`

## Integration with NocturneCompanion

The project includes detailed integration documentation (`NOCTURNED_INTEGRATION_CHEATSHEET.md`) for communicating with the NocturneCompanion Android app via Bluetooth SPP. This enables media control bridging between Android devices and the Car Thing hardware.