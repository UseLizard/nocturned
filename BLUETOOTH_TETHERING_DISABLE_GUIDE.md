# Bluetooth Internet Tethering Disable Guide

## Problem Summary

When pairing Android devices with the Car Thing, **internet connection sharing happens automatically at the system level**, bypassing nocturned's profile management entirely. This occurs through NetworkManager, which automatically detects and connects NAP (Network Access Point) profiles during device pairing.

## Root Cause Analysis

### What's Actually Happening

1. **Device Pairing**: BlueZ with `JustWorksRepairing=always` auto-accepts pairing
2. **Profile Discovery**: BlueZ automatically queries device services during pairing
3. **NetworkManager Detection**: NetworkManager detects NAP profile (`00001116-0000-1000-8000-00805f9b34fb`)
4. **Automatic Connection**: NetworkManager creates and activates a Bluetooth network connection
5. **Internet Sharing**: Car Thing gets IP `192.168.44.33/24` with gateway `192.168.44.1` (Android device)

### System Architecture

```
Android Device (Hotspot)
    ↓ [Bluetooth NAP/PANU]
NetworkManager (System Level) ← NOT controlled by nocturned
    ↓ [Creates bnep0 interface]
Car Thing (Internet Access)
```

### Key Components Involved

1. **BlueZ Daemon** (`bluetoothd`): Handles low-level Bluetooth operations
2. **NetworkManager**: Automatically manages network connections including Bluetooth
3. **nocturned**: Application-level Bluetooth management (SPP, media control)

**The Issue**: NetworkManager operates independently of nocturned's profile settings.

## Current System State

### Active NetworkManager Connection
```
NAME: motorola edge 2024 (XT2405V) Network
UUID: 53aa3709-88d4-4982-b786-72abdd51f0f4
TYPE: bluetooth (panu)
STATE: activated
IP: 192.168.44.33/24
GATEWAY: 192.168.44.1
```

### Configuration Files Locations

| Component | Config File | Purpose |
|-----------|-------------|---------|
| BlueZ | `/etc/bluetooth/main.conf` | Bluetooth daemon settings |
| NetworkManager | `/etc/NetworkManager/NetworkManager.conf` | Network management policy |
| NetworkManager Connections | `/etc/NetworkManager/system-connections/` | Stored connection profiles |
| BlueZ Device Data | `/var/lib/bluetooth/[adapter]/[device]/info` | Paired device information |

## Solutions Available

### Option 1: Delete NetworkManager Bluetooth Connection (Temporary)
- **Action**: Remove the specific Bluetooth network connection
- **Command**: `nmcli connection delete "motorola edge 2024 (XT2405V) Network"`
- **Effect**: Prevents this specific device from creating internet connection
- **Limitation**: Will recreate on next pairing

### Option 2: Disable NetworkManager Bluetooth Plugin (Permanent)
- **Action**: Prevent NetworkManager from managing any Bluetooth connections
- **Effect**: Completely disables automatic Bluetooth networking
- **Risk**: Low - nocturned handles SPP connections independently

### Option 3: Configure NetworkManager Policy (Selective)
- **Action**: Set NetworkManager to ignore Bluetooth devices or require manual activation
- **Effect**: Bluetooth connections available but not automatic
- **Flexibility**: Can be configured per-device or globally

## Recommended Approach: Option 2 (Disable Bluetooth Plugin)

### Files to Backup Before Modification

```bash
# Create backup directory
mkdir -p ~/nocturne-bluetooth-backups/$(date +%Y%m%d_%H%M%S)

# Backup critical configuration files
sshpass -p "nocturne" scp root@172.16.42.2:/etc/NetworkManager/NetworkManager.conf ~/nocturne-bluetooth-backups/$(date +%Y%m%d_%H%M%S)/
sshpass -p "nocturne" scp root@172.16.42.2:/etc/bluetooth/main.conf ~/nocturne-bluetooth-backups/$(date +%Y%m%d_%H%M%S)/
sshpass -p "nocturne" scp -r root@172.16.42.2:/etc/NetworkManager/system-connections/ ~/nocturne-bluetooth-backups/$(date +%Y%m%d_%H%M%S)/

# Backup current network state
sshpass -p "nocturne" ssh root@172.16.42.2 "nmcli connection show" > ~/nocturne-bluetooth-backups/$(date +%Y%m%d_%H%M%S)/current_connections.txt
sshpass -p "nocturne" ssh root@172.16.42.2 "nmcli device status" > ~/nocturne-bluetooth-backups/$(date +%Y%m%d_%H%M%S)/current_devices.txt
```

### Configuration Changes Required

#### 1. NetworkManager Configuration (`/etc/NetworkManager/NetworkManager.conf`)

