# Project Briefing — Planned Vision + Actual Build Status

> Audience: the team. Purpose: one file that says what this project is supposed to become,
> and — with zero exaggeration — how far it has actually been built.
> **Part A = the plan** (from `project_report_final.md`, `ARCHITECTURE_OPENROUTER.md`,
> `PHASES_OPENROUTER.md`). **Part B = ground truth** (verified against code on disk
> and containers running now). If Part B disagrees with any older doc, Part B wins.

---

# PART A — WHAT THE PROJECT IS PLANNED TO DO

## A1. One-sentence pitch

A small online order-processing system deliberately engineered to survive failure —
and to explain itself when failure happens.

## A2. The story it tells (the whole project in one scenario)

1. A customer places an order. That single action must succeed across **four independent
   services that do not share a database**: reserve stock, authorize payment, run a
   fraud/risk check, confirm the order.
2. Normally all three steps succeed and the order completes.
3. Now inject failure: the risk service goes down (one button in the Chaos Lab).
   The order must NOT be left half-finished. The system automatically **refunds the
   payment, releases the reserved inventory, and marks the order cleanly FAILED** —
   not stuck, not silently wrong, not double-charged.
4. Meanwhile, *before* anything broke, an ML layer was forecasting load and scoring
   each service's risk of failing in the next ~10 minutes. When risk rises, a decision
   engine picks a safe action: add capacity, or **refuse** to add capacity (because more
   instances would open more DB connections and worsen the real bottleneck), or shift
   traffic to a healthy region.
5. Once the incident resolves, a GenAI layer reads the actual evidence the system
   produced and writes a real incident postmortem — the document an SRE writes by hand
   today — with every claim labeled FACT / PREDICTION / INFERENCE / RECOMMENDATION.

Supporting scenarios: traffic spike (does the forecaster see it coming?), DB slowdown
(does the controller avoid the classic scale-into-the-bottleneck mistake?), region
outage (does traffic genuinely reroute to the backup?).

## A3. Constraints that shaped every decision

- **C1′. Near-zero API spend, not zero.** OpenRouter key, cheap workhorse models
  (`gpt-4o-mini` / `gemini-flash` class) + `:free` fallbacks, monthly + per-report caps.
  (Original C1 was $0/local-Ollama; the team moved to OpenRouter — see ADR 0011 —
  trading ~1.5–2.5 GB of local LLM RAM for bounded API spend and a fully live demo.)
- **C2. Genuinely end-to-end, demoed live.** No stubs on demo day (except a labeled
  offline fallback).
- **C3. ~8 GB RAM laptops, no GPU.** Usable container budget ~4–5 GB. No local LLM
  means the stack fits without "sequential demo mode".
- **C4. Exactly 3 people, non-blocking.** Week-1 runnable stub rule; nobody owns >40%
  of services; every feature touches ≥2 areas.

## A4. Architecture — two planes, one dashboard

```
TRANSACTION PLANE (does business work)      RELIABILITY PLANE (watches, reacts)
--------------------------------------      -----------------------------------
gateway (:8080)                             prometheus (:9090)
order (:8081) / inventory (:8082)           ml-service (:9000)  XGBoost ×2
payment (:8083) / risk (:8084)              reliability-controller (:8086)
saga-orchestrator (:8085)                     rules + scoring → Action Executor
postgres (:5432, pgvector/pg16)             router/lb (:8000)  health map, failover
kafka (:9092, KRaft single broker)          genai-service (:9001) → OpenRouter cloud
outbox + idempotency + dedupe               dashboard (:5173)
```

Full planned lifecycle:

```
Dashboard → gateway → router (health map) → saga-orchestrator
  → inventory / payment / risk (each: business row + outbox row, one tx)
  → outbox poller → KAFKA → consumers (dedupe via processed_events)
  → prometheus → ml-service (/forecast, /risk)
  → reliability-controller (hard rules → score → Action Executor)
  → router scale/reroute + saga compensations (independent path)
  → verify recovery → decisions/incident_evidence tables
  → genai-service → OpenRouter chat + embeddings → pgvector RAG
  → incident report markdown → dashboard
```

