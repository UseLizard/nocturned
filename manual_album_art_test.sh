#!/bin/bash
# Manual album art performance test using BLE_TEST_CLIENT.py

echo "📱 Manual Album Art Performance Test"
echo "===================================="
echo ""
echo "Prerequisites:"
echo "1. NocturneCompanion app running with BLE server active"
echo "2. Music playing with album art on Android device"
echo "3. Note the start time when you trigger the transfer"
echo ""
echo "Starting BLE test client..."
echo ""

cd "/home/paultownwrites/Google Drive/NocturneCompanion"

# Check if BLE_TEST_CLIENT.py exists
if [ ! -f "BLE_TEST_CLIENT.py" ]; then
    echo "❌ Error: BLE_TEST_CLIENT.py not found!"
    echo "Please ensure you're in the NocturneCompanion directory"
    exit 1
fi

echo "Instructions:"
echo "1. Type 'scan' to find NocturneCompanion device"
echo "2. Type 'connect <address>' to connect to the device"
echo "3. Type 'subscribe state' to get notifications"
echo "4. Type 'subscribe album_art' to get album art notifications"
echo "5. Type 'send {\"command\":\"get_state\"}' to request current state"
echo "6. Note the timestamp, then type:"
echo "   send {\"command\":\"album_art_query\",\"hash\":\"test123\"}"
echo "7. Watch for album art chunks and note completion time"
echo ""
echo "The transfer time = completion time - start time"
echo ""

python3 BLE_TEST_CLIENT.py