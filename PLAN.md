# PLAN.md — Safety-Constrained Autonomy Additions

> **Status:** Planning baseline for the final B.Tech major-project build.
> **Audience:** the 3-person team, the project guide, and the final-viva examiners.
> **Scope of this document:** the *additions* that steer the existing MVP into the final
> project. It does not re-describe code that already exists; it references it.
> **Companions:** `project_report_final.md` (original master), `ARCHITECTURE_OPENROUTER.md`,
> `PHASES_OPENROUTER.md`, `PROJECT_STATUS.md`, `docs/adr/`.

---

## 1. Why this document exists

The repo already contains a working slice: gateway, order, inventory, payment, risk, saga,
Kafka, Postgres+pgvector, a GenAI postmortem service, and a one-page dashboard. The original
plan was a very broad "autonomous reliability platform." That breadth dilutes the contribution.

This plan **sharpens the project into one thesis** and defines everything needed to implement
it:

> **Safety-constrained autonomy.** A prediction-driven controller may propose remediations, but
> a deterministic **safety shield** vetoes any action that would violate transactional or
> operational invariants. The contribution is the invariant model + the shield + an empirical
> baseline-vs-proposed comparison showing the shield prevents harmful remediation.

Everything below serves that thesis.

---

## 2. Locked decisions (from team discussion)

| # | Decision | Consequence |
|---|----------|-------------|
| D1 | Thesis = safety-constrained autonomy | All features are judged by whether they support the shield/invariant story |
| D2 | `safety-shield` is a **separate service** | The safety claim is visible and independently testable |
| D3 | **No independent safety monitor service** | Safety evidence comes from shield verdicts + a read-only invariant audit in the controller (§6.4) |
| D4 | **Keep GenAI** (postmortem + RAG Q&A) | Advisory explanation layer; claims labeled and traceable |
| D5 | **No regions, no router/LB** | Removes the shared-DB/region muddle; gateway does client-side round-robin across scaled replicas |
| D6 | Implement the shield + controller + experiments before the final viva | This is the strong version; the plan is a build plan, not a design-only document |
| D7 | Final evaluation is a **group viva of 3** | One coherent story; each member must understand the whole thesis, not just their slice |

### 2.1 Mid-viva bridge (use this wording)

The mid-viva was based on the older, broader platform idea. Do not hide the pivot. Say:

> "Following the mid-viva review, the project was sharpened from a broad reliability platform
> into a specific research question: *can autonomous remediation be made safe by explicit
> invariants?* The distributed order platform remains the testbed; the safety shield and its
> empirical evaluation are the contribution. Router/regions were removed as out-of-scope for
> that question."

### 2.2 Proposed project identity

- **Name (optional):** AEGIS — *Autonomic Guardrails for Incident Stabilization*.
- **Title:** *Safety-Constrained Autonomous Remediation for Distributed Transaction Systems*.
- **One-line pitch:** A laptop-runnable distributed order platform where a prediction-driven
  controller proposes remediations, but a deterministic safety shield vetoes any action that
  would violate transactional or operational invariants — demonstrated by refusing to scale
  into a database bottleneck.

### 2.3 Contributions (the four things to defend)

1. **Invariant model.** A catalog of machine-checkable transactional and operational safety
   invariants for autonomous remediation, each with a predicate, data source, and enforcement point.
2. **Safety-shield architecture.** A deterministic, stateless shield that sits between a
   scoring policy and the effectors, plus a decision ledger that makes every allow/block auditable.
3. **Empirical comparison.** A reproducible baseline (reactive CPU-threshold scaler) vs proposed
   (predictor + policy + shield) experiment on the database-bottleneck scenario, with correctness
   as a hard floor.
4. **Advisory explanation layer.** A GenAI postmortem/RAG service that reads the same evidence
   tables as the controller and labels every claim `FACT / PREDICTION / INFERENCE / RECOMMENDATION`.

### 2.4 Claim boundaries — state these openly

- The ML models predict **injected fault modes** in a synthetic testbed; they are an early-warning
  signal, not a general production failure predictor.
- "Scale" on one laptop is a **control action under resource contention**, not additional capacity.
  The honest claim is *avoids harmful actions*, not *improves throughput*.
- Regions/router are removed; failover is not claimed.
- Single Kafka broker and single Postgres are SPOFs; this is a testbed, not production.
- The LLM is advisory, text/JSON only, with no infrastructure permissions.
- Resource/cost figures are local proxies, not cloud money.

---

## 3. Current state (what already exists)

Verified against code on disk:

