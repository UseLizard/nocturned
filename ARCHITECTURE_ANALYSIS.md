# Nocturned Architecture Analysis & Improvement Roadmap

## Current System Overview

The nocturned service is a Go-based daemon that acts as the central hub for the Nocturne car interface ecosystem. It manages:

- **BLE Communication**: Binary Protocol V2 with NocturneCompanion Android app
- **Media State Management**: Real-time playback state, volume, and album art
- **Weather Data**: GZIP-compressed weather data reception and storage
- **WebSocket Broadcasting**: Real-time updates to nocturne-ui frontend
- **HTTP API**: RESTful endpoints for system interaction

### Key Components

1. **main_v2.go** - Service entry point with configuration and graceful shutdown
2. **ManagerV2** (`bluetooth/manager.go`) - Central state coordinator
3. **BleClientV2** (`bluetooth/ble_client_v2.go`) - BLE protocol handler
4. **ServerV2** (`server/server.go`) - HTTP/WebSocket server
5. **WeatherHandler** (`bluetooth/weather_handler.go`) - Weather data processor
6. **WebSocketBroadcaster** (`utils/broadcaster.go`) - Event broadcasting

## Current Challenges & Improvement Roadmap

*Sorted from easiest to most challenging*

---

## 🟢 EASY WINS (1-2 days)

### 1. File I/O Consolidation
**Problem**: Scattered file operations across multiple components
- Weather data: `/var/nocturne/weather/*.json`
- Album art: `/var/album_art/*.webp`
- Logs: `/var/nocturne/nocturned_v2.log`
- Each component handles its own file ops

**Solution**: Create a unified `storage` package
```go
// storage/storage.go
type StorageManager struct {
    baseDir string
}

func (s *StorageManager) SaveWeatherData(mode string, data []byte) error
func (s *StorageManager) LoadWeatherData(mode string) ([]byte, error)
func (s *StorageManager) SaveAlbumArt(hash string, data []byte) error
func (s *StorageManager) LoadAlbumArt(hash string) ([]byte, error)
```

**Impact**: 
- Centralized file handling
- Consistent error handling
- Easier testing and mocking
- Cleaner directory structure

### 2. Configuration Management
**Problem**: Configuration scattered across command-line flags and hardcoded paths

**Solution**: Unified configuration with environment variable support
```go
// config/config.go
type Config struct {
    Server   ServerConfig
    Storage  StorageConfig  
    Bluetooth BluetoothConfig
}

func LoadConfig() (*Config, error) // From env vars, config file, and flags
```

**Impact**:
- Environment-specific configurations
- Easier deployment management
- Single source of truth for settings

### 3. Logging Standardization
**Problem**: Inconsistent log formatting and levels throughout codebase

**Solution**: Structured logging with consistent prefixes
```go
// Use structured logging library (logrus/zap)
logger.WithFields(logrus.Fields{
    "component": "BLE_CLIENT",
    "action": "connection_attempt",
    "device": devicePath,
}).Info("Starting BLE connection")
```

**Impact**:
- Better debugging and monitoring
- Consistent log parsing
- Filterable log levels

---

## 🟡 MEDIUM COMPLEXITY (3-7 days)

### 4. Error Handling & Recovery
**Problem**: Errors are logged but recovery strategies are inconsistent

**Current Issues**:
- BLE connection failures don't have exponential backoff
- Weather data corruption doesn't trigger cleanup
- Album art transfer failures leave partial state

**Solution**: Implement error handling patterns
```go
// errors/retry.go
type RetryableError struct {
    Err error
    Retryable bool
    Backoff time.Duration
}

// Add to managers
func (m *ManagerV2) handleError(err error) {
    if re, ok := err.(*RetryableError); ok && re.Retryable {
        // Implement exponential backoff
    }
}
```

**Impact**:
- More reliable operation
- Graceful degradation
- Better user experience

### 5. State Management Consolidation
**Problem**: State is duplicated across multiple components

**Current State Holders**:
- `ManagerV2`: currentPlayState, currentVolume, currentWeather, currentMediaState
- `BleClientV2`: Connection state, protocol state
- `ServerV2`: Endpoint-specific state
- Individual handlers: Transfer states

**Solution**: Centralized state manager with pub/sub
```go
// state/manager.go
type StateManager struct {
    mediaState    *MediaState
    weatherState  *WeatherState
    connectionState *ConnectionState
    subscribers   map[string][]chan StateEvent
}

func (s *StateManager) UpdateMediaState(state *MediaState)
func (s *StateManager) Subscribe(eventType string) <-chan StateEvent
```

**Impact**:
- Single source of truth
- Consistent state across components
- Easier debugging and testing

### 6. Album Art Management Redesign
**Problem**: Album art handling is complex with hash inconsistencies

**Current Issues**:
- Two different hash methods (metadata hash vs. direct hash)
- Cache/file system synchronization issues
- Complex transfer state management

