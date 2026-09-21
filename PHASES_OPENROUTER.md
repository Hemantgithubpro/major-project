# Build Phases — Edition (14 weeks, 3 people)

> Companion to `ARCHITECTURE_OPENROUTER.md` and `project_report_final.md` §18–§19.
> This file is the step-by-step plan only — no code, no scaffolding.
> Language locked: **Go** control plane, **Python** AI sidecars. GenAI via **OpenRouter**.

## Work split (applies to every phase)

- **A — Transaction core:** saga-orchestrator, gateway, outbox/idempotency, Kafka topics,
  migrations, week-1 stub. Reviews domain services. Owns correctness claim.
- **B — Services, ML, experiments:** 2–3 domain services, ml-service, training pipeline,
  chaos scripts, load generator, baseline-vs-proposed suite. Owns research claim.
- **C — Router, frontend, AI:** router/LB, React dashboard, genai-service + RAG, docs/ADRs,
  demo script. Owns presentation + AI claim.
- **Shared:** reliability-controller (A+B), integration testing + rehearsals (all three).
- **Balance rule:** nobody owns >40% of services; recount weekly. Every feature touches ≥2 areas.
- **Cut order (if behind):** Grafana → experiments page → GenAI Job 2 → region failover.
  Never cut: saga compensation path, chaos lab.

## W0 — Lock foundations

- Goal: one language decision, repo skeleton doc'd, Postgres + Kafka compose starts.
- A: draft compose (Postgres + KRaft Kafka), migration conventions.
- B: load-generator sketch, chaos scenario list.
- C: repo layout doc, first ADRs (language=Go, compose-over-k8s, saga-over-2pc).
- Exit: `docker compose up` gives healthy PG + Kafka; ADRs merged; Go locked in writing.

## W1 — Contracts + stub + RAM + OpenRouter smoke test (most important week)

- Goal: frozen API / event-envelope / DB-schema docs; runnable stub; measured RAM table;
  proven OpenRouter key.
- A: freeze `API_SPEC` + `EVENT_CONTRACTS` + schema conventions; ship runnable stub
  gateway + mock event producer on real Kafka topics.
- B: build against stub; define ML feature list + telemetry CSV format.
- C: RAG doc list; OpenRouter smoke test — one chat call (primary + `:free` fallback)
  + one embeddings call from a script; record latency/cost; write `.env.example`
  (`OPENROUTER_*` keys, no values); RAM table on WORST laptop
  (total RAM → Docker idle → +PG/Kafka → +Go services → +Prom/ml/genai).
- Exit: stub publishing/consuming; RAM numbers in docs; Kafka-vs-Redpanda decided;
  OpenRouter key proven with a logged fallback switch.

## W2 — Order + Inventory + outbox

- Goal: first real events flowing.
- A: topics created (1–3 partitions, short retention), outbox poller pattern, migrations.
- B: order-service + inventory-service (state, outbox rows, idempotent consumers).
- C: transaction-list read path for the dashboard skeleton.
- Exit: place order → reserve → events visible in Kafka; outbox rows marked published.

## W3 — Payment + Risk + correctness guards

- Goal: all four domain services with safety properties.
- B: payment-service (authorize/refund simulated) + risk-service (failable on flag).
- A: idempotency keys end-to-end, `processed_events` dedupe, circuit breaker on risk call.
- C: chaos flag endpoints spec'd (`/api/chaos/*`, ENV=local only).
- Exit: duplicate POST with same Idempotency-Key returns original; replayed event ignored.

## W4 — Saga happy + compensation ★ MILESTONE

- Goal: the core correctness story works live.
- A: orchestrator (persist before/after each step, reverse-order compensation,
  resume stuck sagas on startup).
- B: failure injection on risk; compensation handlers (refund, release).
- C: saga timeline read API for Transaction Explorer.
- Exit: happy path COMPLETED; risk-fail path ends FAILED with refund+release rows,
  zero double-charges. **This is the project — protect it.**

## W5 — Observability

- Goal: every service scraped, every trace followable.
- All: Prometheus metrics (rate/latency/errors/in-flight/pool/lag/restarts),
  structured JSON logs with correlation IDs, `/healthz`+`/readyz`, timeouts/retries+jitter.
- C: dashboard skeleton (overview cards + system map); Grafana dev-only if RAM allows.
- Exit: one order traceable across services by correlation ID; metrics endpoint per service.

## W6 — Router + regions + live failover

