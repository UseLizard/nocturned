#\!/bin/bash

set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}[INFO]${NC} Deploying boot reliability fixes to Car Thing..."

# Mount as read-write
echo -e "${GREEN}[INFO]${NC} Mounting filesystem as read-write..."
sshpass -p "nocturne" ssh root@172.16.42.2 "mount -o remount,rw /"

# Deploy improved bluetooth_adapter service
echo -e "${GREEN}[INFO]${NC} Deploying improved bluetooth_adapter service..."
sshpass -p "nocturne" scp device-configs/etc/sv/bluetooth_adapter/run root@172.16.42.2:/etc/sv/bluetooth_adapter/run
sshpass -p "nocturne" ssh root@172.16.42.2 "chmod +x /etc/sv/bluetooth_adapter/run"

# Deploy improved nocturned service
echo -e "${GREEN}[INFO]${NC} Deploying improved nocturned service..."
sshpass -p "nocturne" scp device-configs/etc/sv/nocturned/run root@172.16.42.2:/etc/sv/nocturned/run
sshpass -p "nocturne" ssh root@172.16.42.2 "chmod +x /etc/sv/nocturned/run"

# Ensure log directory and service
echo -e "${GREEN}[INFO]${NC} Ensuring log service is configured..."
sshpass -p "nocturne" ssh root@172.16.42.2 "mkdir -p /etc/sv/nocturned/log"
if [ -f device-configs/etc/sv/nocturned/log/run ]; then
    sshpass -p "nocturne" scp device-configs/etc/sv/nocturned/log/run root@172.16.42.2:/etc/sv/nocturned/log/run
    sshpass -p "nocturne" ssh root@172.16.42.2 "chmod +x /etc/sv/nocturned/log/run"
fi

# Build and deploy the binary
echo -e "${GREEN}[INFO]${NC} Building nocturned binary..."
GOOS=linux GOARCH=arm64 go build -o nocturned

echo -e "${GREEN}[INFO]${NC} Deploying nocturned binary..."
sshpass -p "nocturne" scp nocturned root@172.16.42.2:/usr/local/bin/nocturned
sshpass -p "nocturne" scp nocturned root@172.16.42.2:/usr/local/sbin/nocturned
sshpass -p "nocturne" ssh root@172.16.42.2 "chmod +x /usr/local/bin/nocturned /usr/local/sbin/nocturned"

# Ensure service is enabled
echo -e "${GREEN}[INFO]${NC} Ensuring services are enabled..."
sshpass -p "nocturne" ssh root@172.16.42.2 "
ln -sf /run/runit/supervise.nocturned /etc/sv/nocturned/supervise 2>/dev/null || true
ln -sf /etc/sv/nocturned /etc/runit/runsvdir/default/nocturned 2>/dev/null || true
"

# Mount as read-only
echo -e "${GREEN}[INFO]${NC} Remounting as read-only..."
sshpass -p "nocturne" ssh root@172.16.42.2 "mount -o remount,ro /" || true

echo -e "${GREEN}[INFO]${NC} Boot reliability fixes deployed\!"
echo -e "${GREEN}[INFO]${NC} The system should now:"
echo -e "${GREEN}[INFO]${NC}   - Retry Bluetooth chip initialization up to 3 times"
echo -e "${GREEN}[INFO]${NC}   - Wait up to 45 seconds for Bluetooth to be ready"
echo -e "${GREEN}[INFO]${NC}   - Handle Bluetooth failures gracefully"
echo -e ""
echo -e "${YELLOW}[ACTION]${NC} Please reboot to test:"
echo -e "${YELLOW}[CMD]${NC} sshpass -p 'nocturne' ssh root@172.16.42.2 'reboot'"
