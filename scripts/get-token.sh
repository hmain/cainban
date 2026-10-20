#!/bin/bash
# Cainban MCP Token Helper
# Usage: ./get-token.sh
#
# This script walks you through the GitHub device flow to get a token
# for connecting your MCP client to cainban.

set -e

API_BASE="https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com"

echo "🔐 Cainban MCP Authentication"
echo "=============================="
echo ""

# Step 1: Start device flow
echo "Starting GitHub device flow..."
RESPONSE=$(curl -s -X POST "$API_BASE/auth/github/device" -H "Content-Type: application/json" -d '{}')

USER_CODE=$(echo "$RESPONSE" | grep -o '"user_code":"[^"]*"' | cut -d'"' -f4)
DEVICE_CODE=$(echo "$RESPONSE" | grep -o '"device_code":"[^"]*"' | cut -d'"' -f4)
VERIFICATION_URI=$(echo "$RESPONSE" | grep -o '"verification_uri":"[^"]*"' | cut -d'"' -f4)
INTERVAL=$(echo "$RESPONSE" | grep -o '"interval":[0-9]*' | cut -d':' -f2)

if [ -z "$USER_CODE" ] || [ -z "$DEVICE_CODE" ]; then
    echo "❌ Failed to start device flow"
    echo "Response: $RESPONSE"
    exit 1
fi

echo ""
echo "📋 Your code: $USER_CODE"
echo ""
echo "👉 Open this URL in your browser:"
echo "   $VERIFICATION_URI"
echo ""
echo "   Enter the code: $USER_CODE"
echo ""
echo "Waiting for you to authorize..."

# Step 2: Poll for token
INTERVAL=${INTERVAL:-5}
while true; do
    sleep "$INTERVAL"
    
    POLL_RESPONSE=$(curl -s -X POST "$API_BASE/auth/github/device/poll" \
        -H "Content-Type: application/json" \
        -d "{\"device_code\":\"$DEVICE_CODE\"}")
    
    # Check for access_token
    ACCESS_TOKEN=$(echo "$POLL_RESPONSE" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4 2>/dev/null || true)
    
    if [ -n "$ACCESS_TOKEN" ]; then
        echo ""
        echo "✅ Success! Here's your token:"
        echo ""
        echo "   $ACCESS_TOKEN"
        echo ""
        echo "📝 Add this to your MCP client config:"
        echo ""
        echo '   "headers": {'
        echo "     \"Authorization\": \"Bearer $ACCESS_TOKEN\","
        echo '     "X-Cainban-Repo": "your-org/your-repo"'
        echo '   }'
        echo ""
        exit 0
    fi
    
    # Check for errors
    ERROR=$(echo "$POLL_RESPONSE" | grep -o '"error":"[^"]*"' | cut -d'"' -f4 2>/dev/null || true)
    
    if [ "$ERROR" = "authorization_pending" ]; then
        echo -n "."
        continue
    elif [ "$ERROR" = "slow_down" ]; then
        INTERVAL=$((INTERVAL + 5))
        echo -n "."
        continue
    elif [ -n "$ERROR" ]; then
        echo ""
        echo "❌ Authorization failed: $ERROR"
        exit 1
    fi
done
