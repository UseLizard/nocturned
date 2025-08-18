#!/bin/bash

# Test script for nocturned v2 API endpoints

BASE_URL="http://172.16.42.2:5000/api/v2"

echo "Testing Nocturned v2 API endpoints..."
echo "======================================"

# Test health endpoint
echo -n "Testing /health: "
curl -s -w "%{http_code}" -o /dev/null "${BASE_URL}/health" && echo " ✓" || echo " ✗"

# Test status endpoint
echo -n "Testing /status: "
curl -s -w "%{http_code}" -o /dev/null "${BASE_URL}/status" && echo " ✓" || echo " ✗"

# Test bluetooth status
echo -n "Testing /bluetooth/status: "
curl -s -w "%{http_code}" -o /dev/null "${BASE_URL}/bluetooth/status" && echo " ✓" || echo " ✗"

# Test bluetooth stats
echo -n "Testing /bluetooth/stats: "
curl -s -w "%{http_code}" -o /dev/null "${BASE_URL}/bluetooth/stats" && echo " ✓" || echo " ✗"

# Test current media
echo -n "Testing /media/current: "
curl -s -w "%{http_code}" -o /dev/null "${BASE_URL}/media/current" && echo " ✓" || echo " ✗"

# Test current weather
echo -n "Testing /weather/current: "
curl -s -w "%{http_code}" -o /dev/null "${BASE_URL}/weather/current" && echo " ✓" || echo " ✗"

echo ""
echo "Detailed health response:"
echo "========================="
curl -s "${BASE_URL}/health" | python3 -m json.tool 2>/dev/null || curl -s "${BASE_URL}/health"

echo ""
echo "Detailed status response:"
echo "========================="
curl -s "${BASE_URL}/status" | python3 -m json.tool 2>/dev/null || curl -s "${BASE_URL}/status"