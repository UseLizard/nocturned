# Android Time Sync Implementation Guide

## Overview
The nocturned service now supports receiving time synchronization messages from the NocturneCompanion Android app via BLE. This allows the Car Thing device to automatically sync its system time when connected to the Android device.

## Message Format
The Android app should send a JSON message with type `timeSync` to the STATE_TX characteristic:

```json
{
  "type": "timeSync",
  "timestamp_ms": 1736899200000,  // Current time in milliseconds since epoch
  "timezone": "America/New_York"   // Optional: IANA timezone identifier
}
```

## Android Implementation Example

### 1. Add Time Sync Function (in NocturneServiceBLE.kt)
```kotlin
private fun sendTimeSync() {
    val timeSync = mapOf(
        "type" to "timeSync",
        "timestamp_ms" to System.currentTimeMillis(),
        "timezone" to TimeZone.getDefault().id
    )
    
    bleServerManager.sendStateUpdate(timeSync)
    debugLogger.info(
        DebugLogger.LogType.TIME_SYNC,
        "Time sync sent",
        mapOf(
            "timestamp" to System.currentTimeMillis(),
            "timezone" to TimeZone.getDefault().id
        )
    )
}
```

### 2. Send Time Sync on Connection (in handleDeviceConnected)
After sending capabilities, add:
```kotlin
// Send time sync after a short delay
handler.postDelayed({
    sendTimeSync()
}, 200)
```

### 3. Optional: Periodic Time Sync
You can optionally send time sync periodically (e.g., every hour) to keep the device time accurate:
```kotlin
private val timeSyncHandler = Handler(Looper.getMainLooper())
private val timeSyncRunnable = object : Runnable {
    override fun run() {
        if (connectedDevices.isNotEmpty()) {
            sendTimeSync()
        }
        // Schedule next sync in 1 hour
        timeSyncHandler.postDelayed(this, 3600000)
    }
}

// Start periodic sync in onCreate()
timeSyncHandler.postDelayed(timeSyncRunnable, 3600000)

// Stop in onDestroy()
timeSyncHandler.removeCallbacks(timeSyncRunnable)
```

## How It Works

1. **Android sends time sync** → BLE notification with current time and timezone
2. **nocturned receives** → Parses the timeSync message
3. **System time updated** → Uses `date -s @<timestamp>` command
4. **Hardware clock synced** → Uses `hwclock -w` to persist time
5. **UI notified** → WebSocket event triggers immediate time display update

## Benefits

- **Automatic time sync** - No manual time setting required
- **Timezone support** - Correct timezone from Android device
- **Survives reboots** - Hardware clock sync persists time
- **Real-time updates** - UI updates immediately when time is synced

## Testing

1. Build and deploy nocturned with time sync support
2. Add time sync to Android app
3. Connect via BLE
4. Check nocturned logs for: "BLE_LOG: System time updated successfully"
5. Verify time display in UI matches Android device time