Key design points the plan insists on:
- **Saga over 2PC.** Services own separate databases; no coordinator holding locks.
  Saga trades atomicity for eventual consistency with explicit, testable compensations
  in reverse order, idempotent, bounded retries with backoff+jitter, resume-after-crash.
- **Transactional outbox.** Business row + outbox row commit in one local tx; a poller
  publishes to Kafka. No lost events (dual-write bug eliminated).
- **At-least-once + idempotent consumers.** `processed_events(event_id, consumer_name)`
  dedupe; offsets committed after processing; dead-letter topic for poison messages.
- **Kafka keyed by aggregate_id** so one order's events stay ordered on one partition.
- **ML is a signal, never an authority.** XGBoost regressor (load t+5/t+15) + XGBoost
  classifier (failure-within-10m + top features), one shared feature pipeline, one
  FastAPI process. Time-based train/test split only; must beat a trivial `CPU > 85%`
  baseline or say so.
- **Controller: hard rules first, then scoring.**
  `score = w1·latency + w2·risk + w3·cost + w4·instability`, lowest passing action wins.
  Actions: `NO_ACTION | SCALE_UP | SCALE_DOWN | STOP_SCALING | ROUTE_AWAY | FAILOVER_REGION`.
  Every decision logged with `decision_uid` before execution. Single Action Executor.
- **LLM never touches infra.** Text/JSON output only. Controller and LLM read the SAME
  evidence tables so explanation and action can't drift apart. Model + prompt version +
  token counts + cost stored per report.
- **Regions = two Compose projects on one machine** behind the router. Logical failover
  simulation, stated openly — never claimed as geo-redundancy.
- **Stack:** Go control plane (stdlib `net/http`, `pgx`, `kafka-go`), Python/FastAPI AI
  sidecars, Postgres+pgvector (zero extra vector infra), KRaft Kafka single broker
  512 m heap (Redpanda documented as zero-code-change fallback), plain-HTML/React
  dashboard. Deliberately excluded: K8s, ZooKeeper, Connect/Registry/Debezium,
  Redis, Terraform, Chaos Mesh, tracing stack, service mesh, gRPC, deep learning, RL,
  auth — each with a one-line reason.

## A5. The full feature list (all of it, eventually)

| # | Capability | Status target |
|---|-----------|---------------|
| 1 | Order → reserve → authorize → risk-check saga, happy path | core |
| 2 | Reverse-order compensation (refund → release), clean FAILED | core, never cut |
| 3 | Outbox poller + Kafka consumers + dedupe + idempotency keys | core |
| 4 | Prometheus metrics per service, structured logs, correlation IDs | core |
| 5 | Router + Region A/B + live `docker kill` failover | core |
| 6 | 6 chaos scenarios (kill, DB slowdown, consumer lag, risk outage, region fail, traffic spike) | core, never cut |
| 7 | Load generator (steady/ramp/spike/sustained) + telemetry export | needed for ML |
| 8 | XGBoost forecast + risk models, honestly evaluated | research claim |
| 9 | Reliability controller closed loop (predict → decide → act → verify) | research claim |
| 10 | Live OpenRouter postmortem per incident, saved with model/tokens/cost | AI claim |
| 11 | RAG root-cause Q&A over team docs + past reports (real embeddings) | AI claim |
| 12 | 7-page dashboard (overview, transactions, ML, decisions, chaos lab, experiments, reports) | presentation |
| 13 | Baseline-vs-proposed experiments (N≥5, means+spread), headline DB-slowdown chart | research claim |
| 14 | ~13-min demo script + written report + viva Q bank | delivery |

## A6. Build order (14 weeks, 3 people — `PHASES_OPENROUTER.md`)

