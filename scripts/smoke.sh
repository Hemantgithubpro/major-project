#!/bin/bash
set -e
G=${GATEWAY:-http://localhost:8080}
AI=${GENAI:-http://localhost:9001}
echo "== happy path =="
HAPPY=$(curl -sf -X POST $G/api/orders -H 'Content-Type: application/json' -d '{"customer_ref":"smoke","amount":10}')
echo "$HAPPY"
echo "$HAPPY" | grep -q COMPLETED || { echo "happy path FAILED"; exit 1; }
echo "== risk outage =="
curl -sf -X POST $G/api/chaos/risk-outage -H 'Content-Type: application/json' -d '{"enabled":true}' > /dev/null
FAIL=$(curl -sf -X POST $G/api/orders -H 'Content-Type: application/json' -d '{"customer_ref":"smoke","amount":10}')
echo "$FAIL"
curl -sf -X POST $G/api/chaos/risk-outage -H 'Content-Type: application/json' -d '{"enabled":false}' > /dev/null
echo "$FAIL" | grep -q FAILED || { echo "chaos path FAILED"; exit 1; }
IID=$(echo "$FAIL" | python3 -c "import sys,json;print(json.load(sys.stdin).get('incident_id',''))")
echo "incident=$IID"
echo "SMOKE OK"
