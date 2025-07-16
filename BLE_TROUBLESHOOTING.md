# BLE Media Control Troubleshooting Guide

## Common Issues and Solutions

### 1. Commands not reaching Android device

**Check BLE Write:**
- Run nocturned with: `./nocturned 2>&1 | grep BLE_LOG`
- Look for: "BLE_LOG: Writing X bytes to characteristic"
- If you see "ERROR - Failed to send BLE command", the write is failing

**Possible solutions:**
- The characteristic might need write-without-response mode
- Check Android logs to see if commands are received
- Use bluetoothctl to manually test writing to the characteristic

### 2. No media state updates in UI

**Check notification subscription:**
- Look for: "Successfully enabled notifications on Response TX characteristic"
- If you see "ERROR - failed to enable notifications", notifications aren't working

**Check if notifications are being received:**
- Look for: "Received BLE response data"
- Look for: "handleNotificationData" messages

**Possible issues:**
- Android app might not be sending notifications
- CCCD (Client Characteristic Config Descriptor) might not be set properly
- The characteristic might not support notifications

### 3. Testing with bluetoothctl

```bash
# Connect to device
bluetoothctl
connect XX:XX:XX:XX:XX:XX

# Check notifications
menu gatt
select-attribute 6e400003-b5a3-f393-e0a9-e50e24dcca9e
notify on

# Send test command
select-attribute 6e400002-b5a3-f393-e0a9-e50e24dcca9e
write "7B22636F6D6D616E64223A22706C6179227D"  # {"command":"play"}
```

### 4. Debug with Android Logs

On Android device:
```bash
adb logcat | grep -E "BLE|Nocturne|GATT|Bluetooth"
```

Look for:
- "Command received" messages
- "Sending state update" messages
- Any error messages

### 5. Common Write Options for BlueZ

The nocturned code now tries these write modes:
1. `"type": "command"` - Write without response
2. Empty options - Write with response

If both fail, check the characteristic properties in Android:
- PROPERTY_WRITE - Requires response
- PROPERTY_WRITE_NO_RESPONSE - No response needed

### 6. WebSocket Issues

Test WebSocket connection:
```bash
# Install wscat if needed
npm install -g wscat

# Connect to WebSocket
wscat -c ws://localhost:5000/ws

# You should see events when media state changes
```

### 7. Manual Testing Flow

1. Start nocturned with logging:
   ```bash
   ./nocturned 2>&1 | tee nocturned.log
   ```

2. In another terminal, run tests:
   ```bash
   ./test_ble_media.sh
   ```

3. Check logs for:
   - Connection success
   - Notification enable status
   - Command send status
   - Notification receive status

### 8. Check Characteristic UUIDs

Verify these match between Android and nocturned:
- Service: `6e400001-b5a3-f393-e0a9-e50e24dcca9e`
- Command RX: `6e400002-b5a3-f393-e0a9-e50e24dcca9e` (Write)
- State TX: `6e400003-b5a3-f393-e0a9-e50e24dcca9e` (Notify)

## Quick Diagnostic Commands

```bash
# Check if BLE is connected
curl http://localhost:5000/media/status | jq

# Test play command
curl -X POST http://localhost:5000/media/play

# Simulate media state (tests WebSocket)
curl -X POST http://localhost:5000/media/simulate \
  -H "Content-Type: application/json" \
  -d '{"artist":"Test","track":"Test Song","is_playing":true}'
```

## If Nothing Works

1. Restart Bluetooth service:
   ```bash
   sudo systemctl restart bluetooth
   ```

2. Check BlueZ version (need 5.50+):
   ```bash
   bluetoothctl --version
   ```

3. Enable BlueZ debug mode:
   ```bash
   sudo systemctl stop bluetooth
   sudo /usr/lib/bluetooth/bluetoothd -n -d
   ```