| Component | Path | State |
|-----------|------|-------|
| Gateway | `apps/gateway/main.go` | `POST /api/orders`, transaction proxies, `POST /api/chaos/risk-outage`, `/healthz`, `/metrics` stub |
| Order | `apps/order/main.go` | create/read orders |
| Inventory | `apps/inventory/main.go` | reserve/release |
| Payment | `apps/payment/main.go` | authorize/refund (simulated) |
| Risk | `apps/risk/main.go` | check + chaos 500 flag |
| Saga | `apps/saga/main.go` | synchronous reserve→authorize→risk, reverse compensation, `saga_svc.saga_steps` |
| GenAI | `ai/service/app.py` | OpenRouter postmortem + RAG, mock fallback, `generated_reports` |
| Frontend | `frontend/index.html` | one static page: order, chaos, transaction feed, report box |
| DB | `migrations/001_init.sql` | 6 schemas; outbox/processed_events/idempotency tables exist but are unused |
| Kafka | `apache/kafka` KRaft | saga publishes `transaction.events`; **no consumers** |
| Compose | `docker-compose.yml` | 10 services; no Prometheus, no controller, no shield |
| Scripts | `scripts/smoke.sh`, `scripts/demo.sh` | happy order + risk-outage order + postmortem |

**Known gaps to close (from `PROJECT_STATUS.md`):** no outbox poller, no consumers/dedupe,
no idempotency enforcement, no retries/crash-resume, only 1 of 6 chaos scenarios, no metrics,
no ML, no controller, no shield, no experiments, 1-page frontend.

This plan adds the **shield + controller + ML + experiments + observability + 3-page frontend**,
and folds in the correctness gaps that the safety story depends on (idempotency, dedupe,
retries, terminal-state reaper).

---

## 4. Target architecture

### 4.1 Service inventory

| # | Service | Lang | Port | Role | Status |
|---|---------|------|------|------|--------|
| 1 | gateway | Go | 8080 | entry, correlation ID, idempotency enforcement, client-side round-robin | exists; extend |
| 2 | order-service | Go | 8081 | order state machine | exists |
| 3 | inventory-service | Go | 8082 | reserve/release | exists |
| 4 | payment-service | Go | 8083 | authorize/refund; DB-latency chaos flag | exists; extend |
| 5 | risk-service | Go | 8084 | fraud check; outage flag | exists |
| 6 | saga-orchestrator | Go | 8085 | saga state + compensation; retries; stuck-saga reaper | exists; extend |
| 7 | **safety-shield** | Go | **8087** | **deterministic invariant evaluator (stateless)** | **new** |
| 8 | **reliability-controller** | Go | **8086** | **telemetry → predictor → policy → shield → executor → verifier; decision ledger; read-only invariant audit** | **new** |
| 9 | ml-service | Python | 9000 | `/forecast`, `/risk` (XGBoost ×2) | **new** |
| 10 | genai-service | Python | 9001 | postmortem + RAG over evidence | exists; extend |
| 11 | frontend | React+TS | 5173 | 3 pages: overview, transactions/saga, decisions+chaos | **new** (replaces static page) |
| — | postgres+pgvector | — | 5432 | system of record + vector store | exists |
| — | kafka (KRaft) | — | 9092 | event backbone | exists |
| — | prometheus | — | 9090 | telemetry | **new** |

**Removed:** router/LB (8000), Region A/B, `FAILOVER_REGION` / `ROUTE_AWAY` actions.
**Optional/dev-only:** Grafana (never demo-critical).

### 4.2 Diagram

```
                    React dashboard (5173)
                           │
                           ▼
                     gateway (8080)
          correlation ID · idempotency · round-robin
                           │
                           ▼
                  saga-orchestrator (8085)
       persist step state · compensate · reaper
            ┌──────────────┼──────────────┐
            ▼              ▼              ▼
       inventory       payment         risk
         │               │              │
         └── business row + outbox row (one DB tx) ──┘
                           │
                     outbox poller
                           ▼
                      Kafka (9092)
                           │
        ┌──────────────────┼──────────────────┐
        ▼                  ▼                  ▼
   consumers         prometheus (9090)   ml-service (9000)
   (dedupe)               │              /forecast /risk
                          ▼                  │
                 reliability-controller (8086)
              telemetry → policy → SHIELD → executor → verify
                           │                  │
                           ▼                  ▼
                    safety-shield        effectors:
                      (8087)             docker compose scale
                    pure, deterministic  feature flags / shed
                           │
                           ▼
                    decision ledger (Postgres)
                           │
                           ▼
                   genai-service (9001)
              evidence bundle → labeled postmortem
```

### 4.3 Control loop (proposed mode)

One control cycle, every `CONTROL_INTERVAL` (default 15 s):

1. **Collect.** Controller pulls `/metrics` from each service and a short telemetry window
   from Prometheus; reads replica counts and feature-flag state.
2. **Predict.** Calls `ml-service /risk` (failure probability + top features) and
   `/forecast` (load t+5/t+15). Prediction carries a timestamp.
