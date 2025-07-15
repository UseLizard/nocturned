# BLE Scanning Fix Summary

## Problem Identified
The nocturned BLE client was failing to connect to NocturneCompanion because:
1. It only checked already paired devices
2. It tried to check for GATT services without connecting first
3. It lacked BLE scanning functionality to discover advertising devices

## Key Differences: SPP vs BLE
- **SPP**: Services are visible on paired devices without connection
- **BLE**: GATT services are only visible after connecting to the device OR the service UUID must be advertised

## Changes Implemented

### 1. Enhanced Discovery Flow (`discoverNocturneCompanion`)
- First checks paired devices by name
- If not found, starts BLE scanning using BlueZ's StartDiscovery
- Monitors discovered devices during scan
- Looks for devices by name or advertising the Nordic UART Service UUID

### 2. Added BLE Scanning Methods
- `startDiscovery()`: Starts BLE-only discovery with proper filters
- `stopDiscovery()`: Cleanly stops the discovery process
- `monitorDiscoveredDevices()`: Polls for newly discovered devices during scan

### 3. Improved Connection Flow (`connectBLE`)
- Properly handles device connection before GATT service discovery
- Waits for connection to establish with retry logic
- Handles "InProgress" connection states

### 4. Enhanced Service Discovery (`discoverGattService`)
- Waits for ServicesResolved property before scanning
- Provides better diagnostics with service count
- More robust error handling

## Testing Instructions

1. Build the updated nocturned:
   ```bash
   cd nocturned
   go build -o nocturned .
   ```

2. Run nocturned with debug output:
   ```bash
   ./nocturned
   ```

3. On Android device:
   - Ensure NocturneCompanion is running
   - BLE server should be advertising
   - Device name should be "NocturneCompanion"

4. Monitor logs for:
   - "BLE_LOG: Starting BLE discovery..."
   - "BLE_LOG: Discovered device: NocturneCompanion"
   - "BLE_LOG: Found NocturneCompanion by name"
   - "BLE_LOG: Device connected successfully"
   - "BLE_LOG: Found Nocturne service"

## Expected Behavior
1. nocturned will first check paired devices
2. If not found, it will start BLE scanning
3. When NocturneCompanion is discovered, it will connect
4. After connection, GATT services will be discovered
5. Nordic UART Service will be found and characteristics set up
6. Media control commands can then be sent/received

## Troubleshooting
- Ensure Bluetooth is enabled on both devices
- Check that NocturneCompanion BLE server is running
- Verify the device name is exactly "NocturneCompanion"
- Check BlueZ version supports BLE (5.50+)
- Use `bluetoothctl` to verify device is advertising