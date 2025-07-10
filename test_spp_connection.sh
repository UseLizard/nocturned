#!/bin/bash

# SPP Connection Test Script for debugging nocturned -> NocturneCompanion connection
# Tests RFCOMM channel discovery and connection to Android device B0:C2:C7:03:BD:6E

ANDROID_MAC="B0:C2:C7:03:BD:6E"
SPP_UUID="00001101-0000-1000-8000-00805F9B34FB"
TIMEOUT=5

echo "=== SPP Connection Test for NocturneCompanion ==="
echo "Target device: $ANDROID_MAC"
echo "SPP UUID: $SPP_UUID"
echo "Timeout: ${TIMEOUT}s per test"
echo

# Function to test RFCOMM connection
test_rfcomm_channel() {
    local channel=$1
    echo -n "Testing RFCOMM channel $channel... "
    
    # Try to connect using rfcomm
    timeout $TIMEOUT rfcomm connect /dev/rfcomm0 $ANDROID_MAC $channel 2>/dev/null &
    local pid=$!
    
    sleep 1
    
    # Check if connection was established
    if kill -0 $pid 2>/dev/null; then
        # Connection might be active, check if device exists
        if [ -c /dev/rfcomm0 ]; then
            echo "SUCCESS - Channel $channel is listening"
            # Try to send a simple test message
            echo -n "Testing data transmission... "
            if echo "test" > /dev/rfcomm0 2>/dev/null; then
                echo "Data send SUCCESS"
            else
                echo "Data send FAILED"
            fi
        else
            echo "FAILED - No device created"
        fi
        
        # Clean up
        kill $pid 2>/dev/null
        rfcomm release /dev/rfcomm0 2>/dev/null
    else
        echo "FAILED - Connection refused or timeout"
    fi
    
    # Small delay between tests
    sleep 1
}

# Function to check device pairing and connection status
check_device_status() {
    echo "=== Device Status Check ==="
    
    # Check if device is paired
    echo -n "Checking if device is paired... "
    if bluetoothctl paired-devices | grep -q "$ANDROID_MAC"; then
        echo "YES"
    else
        echo "NO - Device not paired!"
        return 1
    fi
    
    # Check if device is connected
    echo -n "Checking if device is connected... "
    if bluetoothctl info "$ANDROID_MAC" | grep -q "Connected: yes"; then
        echo "YES"
    else
        echo "NO - Device not connected!"
        echo "Attempting to connect..."
        bluetoothctl connect "$ANDROID_MAC"
        sleep 3
    fi
    
    # Show device info
    echo
    echo "Device information:"
    bluetoothctl info "$ANDROID_MAC" | grep -E "(Name|Paired|Connected|UUID)"
    echo
}

# Function to scan for available services
scan_services() {
    echo "=== Service Discovery ==="
    
    echo "Scanning for available services on $ANDROID_MAC..."
    
    # Use sdptool if available
    if command -v sdptool >/dev/null 2>&1; then
        echo "Using sdptool for service discovery:"
        sdptool browse "$ANDROID_MAC" 2>/dev/null | grep -A 5 -B 5 "Serial Port\|SPP\|00001101"
    else
        echo "sdptool not available, using bluetoothctl..."
        # Try to get UUIDs from bluetoothctl
        bluetoothctl info "$ANDROID_MAC" | grep "UUID:"
    fi
    echo
}

# Function to test with hcitool
test_hci_connection() {
    echo "=== HCI Connection Test ==="
    
    if command -v hcitool >/dev/null 2>&1; then
        echo "Testing L2CAP connection with hcitool..."
        echo -n "L2CAP ping test... "
        if timeout 5 hcitool ping "$ANDROID_MAC" >/dev/null 2>&1; then
            echo "SUCCESS"
        else
            echo "FAILED"
        fi
    else
        echo "hcitool not available"
    fi
    echo
}

# Function to show current RFCOMM status
show_rfcomm_status() {
    echo "=== RFCOMM Status ==="
    
    if command -v rfcomm >/dev/null 2>&1; then
        echo "Current RFCOMM bindings:"
        rfcomm -a 2>/dev/null || echo "No RFCOMM bindings"
        echo
        
        echo "Available RFCOMM devices:"
        ls -la /dev/rfcomm* 2>/dev/null || echo "No RFCOMM devices found"
    else
        echo "rfcomm command not available"
    fi
    echo
}

# Main execution
main() {
    echo "Starting SPP connection tests..."
    echo "Date: $(date)"
    echo
    
    # Check device status first
    check_device_status || exit 1
    
    # Show current RFCOMM status
    show_rfcomm_status
    
    # Test HCI connection
    test_hci_connection
    
    # Scan for services
    scan_services
    
    # Test RFCOMM channels 1-8 (common SPP channels)
    echo "=== RFCOMM Channel Tests ==="
    echo "Testing common SPP channels (1-8)..."
    echo
    
    for channel in {1..8}; do
        test_rfcomm_channel $channel
    done
    
    echo
    echo "=== Test Summary ==="
    echo "All RFCOMM channel tests completed."
    echo "Check output above for successful connections."
    echo
    echo "If no channels responded, possible issues:"
    echo "1. NocturneCompanion app not running or not in SPP server mode"
    echo "2. Android device has disabled SPP service"
    echo "3. Bluetooth connection issues"
    echo "4. Firewall or permission issues"
    echo
    echo "Next steps:"
    echo "1. Ensure NocturneCompanion app is running and in foreground"
    echo "2. Check Android Bluetooth settings"
    echo "3. Try pairing/unpairing the device"
    echo "4. Check nocturned logs for specific error messages"
}

# Check if running as root (usually required for bluetooth operations)
if [ "$EUID" -ne 0 ]; then
    echo "Warning: This script may need to run as root for bluetooth operations"
    echo "If you encounter permission errors, try: sudo $0"
    echo
fi

# Run main function
main "$@"