3. **Policy.** Ranks candidate actions from the action space using
   `score = w1·predicted_latency + w2·failure_risk + w3·resource_cost + w4·instability_penalty`.
   Lowest score first.
4. **Shield.** For each candidate in order, POST to `safety-shield /evaluate` with a state
   snapshot. First `allow` wins. A blocked candidate is logged with its invariant.
5. **Execute.** The chosen action is executed by the single Action Executor:
   `docker compose --scale`, feature-flag calls (shed/circuit-break), or `NO_ACTION`.
6. **Verify.** Re-read metrics after a verification window; record before/after and whether
   invariants still hold.
7. **Ledger.** Every proposal, verdict, action, and verification is persisted
   (§6.2). Nothing is executed without a `decision_uid`.

### 4.4 Baseline mode (the comparator)

Same application, same saga, same instrumentation. A **reactive CPU-threshold scaler**
(CPU > 80% → `SCALE_UP`, CPU < 30% → `SCALE_DOWN`) runs instead of predictor+policy+shield.
It can violate operational invariants; those violations are recorded as **harmful actions**.
Baseline mode must not call the shield.

---

## 5. Safety-shield service

### 5.1 Design rules

- **Pure function.** No DB, no clock, no randomness, no network. Input fully determines output.
- **Stateless.** The controller persists all verdicts; the shield can be replicated freely.
- **Deterministic.** Given the same `{action, state}`, it always returns the same verdict.
  This is unit-testable and is the core safety claim.
- **Versioned.** Invariant set has a version; responses include `invariant_version`.
- **Fail-closed at the caller.** If the shield is unreachable, the controller treats it as
  `BLOCKED` and chooses `NO_ACTION`; it never bypasses the shield.

### 5.2 API

```http
POST /evaluate
Content-Type: application/json

{
  "decision_uid": "uuid",
  "action": "SCALE_UP",
  "state": {
    "service": "payment-service",
    "replicas": 2,
    "min_replicas": 1,
    "max_replicas": 5,
    "db_conn_pct": 92.5,
    "db_conn_threshold": 80,
    "error_rate": 0.02,
    "p95_latency_ms": 420,
    "risk": 0.87,
    "prediction_ts": "2026-11-18T10:00:00Z",
    "prediction_max_age_s": 120,
    "now": "2026-11-18T10:00:30Z",
    "last_action_at": "2026-11-18T09:58:00Z",
    "cooldown_s": 60,
    "actions_in_window": 1,
    "action_budget_per_window": 5,
    "budget_remaining_usd": 8.5,
    "budget_ceiling_usd": 10,
    "target_healthy": true,
    "order_id": "uuid-or-empty",
    "transaction_state": { "status": "RUNNING", "current_step": "authorize" }
  }
}
```

Response (first violated invariant wins, in priority order):

```json
{ "allow": false, "invariant_id": "INV-O2", "reason": "db_conn_pct 92.5 >= threshold 80; scale-up would add DB connections", "invariant_version": 1 }
```

```json
{ "allow": true, "invariant_id": null, "reason": "all invariants satisfied", "invariant_version": 1 }
```

Other endpoints: `GET /invariants` (catalog + version), `GET /healthz`.

### 5.3 Invariant priority

Evaluate in this order; return the first violation:

1. Freshness / validity of input (stale prediction, malformed state) → `INV-O5`
2. Action idempotency (`decision_uid` not seen) → `INV-O3`
3. Replica bounds → `INV-O1`
4. DB connection ceiling → `INV-O2`
5. Target health (if action routes traffic) → `INV-O3b` (only if routing is reintroduced)
6. Cooldown / oscillation → `INV-O4`
7. Budget ceiling → `INV-O6`
8. Transaction non-regression → `INV-O7`

---

## 6. Invariant catalog (the core artifact)

Each invariant is stored in code (and mirrored in `safety_svc.invariants`) with: id, family,
predicate, data source, enforcement point, error message, version.

### 6.1 Transaction invariants — correctness floor

| ID | Predicate | Data source | Enforcement |
|----|-----------|-------------|-------------|
| **INV-T1** | For each order, at most one `SUCCEEDED` payment authorization; a refund exists only after an authorization | `payment_svc.payments` | saga + reaper audit |
| **INV-T2** | Every reservation is either consumed by a `COMPLETED` order or `released`; no `reserved` row for a terminal order | `inventory_svc.reservations`, `order_svc.orders` | saga + reaper audit |
| **INV-T3** | Every saga reaches `COMPLETED` or `FAILED` within `SAGA_MAX_AGE` (default 120 s); no `RUNNING`/`COMPENSATING` beyond that | `saga_svc.sagas` | stuck-saga reaper |
| **INV-T4** | Every failed step with prior successes has a corresponding `COMPENSATED` step | `saga_svc.saga_steps` | saga + audit |
| **INV-T5** | Duplicate `Idempotency-Key` returns the original response; duplicate `event_id` produces no second side effect | `order_svc.idempotency_keys`, `*.processed_events` | gateway + consumers |

