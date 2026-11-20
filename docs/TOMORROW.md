# TOMORROW.md — Phased Execution Plan for the Final Viva Demo

> **Goal:** a live, real closed loop on this Mac by tomorrow:
> real metrics → real XGBoost risk → real safety shield → real action → real ledger → GenAI postmortem.
> **No mock LLM. No hardcoded verdicts. Simulated training data is allowed.**
> **One device:** this Mac (16 GB, 10 cores, Docker installed). Start Docker Desktop first.
> **Scope:** testbed only. The e-commerce server integration is not part of tomorrow.

---

## 0. Definition of done

- [ ] `docker compose up` brings up the full stack, all healthy.
- [ ] Load generator drives real traffic through the gateway.
- [ ] `/metrics` on gateway and payment shows real request/latency/pool numbers.
- [ ] Enabling DB latency raises p95 and DB pool usage.
- [ ] `ml-service /risk` returns a real XGBoost probability that rises under that latency.
- [ ] Controller proposes `SCALE_UP`; shield blocks it on `INV-O2`; policy falls through to `SHED_LOAD`.
- [ ] Gateway actually sheds new orders (real 503s); metrics recover.
- [ ] Ledger shows proposal → verdict → action → verification, live on screen.
- [ ] GenAI generates a postmortem from the ledger/evidence (live OpenRouter, no mock).
- [ ] Two clean end-to-end rehearsals.

---

## 1. Frozen interfaces (freeze in Phase 0, do not change after)

Everyone codes against these. Changing them mid-build is how integration dies.

### 1.1 Metrics (Prometheus text format)

```
http_requests_total{service,route,status}
http_request_duration_seconds_bucket{service,route,le}
db_query_duration_seconds_bucket{service,op,le}
db_pool_acquired{service}
db_pool_idle{service}
db_pool_waiting{service}
db_pool_max{service}
db_latency_injected_ms{service}
saga_steps_total{step,status}
orders_total{status}
risk_checks_total{verdict}
```

### 1.2 `ml-service` contracts

```http
POST /risk
{
  "service": "payment-service",
  "features": {
    "requests_per_min": 120,
    "p95_latency_ms": 480,
    "error_rate": 0.03,
    "db_conn_pct": 0.85,
    "db_pool_waiting": 4,
    "saga_failure_rate": 0.10,
    "db_latency_injected_ms": 300,
    "risk_slope_5m": 0.20
  }
}
→ { "failure_probability": 0.82, "risk_level": "high", "horizon_minutes": 10,
    "predicted_cause": "db_conn_pct",
    "top_features": [{"name":"db_conn_pct","contribution":0.41}] }

POST /forecast
{ "service": "payment-service", "window": [ ... ] }
→ { "requests_5m": 140, "requests_15m": 170, "cpu_pct_5m": 0.72, "db_conn_pct_5m": 0.88 }
```

### 1.3 `safety-shield` contract

```http
POST /evaluate
{
  "decision_uid": "uuid",
  "action": "SCALE_UP",
  "state": {
    "service": "payment-service",
    "replicas": 1, "min_replicas": 1, "max_replicas": 4,
    "db_conn_pct": 0.85, "db_conn_threshold": 0.70,
    "prediction_ts": "2026-11-20T18:00:00Z", "now": "2026-11-20T18:00:05Z",
    "prediction_max_age_s": 60,
    "last_action_at": "2026-11-20T17:58:00Z", "cooldown_s": 60,
    "actions_in_window": 0, "action_budget_per_window": 3,
    "budget_remaining_usd": 8.5, "transaction_state_ok": true
  }
}
→ { "allow": false, "invariant_id": "INV-O2",
    "reason": "db_conn_pct 0.85 >= threshold 0.70; scale-up would add DB connections",
    "invariant_version": 1 }
```

Invariant ids: `INV-O0` (unknown action), `INV-O5` (freshness), `INV-O3` (decision id present;
uniqueness is enforced by the controller ledger), `INV-O1` (replica bounds), `INV-O2`
(DB ceiling), `INV-O4` (cooldown/budget), `INV-O6` (USD budget), `INV-O7` (transaction state).