- Goal: real replicas, real rerouting.
- C: router health map + checks; Region B compose project; failover endpoint.
- A: gateway ↔ router wiring; `MAX_REPLICAS` + health semantics.
- B: `docker compose --scale` matrix test (which services scale cleanly).
- Exit: `docker kill` a Region A instance on screen → traffic moves to B, visible recovery.

## W7 — Chaos lab + load generator

- Goal: all six scenarios scripted + telemetry export.
- B: kill, DB-latency flag, consumer-lag flag, risk-outage, region-fail, traffic-spike
  scripts + load patterns (steady/ramp/spike/sustained).
- A: `incidents` + `incident_evidence` recording per run.
- C: Chaos Lab page (buttons + injected/detected/predicted/acted/recovered timestamps).
- Exit: each scenario runs from one button/command and leaves an incidents row + evidence.

## W8 — Dataset + train + honest evaluation

- Goal: two trained XGBoost models that beat trivial.
- B: export telemetry → CSVs tagged (scenario, failure time); label 10-min pre-failure=1;
  **time-based split**; train forecast regressor + risk classifier; evaluate
  (precision/recall/F1/ROC-AUC/false-alarms/lead-time) vs `CPU > 85%` baseline;
  commit artifacts to `ml/models/`, notes to `ML_EXPERIMENTS.md`.
- A+C: review labeling; dashboard risk-view mock data contract.
- Exit: models + metrics doc committed; negative result documented if baseline wins.

## W9 — Closed loop ★ MILESTONE

- Goal: predict → decide → act → verify running.
- B: ml-service serving `/forecast` + `/risk` with shared features.
- A+B: reliability-controller (hard rules, scoring, Action Executor, decision log,
  `decision_uid` idempotency, recovery verification).
- C: reliability/ML + decision-timeline pages wired to real endpoints.
- Exit: DB-slowdown run shows risk climbing on `db_conn_pct`, controller choosing
  STOP_SCALING with reason, metrics recovering. Baseline would have scaled — note it.

## W10 — GenAI on OpenRouter (live, no sequential mode)

- Goal: real postmortems + RAG from system evidence, same session as chaos.
- C: genai-service (chat + embeddings via OpenRouter, fallback chain, retries/timeouts),
  chunking → `knowledge_chunks vector(1536)` → HNSW retrieval, pinned prompts in
  `ai/prompts/`, `POST /api/ai/generate-report` + `/rag-query`, cost counters,
  `GENAI_OFFLINE_MOCK` cached fallback.
- B: evidence-bundle query (predictions+decisions+evidence+saga steps per incident).
- A: review guardrail (LLM text-only, same tables as controller).
- Exit: after a W7 scenario, one API call returns a labeled markdown report saved to
  `generated_reports` with model/prompt/tokens/cost; RAG answer cites chunks.

## W11 — Frontend core

- Goal: correctness + reliability visible on one screen.
- C: overview, transaction explorer (saga timeline incl. compensations), reliability/ML,
  decision timeline — TanStack Query, typed responses, loading/error/empty states.
- A+B: stabilize read APIs (`/api/transactions`, `/predictions`, `/decisions`).
- Exit: full incident story readable without touching logs or DB.

## W12 — Frontend lab + reports

- Goal: demo operable from the UI.
- C: chaos lab, experiments page (baseline-vs-proposed charts), reports viewer
  (+ model/cost line, RAG question box).
- All: `make demo` one-command startup; `DEMO_SCRIPT.md` drafted.
- Exit: entire demo clickable from dashboard; script timed ~13 min.

## W13 — Experiments

- Goal: the research claim with numbers.
- B: automate N≥5 runs per scenario per system (baseline: CPU-threshold scaler only;
  proposed: full loop), same load traces; collect SLO violations, P95/P99, error rate,
  MTTR, correctness zeros, ML quality, controller actions, local cost proxy.
- A+C: review protocol; charts for report + experiments page.
- Exit: headline DB-slowdown chart (baseline scales into worse state, proposed refuses
  + reroutes) + full metrics table with means and spread.

## W14 — Report + rehearsals (buffer)

- Goal: written report + bulletproof demo.
- C: report assembly, limitations slide, pre-generated fallback report.
- All: two full rehearsals on the DEMO machine; RAM re-check; close other apps;
  fallback drill (kill WiFi → cached report + honest label).
- Exit: report done; demo run twice end-to-end without surprises.

## Phase exit checklist (every phase)

Each phase is done only when: docs updated in-PR, contracts changed in same PR if touched,
unit tests noted for pure logic (saga machine, rules/scoring, features), integration
notes for outbox/dedupe/idempotency, and the next phase's owners unblocked.
