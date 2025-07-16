#!/bin/bash

echo "BLE Media Control Test"
echo "====================="

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

# API endpoints
BASE_URL="http://localhost:5000"

echo -e "${YELLOW}1. Testing media status endpoint...${NC}"
curl -s "$BASE_URL/media/status" | jq '.' || echo "Failed to get media status"

echo -e "\n${YELLOW}2. Testing play command...${NC}"
curl -s -X POST "$BASE_URL/media/play" | jq '.' || echo "Failed to send play command"
sleep 2

echo -e "\n${YELLOW}3. Checking media status after play...${NC}"
curl -s "$BASE_URL/media/status" | jq '.' || echo "Failed to get media status"

echo -e "\n${YELLOW}4. Testing pause command...${NC}"
curl -s -X POST "$BASE_URL/media/pause" | jq '.' || echo "Failed to send pause command"
sleep 2

echo -e "\n${YELLOW}5. Testing simulate endpoint (to verify WebSocket works)...${NC}"
curl -s -X POST "$BASE_URL/media/simulate" \
  -H "Content-Type: application/json" \
  -d '{
    "artist": "Test Artist",
    "track": "Test Track",
    "album": "Test Album",
    "is_playing": true,
    "volume_percent": 75,
    "position_ms": 30000,
    "duration_ms": 180000
  }' | jq '.' || echo "Failed to simulate"

echo -e "\n${YELLOW}6. Final media status check...${NC}"
curl -s "$BASE_URL/media/status" | jq '.' || echo "Failed to get media status"

echo -e "\n${GREEN}Test complete!${NC}"
echo "Check the nocturned logs for:"
echo "  - BLE_LOG messages"
echo "  - 'Sending command' messages"
echo "  - 'Received BLE response data' messages"
echo "  - WebSocket broadcast messages"