### 1.4 Action names (exact strings)

`NO_ACTION · SCALE_UP · SCALE_DOWN · SHED_LOAD · ENABLE_CIRCUIT_BREAKER`

### 1.5 Ledger tables

`reliability_svc.decisions(decision_uid, service, policy_action, shield_action, verdict,
blocked_by, reason, score, state_snapshot, created_at)`
`reliability_svc.actions(decision_uid, action, status, executed_at, result, error)`
`reliability_svc.verifications(decision_uid, metrics_before, metrics_after, improved,
invariants_ok, created_at)`

### 1.6 Endpoints

| Service | Endpoint | Purpose |
|---------|----------|---------|
| controller | `POST /control/cycle` | run one cycle on demand |
| controller | `GET /decisions` | ledger for the UI |
| controller | `GET /predictions` | latest risk |
| controller | `GET /audit` | invariant audit |
| gateway | `GET /api/decisions` | proxy to controller |
| gateway | `POST /api/chaos/db-latency` | toggle payment DB latency |
| gateway | `POST /admin/shed` | internal shed flag (controller only) |
| shield | `POST /evaluate` | verdict |
| shield | `GET /invariants` | catalog |
| ml | `POST /risk`, `POST /forecast` | inference |

---

## 2. Phases

### Phase 0 — Verify baseline and freeze interfaces (30 min)

**Tasks**
- Start Docker Desktop; wait for the daemon.
- `make up` → all healthy.
- `make demo` → one COMPLETED order, one FAILED-compensated order, one live postmortem.
- Commit the current state (so you can revert): `git add -A && git commit -m "baseline before final build"`.
- Write the frozen interfaces (§1) somewhere the team can see.

**Exit:** baseline green; contracts frozen.

---

### Phase 1 — Real metrics + DB-latency chaos (2–3 h)

**Tasks**
- Add a small metrics package (counters/histograms/gauges, Prometheus text output).
- Middleware on gateway, order, inventory, payment, risk, saga:
  `http_requests_total`, `http_request_duration_seconds`.
- Payment:
  - `POST /chaos/db-latency {enabled, added_ms}` → adds real sleep around DB calls.
  - `db_latency_injected_ms` gauge.
  - pgxpool gauges: `db_pool_acquired`, `db_pool_idle`, `db_pool_waiting`, `db_pool_max`.
  - `db_query_duration_seconds` histogram.
- Domain gauges: `orders_total{status}`, `saga_steps_total{step,status}`, `risk_checks_total{verdict}`.
- Write `scripts/loadgen.py`: steady/ramp load against `POST /api/orders`; prints rate/p95/errors.

**Exit:** `curl localhost:8080/metrics` and `curl localhost:8083/metrics` show real numbers;
enabling DB latency visibly raises p95 and `db_pool_waiting`.

---

### Phase 2 — Safety shield (1.5–2 h)

**Tasks**
- `apps/safety-shield/main.go` + `Dockerfile`.
- Invariant catalog in code: O1, O2, O3, O4, O5, O7 (priority order from `PLAN.md` §5.3).
- `POST /evaluate`, `GET /invariants`, `GET /healthz`.
- Table-driven tests: allow, block O2, stale O5, cooldown O4, duplicate O3, bounds O1.
- Determinism test: same input → same output.
- Add to `docker-compose.yml` on `:8087`.

**Exit:** `go test ./apps/safety-shield/...` green; curl shows a block with `INV-O2` and a reason.

---

### Phase 3 — ML risk service (2–3 h)

**Tasks**
- `ai/ml/dataset.py`: generate simulated telemetry windows (steady/ramp/spike + DB-latency faults);
  label 10 min before failure = 1; time-based split.
- `ai/ml/train.py`: XGBoost classifier + regressor; evaluate precision/recall/F1/ROC-AUC vs
  a trivial `CPU > 85%` baseline; save artifacts + metrics.
