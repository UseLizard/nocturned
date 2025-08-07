#\!/bin/bash

set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}[INFO]${NC} Deploying fixed service configuration to Car Thing..."

# Mount as read-write
echo -e "${GREEN}[INFO]${NC} Mounting filesystem as read-write..."
sshpass -p "nocturne" ssh root@172.16.42.2 "mount -o remount,rw /"

# Deploy service files
echo -e "${GREEN}[INFO]${NC} Deploying service configurations..."
sshpass -p "nocturne" scp -r device-configs/etc/sv/nocturned root@172.16.42.2:/etc/sv/
sshpass -p "nocturne" scp -r device-configs/etc/sv/bluetooth_adapter root@172.16.42.2:/etc/sv/
sshpass -p "nocturne" scp -r device-configs/etc/sv/chromium root@172.16.42.2:/etc/sv/

# Fix permissions
echo -e "${GREEN}[INFO]${NC} Setting permissions..."
sshpass -p "nocturne" ssh root@172.16.42.2 "
chmod +x /etc/sv/nocturned/run
chmod +x /etc/sv/nocturned/log/run
chmod +x /etc/sv/bluetooth_adapter/run
chmod +x /etc/sv/chromium/run
"

# Ensure service is enabled
echo -e "${GREEN}[INFO]${NC} Ensuring nocturned is enabled..."
sshpass -p "nocturne" ssh root@172.16.42.2 "
ln -sf /etc/sv/nocturned /etc/runit/runsvdir/default/nocturned 2>/dev/null || true
"

# Deploy the binary
echo -e "${GREEN}[INFO]${NC} Deploying nocturned binary..."
sshpass -p "nocturne" scp nocturned root@172.16.42.2:/usr/local/bin/nocturned
sshpass -p "nocturne" scp nocturned root@172.16.42.2:/usr/local/sbin/nocturned
sshpass -p "nocturne" ssh root@172.16.42.2 "chmod +x /usr/local/bin/nocturned /usr/local/sbin/nocturned"

# Mount as read-only
echo -e "${GREEN}[INFO]${NC} Remounting as read-only..."
sshpass -p "nocturne" ssh root@172.16.42.2 "mount -o remount,ro /" || true

echo -e "${GREEN}[INFO]${NC} Service configuration deployed\!"
echo -e "${GREEN}[INFO]${NC} Please reboot to test: ${YELLOW}sshpass -p 'nocturne' ssh root@172.16.42.2 'reboot'${NC}"
