#\!/bin/bash

set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}[INFO]${NC} Deploying nocturned with logging configuration..."

# Deploy the run scripts
echo -e "${GREEN}[INFO]${NC} Deploying service configuration..."
sshpass -p "nocturne" ssh root@172.16.42.2 "mount -o remount,rw /"
sshpass -p "nocturne" scp device-configs/etc/sv/nocturned/run root@172.16.42.2:/etc/sv/nocturned/run
sshpass -p "nocturne" ssh root@172.16.42.2 "chmod +x /etc/sv/nocturned/run"
sshpass -p "nocturne" ssh root@172.16.42.2 "mkdir -p /etc/sv/nocturned/log"
sshpass -p "nocturne" scp device-configs/etc/sv/nocturned/log/run root@172.16.42.2:/etc/sv/nocturned/log/run
sshpass -p "nocturne" ssh root@172.16.42.2 "chmod +x /etc/sv/nocturned/log/run"

# Create log directory
echo -e "${GREEN}[INFO]${NC} Creating log directory..."
sshpass -p "nocturne" ssh root@172.16.42.2 "mkdir -p /var/nocturne/logs/nocturned"

# Deploy the binary
echo -e "${GREEN}[INFO]${NC} Deploying nocturned binary..."
./deploy_nocturned.sh

echo -e "${GREEN}[INFO]${NC} Deployment complete\! Logs will be available at /var/nocturne/logs/nocturned/current"
echo -e "${GREEN}[INFO]${NC} To view logs: sshpass -p 'nocturne' ssh root@172.16.42.2 'tail -f /var/nocturne/logs/nocturned/current'"