### 6.2 Operational invariants — shield-enforced

| ID | Predicate | Shield check |
|----|-----------|--------------|
| **INV-O1** | `min_replicas ≤ replicas ≤ max_replicas` for every service | scale up/down |
| **INV-O2** | No `SCALE_UP` for a service whose `db_conn_pct ≥ db_conn_threshold` | scale up |
| **INV-O3** | A `decision_uid` is executed at most once | all actions |
| **INV-O4** | No action repeats within `cooldown_s`; actions per window ≤ `action_budget_per_window` | all actions |
| **INV-O5** | No action on a prediction older than `prediction_max_age_s` | all actions |
| **INV-O6** | Cumulative action cost ≤ `budget_ceiling_usd` | all actions |
| **INV-O7** | Post-action state violates no T-invariant; a running saga is never left stranded | all actions |

**Extended (optional):** per-service action quotas, shed-vs-degrade preference ordering,
prediction-confidence floor before acting.

### 6.3 Action space

`NO_ACTION · SCALE_UP · SCALE_DOWN · SHED_LOAD · ENABLE_CIRCUIT_BREAKER`

The database-bottleneck story: baseline chooses `SCALE_UP` (harmful, violates INV-O2);
proposed shield blocks it and the policy chooses `SHED_LOAD` or `ENABLE_CIRCUIT_BREAKER`.

### 6.4 Read-only invariant audit (replaces the independent monitor)

Since there is no separate monitor service, the controller runs a **periodic audit** (default
every 30 s) that evaluates T1–T5 and O1/O7 against the database and writes
`reliability_svc.invariant_audit(id, invariant_id, ok, detail, checked_at)`. Failures raise an
`InvariantViolation` event and appear in the evidence bundle. This keeps the safety claim honest
without adding a service.

---

## 7. Data model additions

New migration `migrations/002_safety_reliability.sql`. One schema per service; no cross-schema
reads from application code beyond the audit (which is a read-only analytical job, documented
as such).

```sql
CREATE SCHEMA IF NOT EXISTS safety_svc;
CREATE SCHEMA IF NOT EXISTS reliability_svc;

-- Catalog mirror (the authoritative copy lives in the shield's code)
CREATE TABLE safety_svc.invariants(
  id text PRIMARY KEY,              -- 'INV-T1'
  family text NOT NULL,             -- 'transaction' | 'operational'
  name text NOT NULL,
  description text NOT NULL,
  predicate text NOT NULL,
  enforcement text NOT NULL,        -- 'shield' | 'saga' | 'audit'
  error_message text NOT NULL,
  enabled boolean DEFAULT true,
  version int DEFAULT 1
);

CREATE TABLE reliability_svc.predictions(
  id bigserial PRIMARY KEY,
  service text NOT NULL,
  failure_probability numeric,
  risk_level text,
  horizon_minutes int,
  predicted_cause text,
  top_features jsonb,
  forecast jsonb,
  created_at timestamptz DEFAULT now()
);

CREATE TABLE reliability_svc.decisions(
  decision_uid uuid PRIMARY KEY,
  incident_id uuid,
  service text NOT NULL,
  policy_action text NOT NULL,
  shield_action text NOT NULL,
  verdict text NOT NULL,            -- 'ALLOWED' | 'BLOCKED'
  blocked_by text,                  -- invariant id
  reason text,
  score numeric,
  state_snapshot jsonb,
  created_at timestamptz DEFAULT now()
);

CREATE TABLE reliability_svc.actions(
  id bigserial PRIMARY KEY,
  decision_uid uuid NOT NULL,
  action text NOT NULL,
  status text NOT NULL,             -- 'PENDING' | 'EXECUTED' | 'FAILED'
  executed_at timestamptz,
  result text,
  error text
);

CREATE TABLE reliability_svc.verifications(
  id bigserial PRIMARY KEY,
  decision_uid uuid NOT NULL,
  metrics_before jsonb,
  metrics_after jsonb,
  improved boolean,
  invariants_ok boolean,
  created_at timestamptz DEFAULT now()
);

CREATE TABLE reliability_svc.invariant_audit(
  id bigserial PRIMARY KEY,
  invariant_id text NOT NULL,
  ok boolean NOT NULL,
  detail text,
  checked_at timestamptz DEFAULT now()
);

CREATE TABLE reliability_svc.metrics_snapshots(
  id bigserial PRIMARY KEY,
  service text NOT NULL,
  ts timestamptz DEFAULT now(),
  requests_per_min numeric,
  p95_latency_ms numeric,
  error_rate numeric,
  cpu_pct numeric,
  mem_pct numeric,
  db_conn_pct numeric,
  consumer_lag numeric,
  replicas int
);

CREATE TABLE reliability_svc.experiments(
  id uuid PRIMARY KEY,
  scenario text NOT NULL,           -- 'db-slowdown' | 'risk-outage' | ...
  mode text NOT NULL,               -- 'baseline' | 'proposed'
  status text NOT NULL,             -- 'running' | 'done' | 'failed'
  params jsonb,
  started_at timestamptz DEFAULT now(),
  ended_at timestamptz
);

CREATE TABLE reliability_svc.experiment_runs(
  id bigserial PRIMARY KEY,
  experiment_id uuid NOT NULL,
  run int NOT NULL,
  p95_latency_ms numeric,
  p99_latency_ms numeric,
  error_rate numeric,
  slo_violations int,
  mttr_ms numeric,
  harmful_actions int,
  incorrect_final_states int,
  shield_blocks int,
  actions_taken int,
  notes text
);
```

