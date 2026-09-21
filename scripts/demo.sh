#!/bin/bash
set -e
G=${GATEWAY:-http://localhost:8080}
AI=${GENAI:-http://localhost:9001}
echo "### 0. reset chaos flags"
curl -s -X POST $G/api/chaos/risk-outage -H 'Content-Type: application/json' -d '{"enabled":false}' > /dev/null || true; echo
echo "### 1. happy order"
curl -s -X POST $G/api/orders -H 'Content-Type: application/json' -d '{"customer_ref":"demo","amount":99.5}'; echo
echo "### 2. risk outage order"
curl -s -X POST $G/api/chaos/risk-outage -H 'Content-Type: application/json' -d '{"enabled":true}'; echo
FAIL=$(curl -s -X POST $G/api/orders -H 'Content-Type: application/json' -d '{"customer_ref":"demo","amount":99.5}')
echo "$FAIL"
curl -s -X POST $G/api/chaos/risk-outage -H 'Content-Type: application/json' -d '{"enabled":false}'; echo
IID=$(echo "$FAIL" | python3 -c "import sys,json;print(json.load(sys.stdin).get('incident_id',''))")
echo "### 3. live postmortem for $IID"
curl -s -X POST $AI/generate-postmortem -H 'Content-Type: application/json' -d "{\"incident_id\":\"$IID\"}"; echo