**Solution**: Unified album art service
```go
// albumart/service.go
type AlbumArtService struct {
    cache   map[string]*AlbumArt
    storage StorageManager
}

type AlbumArt struct {
    Hash     string
    Metadata *MediaMetadata
    Data     []byte
    Source   string // "companion", "file", "cache"
}
```

**Impact**:
- Consistent hash handling
- Simplified caching logic
- Better performance

---

## 🟠 COMPLEX IMPROVEMENTS (1-2 weeks)

### 7. Protocol Handler Refactoring
**Problem**: `BleClientV2.handleNotification()` is becoming unwieldy

**Current Issues**:
- Large switch statement with mixed concerns
- Protocol parsing, state updates, and broadcasting in one place
- Difficult to add new message types

**Solution**: Message router with handlers
```go
// protocol/router.go
type MessageRouter struct {
    handlers map[uint16]MessageHandler
}

type MessageHandler interface {
    Handle(payload []byte, messageID uint16) error
}

// protocol/handlers/media.go
type MediaHandler struct {
    stateManager *StateManager
    broadcaster  *Broadcaster
}
```

**Impact**:
- Cleaner protocol handling
- Easier to add new message types
- Better separation of concerns
- More testable code

### 8. Database Integration (bbolt)
**Problem**: File-based storage is becoming complex and error-prone

**Solution**: Replace scattered file operations with embedded database
```go
// storage/db.go
type Database struct {
    db *bbolt.DB
}

// Buckets:
// - "weather_hourly", "weather_weekly"
// - "album_art_metadata", "album_art_cache"  
// - "media_state", "connection_state"
// - "config", "settings"

func (d *Database) StoreWeatherData(mode string, timestamp int64, data []byte) error
func (d *Database) GetWeatherData(mode string, since int64) ([]WeatherEntry, error)
func (d *Database) StoreAlbumArt(hash string, metadata *AlbumMetadata, data []byte) error
```

**Benefits**:
- Atomic operations and transactions
- Better query capabilities
- Consistent data format
- Faster lookups
- Automatic cleanup/expiration
- Better concurrent access

**Migration Strategy**:
1. Add database alongside existing file system
2. Implement dual-write (both file and DB)
3. Migrate reads to database
4. Remove file operations
5. Add advanced features (queries, cleanup)

---

## 🔴 MAJOR ARCHITECTURAL CHANGES (2-4 weeks)

### 9. Service Architecture Redesign
**Problem**: Components are tightly coupled, making testing and maintenance difficult

**Solution**: Clean architecture with dependency injection
```go
// Core business logic
type UseCases interface {
    HandleMediaStateUpdate(state *MediaState) error
    ProcessWeatherData(data *WeatherData) error
    HandleAlbumArtRequest(hash string) error
}

// Infrastructure interfaces
type MediaRepository interface {
    GetCurrentState() (*MediaState, error)
    UpdateState(state *MediaState) error
}

type WeatherRepository interface {
    GetWeatherData(mode string, timeRange TimeRange) ([]*WeatherEntry, error)
    SaveWeatherData(entry *WeatherEntry) error
}
```

**Impact**:
- Testable business logic
- Pluggable infrastructure
- Cleaner dependency management
- Better separation of concerns

### 10. Event-Driven Architecture
**Problem**: Direct coupling between components makes the system brittle

**Solution**: Event-driven architecture with message bus
```go
// events/bus.go
type EventBus interface {
    Publish(event Event) error
    Subscribe(eventType string, handler EventHandler) error
}

// Events
type MediaStateChangedEvent struct {
    OldState *MediaState
    NewState *MediaState
}

type WeatherUpdatedEvent struct {
    Mode string
    Data *WeatherData
}
```

**Impact**:
- Loose coupling between components
- Easier to add new features
- Better scalability
- Event sourcing possibilities

---

## Implementation Priority

### Phase 1 (Immediate - 1 week)
1. File I/O consolidation
2. Configuration management 
3. Logging standardization

### Phase 2 (Short term - 2 weeks)
4. Error handling & recovery
5. State management consolidation

### Phase 3 (Medium term - 1 month)
6. Album art management redesign
7. Protocol handler refactoring

### Phase 4 (Long term - 2+ months)
8. Database integration
9. Service architecture redesign
10. Event-driven architecture

## Risks & Considerations

- **Breaking Changes**: Database integration will require careful migration
- **Testing**: Current system has limited test coverage; improvements should include tests
- **Backwards Compatibility**: Must maintain API compatibility with nocturne-ui
- **Performance**: Changes should not impact real-time BLE communication
- **Deployment**: Consider Car Thing deployment constraints and update mechanisms

## Success Metrics

- **Reliability**: Reduced error rates and better recovery
- **Maintainability**: Easier to add features and fix bugs
- **Performance**: Faster startup, lower memory usage
- **Developer Experience**: Cleaner code, better documentation, comprehensive tests