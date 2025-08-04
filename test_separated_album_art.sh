#!/bin/bash

echo "Testing separated album art implementation..."
echo "==========================================="

# Test endpoints
API_BASE="http://localhost:5000"

# Test 1: Check test album art request endpoint
echo -e "\n1. Testing test album art request endpoint:"
curl -X POST "${API_BASE}/api/test/album-art/request" \
  -H "Content-Type: application/json" \
  -w "\nHTTP Status: %{http_code}\n"

# Test 2: Check test transfer status endpoint
echo -e "\n2. Testing test transfer status endpoint:"
curl -X GET "${API_BASE}/api/test/album-art/status" \
  -H "Content-Type: application/json" \
  -w "\nHTTP Status: %{http_code}\n"

# Test 3: Check test album art image endpoint (should be 404 if no test transfer)
echo -e "\n3. Testing test album art image endpoint:"
curl -X GET "${API_BASE}/api/test/album-art/image" \
  -H "Content-Type: application/json" \
  -w "\nHTTP Status: %{http_code}\n"

# Test 4: Verify production album art endpoint still works
echo -e "\n4. Testing production album art endpoint:"
curl -X GET "${API_BASE}/api/albumart" \
  -H "Content-Type: application/json" \
  -w "\nHTTP Status: %{http_code}\n"

echo -e "\n\nTest complete!"
echo "=============="
echo "Production album art path: /tmp/album_art.jpg"
echo "Test album art path: /tmp/test_album_art.jpg"