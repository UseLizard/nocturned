# Time Sync Implementation Summary

## Changes Made

### 1. nocturned Service (Go)

#### utils/device.go
- Added `SetSystemTime(timestampMs int64)` function
  - Converts milliseconds to seconds
  - Uses `date -s @<seconds>` to set system time
  - Syncs hardware clock with `hwclock -w`

#### bluetooth/ble_client.go
- Added handler for `timeSync` message type
  - Parses timestamp_ms and timezone from JSON
  - Calls `SetSystemTime()` to update system time
  - Calls `SetTimezone()` if timezone provided
  - Broadcasts `system/time_updated` WebSocket event

### 2. nocturne-ui (React)

#### StatusBar.jsx
- Added WebSocket listener for `system/time_updated` events
- Updates timezone cache when received
- Forces immediate time display refresh
- Time automatically syncs when Android device connects

## Message Flow

```
Android Device                 nocturned                    nocturne-ui
     |                            |                             |
     |-- BLE: timeSync msg ------>|                             |
     |   {type: "timeSync",       |                             |
     |    timestamp_ms: ...,      |                             |
     |    timezone: "..."}        |                             |
     |                            |                             |
     |                      Set system time                     |
     |                      Set timezone                        |
     |                            |                             |
     |                            |-- WS: time_updated -------->|
     |                            |                             |
     |                            |                       Update display
```

## Testing Without Android App Changes

You can test the time sync by sending a test message via bluetoothctl:

```bash
# Connect to device
bluetoothctl
connect <device_address>

# Find the STATE_TX characteristic handle
select-attribute /org/bluez/hci0/dev_XX_XX_XX_XX_XX_XX/service00XX/char00XX

# Send time sync message (replace with current timestamp)
write "7b2274797065223a2274696d6553796e63222c2274696d657374616d705f6d73223a313733363839393230303030302c2274696d657a6f6e65223a22416d65726963612f4e65775f596f726b227d"
```

The hex string above represents:
```json
{"type":"timeSync","timestamp_ms":1736899200000,"timezone":"America/New_York"}
```

## Benefits

1. **Automatic** - Time syncs when Android connects
2. **Accurate** - Uses Android device time as source of truth  
3. **Timezone aware** - Handles timezone changes automatically
4. **Persistent** - Hardware clock sync survives reboots
5. **Real-time** - UI updates immediately via WebSocket