**Current State**: Default configuration allows all plugins
**Required Change**: Disable Bluetooth plugin

```ini
[main]
plugins=keyfile
no-auto-default=*

[device]
# Disable Bluetooth networking
bluetooth.scan-now=false

[connection]
# Prevent automatic Bluetooth connections
bluetooth.autoconnect=false
```

#### 2. Alternative: Per-Device Policy

Instead of disabling globally, create a policy to ignore Bluetooth devices:

```ini
[device-bluetooth-ignore]
match-device=type:bluetooth
managed=false
```

### Implementation Steps

1. **Backup Current Configuration** (see commands above)
2. **Remount Filesystem**: `mount -o remount,rw /`
3. **Modify NetworkManager Config**: Edit `/etc/NetworkManager/NetworkManager.conf`
4. **Remove Existing Connection**: `nmcli connection delete "motorola edge 2024 (XT2405V) Network"`
5. **Restart NetworkManager**: `rc-service networkmanager restart`
6. **Verify Changes**: Check that Bluetooth devices don't auto-connect
7. **Test Functionality**: Ensure SPP/media control still works via nocturned
8. **Remount Read-Only**: `mount -o remount,ro /`

### Testing Validation

After changes, verify:

1. **SPP Connection Works**: NocturneCompanion can still connect for media control
2. **No Internet Sharing**: `ip route` shows no routes via `bnep0`
3. **No Bluetooth Networks**: `nmcli connection show` has no Bluetooth connections
4. **Device Pairing Works**: Devices can still pair normally
5. **nocturned API Works**: `/bluetooth/status` returns correct information

## Recovery Plan

### If Changes Break Functionality

```bash
# 1. Remount filesystem
sshpass -p "nocturne" ssh root@172.16.42.2 "mount -o remount,rw /"

# 2. Restore original NetworkManager config
sshpass -p "nocturne" scp ~/nocturne-bluetooth-backups/[timestamp]/NetworkManager.conf root@172.16.42.2:/etc/NetworkManager/

# 3. Restore connection profiles
sshpass -p "nocturne" scp -r ~/nocturne-bluetooth-backups/[timestamp]/system-connections/* root@172.16.42.2:/etc/NetworkManager/system-connections/

# 4. Restart services
sshpass -p "nocturne" ssh root@172.16.42.2 "rc-service networkmanager restart"
sshpass -p "nocturne" ssh root@172.16.42.2 "rc-service nocturned restart"

# 5. Remount read-only
sshpass -p "nocturne" ssh root@172.16.42.2 "mount -o remount,ro /"
```

## Alternative Solutions (If NetworkManager Approach Fails)

### 1. BlueZ Profile Filtering
Modify BlueZ configuration to not advertise or accept NAP connections:

```ini
# /etc/bluetooth/main.conf
[General]
Disable=network
```

### 2. Firewall-Based Approach
Use iptables to block traffic on `bnep0` interface while keeping interface up

### 3. nocturned Integration
Modify nocturned to actively monitor and disconnect network profiles when detected

## Expected Outcome

After implementing the recommended solution:

✅ **Bluetooth pairing continues to work normally**  
✅ **SPP connections for media control remain functional**  
✅ **NocturneCompanion communication unaffected**  
❌ **No automatic internet connection sharing**  
❌ **No `bnep0` interface creation during device connections**  
❌ **No routes via Bluetooth network**

## Monitoring Commands

Use these commands to monitor the system state:

```bash
# Check NetworkManager connections
sshpass -p "nocturne" ssh root@172.16.42.2 "nmcli connection show"

# Check active network interfaces
sshpass -p "nocturne" ssh root@172.16.42.2 "ip link show"

# Check routing table
sshpass -p "nocturne" ssh root@172.16.42.2 "ip route"

# Check Bluetooth device connections
sshpass -p "nocturne" ssh root@172.16.42.2 "bluetoothctl info [device-address]"

# Test nocturned functionality
curl http://172.16.42.2:5000/bluetooth/status
```

## Risk Assessment

| Risk Level | Component | Impact | Mitigation |
|------------|-----------|---------|------------|
| **Low** | SPP Connections | No change - handled by nocturned | N/A |
| **Low** | Media Control | No change - uses SPP protocol | N/A |
| **Low** | Device Pairing | No change - handled by BlueZ | N/A |
| **Medium** | Network Recovery | Manual config restore needed | Backup files |
| **Low** | System Stability | NetworkManager restart required | Standard service restart |

## Notes

- This modification affects **system-level networking only**
- **nocturned functionality remains completely intact**
- Changes are **reversible** with proper backups
- **No code changes** required in nocturned itself
- Solution addresses the **root cause** rather than symptoms