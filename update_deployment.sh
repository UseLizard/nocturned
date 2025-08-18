#!/bin/bash

# Quick update script for nocturned v2 - rebuilds and redeploys

set -e

echo "========================================"
echo "Nocturned v2 Quick Update"
echo "========================================"

# Build new binary
echo "Building updated binary..."
GOOS=linux GOARCH=arm64 go build -o nocturned main_v2.go

# Deploy using existing script
echo "Deploying updated binary..."
./deploy_nocturned.sh

echo ""
echo "========================================"
echo "Update completed!"
echo "========================================"
echo "You can test the API with:"
echo "  ./test_api.sh"
echo ""
echo "Or check logs with:"
echo "  ssh root@172.16.42.2 'tail -f /var/nocturne/nocturned_v2.log'"