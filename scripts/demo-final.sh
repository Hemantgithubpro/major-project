#!/usr/bin/env bash
# Full closed-loop demo for the final viva.
#   loadgen -> real metrics -> XGBoost /risk -> shield veto (INV-O2)
#   -> SHED_LOAD -> real 503s -> ledger + verification -> live GenAI postmortem
#
# No manual DB edits. No mock LLM. Run: make demo-final
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
G="${GATEWAY:-http://localhost:8080}"
C="${CONTROLLER:-http://localhost:8086}"
AI="${GENAI:-http://localhost:9001}"
CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
SHOTS="$ROOT/docs/screenshots"
mkdir -p "$SHOTS"
START=$(date +%s)

post() { curl -s --max-time 10 -X POST "$1" -H 'Content-Type: application/json' -d "$2"; }
say()  { printf '\n### %s\n' "$*"; }

say "0. reset fault flags (no DB edits)"
post "$G/api/chaos/risk-outage" '{"enabled":false}' >/dev/null
post "$G/api/chaos/db-latency"  '{"enabled":false}' >/dev/null
post "$G/admin/shed"            '{"enabled":false}' >/dev/null

say "0b. wait out any controller cooldown left from a previous rehearsal"
SINCE=$(python3 -c "from datetime import datetime,timezone;print(datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%fZ'))")
python3 - "$C" <<'PY'
import json, sys, time, urllib.request
from datetime import datetime, timezone
base = sys.argv[1]
while True:
    try:
        rows = json.load(urllib.request.urlopen(base + "/decisions", timeout=5))
        last = None
        for d in rows:
            if any(a.get("action") != "NO_ACTION" for a in (d.get("actions") or [])):
                last = d
                break
        if not last:
            break
        t = datetime.fromisoformat(last["created_at"].replace("Z", "+00:00"))
        remain = 60 - (datetime.now(timezone.utc) - t).total_seconds()
        if remain <= 0:
            break
        print("  cooldown: %.0fs remaining" % remain, flush=True)
        time.sleep(min(remain, 10))
    except Exception:
        time.sleep(1)
PY

say "1. baseline traffic (20 rps, 80s total) — risk should be low"
python3 scripts/loadgen.py --rps 20 --duration 80 >/tmp/demo-final-load.log 2>&1 &
LOAD=$!
sleep 14
curl -s --max-time 20 -X POST "$C/control/cycle" | python3 -c "
import sys,json
d=json.load(sys.stdin)
print('  normal cycle: risk=%.4f proposed=%s verdict=%s action=%s' % (d['score'],d['policy_action'],d['verdict'],d['shield_action']))
"

say "2. inject real DB latency on payment (pg_sleep, holds connections)"
post "$G/api/chaos/db-latency" '{"enabled":true,"added_ms":300}' >/dev/null
echo "  db_latency_injected_ms: $(curl -s localhost:8083/metrics | awk '/^db_latency_injected_ms/{print $2}')"

say "3. wait for shield to veto SCALE_UP (INV-O2) and SHED_LOAD to execute"
SINCE="$SINCE" python3 - "$C" <<'PY'
import json, os, sys, time, urllib.request
base, since = sys.argv[1], os.environ["SINCE"]
deadline = time.time() + 50
while time.time() < deadline:
    try:
        rows = json.load(urllib.request.urlopen(base + "/decisions", timeout=5))
        for d in rows:
            if (d.get("created_at", "") >= since and d.get("verdict") == "BLOCKED"
                    and d.get("blocked_by") == "INV-O2" and d.get("shield_action") == "SHED_LOAD"):
                print("  %s proposed=%s verdict=%s blocked_by=%s action=%s risk=%.4f"
                      % (d["created_at"][11:19], d["policy_action"], d["verdict"],
                         d["blocked_by"], d["shield_action"], d["score"]))
                for a in (d.get("actions") or []):
                    print("  executed: %s %s %s" % (a["action"], a["status"], a.get("result")))
                sys.exit(0)
    except Exception:
        pass
    time.sleep(2)
print("  TIMEOUT: no INV-O2 -> SHED_LOAD decision observed", file=sys.stderr)
sys.exit(1)
PY

say "4. capture live ledger screenshot"
if [ -x "$CHROME" ]; then
  "$CHROME" --headless --disable-gpu --no-sandbox --window-size=1600,1150 \
    --virtual-time-budget=6000 --screenshot="$SHOTS/ledger.png" http://localhost:5173/ >/dev/null 2>&1 || true
  echo "  saved $SHOTS/ledger.png"
else
  echo "  Chrome not found; skipping screenshot"
fi

say "5. let the load generator finish; load shedding should produce real 503s"
wait "$LOAD"
tail -8 /tmp/demo-final-load.log

say "6. remove faults and reset the shed flag"
post "$G/api/chaos/db-latency" '{"enabled":false}' >/dev/null
post "$G/admin/shed"            '{"enabled":false}' >/dev/null
sleep 3

say "7. full ledger + verification"
curl -s "$G/api/decisions" | python3 -c "
import sys,json
rows=json.load(sys.stdin)
key=[d for d in rows if d.get('blocked_by')=='INV-O2']
rest=[d for d in rows if d.get('blocked_by')!='INV-O2']
print('  --- key shield veto ---')
for d in (key[:2]+rest[:3]):
    v=d.get('verifications') or []
    last=v[-1] if v else {}
    print('  %s %-8s %-10s %-8s -> %-22s verify=%s' % (
        d['created_at'][11:19], d['verdict'], d.get('blocked_by') or '-',
        d['policy_action'], d['shield_action'],
        ('improved=%s invariants=%s' % (last.get('improved'),last.get('invariants_ok'))) if v else 'pending'))
"

say "8. risk-outage order -> live GenAI postmortem (real OpenRouter)"
post "$G/api/chaos/risk-outage" '{"enabled":true}' >/dev/null
ORDER=$(post "$G/api/orders" '{"customer_ref":"viva-demo","amount":149.0}')
post "$G/api/chaos/risk-outage" '{"enabled":false}' >/dev/null
IID=$(printf '%s' "$ORDER" | python3 -c "import sys,json;print(json.load(sys.stdin).get('incident_id',''))")
if [ -z "$IID" ]; then echo "  no incident_id in: $ORDER" >&2; exit 1; fi
echo "  incident_id: $IID"
REPORT=$(curl -s --max-time 90 -X POST "$AI/generate-postmortem" \
  -H 'Content-Type: application/json' -d "{\"incident_id\":\"$IID\"}")
printf '%s' "$REPORT" | python3 -c "
import sys,json,html
d=json.load(sys.stdin)
print('  model: %s cached=%s' % (d.get('model'),d.get('cached')))
body='[%s%s]\n\n%s' % (d.get('model'),' cached' if d.get('cached') else '',d.get('markdown',''))
open('/tmp/demo-final-report.html','w').write('<html><meta charset=utf-8><body style=\"font-family:system-ui;max-width:1000px;margin:30px auto\"><pre style=\"white-space:pre-wrap\">'+html.escape(body)+'</pre></body></html>')
"
if [ -x "$CHROME" ]; then
  "$CHROME" --headless --disable-gpu --no-sandbox --window-size=1400,1600 \
    --virtual-time-budget=4000 --screenshot="$SHOTS/postmortem.png" file:///tmp/demo-final-report.html >/dev/null 2>&1 || true
  echo "  saved $SHOTS/postmortem.png"
fi

printf '\n### DEMO OK — %.0fs\n' "$(($(date +%s) - START))"
