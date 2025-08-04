#!/bin/bash

echo "Debugging test album art crash..."
echo "================================"

# Check if nocturned is running
echo -e "\n1. Checking nocturned service status:"
curl -s http://localhost:5000/info || echo "Service not responding"

# Check BLE connection status
echo -e "\n2. Checking BLE connection status:"
curl -s http://localhost:5000/media/ble/status | jq . || echo "Failed to get BLE status"

# Monitor logs while making request
echo -e "\n3. Starting log monitoring (press Ctrl+C to stop after crash)..."
echo "   Make the test album art request now..."

# Tail the service logs
journalctl -u nocturned -f --no-pager | grep -E "test_album_art|TEST_ALBUM|BLE_LOG|panic|error|Error"