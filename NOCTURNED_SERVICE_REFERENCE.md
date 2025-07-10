# nocturned Service - Reference Guide

## Overview

nocturned is a Go-based daemon service that acts as the core communication hub for the Nocturne ecosystem. It runs on the Car Thing device and provides Bluetooth management, media control bridging, and hardware abstraction through REST API and WebSocket interfaces.

## Technology Stack

- **Language**: Go 1.23+
- **Operating System**: Alpine Linux (OpenRC)
- **Bluetooth Stack**: BlueZ via D-Bus
- **Communication**: HTTP REST API + WebSocket
- **Service Management**: OpenRC with supervise-daemon
- **Hardware Platform**: Amlogic-based Car Thing

## Project Structure

```
/nocturned/
├── main.go                     # HTTP server and API endpoints
├── bluetooth/
│   ├── manager.go              # Bluetooth device management via D-Bus
│   └── media_client.go         # SPP client for NocturneCompanion
├── utils/
│   ├── types.go               # Data structures and JSON models
│   ├── websocket.go           # WebSocket hub for real-time events
│   └── device.go              # Car Thing hardware utilities
├── go.mod                      # Go module definition
├── go.sum                      # Dependency checksums
└── flake.nix                   # Nix development environment
```

## Build Commands

### Development Build
```bash
# Using Go directly
go build -o nocturned .
go run .

# Using Nix (recommended)
nix develop              # Enter development shell
nix build               # Build the application

# Run tests
go test ./...
go test -v ./bluetooth
go test -cover ./...
```

### Production Deployment
```bash
# Build for Car Thing (ARM64)
GOOS=linux GOARCH=arm64 go build -o nocturned .

# Deploy to Car Thing
sshpass -p 'nocturne' scp nocturned root@172.16.42.2:/usr/sbin/nocturned

# Restart service
sshpass -p 'nocturne' ssh root@172.16.42.2 "rc-service nocturned restart"
```

### Dependency Management
```bash
# Update dependencies
go mod tidy
go mod download

# Update Nix dependencies
gomod2nix

# Check for vulnerabilities
go list -json -m all | nancy sleuth
```

## Core Architecture

### Main Service (main.go)
HTTP server providing REST API and WebSocket endpoints.

**Key Features**:
- CORS-enabled HTTP server on port 5000
- WebSocket hub for real-time communication
- Network connectivity monitoring
- Graceful shutdown handling

**Server Configuration**:
```go
server := &http.Server{
    Addr:         ":5000",
    Handler:      router,
    ReadTimeout:  15 * time.Second,
    WriteTimeout: 15 * time.Second,
    IdleTimeout:  60 * time.Second,
}
```

### Bluetooth Manager (bluetooth/manager.go)
D-Bus interface for BlueZ Bluetooth operations.

**Key Features**:
- Device discovery and pairing
- Connection management
- Adapter control (power, discoverable mode)
- Real-time event monitoring via D-Bus signals

**D-Bus Integration**:
```go
// Connect to system D-Bus
conn, err := dbus.SystemBus()
if err != nil {
    return fmt.Errorf("failed to connect to D-Bus: %v", err)
}

// Call BlueZ methods
call := conn.Object("org.bluez", "/org/bluez/hci0").Call(
    "org.bluez.Adapter1.StartDiscovery", 0)
```

### Media Client (bluetooth/media_client.go)
Bluetooth SPP client for NocturneCompanion communication.

**Key Features**:
- Auto-discovery of NocturneCompanion devices
- SPP connection establishment and management
- JSON command sending and state receiving
- WebSocket event broadcasting

**SPP Connection Flow**:
```go
// Discover paired devices
devices := bt.GetPairedDevices()

// Find NocturneCompanion
for _, device := range devices {
    if strings.Contains(device.Name, "NocturneCompanion") {
        // Attempt SPP connection
        conn := bt.ConnectSPP(device.Address, SPP_UUID)
    }
}
```

### WebSocket Hub (utils/websocket.go)
Real-time event broadcasting system.

**Event Types**:
- `media/connected` - Android device connected
- `media/disconnected` - Android device disconnected
- `media/state_update` - Media state changed
- `bluetooth/connect` - Bluetooth device connected
- `bluetooth/disconnect` - Bluetooth device disconnected
- `bluetooth/pairing/request` - Pairing request received

**Broadcasting Implementation**:
```go
type Hub struct {
    clients    map[*Client]bool
    broadcast  chan []byte
    register   chan *Client
    unregister chan *Client
}

func (h *Hub) BroadcastEvent(eventType string, data interface{}) {
    event := Event{
        Type: eventType,
        Data: data,
        Timestamp: time.Now(),
    }
    
    jsonData, _ := json.Marshal(event)
    h.broadcast <- jsonData
}
```

