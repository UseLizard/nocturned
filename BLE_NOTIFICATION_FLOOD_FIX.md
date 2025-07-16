# BLE Notification Flood Fix

## Problem Identified
A feedback loop was causing infinite notifications after BLE pairing between nocturned and NocturneCompanion.

## Root Cause
The Android app's debug logging system created a feedback loop:

1. **State Update Sent**: App sends media state update notification
2. **Debug Log Created**: `sendNotification()` logs "Notification sent" via `debugLogger.verbose()`
3. **Debug Log Streamed**: `startDebugLogStreaming()` sends this debug log as a notification to DEBUG_LOG_CHAR_UUID subscribers
4. **Loop Continues**: Sending the debug log notification creates another "Notification sent" log, which gets sent as another notification, creating an infinite loop

## The Feedback Loop Code (Android App)
```kotlin
// In sendNotification():
if (success) {
    debugLogger.verbose(
        DebugLogger.LogType.NOTIFICATION_SENT,
        "Notification sent",  // <-- This creates a new log entry
        mapOf(...)
    )
}

// In startDebugLogStreaming():
debugLogger.logFlow.collect { logEntry ->
    // This sends ALL log entries as notifications, including "Notification sent" logs
    sendNotification(device, DEBUG_LOG_CHAR_UUID, json)
}
```

## Fix Applied (nocturned)
Disabled debug log notifications in nocturned to break the feedback loop:

1. **Commented out StartNotify for debug logs** in `setupCharacteristics()`
2. **Commented out D-Bus signal monitoring** for debug log characteristic in `monitorCharacteristicNotifications()`

## Files Modified
- `bluetooth/ble_client.go`: Disabled debug log notifications and monitoring

## Long-term Solution
The proper fix should be in the Android app:
- Modify `sendNotification()` to NOT create debug logs when sending debug log notifications
- Add a parameter like `isDebugLog: Boolean` to prevent recursive logging
- Or use a different logging mechanism that doesn't trigger notifications

## Testing
After building and deploying the fixed nocturned:
1. Pair with NocturneCompanion 
2. Connection should be stable without notification floods
3. Media state updates should work normally
4. Debug logs won't be received (expected with this fix)