#!/bin/bash

echo "Comprehensive test album art crash debugging"
echo "==========================================="

# Function to check service
check_service() {
    if sv status nocturned | grep -q "run:"; then
        echo "✓ nocturned is running"
        return 0
    else
        echo "✗ nocturned is NOT running"
        return 1
    fi
}

# Initial check
echo -e "\n1. Initial service check:"
check_service

# Check for crash dumps
echo -e "\n2. Checking for recent crashes:"
dmesg | tail -20 | grep -E "nocturned|segfault|panic" || echo "No recent crashes in dmesg"

# Check memory usage
echo -e "\n3. Memory status:"
free -m

# Monitor service while testing
echo -e "\n4. Starting real-time monitoring..."
echo "   Open another terminal and trigger the test album art request"
echo "   Press Ctrl+C when done"
echo ""

# Start monitoring in background
(
    # Monitor service status
    while true; do
        if ! check_service >/dev/null 2>&1; then
            echo "[$(date '+%H:%M:%S')] SERVICE CRASHED!"
            echo "Last 50 lines of log:"
            tail -50 /var/log/nocturned.log 2>/dev/null || echo "No log file found"
            break
        fi
        sleep 0.5
    done
) &
MONITOR_PID=$!

# Tail logs
tail -f /var/log/nocturned.log 2>/dev/null | grep -E "test_album_art|TEST_ALBUM|BLE_LOG|panic|error|Error|SIGSEGV|fatal"

# Cleanup
kill $MONITOR_PID 2>/dev/null