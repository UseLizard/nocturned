#!/bin/bash

echo "BLE Connection Debug Script"
echo "=========================="

# Check if nocturned is running
echo -e "\n1. Checking nocturned process:"
ps aux | grep -E "nocturned|BLE" | grep -v grep

# Check system logs for BLE errors
echo -e "\n2. Recent BLE logs from system:"
sudo journalctl -u bluetooth -n 20 --no-pager | grep -E "GATT|BLE|nocturne" || echo "No bluetooth service logs found"

# Run nocturned with debug output
echo -e "\n3. Starting nocturned with BLE debug output..."
echo "Press Ctrl+C to stop when done testing"
echo "Try these actions:"
echo "  - Check if you see 'BLE_LOG: Monitoring notifications...'"
echo "  - Send a play/pause command from the UI"
echo "  - Check if you see 'Received BLE response data'"
echo "  - Check if you see 'Sending media command via BLE'"
echo ""

cd /home/paultownwrites/Google\ Drive/nocturned
./nocturned 2>&1 | grep -E "BLE_LOG|media|WebSocket|Send|Received|notification|characteristic|GATT" --color=always