W0 lock foundations → W1 contracts + stub + RAM table + OpenRouter smoke test →
W2 order+inventory+outbox → W3 payment+risk+guards → **W4 saga happy+compensation ★** →
W5 observability → W6 router+regions+failover → W7 chaos lab+load gen →
W8 dataset+train+evaluate → **W9 closed loop ★** → W10 GenAI live → W11/W12 frontend →
W13 experiments → W14 report+rehearsals. Cut order if behind: Grafana → experiments
page → GenAI Job 2 → region failover. Never cut: saga compensation, chaos lab.

---

# PART B — WHERE THE PROJECT ACTUALLY IS (fact-checked)

## B0. Latest update — safety-constrained autonomy pivot (verified during the final build)

The sections below were written before the pivot recorded in ADR 0012–0014 and are
**stale wherever they disagree with this section or with `docs/TOMORROW.md`**.

- Thesis is now **safety-constrained autonomy**: a prediction-driven controller proposes
  remediations; a deterministic safety shield is the sole veto; every decision is ledged.
  Regions, router and failover claims were removed (ADR 0013).
- Running now: **13 containers** — the original 10 plus `safety-shield :8087`,
  `ml-service :9000`, `reliability-controller :8086`.
- Real Prometheus `/metrics` on every Go service (the `up 1` stub is gone). Payment has
  a real DB-latency chaos flag using `pg_sleep`, and real `pgxpool` gauges.
- `ml-service` serves a real XGBoost classifier + two forecasters. Honest holdout:
  classifier F1 0.960 / ROC-AUC 0.996 vs trivial `db_conn_pct >= 0.55` baseline
  F1 0.660 / ROC-AUC 0.767. Training data is synthetic (testbed), stated openly.
- `safety-shield` is pure/stateless: `POST /evaluate`, `GET /invariants`. Invariants
  O0/O5/O3/O1/O2/O4/O6/O7 in fixed priority order; table-driven + determinism tests pass.
- `reliability-controller` is a **singleton by design**: scrapes real metrics, calls
  `/risk`, scores candidates `SCALE_UP → SHED_LOAD → ENABLE_CIRCUIT_BREAKER → NO_ACTION`,
  asks the shield for each, writes `decisions → actions → verifications`, and executes
  `SHED_LOAD` against the gateway. Verified live: `SCALE_UP` blocked by `INV-O2`,
  `SHED_LOAD` executed, load generator then shows real 503s and recovering p95.
- Ledger tables: `migrations/002_safety_reliability.sql` (auto-runs on a fresh volume;
  apply manually with `psql` on an existing one).
- Still not built / not claimed: outbox poller, Kafka consumers, idempotency enforcement,
  crash-resume, N≥5 experiments, RAG corpus, regions/failover, production readiness.
  No mock LLM is used in the demo.

## B1. TL;DR

**The transaction core + one chaos scenario + live AI postmortem + a live dashboard
feed are built, containerized, and green.** That's roughly the locked 30% milestone
(W0–W4 + W10-lite + a dashboard slice). Everything involving prediction, automated
reaction, regions, and measurement (W5–W9, W11–W13) is **not started**. Several
correctness mechanisms the plan calls "core" (outbox poller, consumers, dedupe,
idempotency enforcement, retries) have **tables but no running code** — the single
most important thing for the team to understand before claiming them.

## B2. What is running right now (10 containers, verified)

| Container | Image / source | Port | Verified working |
|-----------|---------------|------|------------------|
| postgres | `pgvector/pgvector:pg16`, `migrations/001_init.sql` on first boot | 5432 | healthy; all 6 schemas + tables exist |
| kafka | `apache/kafka:latest`, KRaft single broker, 512 m heap | 9092 | `Kafka Server started`; accepts publishes |
| gateway | `apps/gateway/main.go` | 8080 | order placement, chaos proxy, recent/detail proxy, health |
| order | `apps/order/main.go` | 8081 | create + read order rows |
| inventory | `apps/inventory/main.go` | 8082 | reserve / release |
| payment | `apps/payment/main.go` | 8083 | authorize / refund (simulated) |
| risk | `apps/risk/main.go` | 8084 | check approve / chaos 500s |
| saga | `apps/saga/main.go` | 8085 | run, recent list, per-tx timeline |
| genai | `ai/service/app.py` (FastAPI) | 9001 | live postmortem (real LLM), mock fallback, report fetch |
| frontend | `frontend/index.html` (nginx) | 5173 | live feed, order/chaos/report controls |

