# Nocturned v2

This is the v2 implementation of the Nocturne service, featuring a complete rewrite with BLE-only, binary protocol communication.

## Architecture Overview

The v2 service is organized into the following components:

- **`bluetooth/`** - BLE client, protocol handling, album art management, and rate limiting
- **`server/`** - HTTP API server with RESTful endpoints and WebSocket support
- **`utils/`** - Shared utilities for WebSocket, device management, and media handling
- **`docs/`** - Architecture documentation and API specifications
- **`main_v2.go`** - Main entry point for the v2 service

## Key Features

- **BLE-only communication** - No classic Bluetooth dependencies
- **Binary protocol** - Efficient, compact message format defined in docs/
- **Chunked album art transfers** - Reliable large data transfer with hash verification
- **Rate limiting** - Configurable throttling to prevent BLE overload
- **WebSocket integration** - Real-time updates to connected clients
- **Comprehensive caching** - Memory-efficient album art storage
- **RESTful API** - Clean HTTP endpoints for all operations

## Building and Running

### Build

```bash
cd nocturned-v2
go build -o nocturned_v2 main_v2.go
```

### Cross-compile for Car Thing (ARM64)

```bash
cd nocturned-v2
GOOS=linux GOARCH=arm64 go build -o nocturned_v2_arm64 main_v2.go
```

### Run

```bash
./nocturned_v2 --port 5000 --rate-limit-rps 10 --log /var/nocturne/nocturned_v2.log
```

### Command Line Options

- `--port int` - HTTP server port (default: 5000)
- `--log string` - Log file path (default: stderr)
- `--debug` - Enable debug logging
- `--rate-limit-rps int` - Rate limit requests per second (default: 10)
- `--rate-limit-burst int` - Rate limit burst size (default: 5)
- `--chunk-delay duration` - Delay between album art chunks (default: 50ms)

## API Endpoints

### System Status
- `GET /api/v2/status` - Overall system status
- `GET /api/v2/health` - Health check

### Bluetooth Management
- `GET /api/v2/bluetooth/status` - BLE connection status
- `GET /api/v2/bluetooth/stats` - Detailed statistics

### Media Control
- `POST /api/v2/media/play-state` - Send play state updates
- `POST /api/v2/media/volume` - Send volume updates
- `GET /api/v2/media/current` - Get current media state

### Album Art
- `POST /api/v2/media/album-art` - Upload album art (JSON or multipart)
- `GET /api/v2/media/album-art/status` - Transfer status
- `GET /api/v2/media/album-art/cache` - Cache information
- `DELETE /api/v2/media/album-art/cache` - Clear cache
- `GET /api/v2/media/album-art/{hash}` - Get album art by hash

### Weather
- `POST /api/v2/weather` - Send weather updates
- `GET /api/v2/weather/current` - Get current weather

### WebSocket
- `GET /ws` - WebSocket endpoint for real-time events

## Testing

```bash
cd nocturned-v2
go test ./bluetooth/ -v
go test ./server/ -v
```

## Deployment

For Car Thing deployment, use the same deployment scripts as v1 but point to the v2 binary:

```bash
# Copy to parent deployment script and modify to use nocturned_v2_arm64
cp ../deployment/deploy_nocturned.sh ./deploy_nocturned_v2.sh
# Edit script to use nocturned_v2_arm64 binary
```

## Integration with UI

See the UI integration guides in `docs/` for detailed information on how to connect the nocturne-ui frontend to the v2 service.

## Migration from v1

The v2 service is designed to run alongside v1 during migration. Key differences:

1. **Port**: v2 runs on port 5000 by default (v1 uses 8080)
2. **API Path**: All v2 endpoints use `/api/v2/` prefix
3. **Protocol**: Binary-only BLE communication (no JSON over BLE)
4. **Album Art**: Improved chunked transfer with hash verification
5. **Rate Limiting**: Built-in throttling for BLE commands

## Documentation

See the `docs/` directory for detailed specifications:

- `BLE_API.md` - BLE service and characteristic definitions
- `BINARY_PROTOCOL.md` - Message format specifications
- `ALBUM_ART.md` - Album art transfer mechanism
- `README.md` - High-level architecture overview