Existing tables to finally use: `order_svc.idempotency_keys`, all `*.outbox`,
`*.processed_events`, `saga_svc.saga_steps`. Existing `ai_svc.incidents` /
`ai_svc.incident_evidence` gain rows for non-risk chaos scenarios.

---

## 8. API surface

### 8.1 Gateway (public)

```
POST /api/orders                       header Idempotency-Key (enforced)
GET  /api/orders/:id
GET  /api/transactions                 paginated
GET  /api/transactions/:id             saga timeline
POST /api/transactions/:id/retry
GET  /api/system/health
GET  /api/services
GET  /api/predictions
GET  /api/decisions
GET  /api/incidents
GET  /api/metrics/summary
POST /api/chaos/risk-outage            {enabled}
POST /api/chaos/db-latency             {service, added_ms}
POST /api/chaos/kill                   {service}
POST /api/chaos/consumer-lag           {service, sleep_ms}
POST /api/chaos/traffic-spike          {rps, duration_s}
POST /api/ai/generate-report           {incident_id}
POST /api/ai/query                     {question, incident_id?}
POST /api/experiments/run              {scenario, mode, runs}
GET  /api/experiments
```

Chaos and AI endpoints are enabled only when `ENV=local`.

### 8.2 safety-shield (internal, :8087)

```
POST /evaluate        {decision_uid, action, state} -> {allow, invariant_id, reason, invariant_version}
GET  /invariants      -> catalog + version
GET  /healthz
```

### 8.3 reliability-controller (internal, :8086)

```
POST /control/cycle                    run one cycle on demand
GET  /decisions                        ledger
GET  /decisions/:uid
GET  /predictions
GET  /actions
GET  /verifications
GET  /audit                            invariant audit results
GET  /metrics/summary
POST /mode                             {mode: baseline|proposed}
POST /experiments/run                  {scenario, mode, runs}
GET  /experiments
```

### 8.4 ml-service (internal, :9000)

```
POST /forecast   {service, window:[{ts,requests,cpu_pct,mem_pct,p95_latency_ms,db_conn_pct,consumer_lag}]}
                 -> {requests_5m, requests_15m, cpu_pct_5m, db_conn_pct_5m}
POST /risk       {service, features:{...}}
                 -> {failure_probability, risk_level, horizon_minutes, predicted_cause, top_features:[{name,contribution}]}
GET  /healthz
```

### 8.5 Event additions

Existing envelope is unchanged. New event types (all keyed by `aggregate_id`):

`PredictionMade · DecisionProposed · ShieldVerdict · ActionExecuted · RecoveryVerified ·
InvariantViolation · ExperimentCompleted`

New topics: `reliability.events`, `safety.events`, `dead-letter.events`.

---

## 9. Tech stack for the additions

| Layer | Choice | Why |
|-------|--------|-----|
| Control plane | Go 1.23, stdlib `net/http` (or chi), `pgx/v5`, `segmentio/kafka-go`, `prometheus/client_golang` | tiny RAM per service, matches existing code |
| Shield | Go, pure functions, table-driven tests | determinism is the claim |
| ML | Python 3.11, FastAPI, XGBoost, scikit-learn, pandas, psycopg | correct ecosystem; sidecar over HTTP |
| GenAI | Python, FastAPI, OpenRouter chat + embeddings, pgvector | already built; keep |
| DB | Postgres 16 + pgvector | already built; add `002` migration |
| Events | Kafka KRaft single broker, 512 m heap | already built; add consumers |
| Metrics | Prometheus + `client_golang`; Grafana dev-only | needed for telemetry and ML features |
| Frontend | React 18 + TypeScript + Vite, TanStack Query, Recharts, Tailwind | 3 pages, typed API, live feed |
| Load generator | Python script (`scripts/loadgen.py`) | reproducible traces |
| Experiments | Python harness (`experiments/run.py`) | automated N-run comparison |
| Tests | Go tests (shield, saga machine, rules), pytest (ML/features), Compose integration, E2E scripts | determinism + correctness |
| CI | GitHub Actions: lint → unit → build → integration (<10 min) | trunk-based, protected main |

