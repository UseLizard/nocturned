#!/bin/bash

# nocturned deployment script for Car Thing
# This script builds and deploys the nocturned service to a Car Thing device

set -e  # Exit on any error

# Configuration
CAR_THING_IP="172.16.42.2"
CAR_THING_PASSWORD="nocturne"
BINARY_NAME="nocturned"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

echo_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

echo_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if we're in the nocturned directory
if [ ! -f "main.go" ]; then
    echo_error "This script must be run from the nocturned directory"
    exit 1
fi

# Check if sshpass is available
if ! command -v sshpass &> /dev/null; then
    echo_error "sshpass is required but not installed. Install with: sudo apt install sshpass"
    exit 1
fi

echo_info "Starting nocturned deployment to Car Thing..."

# Step 1: Build the binary for ARM64
echo_info "Building nocturned binary for ARM64..."
GOOS=linux GOARCH=arm64 go build -o $BINARY_NAME .
if [ $? -eq 0 ]; then
    echo_info "Binary built successfully"
else
    echo_error "Failed to build binary"
    exit 1
fi

# Step 2: Test connection to Car Thing
echo_info "Testing connection to Car Thing ($CAR_THING_IP)..."
if ! sshpass -p "$CAR_THING_PASSWORD" ssh -o ConnectTimeout=5 -o StrictHostKeyChecking=no root@$CAR_THING_IP "echo 'Connection test successful'" > /dev/null 2>&1; then
    echo_error "Cannot connect to Car Thing at $CAR_THING_IP"
    echo_error "Make sure the device is powered on and connected to the network"
    exit 1
fi
echo_info "Connection successful"

# Step 3: Stop service and remount filesystem as read-write
echo_info "Stopping nocturned service..."
sshpass -p "$CAR_THING_PASSWORD" ssh root@$CAR_THING_IP "sv stop nocturned"

echo_info "Remounting Car Thing filesystem as read-write..."
sshpass -p "$CAR_THING_PASSWORD" ssh root@$CAR_THING_IP "mount -o remount,rw /"

# Step 4: Deploy binary to both locations
echo_info "Deploying binary to /usr/local/bin/..."
sshpass -p "$CAR_THING_PASSWORD" scp $BINARY_NAME root@$CAR_THING_IP:/usr/local/bin/

echo_info "Deploying binary to /usr/local/sbin/..."
sshpass -p "$CAR_THING_PASSWORD" scp $BINARY_NAME root@$CAR_THING_IP:/usr/local/sbin/

echo_info "Deploying binary to /usr/bin/..."
sshpass -p "$CAR_THING_PASSWORD" scp $BINARY_NAME root@$CAR_THING_IP:/usr/bin/

echo_info "Deploying binary to /usr/sbin/..."
sshpass -p "$CAR_THING_PASSWORD" scp $BINARY_NAME root@$CAR_THING_IP:/usr/sbin/

# Step 5: Make binaries executable
echo_info "Making binaries executable..."
sshpass -p "$CAR_THING_PASSWORD" ssh root@$CAR_THING_IP "chmod +x /usr/local/bin/$BINARY_NAME /usr/local/sbin/$BINARY_NAME /usr/bin/$BINARY_NAME /usr/sbin/$BINARY_NAME"

# Step 6: Start the service
echo_info "Starting nocturned service..."
START_OUTPUT=$(sshpass -p "$CAR_THING_PASSWORD" ssh root@$CAR_THING_IP "sv start nocturned" 2>&1)
echo_info "Service start output: $START_OUTPUT"

# Step 7: Verify service is running
echo_info "Verifying service status..."
SERVICE_STATUS=$(sshpass -p "$CAR_THING_PASSWORD" ssh root@$CAR_THING_IP "sv status nocturned" 2>&1)
echo_info "Service status: $SERVICE_STATUS"

sshpass -p "$CAR_THING_PASSWORD" ssh root@$CAR_THING_IP "sync"


# Step 8: Remount filesystem as read-only
echo_info "Remounting filesystem as read-only..."
sshpass -p "$CAR_THING_PASSWORD" ssh root@$CAR_THING_IP "mount -o remount,ro /"

# Step 9: Test API endpoint (optional)
echo_info "Testing API endpoint..."
if sshpass -p "$CAR_THING_PASSWORD" ssh root@$CAR_THING_IP "wget -q -O - --timeout=5 http://localhost:5000/bluetooth/status" > /dev/null 2>&1; then
    echo_info "API endpoint responding correctly"
else
    echo_warn "API endpoint test failed - service may still be starting"
fi

echo_info "Deployment completed successfully!"
echo_info "nocturned service is now running on Car Thing"
echo_info ""
echo_info "You can check the service status with:"
echo_info "  sshpass -p \"$CAR_THING_PASSWORD\" ssh root@$CAR_THING_IP \"sv status nocturned\""
echo_info ""
echo_info "You can view logs with:"
echo_info "  sshpass -p \"$CAR_THING_PASSWORD\" ssh root@$CAR_THING_IP \"sv log nocturned\""