## API Reference

### Media Control Endpoints

#### GET /media/status
Get current media connection status and state.

**Response**:
```json
{
  "connected": true,
  "state": {
    "artist": "Artist Name",
    "album": "Album Name",
    "track": "Track Title",
    "duration_ms": 240000,
    "position_ms": 45000,
    "is_playing": true,
    "volume_percent": 75
  }
}
```

#### POST /media/{command}
Send media control commands.

**Commands**: `play`, `pause`, `next`, `previous`

**Response**: `200 OK` on success, `500` on error

#### POST /media/seek/{position_ms}
Seek to specific position in milliseconds.

**Parameters**: `position_ms` - Position in milliseconds (0 to track duration)

#### POST /media/volume/{percent}
Set volume level.

**Parameters**: `percent` - Volume level (0-100)

#### POST /media/simulate
Simulate media state updates for testing.

### Bluetooth Management Endpoints

#### POST /bluetooth/discover/{on|off}
Control Bluetooth discoverability.

**Response**: `200 OK` on success

#### GET /bluetooth/devices
List all paired Bluetooth devices.

**Response**:
```json
{
  "devices": [
    {
      "address": "AA:BB:CC:DD:EE:FF",
      "name": "Device Name",
      "connected": true,
      "paired": true,
      "rssi": -45
    }
  ]
}
```

#### POST /bluetooth/connect/{address}
Connect to specific Bluetooth device.

**Parameters**: `address` - Bluetooth MAC address

#### POST /bluetooth/disconnect/{address}
Disconnect from specific Bluetooth device.

#### POST /bluetooth/remove/{address}
Remove (forget) paired Bluetooth device.

#### POST /bluetooth/pairing/{accept|deny}
Handle incoming pairing requests.

### Device Management Endpoints

#### GET /device/info
Get device information and status.

**Response**:
```json
{
  "version": "1.0.0",
  "bluetooth_adapter": "hci0",
  "network_interface": "bnep0",
  "uptime": 3600,
  "memory_usage": "45%"
}
```

#### POST /device/brightness/{value}
Set screen brightness (0-255).

#### POST /device/power/{shutdown|reboot}
System power control.

#### GET /network/status
Get network connectivity status.

**Response**:
```json
{
  "connected": true,
  "interface": "bnep0",
  "ip_address": "172.16.42.2",
  "ping_status": "ok"
}
```

## Service Configuration

### OpenRC Service Definition
```bash
#!/sbin/openrc-run
# /etc/init.d/nocturned

name="nocturned"
supervisor="supervise-daemon"
command="/usr/sbin/nocturned"
command_args="--"

depend() {
    need localmount dbus bluetooth bluetooth_adapter network-bridge
}
```

### Service Management Commands
```bash
# Start service
rc-service nocturned start

# Stop service
rc-service nocturned stop

# Restart service
rc-service nocturned restart

# Check status
rc-service nocturned status

# View logs
ps aux | grep nocturned
```

### Boot Dependencies Fix
To resolve boot timing issues, ensure proper service dependencies:

```bash
# Current problematic dependency
need localmount dbus bluetooth network-bridge

# Fixed dependency (includes bluetooth_adapter)
need localmount dbus bluetooth bluetooth_adapter network-bridge
```

## Communication Protocols

### Bluetooth SPP Protocol
JSON commands sent to/from NocturneCompanion Android app.

**Command Format** (nocturned → Android):
```json
{"command": "play"}
{"command": "pause"}
{"command": "next"}
{"command": "previous"}
{"command": "seek_to", "value_ms": 30000}
{"command": "set_volume", "value_percent": 75}
```

**State Update Format** (Android → nocturned):
```json
{
  "type": "stateUpdate",
  "artist": "Artist Name",
  "album": "Album Name",
  "track": "Track Title",
  "duration_ms": 240000,
  "position_ms": 45000,
  "is_playing": true,
  "volume_percent": 75
}
```

### WebSocket Events
Real-time events broadcast to connected clients.

**Event Structure**:
```json
{
  "type": "event_type",
  "data": { /* event-specific data */ },
  "timestamp": "2025-01-05T10:30:00Z"
}
```

## Development Guidelines

### Error Handling
```go
// Consistent error handling pattern
func (m *MediaClient) SendCommand(command string, value interface{}) error {
    if !m.connected {
        return fmt.Errorf("not connected to Android device")
    }
    
    cmd := Command{
        Command: command,
        Value:   value,
    }
    
    data, err := json.Marshal(cmd)
    if err != nil {
        return fmt.Errorf("failed to marshal command: %v", err)
    }
    
    if err := m.sendData(append(data, '\n')); err != nil {
        return fmt.Errorf("failed to send command: %v", err)
    }
    
    return nil
}
```