`./scripts/smoke.sh` → `SMOKE OK` (one COMPLETED + one FAILED-compensated order).
`./scripts/demo.sh` → happy order → risk-outage order → live postmortem. `RUN.md` has
the clone-to-demo instructions.

## B3. What each request actually does (traced through the code)

**Happy order:** `POST gateway:8080/api/orders` → gateway inserts a `PENDING` row via
`order:8081/orders`, then calls `saga:8085/saga/run` **synchronously over HTTP** and
returns the merged result. Saga mints `transaction_id + saga_id`, then calls
`inventory/reserve` → `payment/authorize` → `risk/check` in sequence, recording each
step in `saga_svc.saga_steps`, and flips `order_svc.orders` to `COMPLETED`. Each
success also publishes a Kafka envelope to `transaction.events` — **fire-and-forget;
nothing consumes it** (B5).

**Failed order (risk outage):** `POST /api/chaos/risk-outage{enabled:true}` flips an
**in-memory** flag in risk. Next order: reserve + authorize still SUCCEED, risk
returns 500 → saga calls `payment/refund` + `inventory/release`, records
`risk FAILED` + two `COMPENSATED` steps, marks saga + order `FAILED`, writes
`ai_svc.incidents (scenario 'risk-outage')` + one evidence row, returns
`{compensated:true, incident_id}`. Inventory/payment failures compensate earlier in
the chain the same way but write **no incident row** — only the risk path creates one.

**Postmortem:** `POST genai:9001/generate-postmortem{incident_id}` pulls incident +
evidence + saga steps from Postgres, tries OpenRouter chat (primary + fallbacks,
3 attempts each, exponential backoff), saves markdown + model + prompt/completion
tokens + cached flag to `ai_svc.generated_reports`, returns it. Verified live with
`liquid/lfm-2.5-2.6b:free` (`cached:false`). Embeddings/RAG retrieval calls OpenRouter
too — on a free-tier key it 402s, and `retrieve_chunks` degrades to `[]`, so **reports
today are grounded purely in DB evidence, with zero doc chunks**. `cost_usd` is never
written (always 0). If the key is missing, `GENAI_OFFLINE_MOCK=true`, or all models
fail → labeled mock report (`cached:true`), never a silent fake.

**Dashboard:** single static page (vanilla HTML/JS, **not** React). Top card polls
`GET /api/transactions/recent` every 3 s (last-20 sagas, green COMPLETED / red
FAILED); click a row for its full step timeline. Order buttons, chaos toggles, and
the report box (paste incident_id → markdown + model badge) all call the backends
directly. CORS is enabled on all 6 Go services + genai, so the page works from :5173
and `demo.sh` runs show up in the feed untouched.

## B4. Real API surface (only what exists — nothing aspirational)

- Gateway: `POST /api/orders` · `GET /api/transactions/recent` ·
  `GET /api/transactions/<tx>` · `POST /api/chaos/risk-outage{enabled}` ·
  `GET /api/system/health` (+ `/healthz`, `/metrics` stub on every Go service)
- Domain (internal): `POST /orders`, `GET /orders/<id>` · `POST /reserve|/release` ·
  `POST /authorize|/refund` · `POST /check`, `POST /chaos/fail`
- Saga (internal): `POST /saga/run{order_id,amount}` · `GET /sagas/recent` ·
  `GET /saga/<tx>` (status + ordered steps)