- `ai/ml/service/app.py`: FastAPI `POST /risk`, `POST /forecast`; load the artifact.
- `Dockerfile` + compose on `:9000`.
- Keep feature names exactly as §1.2.

**Exit:** under simulated high `db_conn_pct`/latency, `/risk` returns > 0.7;
under normal load, < 0.3.

---

### Phase 4 — Controller + ledger (3–4 h)

**Tasks**
- `migrations/002_safety_reliability.sql`: `safety_svc.invariants`, `reliability_svc.decisions`,
  `actions`, `verifications`, `metrics_snapshots`.
- `apps/reliability-controller/main.go` + `Dockerfile`:
  - Scrape `/metrics` from gateway/payment/order/saga/risk every 5 s.
  - Build the feature vector; call `ml-service /risk`.
  - Policy: score candidates; order `SCALE_UP`, `SHED_LOAD`, `ENABLE_CIRCUIT_BREAKER`, `NO_ACTION`.
  - For each candidate: call `safety-shield /evaluate`; first allow wins.
  - Executor: `SHED_LOAD` → `POST gateway:/admin/shed {enabled:true}`;
    `ENABLE_CIRCUIT_BREAKER` → `POST risk:/chaos/fail {enabled:true}`;
    `SCALE_UP/DOWN` → record only (do not execute in the demo).
  - Write decisions/actions; after 30 s re-read metrics and write verification.
  - `GET /decisions`, `GET /predictions`, `GET /audit`, `POST /control/cycle`.
- Add to compose on `:8086`; depends on shield + ml + postgres.

**Exit:** one manual `POST /control/cycle` writes a full row set:
`policy_action=SCALE_UP, verdict=BLOCKED, blocked_by=INV-O2, shield_action=SHED_LOAD`.

---

### Phase 5 — Gateway + UI integration (2 h)

**Tasks**
- Gateway proxies: `GET /api/decisions`, `GET /api/predictions`, `GET /api/audit`.
- Gateway `POST /api/chaos/db-latency` → payment `/chaos/db-latency`.
- Gateway `POST /admin/shed` (internal, controller-only) → sets an in-memory flag;
  when on, `POST /api/orders` returns `503 {"error":"load shed"}`.
- Frontend: add a **Decisions** panel polling `/api/decisions` every 2 s:
  time · service · proposed · verdict · blocked_by · final action · reason.
- Optional: a small "DB latency" button in the existing chaos area.

**Exit:** the whole loop is visible on one screen without touching a terminal.

---

### Phase 6 — End-to-end rehearsal (2 h)

**Tasks**
- Full run: stack up → loadgen → normal → enable DB latency → observe loop → disable → postmortem.
- Fix integration bugs; tune thresholds so the demo is reliable:
  `db_conn_threshold=0.70`, risk threshold `0.60`, controller interval `5 s`, cooldown `60 s`.
- Write `scripts/demo-final.sh` (or extend `demo.sh`) with the exact demo steps.
- Capture fallback screenshots of the ledger and the postmortem.
- Rehearse twice, timed.

**Exit:** two clean runs, each under 6 minutes, no manual DB edits.

---

### Phase 7 — Viva morning (1–2 h)

**Tasks**
- Final `make up` + one full rehearsal on the demo Mac.
- Close all other apps; check RAM.
- Have the fallback screenshots ready.
- Rehearse the viva answers (problem, prior gaps, tech, novelty, limitations).
- Keep the demo script visible on a second window.

**Exit:** confidence; no code changes after this point.

---

## 3. Parallel lanes

| Lane | Owner | Phases |
|------|-------|--------|
| **A — Go core** | strongest Go | Phase 1 → Phase 2 → Phase 4 |
| **B — ML** | strongest Python | Phase 3 → support Phase 4 |
| **C — integration/UI** | strongest frontend/ops | Phase 1 (loadgen/gateway) → Phase 5 → Phase 6 |

If only two people are effective, merge B into A and let C own integration.

**Critical path:** metrics → controller → UI → rehearsal. Shield and ML can be built in parallel
but must land before Phase 4 integration.

---

