# Nocturned BLE Update Summary

## Changes Made

### 1. Updated BLE UUIDs (`bluetooth/ble_constants.go`)
- Changed from custom UUIDs to Nordic UART Service compatible UUIDs
- Service UUID: `6e400001-b5a3-f393-e0a9-e50e24dcca9e`
- Command RX: `6e400002-b5a3-f393-e0a9-e50e24dcca9e` (Write)
- Response TX: `6e400003-b5a3-f393-e0a9-e50e24dcca9e` (Notify)
- Debug Log: `6e400004-b5a3-f393-e0a9-e50e24dcca9e` (Notify)
- Device Info: `6e400005-b5a3-f393-e0a9-e50e24dcca9e` (Read)

### 2. Enhanced BLE Client (`bluetooth/ble_client.go`)

#### Discovery Changes
- No longer requires device to be "connected" at system level
- Looks for paired devices OR devices with GATT support
- Better handles BLE devices in advertising mode

#### Protocol Support
- Added support for new message types:
  - `ack`: Command acknowledgments
  - `error`: Error messages
  - `capabilities`: Device capability negotiation
  - `stateUpdate`: Media state updates
  - `debugLog`: Debug log entries

#### New Features
- Monitors debug log characteristic for real-time debugging
- Reads device info characteristic on connection
- Updates MTU from capabilities message
- Better error handling and logging

#### Removed Features
- Removed album art transfer support (not in new BLE implementation)

## Testing Procedure

### 1. Start NocturneCompanion App
1. Install the updated APK on Android device
2. Open the app (use Debug Activity for better visibility)
3. Press "Start BLE Server"
4. Should see "Advertising" status

### 2. Pair Devices (First Time Only)
```bash
# On Car Thing or test machine
bluetoothctl
power on
agent on
scan on
# Wait for NocturneCompanion to appear
pair <MAC_ADDRESS>
trust <MAC_ADDRESS>
exit
```

### 3. Start Nocturned Service
```bash
# Run with debug logging
DEBUG=1 ./nocturned

# Or with specific port
PORT=5000 DEBUG=1 ./nocturned
```

### 4. Monitor Logs
Look for these key log messages:
- `BLE_LOG: Scanning X devices for NocturneCompanion`
- `BLE_LOG: Found device with matching name`
- `BLE_LOG: Found Nocturne service at: /org/bluez/...`
- `BLE_LOG: Found Command RX characteristic`
- `BLE_LOG: Found Response TX characteristic`
- `BLE_LOG: Enabled notifications on Debug Log characteristic`
- `BLE Device Capabilities: Version 2.0, MTU 512`

### 5. Test Media Commands
```bash
# Test commands
curl -X POST http://localhost:5000/media/play
curl -X POST http://localhost:5000/media/pause
curl -X POST http://localhost:5000/media/next
curl -X POST http://localhost:5000/media/volume/75

# Check status
curl http://localhost:5000/media/status
```

### 6. Expected Responses
- Command acknowledgments in logs: `BLE ACK: cmd_X - success`
- Media state updates via WebSocket
- Debug logs streamed from Android device

## Troubleshooting

### No Device Found
1. Ensure NocturneCompanion is advertising (not just paired)
2. Check device name matches exactly: "NocturneCompanion"
3. Try unpairing and re-pairing

### Connection Fails
1. Check service UUID matches
2. Ensure BLE advertising is active on Android
3. Look for GATT service resolution errors

### No Notifications
1. Check characteristic UUIDs match
2. Verify notification enable succeeded
3. Check for D-Bus signal subscription errors

### Debug Mode
Enable verbose BLE logging:
```bash
export DEBUG=1
./nocturned
```

## Key Differences from SPP

1. **Connection Model**: BLE uses GATT services instead of RFCOMM sockets
2. **Discovery**: Looks for advertising devices, not just connected ones
3. **Protocol**: Enhanced with acks, errors, and capabilities
4. **Debugging**: Real-time debug log streaming via BLE
5. **MTU**: Dynamic MTU negotiation up to 512 bytes