---

## 10. Implementation plan

### P0 — must work before the final viva (thesis-critical)

**P0.1 Correctness foundations (A)**
- Enforce `Idempotency-Key` at the gateway; return the stored response on duplicates.
- Outbox poller in each service; at-least-once publish; mark published.
- One real consumer with `processed_events` dedupe; DLQ for poison messages.
- Bounded retries + backoff/jitter in saga; stuck-saga reaper on startup (T3/T4).
- Chaos: `docker kill` path; DB-latency flag in payment.
- Exit: duplicate POST → one order; replayed event → one effect; crashed saga resumes.

**P0.2 Safety shield (A)**
- Invariant catalog as versioned Go code; `POST /evaluate`; priority order; fail-closed.
- Unit tests: every invariant, allow + block cases; determinism test (same input → same output).
- `GET /invariants`; `safety_svc.invariants` mirror.
- Exit: table-driven test suite green; shield reachable from controller.

**P0.3 Controller + ledger (A)**
- Telemetry collection from `/metrics` + Prometheus; control cycle ticker.
- Policy scoring over the action space; shield client; Action Executor (`docker compose scale`,
  shed/circuit-break flags); post-verify.
- Decision ledger tables; read-only invariant audit.
- `POST /mode` to switch baseline/proposed.
- Exit: DB-bottleneck run produces a blocked `SCALE_UP` and a chosen safe action, all ledgered.

**P0.4 Observability (A+B)**
- Real Prometheus metrics on all services: rate, latency histogram, error rate, in-flight,
  DB pool usage, consumer lag, restart count; structured JSON logs with correlation ID.
- `/healthz` + `/readyz`.
- Exit: one order traceable by correlation ID; `/metrics/summary` works.

**P0.5 ML service + dataset (B)**
- Load generator (steady/ramp/spike/sustained) + telemetry export to CSV.
- Label 10-min pre-failure windows; **time-based split**; train XGBoost forecast + risk.
- Evaluate vs trivial `CPU > 85%` baseline (precision/recall/F1/ROC-AUC/false-alarm/lead-time).
- Serve `/forecast` and `/risk`; save artifacts to `ml/models/`.
- Exit: models + metrics doc committed; negative result documented if baseline wins.

**P0.6 Chaos + incidents (B)**
- All scenarios write `ai_svc.incidents` + `incident_evidence` (risk-outage already does).
- DB-latency flag, kill, traffic spike, consumer lag.
- Exit: one button/command per scenario; each leaves an incident + evidence.

**P0.7 Headline experiment (B)**
- Baseline vs proposed on DB-slowdown, N≥5, same load trace, same seeds; means + spread.
- Collect: harmful actions, P95/P99, error rate, SLO violations, MTTR, incorrect final states,
  shield blocks, actions taken.
- Exit: headline chart + metrics table; correctness zeros in both modes.

**P0.8 Frontend 3 pages (C)**
- Overview (health, active tx, P95, risk, lag, SLO), Transactions (saga timeline incl.
  compensation), Decisions + Chaos (ledger, shield verdicts, buttons, experiments summary).
- Typed API client, loading/error/empty states, live polling.
- Exit: full incident story readable without touching logs/DB.

**P0.9 GenAI evidence update (C)**
- Evidence bundle includes predictions, decisions, shield verdicts, audit results.
- Postmortem labels claims; report stores model/prompt/evidence/tokens/cost.
- Exit: report for the DB-bottleneck incident cites shield decisions and real rows.

### P1 — strengthens results
- Remaining chaos scenarios and experiment breadth.
- RAG corpus seeding (docs + past reports) and root-cause Q&A.
- More experiment scenarios; charts.
- Report assembly begins.

### P2 — polish
- Grafana dashboards (dev-only), extra dashboard pages, spend display, demo rehearsals.

### 10.1 Suggested ownership

| Member | Owns |
|--------|------|
| **A** | correctness foundations, safety-shield, controller, ledger, audit, metrics |
| **B** | ML service, dataset/training, chaos, load generator, experiments |
| **C** | frontend, GenAI evidence/RAG, docs/ADRs/report, demo script |
| All | integration testing, rehearsals, viva prep |

Balance rule: nobody owns >40% of services; recount weekly.

### 10.2 Build order (dependency-aware)