## 4. Timeline (relative; adjust to your start time)

| Block | Duration | What must be true at the end |
|-------|----------|------------------------------|
| Phase 0 | 0:30 | baseline green, interfaces frozen |
| Phase 1 | 2–3 h | real metrics + DB latency visible |
| Phase 2 | 1.5–2 h | shield blocks/allows with tests |
| Phase 3 | 2–3 h | real risk score responds to features |
| Phase 4 | 3–4 h | controller writes the full ledger |
| Phase 5 | 2 h | loop visible on one screen |
| Phase 6 | 2 h | two clean rehearsals |
| Phase 7 | 1–2 h | viva-ready |

Phases 1–3 run in parallel. Phase 4 starts as soon as shield + ML contracts are live.
Phase 5 can start once the controller returns real JSON.

---

## 5. Demo script (~5 minutes)

1. **0:00** Stack up, load generator running. "Distributed order system + reliability controller."
2. **0:45** Point at metrics: p95 low, DB connections low, risk low, no actions.
3. **1:00** Enable DB latency (chaos button). "The database is slowing down; connections pile up."
4. **1:30** p95 rises, DB pool rises, risk score climbs.
5. **2:00** Controller proposes `SCALE_UP`. Shield blocks on `INV-O2`. Ledger row appears.
6. **2:15** Policy falls through to `SHED_LOAD`; gateway returns 503 for new orders.
7. **2:45** Load generator shows 503s; new order rate drops; p95 recovers.
8. **3:30** Disable chaos; show the full ledger timeline.
9. **4:00** Generate the GenAI postmortem; read FACT / PREDICTION / INFERENCE / RECOMMENDATION.
10. **4:30** "A CPU-threshold scaler would have scaled up here and made it worse. The shield refused."

---

## 6. Fallback matrix

| Failure | Fallback |
|---------|----------|
| ML service down | Controller fails closed (`NO_ACTION`); show shield via curl with a real state snapshot |
| Controller down | Show existing saga demo + shield `/evaluate` directly |
| Shed flag broken | Use `ENABLE_CIRCUIT_BREAKER` on risk (existing flag) |
| Frontend panel broken | `curl localhost:8080/api/decisions` in a terminal |
| Kafka/Postgres slow | restart stack; keep a screenshot of a clean run |
| OpenRouter fails | `:free` fallback chain; if all fail, labeled offline mock — say so |
| Docker flaky | restart Docker Desktop; keep fallback screenshots |

---

## 7. Cut list if time slips (in order)

1. Frontend panel → use `curl` for the ledger.
2. `forecast` endpoint → risk only.
3. `ENABLE_CIRCUIT_BREAKER` → `SHED_LOAD` only.
4. Invariant audit → shield + ledger only.
5. `SCALE_DOWN` action → not needed for the demo.

**Never cut:** real metrics, real XGBoost inference, shield logic, ledger, real action, live LLM.

---

## 8. Viva talking points

- **Problem:** autonomous remediation can harm; reactive autoscalers act on symptoms and can
  scale into a database bottleneck.
- **Prior gaps:** no formal safety invariants; no deterministic veto; no audit of allowed/blocked
  actions; LLM tools explain after the fact but cannot enforce; chaos tools break things without
  deciding safely.
- **Our approach:** invariant catalog + deterministic shield + prediction as a signal + ledger +
  evidence-grounded explanation.
- **Tech:** Go control plane, Python/FastAPI ML+GenAI, Prometheus metrics, Postgres+pgvector,
  Kafka, XGBoost, OpenRouter, Docker Compose.
- **Honesty:** testbed with injected faults; simulated training data; no regions; no production claims.

---

## 9. Do-not-claim list for tomorrow

- Full baseline-vs-proposed experiment suite with N≥5.
- Outbox poller, consumers, idempotency enforcement, crash-resume.
- Regions/router/failover.
- RAG corpus seeding.
- A product catalog or storefront — the demo UI is an operator console. The catalog is P1
  (`PLAN.md` §7.1), not part of tomorrow.
- Production readiness or real cloud scaling.