- GenAI: `POST /generate-postmortem{incident_id}` · `POST /rag-query{question,
  incident_id?}` · `GET /incidents/<id>/report` · `/healthz`
- Not built: everything under `/api/services`, `/predictions`, `/decisions`,
  `/incidents`, `/metrics/summary`, `/api/ai/*`, retry endpoints, region routes.

## B5. Honest gaps — what looks done but isn't, and what's untouched

1. **Outbox tables exist; no poller runs.** `order_svc.outbox` etc. are created by the
   migration but no service writes to or reads from them. The "no lost events" claim
   is **not yet true**.
2. **Kafka is publish-only.** Saga emits envelopes; there are zero consumers, so
   `processed_events` dedupe, consumer groups, offset discipline, and the DLQ are all
   **not yet real**. The "never processed twice" claim is **not yet true**.
3. **Idempotency keys table exists; nothing enforces it.** Duplicate POSTs create
   duplicate orders today.
4. **No retries, no backoff, no circuit breaker, no crash-resume** in the saga —
   single-attempt synchronous HTTP with 5 s timeouts. A container dying mid-saga
   leaves a stuck RUNNING row. Compensation itself is correct and verified; the
   *resilience around it* is future work.
5. **Chaos = 1 of 6 scenarios.** Only risk-outage exists, and its flag is in-memory
   (leaks between runs until reset; vanishes on restart — `demo.sh`/`smoke.sh` now
   reset it up front). Kill/DB-slowdown/consumer-lag/region-fail/traffic-spike: not built.
6. **RESOLVED by the pivot — see B0.** Real `/metrics`, ML, shield, controller and the
   decision ledger now exist. Still absent (and not claimed): router, regions, failover,
   Prometheus UI/Grafana. This numbered item is kept for historical context only.
7. **RAG is schema without substance.** `knowledge_chunks` table + HNSW index exist;
   `ai/service/seed.py` exists but has never been run successfully (embeddings 402 on
   free-tier keys), so the table is empty and every report's `<docs>` section is `[]`.
8. **Frontend is 1 page, not 7.** No system map, no ML/decision views, no chaos lab
  > page, no experiments charts, no reports list, no spend display. No load generator,
   no experiment harness, no CI, no ADRs beyond what lives in the plan docs.
9. **Small dead code** (harmless, noted for cleanup): `step()` stub in saga, unused
   `pool` var in inventory, unused variable in gateway order handler, `REGION`
   hardcoded `region-a` in the Kafka envelope.

## B6. What "presentable" means today (the demo you can actually give)

Open `localhost:5173`, keep it visible, and in a terminal run `./scripts/demo.sh`:
rows appear live in the feed (COMPLETED, then FAILED), click the red row to walk the
5-step timeline (reserve → authorize → risk FAILED → refund + release COMPENSATED),
paste the incident_id into the report box and read a live LLM postmortem whose numbers
all trace to DB rows. Second-laptop variant: same scripts with
`GATEWAY=http://<laptop-A-ip>:8080` as concurrent remote clients. That is the whole
honest story: **per-transaction correctness under injected failure, with a
machine-written incident report** — no prediction, no auto-remediation, no regions
yet, and the team should say exactly that.

## B7. Suggested next slices (in plan order, each independently demoable)

1. **Make the correctness claims real:** outbox poller + one real consumer + dedupe
   enforced + idempotency keys enforced + bounded retries + resume-stuck-sagas on
   startup. (Turns B5.1–B5.4 from tables into truth; needs no new infra.)
2. **Observability that feeds the future:** real per-service metrics + structured-log
   correlation + a `/metrics/summary` the dashboard can show. (Pre-req for ML.)
3. **Chaos 2–3 + load generator:** DB-slowdown flag, `docker kill` path, traffic
   spike — each writing incidents + evidence like risk-outage does.
4. **Router + Region B + live failover.** 5. **ML + controller closed loop.**
   6. **Experiments + remaining frontend pages + report.**
