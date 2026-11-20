# VIVA DAY — open this file first

Repo: `/Users/hemant/College/semester-7/major-project/try2`
Everything below is already built and committed. **Do not change code today.**

---

## 1. Morning setup (5 min)

```bash
cd /Users/hemant/College/semester-7/major-project/try2
open -a Docker                      # if Docker Desktop isn't running
docker compose ps                   # expect 13 services, all Up
```

Open two windows:

- Browser: **http://localhost:5173** — operator console (ledger, risk signal, chaos, report).
- Terminal: repo root, ready for `make demo-final`.

Fallbacks are already saved at `docs/screenshots/ledger.png` and
`docs/screenshots/postmortem.png`.

---

## 2. Checklist (tick as you go)

- [ ] Docker Desktop running; `docker compose ps` shows **13/13** services Up.
- [ ] Browser at `localhost:5173` shows the **Decision ledger** and **Risk signal** panels.
- [ ] `make demo-final` runs clean (~105 s). Script resets all fault flags itself — no DB edits.
- [ ] Point at the key ledger row: **proposed `SCALE_UP` → verdict `BLOCKED` → blocked by `INV-O2` → final action `SHED_LOAD`**.
- [ ] Point at the load summary: **real 503s** (≈1100–1300 of 1600) and p95 dropping after shed.
- [ ] Point at verification: **improved=true, invariants=true**.
- [ ] Live postmortem generated (model shown, **cached=false**), claims labelled
      FACT / PREDICTION / INFERENCE / RECOMMENDATION.
- [ ] Close other apps if memory gets tight (last check: 53% RAM free).

**If the script fails:** use the fallback matrix in §5, then the saved screenshots. Never
fabricate a verdict, metric, or result.

---

## 3. What to say during the demo (2 minutes)

1. "Orders flow through a saga; real `/metrics` show p95 and DB connections low, risk low."
2. "I inject DB latency on payment. Connections saturate, p95 rises, the XGBoost risk score climbs."
3. "The controller proposes `SCALE_UP`. The **shield blocks it on `INV-O2`** — adding replicas
   would add DB connections. The ledger records the veto; the controller falls through to
   `SHED_LOAD`."
4. "New orders get real 503s and latency recovers. Verification is written to the ledger."
5. "A live OpenRouter postmortem explains the incident — advisory only, never in the control path."
6. "A threshold-only scaler would have scaled up here and made it worse. **The shield refused.**"

Do **not** walk through code. Answer conceptual questions from `docs/VIVA_QBANK.md`.

---

## 4. Honest story to tell

**Thesis:** prediction proposes, a pure invariant shield vetoes, the ledger records, and the LLM
only explains. ML and the LLM are **not** in the control path.

**Our contribution (what is genuinely new here):**
- A versioned invariant catalog and a **deterministic, stateless safety shield** as the sole veto.
- A controller that proposes candidate actions in priority order and **fails closed** (`NO_ACTION`)
  if the shield or ML service is unavailable.
- A decision ledger: proposal → verdict → action → verification, with `decision_uid` idempotency.
- The shield refusing `SCALE_UP` on `INV-O2` — the central demonstration.

**Assembled, not novel:** the saga, real metrics, XGBoost, and the GenAI postmortem are standard
tools. The research claim is their composition under a safety constraint.

**Say this openly:**
- Testbed on one Mac; faults are injected; training data is **simulated**.
- One controller replica by design — no leader election.
- Real evaluation: classifier F1 **0.960 / ROC-AUC 0.996** vs trivial baseline F1 0.660 / 0.767.
- No mock LLM; OpenRouter call is live.

**Do not claim:**
- N≥5 baseline-vs-proposed experiment suite.
- Outbox poller, Kafka consumers, idempotency enforcement, crash-resume.
- Regions / router / failover.
- RAG corpus seeding (schema exists, corpus empty).
- Production readiness or real cloud scaling.
- That the decision ledger is already in the GenAI evidence bundle — it is **not** yet.

---

## 5. Compact fallback matrix

| Problem | Fallback |
|---|---|
| ML service down | Controller fails closed (`NO_ACTION`); show `/evaluate` via curl |
| Controller down | Show saga demo + shield `POST /evaluate` directly |
| Shed flag broken | Use `ENABLE_CIRCUIT_BREAKER` on risk |
| Frontend blank | `curl localhost:8080/api/decisions` in terminal |
| OpenRouter fails | `:free` fallback chain; if all fail, say so — do not fake it |
| Docker flaky | Restart Docker Desktop; use `docs/screenshots/*.png` |

---

## 6. Other docs

- `docs/TOMORROW.md` §6 — full fallback matrix, §10 — rehearsal log.
- `docs/VIVA_QBANK.md` — 15 likely questions with short answers.
- `docs/adr/0012`–`0014` — thesis, removal of regions/router, shield-as-a-service.
- `PROJECT_STATUS.md` — §B0 is the current verified state; older sections are historical.
