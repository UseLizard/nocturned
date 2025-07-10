# Car Thing Device Configuration Backup

This directory contains backup copies of all service configuration files and system changes made to the Car Thing device during the nocturned SPP connection stability fixes.

## Service Scripts Modified

### nocturned Service (`/etc/sv/nocturned/run`)
- **Problem**: Service was using outdated binary path and had timing issues with Bluetooth initialization
- **Fix**: Updated to use `/usr/local/bin/nocturned`, added proper dependency checks, and hardware initialization delays
- **Dependencies**: `dbus`, `bluetoothd`, `bluetooth_adapter`
- **Key Changes**: 
  - 5-second hardware initialization delay
  - Bluetooth adapter readiness verification loop
  - Proper service dependency ordering

### chromium Service (`/etc/sv/chromium/run`)
- **Problem**: Started before nocturned was fully ready, causing WebSocket connection failures
- **Fix**: Added nocturned dependency and WebSocket readiness check
- **Key Changes**:
  - Added `sv check nocturned` dependency
  - Added WebSocket API readiness loop (checks `http://localhost:5000/bluetooth/status`)
  - Up to 20-second wait for nocturned to be fully ready

### bluetooth_adapter Service (`/etc/sv/bluetooth_adapter/run`)
- **Role**: Hardware-level Bluetooth chip initialization via GPIO and btattach
- **Dependencies**: `superbird_init`
- **Notes**: Critical for proper Bluetooth hardware setup before bluetoothd starts

### bluetoothd Service (`/etc/sv/bluetoothd/run`)
- **Role**: BlueZ Bluetooth daemon
- **Dependencies**: `dbus`
- **Notes**: Standard BlueZ configuration

## Service Dependency Chain

The final working dependency chain:
```
superbird_init → bluetooth_adapter → bluetoothd → nocturned → chromium
                                  ↗
                              dbus
```

## Deployment Process

To restore these configurations on a fresh Car Thing:

1. **Remount filesystem as read-write**:
   ```bash
   mount -o remount,rw /
   ```

2. **Copy service scripts**:
   ```bash
   cp device-configs/etc/sv/nocturned/run /etc/sv/nocturned/
   cp device-configs/etc/sv/chromium/run /etc/sv/chromium/
   chmod +x /etc/sv/nocturned/run /etc/sv/chromium/run
   ```

3. **Deploy nocturned binary**:
   ```bash
   cp nocturned /usr/local/bin/
   chmod +x /usr/local/bin/nocturned
   ```

4. **Restart services**:
   ```bash
   sv restart nocturned chromium
   ```

5. **Remount filesystem as read-only**:
   ```bash
   sync && mount -o remount,ro /
   ```

## Key Issues Resolved

1. **SPP Connection Stability**: Fixed by ensuring proper Bluetooth hardware initialization timing
2. **Service Boot Ordering**: Added proper dependency chains to prevent race conditions
3. **WebSocket Connectivity**: Ensured chromium waits for nocturned API to be ready
4. **Binary Path Issues**: Updated service to use correct nocturned binary location

## Files in This Backup

- `etc/sv/nocturned/run` - Main nocturned service script with timing fixes
- `etc/sv/nocturned/finish` - Service cleanup script (if exists)
- `etc/sv/chromium/run` - Chromium service with nocturned dependency
- `etc/sv/bluetooth_adapter/run` - Bluetooth hardware initialization
- `etc/sv/bluetoothd/run` - BlueZ daemon configuration
- `var/service/service-list.txt` - Complete service list and symlinks
- `service-status.txt` - Service status snapshot
- `README.md` - This documentation

## Troubleshooting

If SPP connections are unstable after applying these configs:

1. Check service startup order: `sv status`
2. Verify Bluetooth adapter: `bluetoothctl show`
3. Test nocturned API: `curl http://localhost:5000/bluetooth/status`
4. Check service dependencies: `sv check dbus bluetoothd bluetooth_adapter`

## Notes

- These configurations assume the nocturned binary is deployed to `/usr/local/bin/nocturned`
- The Car Thing uses runit (sv) for service management, not systemd
- All timing delays were empirically determined for reliable startup
- The finish script provides clean shutdown for Bluetooth connections