#!/bin/bash

# Car Thing SSH details
CAR_THING_IP="172.16.42.2"
SSH_USER="root"
SSH_PASS="nocturne"

echo "[INFO] Deploying debug scripts to Car Thing..."

# Create the test_scripts directory on the device
echo "[INFO] Creating /var/test_scripts directory..."
sshpass -p "$SSH_PASS" ssh -o StrictHostKeyChecking=no "$SSH_USER@$CAR_THING_IP" "mkdir -p /var/test_scripts"

# Copy debug scripts
echo "[INFO] Copying debug scripts..."
sshpass -p "$SSH_PASS" scp -o StrictHostKeyChecking=no ./debug_test_album_art.sh "$SSH_USER@$CAR_THING_IP:/var/test_scripts/"
sshpass -p "$SSH_PASS" scp -o StrictHostKeyChecking=no ./test_separated_album_art.sh "$SSH_USER@$CAR_THING_IP:/var/test_scripts/"
sshpass -p "$SSH_PASS" scp -o StrictHostKeyChecking=no ./debug_crash_test_album_art.sh "$SSH_USER@$CAR_THING_IP:/var/test_scripts/"

# Make scripts executable
echo "[INFO] Making scripts executable..."
sshpass -p "$SSH_PASS" ssh -o StrictHostKeyChecking=no "$SSH_USER@$CAR_THING_IP" "chmod +x /var/test_scripts/*.sh"

# List the deployed scripts
echo "[INFO] Deployed scripts:"
sshpass -p "$SSH_PASS" ssh -o StrictHostKeyChecking=no "$SSH_USER@$CAR_THING_IP" "ls -la /var/test_scripts/"

echo "[SUCCESS] Debug scripts deployed to /var/test_scripts/"
echo ""
echo "To run debug script on device:"
echo "  ssh root@$CAR_THING_IP"
echo "  /var/test_scripts/debug_test_album_art.sh"