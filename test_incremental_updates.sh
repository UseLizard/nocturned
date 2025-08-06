#!/bin/bash

# Test script for binary incremental state updates

echo "Testing Binary Incremental State Updates"
echo "========================================"
echo ""

# Monitor BLE logs
echo "Starting log monitoring..."
echo "Watch for:"
echo "  - 'Enabling binary incremental updates' in Android logs"
echo "  - 'BLE_LOG: Incremental * update' messages in nocturned"
echo ""

# Start nocturned with debug logging
echo "Starting nocturned with BLE logging..."
./nocturned-arm64 2>&1 | grep -E "BLE_LOG|Incremental|binary" &
NOCTURNED_PID=$!

echo "nocturned started with PID: $NOCTURNED_PID"
echo ""
echo "Now:"
echo "1. Install the APK on your Android device"
echo "2. Start the NocturneCompanion app"
echo "3. Enable the BLE service"
echo "4. Connect from Car Thing"
echo "5. Play/pause music and change tracks"
echo "6. Watch for incremental updates in the logs"
echo ""
echo "Press Ctrl+C to stop monitoring"

# Wait for user to stop
trap "kill $NOCTURNED_PID 2>/dev/null; exit" INT
wait $NOCTURNED_PID