1. Correctness foundations (P0.1) — unblocks everything.
2. Metrics/observability (P0.4) — needed by controller and ML.
3. Shield (P0.2) — independent, can parallelize.
4. ML service (P0.5) — can parallelize with shield.
5. Controller (P0.3) — needs shield + metrics + ML.
6. Chaos (P0.6) — needs metrics + controller.
7. Experiment (P0.7) — needs all above.
8. Frontend + GenAI (P0.8/P0.9) — can parallelize late.

---

## 11. Experiment design

### 11.1 Research question

> Can a telemetry-driven controller, constrained by explicit safety invariants, reduce harmful
> remediations and SLO violations versus an unconstrained reactive baseline, without violating
> transaction correctness?

### 11.2 The headline experiment (DB bottleneck)

| | Baseline | Proposed |
|---|---|---|
| Policy | CPU threshold | predictor + scoring |
| Safety | none | shield |
| Action under DB pressure | `SCALE_UP` (harmful) | blocked by INV-O2 → `SHED_LOAD` / `ENABLE_CIRCUIT_BREAKER` |
| Expected | more DB connections, worse P95, more errors | load capped, P95 stabilizes, errors bounded |
| Correctness | zero violations | zero violations |

### 11.3 Protocol
- Same application, same saga, same load trace, same seed, same initial DB state.
- N ≥ 5 runs per scenario per mode; automate; report mean ± spread (never single runs).
- Reset/ isolate state between runs (separate run IDs; do not mix).
- Baseline and proposed run on the same machine, same time window if possible.

### 11.4 Metrics
- **Reliability:** SLO violation rate, P95/P99 latency, error rate, MTTR.
- **Correctness:** incomplete transactions, duplicate operations, incorrect final states,
  failed compensations (target zero in both modes — a floor, not a variable).
- **ML:** precision, recall, F1, ROC-AUC, false-alarm rate, mean prediction lead time.
- **Controller:** actions taken, harmful actions, shield blocks, oscillation, time to stabilise.
- **Efficiency proxy:** CPU-seconds, memory-seconds, replica-minutes (labelled local proxy).

### 11.5 ML honesty rules
- Time-based split only (never random across incident windows).
- Always compare against a trivial baseline (`CPU > 85%`).
- Document a negative result if the baseline wins.
- Frame the model as early-warning for injected fault modes.

---

## 12. Demo & presentation plan

### 12.1 Slides (~16)
1. Title + team
2. Problem: autonomous remediation can harm (DB-bottleneck anecdote)
3. Research question + objectives
4. Related work: MAPE-K, safe-RL shields/runtime enforcement, saga, chaos engineering
5. Proposed approach: safety-constrained autonomy
6. Architecture (two planes, shield in the middle)
7. Invariant catalog (T1–T5, O1–O7)
8. Controller pipeline (predict → policy → shield → execute → verify)
9. Implementation stack
10. Demo 1: happy order + saga
11. Demo 2: risk outage → compensation
12. Demo 3: DB bottleneck — baseline scales, shield blocks
13. Results / metrics
14. GenAI incident analysis
15. Limitations (use §2.4)
16. Conclusion + future work

### 12.2 Demo script (~8–10 min, four beats)
1. **Happy order.** Place an order; open the saga timeline; note reserve → authorize → risk → COMPLETED.
2. **Risk outage.** Enable the flag; place an order; show reserve + authorize succeed, risk fails,
   refund + release compensate, order FAILED, zero double charge.
3. **DB bottleneck.** Start load; enable DB latency. Baseline: CPU rises → `SCALE_UP` → P95 worsens.
   Proposed: risk rises, top feature `db_conn_pct`; policy proposes `SCALE_UP`; shield blocks on
   INV-O2; `SHED_LOAD` chosen; P95 stabilises. Show the ledger row and the invariant reason.
4. **GenAI.** Generate the postmortem live; read the FACT/PREDICTION/INFERENCE/RECOMMENDATION
   lines; point out the shield verdict in the evidence.

### 12.3 Group viva split
- **A:** problem, research question, architecture, shield + invariants.
- **B:** controller, ML, experiments, results.
- **C:** live demo, GenAI, limitations, report.
- Everyone answers the shared Q bank.

### 12.4 Report outline
Abstract → Introduction (problem, motivation, objectives, scope) → Literature Survey →
System Requirements & Design (architecture, invariants, data model, APIs) → Implementation
(technologies, algorithms, chaos, ML) → Testing & Results (experiments, metrics, screenshots) →
Conclusion & Future Work → References → Individual Contributions.

### 12.5 Viva Q bank (updated)
- What is your novelty? → the invariant model + shield + empirical comparison.
- How do you guarantee safety? → deterministic shield, priority order, ledger, fail-closed.
- Why not Kubernetes? → RAM and no added research value; Compose gives real replicas.
- Why Kafka? → durable partitioned log, per-key ordering; KRaft single broker + Redpanda fallback.
- Is the ML real? → XGBoost on injected faults, time-based split, beats a trivial baseline or documented negative.
- What did each member build? → per §10.1.
- What are the limitations? → §2.4.

