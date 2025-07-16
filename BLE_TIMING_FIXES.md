# BLE Timing and ATT Error Fixes

## Problem Summary
1. State updates from Android were delayed by several seconds
2. Rapid play/pause commands caused ATT error 0x0e ("Unlikely Error")
3. Service would crash after receiving the ATT error

## Root Causes
1. **BLE Write Conflicts**: Trying multiple write modes rapidly when first fails
2. **No Rate Limiting**: Commands sent too quickly can overwhelm BLE stack
3. **Characteristic Busy**: ATT error 0x0e often means the GATT server is busy

## Fixes Applied

### 1. Simplified Write Strategy
- Use standard write-with-response mode first (most compatible)
- Only retry on specific ATT error 0x0e with a 100ms delay
- Don't try multiple write modes which can make things worse

### 2. Command Rate Limiting
- Added mutex to prevent concurrent writes
- Minimum 50ms between commands
- Tracks last command time

### 3. Connection Monitoring Improvements
- Increased health check interval from 30s to 60s
- Added failure counter (3 strikes before disconnect)
- Prevents premature disconnection

## Testing the Fixes

1. **Start nocturned with logging**:
   ```bash
   ./nocturned 2>&1 | grep -E "BLE_LOG|Rate limiting|ATT error"
   ```

2. **Test rapid commands**:
   - Play → Pause → Play quickly
   - Should see "Rate limiting - waiting Xms" messages
   - No more ATT errors

3. **Monitor state updates**:
   - Check if updates arrive more consistently
   - Look for "Received notification on Response TX" messages

## If Issues Persist

1. **Check Android side**:
   - Ensure notifications are sent immediately after state changes
   - Check for queuing/batching of notifications
   - Monitor `sendNotification` success in Android logs

2. **Increase rate limit if needed**:
   ```go
   const minCommandInterval = 100 * time.Millisecond  // Increase from 50ms
   ```

3. **Debug notification delays**:
   - Add timestamps to Android state updates
   - Compare with receive timestamps in nocturned
   - Check for notification queue buildup

## Expected Behavior
- Commands should execute within 100-200ms
- State updates should arrive within 1-2 seconds
- No ATT errors during normal operation
- Stable connection even with rapid commands