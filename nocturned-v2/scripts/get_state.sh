#!/bin/bash

# Get nocturned-v2 state information from Car Thing device
# Usage: ./get_state.sh [endpoint]

# Device connection details
DEVICE_IP="172.16.42.2"
DEVICE_USER="root"
DEVICE_PASS="nocturne"
NOCTURNED_PORT="5000"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
GRAY='\033[0;37m'
NC='\033[0m' # No Color

# Default endpoint
ENDPOINT="${1:-status}"

# Available endpoints
declare -A ENDPOINTS=(
    ["status"]="/api/v2/status"
    ["health"]="/api/v2/health"
    ["bluetooth"]="/api/v2/bluetooth/status"
    ["media"]="/api/v2/media/current"
    ["v1-media"]="/media/status"
    ["v1-info"]="/info"
    ["v1-date"]="/device/date"
    ["all"]="all"
)

# Function to make HTTP request and format JSON output
make_request() {
    local endpoint_path="$1"
    local endpoint_name="$2"
    
    echo -e "${BLUE}📡 Requesting: ${WHITE}$endpoint_name${NC} ${GRAY}($endpoint_path)${NC}"
    
    # Make the HTTP request via SSH
    local response=$(sshpass -p "$DEVICE_PASS" ssh -o StrictHostKeyChecking=no "$DEVICE_USER@$DEVICE_IP" \
        "wget -q -O - --timeout=5 http://localhost:$NOCTURNED_PORT$endpoint_path 2>/dev/null")
    
    local exit_code=$?
    
    if [ $exit_code -eq 0 ] && [ -n "$response" ]; then
        echo -e "${GREEN}✅ Response received${NC}"
        
        # Try to format JSON if jq is available, otherwise show raw
        if command -v jq &> /dev/null; then
            echo "$response" | jq . 2>/dev/null || echo "$response"
        else
            # Basic JSON formatting without jq
            echo "$response" | python3 -m json.tool 2>/dev/null || echo "$response"
        fi
    else
        echo -e "${RED}❌ Request failed (exit code: $exit_code)${NC}"
        if [ -n "$response" ]; then
            echo -e "${YELLOW}Response: $response${NC}"
        fi
    fi
    
    echo -e "${CYAN}===========================================${NC}"
}

# Function to show usage
show_usage() {
    echo -e "${GREEN}🔍 Nocturned-v2 State Information Tool${NC}"
    echo
    echo -e "${WHITE}Usage:${NC} $0 [endpoint]"
    echo
    echo -e "${WHITE}Available endpoints:${NC}"
    echo -e "  ${CYAN}status${NC}      - Overall system status (default)"
    echo -e "  ${CYAN}health${NC}      - Health check information"
    echo -e "  ${CYAN}bluetooth${NC}   - Bluetooth connection status"
    echo -e "  ${CYAN}media${NC}       - Current media information"
    echo -e "  ${CYAN}v1-media${NC}    - V1 compatible media status"
    echo -e "  ${CYAN}v1-info${NC}     - V1 compatible system info"
    echo -e "  ${CYAN}v1-date${NC}     - V1 compatible date/time"
    echo -e "  ${CYAN}all${NC}         - Query all endpoints"
    echo
    echo -e "${WHITE}Examples:${NC}"
    echo -e "  $0                # Get system status"
    echo -e "  $0 bluetooth      # Get Bluetooth status"
    echo -e "  $0 all            # Get all information"
}

# Function to check service status
check_service_status() {
    echo -e "${BLUE}🔍 Checking nocturned service status...${NC}"
    
    local service_status=$(sshpass -p "$DEVICE_PASS" ssh -o StrictHostKeyChecking=no "$DEVICE_USER@$DEVICE_IP" \
        "sv status nocturned 2>/dev/null" 2>/dev/null)
    
    if [ -n "$service_status" ]; then
        if echo "$service_status" | grep -q "run:"; then
            echo -e "${GREEN}✅ Service is running${NC}"
            echo -e "${GRAY}   $service_status${NC}"
        else
            echo -e "${RED}❌ Service is not running${NC}"
            echo -e "${GRAY}   $service_status${NC}"
        fi
    else
        echo -e "${YELLOW}⚠️  Could not check service status${NC}"
    fi
    
    echo -e "${CYAN}===========================================${NC}"
}

# Function to test connectivity
test_connectivity() {
    echo -e "${BLUE}🔗 Testing connectivity to Car Thing...${NC}"
    
    # Test SSH connection
    if sshpass -p "$DEVICE_PASS" ssh -o ConnectTimeout=5 -o StrictHostKeyChecking=no "$DEVICE_USER@$DEVICE_IP" "echo 'SSH OK'" &>/dev/null; then
        echo -e "${GREEN}✅ SSH connection successful${NC}"
    else
        echo -e "${RED}❌ SSH connection failed${NC}"
        echo -e "${YELLOW}Please check:${NC}"
        echo -e "  1. Device is powered on and connected"
        echo -e "  2. IP address is correct: $DEVICE_IP"
        echo -e "  3. SSH service is running"
        exit 1
    fi
    
    # Test if nocturned port is listening
    local port_check=$(sshpass -p "$DEVICE_PASS" ssh -o StrictHostKeyChecking=no "$DEVICE_USER@$DEVICE_IP" \
        "netstat -ln | grep :$NOCTURNED_PORT || ss -ln | grep :$NOCTURNED_PORT" 2>/dev/null)
    
    if [ -n "$port_check" ]; then
        echo -e "${GREEN}✅ Nocturned service is listening on port $NOCTURNED_PORT${NC}"
    else
        echo -e "${YELLOW}⚠️  Port $NOCTURNED_PORT not found - service may not be running${NC}"
    fi
    
    echo -e "${CYAN}===========================================${NC}"
}

# Check if sshpass is installed
if ! command -v sshpass &> /dev/null; then
    echo -e "${RED}Error: sshpass is not installed${NC}"
    echo "Please install sshpass: sudo apt-get install sshpass"
    exit 1
fi

# Handle help request
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    show_usage
    exit 0
fi

# Validate endpoint
if [ "$ENDPOINT" != "all" ] && [ -z "${ENDPOINTS[$ENDPOINT]}" ]; then
    echo -e "${RED}❌ Unknown endpoint: $ENDPOINT${NC}"
    echo
    show_usage
    exit 1
fi

# Header
echo -e "${GREEN}🚀 Nocturned-v2 State Information${NC}"
echo -e "${BLUE}Device: ${WHITE}$DEVICE_USER@$DEVICE_IP:$NOCTURNED_PORT${NC}"
echo -e "${BLUE}Timestamp: ${WHITE}$(date)${NC}"
echo -e "${CYAN}===========================================${NC}"

# Test connectivity first
test_connectivity

# Check service status
check_service_status

# Make requests
if [ "$ENDPOINT" = "all" ]; then
    echo -e "${MAGENTA}📋 Querying all endpoints...${NC}"
    echo
    
    for key in status health bluetooth media v1-media v1-info v1-date; do
        if [ -n "${ENDPOINTS[$key]}" ]; then
            make_request "${ENDPOINTS[$key]}" "$key"
        fi
    done
else
    make_request "${ENDPOINTS[$ENDPOINT]}" "$ENDPOINT"
fi

echo -e "${GREEN}🏁 State query complete${NC}"