---

## 13. Documentation changes required

1. **New ADRs**
   - `docs/adr/0012-safety-constrained-autonomy.md` — the thesis and shield decision.
   - `docs/adr/0013-no-regions-no-router.md` — removal rationale.
   - `docs/adr/0014-separate-safety-shield-service.md` — why a separate service, why stateless.
2. **Update** `ARCHITECTURE_OPENROUTER.md` — replace the reliability plane with shield + controller;
   remove router/regions; update ports and RAM table.
3. **Update** `PHASES_OPENROUTER.md` — align with P0/P1/P2 in §10.
4. **Update** `PROJECT_STATUS.md` — mark stale claims; record what is actually built after each phase.
5. **Fix contradictions** across `project_report_final.md`:
   - viva answer "local open-weight model with no API" → OpenRouter.
   - limitation 4 "sequential demo mode" → removed by ADR 0011.
   - one-line pitch "local GenAI layer" → OpenRouter advisory layer.
   - `final_project_plan.md` still says NATS and lists Kafka as cut → align to Kafka.
6. **Add** `docs/SAFETY_INVARIANTS.md` (human-readable catalog) and `docs/EVENT_CONTRACTS.md`.
7. **Add** `docs/EXPERIMENTS.md` (protocol + results) and `docs/DEMO_SCRIPT.md`.

---

## 14. Edge cases and how the design handles them

| Edge case | Handling |
|----------|----------|
| Shield unreachable | Controller fails closed → `NO_ACTION`; logged |
| TOCTOU between shield check and execution | Executor re-checks invariants; `decision_uid` ledger prevents double execution |
| Shield blocks the only policy action | Policy walks to the next candidate; if none pass, `NO_ACTION` + alert |
| Action execution fails | Recorded `FAILED`; bounded retry; no blind retry storm |
| Stale prediction | INV-O5 blocks; controller re-predicts |
| False-positive risk | Require N consecutive samples / confidence floor before acting; action budget |
| Oscillation | INV-O4 cooldown + instability penalty in scoring |
| Controller crash mid-cycle | Next tick resumes; all state in DB; `decision_uid` idempotent |
| Multiple controllers | Run a single controller replica (documented) |
| Saga stuck after crash | Reaper on startup + audit (T3/T4) |
| Outbox publish succeeds, mark fails | At-least-once + consumer dedupe (T5) |
| Kafka down | Outbox backlog; retry with backoff; transactions unaffected |
| Region removal | No cross-region claims; failover not in scope |
| LLM hallucination | Evidence-only prompt, claim labels, traceable rows, deterministic audit |
| LLM network/cost failure | `:free` fallback chain → labeled offline mock; never silent |
| Experiment contamination | Same trace/seed; isolated run IDs; reset state |
| ML leakage | Time-based split; incident-level separation |

---

## 15. Risks

| Risk | Mitigation |
|------|------------|
| Scope creep | Protect P0; cut P2 without guilt |
| Time | Decide the P0 cutoff date now; weekly balance check |
| ML overclaim | Honest framing + trivial baseline + time split |
| Mid-viva mismatch | Use §2.1 bridge everywhere |
| Group viva silos | Shared Q bank; each member learns the whole thesis |
| Unfinished correctness claims | P0.1 is first; no "outbox works" claim until poller + consumer are green |
| Single host measurement noise | N≥5, means + spread, controlled traces |
| Demo-day network loss | Offline mock + pre-generated fallback report, clearly labeled |

---

## 16. Definition of done (final viva)

- [ ] One happy order and one compensated failed order, live.
- [ ] Shield blocks `SCALE_UP` under DB pressure; safe action chosen; ledger row visible.
- [ ] Baseline-vs-proposed DB-bottleneck experiment with N≥5 and a headline chart.
- [ ] Zero incorrect final states in both modes.
- [ ] ML models evaluated against a trivial baseline; honest metrics doc.
- [ ] GenAI postmortem cites real rows and shield verdicts; claims labeled.
- [ ] 3-page dashboard tells the whole story.
- [ ] Report + slides + viva Q bank complete; two rehearsals on the demo machine.
- [ ] Docs consistent; contradictions removed; ADRs merged.

---

## 17. Open questions / assumptions

1. Exact number of weeks until the final viva — needed to set the P0 cutoff.
2. Institute report format/rubric — if prescribed, map §12.4 onto it.
3. Member skills (Go / Python-ML / frontend-docs) — confirm the §10.1 ownership split.
4. Whether to keep the name AEGIS or use a plain descriptive title.
5. Whether `RESTART_SERVICE` is wanted in the action space (currently excluded).
6. Whether the frontend should be React (planned) or extend the existing static page (faster, weaker story).