### Logging Best Practices
```go
import "log/slog"

// Structured logging
slog.Info("Bluetooth device connected",
    "address", device.Address,
    "name", device.Name,
    "connection_time", time.Now())

slog.Error("Failed to send media command",
    "command", command,
    "error", err)
```

### Concurrent Programming
```go
// Safe concurrent access
type MediaClient struct {
    mu       sync.RWMutex
    connected bool
    conn     net.Conn
    state    MediaState
}

func (m *MediaClient) GetState() MediaState {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.state
}

func (m *MediaClient) updateState(newState MediaState) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.state = newState
}
```

## Testing and Debugging

### Unit Testing
```go
func TestMediaClientConnect(t *testing.T) {
    client := NewMediaClient()
    
    // Mock Bluetooth connection
    mockConn := &MockConnection{}
    client.conn = mockConn
    
    err := client.SendCommand("play", nil)
    assert.NoError(t, err)
    
    // Verify command was sent
    assert.Equal(t, `{"command":"play"}\n`, mockConn.SentData)
}
```

### Integration Testing
```bash
# Test Bluetooth operations
bluetoothctl show
bluetoothctl scan on
bluetoothctl devices

# Test API endpoints
curl -X GET http://localhost:5000/media/status
curl -X POST http://localhost:5000/media/play

# Test WebSocket connection
websocat ws://localhost:8080
```

### Debugging Commands
```bash
# Monitor D-Bus activity
dbus-monitor --system "interface='org.bluez.Device1'"

# Check service logs
journalctl -u nocturned -f

# Monitor Bluetooth connections
hcidump -X

# Check process status
ps aux | grep nocturned
netstat -tlnp | grep :5000
```

## Performance Optimization

### Memory Management
```go
// Efficient buffer pooling
var bufferPool = sync.Pool{
    New: func() interface{} {
        return make([]byte, 1024)
    },
}

func (m *MediaClient) readData() ([]byte, error) {
    buffer := bufferPool.Get().([]byte)
    defer bufferPool.Put(buffer)
    
    n, err := m.conn.Read(buffer)
    if err != nil {
        return nil, err
    }
    
    // Return copy of data
    result := make([]byte, n)
    copy(result, buffer[:n])
    return result, nil
}
```

### Connection Pooling
```go
// Reuse Bluetooth connections when possible
type ConnectionPool struct {
    connections map[string]*Connection
    mu         sync.RWMutex
}

func (p *ConnectionPool) GetConnection(address string) *Connection {
    p.mu.RLock()
    defer p.mu.RUnlock()
    return p.connections[address]
}
```

## Common Issues and Solutions

### Issue: Service Fails to Start
**Symptoms**: nocturned service doesn't start after boot
**Cause**: Race condition with Bluetooth hardware initialization
**Solution**: Add `bluetooth_adapter` to service dependencies

### Issue: SPP Connection Fails
**Symptoms**: Cannot connect to NocturneCompanion
**Solutions**:
- Verify Android app is running and SPP server started
- Check Bluetooth pairing status
- Monitor D-Bus signals for connection events
- Verify SPP UUID matches between devices

### Issue: WebSocket Disconnections
**Symptoms**: Frontend loses real-time updates
**Solutions**:
- Implement client-side reconnection logic
- Monitor WebSocket connection health
- Check firewall settings on Car Thing
- Verify nocturned service is running

### Issue: D-Bus Permission Errors
**Symptoms**: Bluetooth operations fail with permission errors
**Solutions**:
- Ensure nocturned runs as root
- Check D-Bus policy configuration
- Verify BlueZ service is running

### Issue: High Memory Usage
**Symptoms**: Service consumes excessive memory
**Solutions**:
- Implement buffer pooling for frequent allocations
- Close unused Bluetooth connections
- Monitor goroutine leaks
- Use memory profiling tools

## Deployment Checklist

- [ ] Build binary for ARM64 architecture
- [ ] Copy binary to `/usr/sbin/nocturned`
- [ ] Ensure proper file permissions (executable)
- [ ] Verify service dependencies in OpenRC
- [ ] Test Bluetooth adapter detection
- [ ] Confirm WebSocket port availability
- [ ] Validate API endpoint responses
- [ ] Test SPP connection to Android device
- [ ] Monitor service startup in boot logs

---

**Last Updated**: 2025-01-05  
**Service Version**: 1.0.0  
**Go Version**: 1.23+  
**Target Platform**: Alpine Linux (OpenRC) on Car Thing