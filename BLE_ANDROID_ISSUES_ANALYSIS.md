# BLE Android Issues Analysis

## Issues Identified

### 1. Media Controller Not Available
**Problem**: Commands are received but not executed because `currentMediaController` is null.

**Root Cause**:
- The NotificationListenerService might not have proper permissions
- Media sessions aren't being detected
- The media controller callback isn't set up before commands arrive

**Evidence**:
```kotlin
// Line 240 in NocturneServiceBLE.kt
} ?: run {
    Log.w(TAG, "No active media controller to handle command: ${command.command}")
}
```

### 2. Volume Control Crashes
**Problem**: Volume wheel commands crash the BLE server.

**Possible Causes**:
- Rapid volume changes overwhelming the BLE write queue
- AudioManager permissions issue
- Division by zero when calculating volume percentage

### 3. Connection Instability
**Problem**: Connection drops after initial data exchange.

**Possible Causes**:
- MTU negotiation issues
- Notification sending failures
- D-Bus signal handling problems in nocturned

## Solutions

### 1. Fix Media Controller Detection

**Check NotificationListenerService is enabled**:
```bash
# On Android device
adb shell cmd notification list_approved_listeners | grep nocturne
```

**Add defensive null checks and retry logic**:
```kotlin
private fun handleCommand(command: Command) {
    Log.d(TAG, "Handling command: ${command.command}")
    
    // If no controller, try to update and retry once
    if (currentMediaController == null) {
        Log.w(TAG, "No media controller available, attempting to find one...")
        updateActiveMediaSession()
        Thread.sleep(100) // Brief delay
    }
    
    currentMediaController?.let { controller ->
        // Execute command
    } ?: run {
        Log.e(TAG, "STILL no active media controller for command: ${command.command}")
        // Send error response back via BLE
    }
}
```

### 2. Fix Volume Control

**Add rate limiting**:
```kotlin
private var lastVolumeChange = 0L
private val VOLUME_CHANGE_THROTTLE = 100L // ms

"set_volume" -> {
    val now = System.currentTimeMillis()
    if (now - lastVolumeChange < VOLUME_CHANGE_THROTTLE) {
        Log.d(TAG, "Throttling volume change")
        return
    }
    lastVolumeChange = now
    
    command.value_percent?.let { percent ->
        try {
            val maxVolume = audioManager.getStreamMaxVolume(AudioManager.STREAM_MUSIC)
            if (maxVolume > 0) {
                val targetVolume = (maxVolume * percent / 100).coerceIn(0, maxVolume)
                audioManager.setStreamVolume(AudioManager.STREAM_MUSIC, targetVolume, 0)
            }
        } catch (e: Exception) {
            Log.e(TAG, "Error setting volume", e)
        }
    }
}
```

### 3. Fix Connection Stability

**In nocturned ble_client.go**:
- Add keepalive mechanism
- Handle notification failures gracefully
- Implement reconnection logic

**In Android EnhancedBleServerManager**:
- Check notification success
- Handle device disconnection properly
- Add connection state monitoring

## Testing Steps

1. **Enable NotificationListener**:
   ```
   Settings > Apps > Special app access > Device & app notifications > NocturneCompanion
   ```

2. **Test with music app open**:
   - Start music app (Spotify, YouTube Music, etc.)
   - Start playing a song
   - Then connect nocturned

3. **Debug commands**:
   ```bash
   # Monitor Android logs
   adb logcat | grep -E "NocturneService|NotificationListener|BLE"
   ```

4. **Test volume gradually**:
   - Use small increments
   - Check for crashes in logs

## Quick Fixes to Try

1. **Force update media session**:
   - Add a "refresh" command that calls updateActiveMediaSession()

2. **Add media session fallback**:
   - If no controller found, try to get system default media session

3. **Add connection keepalive**:
   - Send periodic "ping" commands from nocturned
   - Respond with "pong" from Android