#!/bin/bash

# Stream nocturned-v2 logs from Car Thing device
# Usage: ./stream_logs.sh

# Device connection details
DEVICE_IP="172.16.42.2"
DEVICE_USER="root"
DEVICE_PASS="nocturne"
LOG_PATH="/var/nocturne/nocturned_v2.log"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
NC='\033[0m' # No Color

# Function to colorize log output based on content
colorize_log() {
    while IFS= read -r line; do
        case "$line" in
            *"✅"*|*"Connected to Nocturne"*|*"BLE connection setup complete"*|*"subscriptions active"*)
                echo -e "${GREEN}$line${NC}"
                ;;
            *"❌"*|*"Failed"*|*"Error"*|*"error"*|*"ERROR"*)
                echo -e "${RED}$line${NC}"
                ;;
            *"⚠️"*|*"Warning"*|*"warning"*|*"WARN"*)
                echo -e "${YELLOW}$line${NC}"
                ;;
            *"🔗"*|*"🔧"*|*"Attempting"*|*"Starting"*|*"Scanning"*)
                echo -e "${BLUE}$line${NC}"
                ;;
            *"📨"*|*"📤"*|*"Received"*|*"Sending"*)
                echo -e "${CYAN}$line${NC}"
                ;;
            *"📅"*|*"TimeSync"*|*"TIME_SYNC"*)
                echo -e "${MAGENTA}$line${NC}"
                ;;
            *"🌤️"*|*"Weather"*|*"weather"*)
                echo -e "${CYAN}$line${NC}"
                ;;
            *"FOUND NOCTURNE DEVICE"*)
                echo -e "${WHITE}$line${NC}"
                ;;
            *)
                echo "$line"
                ;;
        esac
    done
}

# Check if sshpass is installed
if ! command -v sshpass &> /dev/null; then
    echo -e "${RED}Error: sshpass is not installed${NC}"
    echo "Please install sshpass: sudo apt-get install sshpass"
    exit 1
fi

# Function to handle cleanup on script exit
cleanup() {
    echo -e "\n${YELLOW}Stopping log stream...${NC}"
    exit 0
}

# Set up signal handlers
trap cleanup SIGINT SIGTERM

echo -e "${GREEN}🚀 Streaming nocturned-v2 logs from Car Thing device...${NC}"
echo -e "${BLUE}Device: ${DEVICE_USER}@${DEVICE_IP}${NC}"
echo -e "${BLUE}Log file: ${LOG_PATH}${NC}"
echo -e "${YELLOW}Press Ctrl+C to stop${NC}"
echo -e "${CYAN}===========================================${NC}"

# Test connection first
if ! sshpass -p "$DEVICE_PASS" ssh -o ConnectTimeout=5 -o StrictHostKeyChecking=no "$DEVICE_USER@$DEVICE_IP" "echo 'Connection test successful'" &>/dev/null; then
    echo -e "${RED}Error: Cannot connect to Car Thing device at $DEVICE_IP${NC}"
    echo "Please check:"
    echo "1. Device is powered on and connected to network"
    echo "2. IP address is correct: $DEVICE_IP"
    echo "3. SSH service is running on the device"
    exit 1
fi

# Stream the logs with tail -f
sshpass -p "$DEVICE_PASS" ssh -o StrictHostKeyChecking=no "$DEVICE_USER@$DEVICE_IP" "tail -f $LOG_PATH" | colorize_log