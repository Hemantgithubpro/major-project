# Architecture — OpenRouter Edition

> Companion to `project_report_final.md` (master reference) and `final_project_plan.md`.
> Originals are untouched. This file supersedes ONLY Section 5.7 / Step 10 / Decision 5
> (local Ollama + sequential demo mode). Everything else is inherited, not redefined.
> Control plane language: **Go**. AI sidecars: **Python/FastAPI**. Tokens: **OpenRouter (cheap paid primary + :free fallback)**.

## 0. What changed and why (one paragraph)

The transaction plane, reliability plane, Kafka backbone, Postgres, ML design, chaos set,
frontend pages, and all contracts are unchanged. The single change: `genai-service` calls
**OpenRouter cloud API** instead of a local Ollama container. This deletes ~1.5–2.5 GB of
resident LLM RAM, deletes `deploy/docker-compose.ai.yml`'s Ollama service and the entire
"sequential demo mode" procedure, and makes the demo fully live (chaos → recovery → report
in one session). Cost is bounded by cheap workhorse models + per-incident caps + `:free`
fallbacks. See `docs/adr/0011-openrouter-over-ollama.md`.

## 1. Constraints (updated)

- C1': **Near-zero API spend, not zero.** OpenRouter key with a small credit balance.
  Budget: generation on `gpt-4o-mini` / `gemini-flash` class (~$0.15–0.60 / 1M tokens);
  embeddings on `text-embedding-3-small`. Monthly cap + per-report token counter enforced.
- C2: Genuinely end-to-end, demoed live. No stubs on demo day (except documented offline fallback).
- C3: 8 GB RAM, no GPU. Usable container budget ~4–5 GB on Windows/WSL2.
  No local LLM means the steady-state stack fits comfortably (see §11).
- C4: Exactly 3 people, non-blocking (week-1 runnable stub rule still applies).

## 2. System map — two planes, one dashboard

```
TRANSACTION PLANE                    RELIABILITY PLANE
(does business work)                 (watches, predicts, reacts)
---------------------                --------------------------
gateway (:8080)                      prometheus (:9090)
order (:8081) / inventory (:8082)    ml-service (:9000)  XGBoost forecast + risk
payment (:8083) / risk (:8084)       reliability-controller (:8086)
saga-orchestrator (:8085)              rules + scoring → Action Executor
postgres (:5432, pgvector/pg16)      router/lb (:8000)  health map, reroute/failover
kafka (:9092, KRaft single broker)   genai-service (:9001) → OpenRouter cloud (NEW)
outbox + idempotency + dedupe        react dashboard (:5173)
```

Full lifecycle:

```
Dashboard → gateway → router (health map) → saga-orchestrator
  → inventory / payment / risk (each: business row + outbox row, one tx)
  → outbox poller → KAFKA → consumers (dedupe via processed_events)
  → prometheus → ml-service (/forecast, /risk)
  → reliability-controller (hard rules → score → Action Executor)
  → router scale/reroute  +  saga compensations (independent path)
  → verify recovery → decisions/incident_evidence tables
  → genai-service → OpenRouter chat + embeddings → pgvector RAG
  → incident report markdown → dashboard
```

Regions: Region A / Region B = two Compose projects on one machine behind the router.
Logical failover simulation, stated openly. Unchanged.

## 3. Service list (ports frozen)

| # | Service | Lang | Port | Role |
|---|---------|------|------|------|
| 1 | gateway | Go | 8080 | entry, correlation IDs, region routing |
| 2 | order-service | Go | 8081 | order state machine |
| 3 | inventory-service | Go | 8082 | reserve / release |
| 4 | payment-service | Go | 8083 | authorize / refund (simulated) |
| 5 | risk-service | Go | 8084 | fraud check, intentionally failable |
| 6 | saga-orchestrator | Go | 8085 | workflow state + compensation |
| 7 | router/lb | Go | 8000 | health-aware reverse proxy |
| 8 | reliability-controller | Go | 8086 | rules + scoring + Action Executor trigger |
| 9 | ml-service | Python | 9000 | `POST /forecast`, `POST /risk` (XGBoost ×2) |
| 10 | genai-service | Python | 9001 | `POST /generate-postmortem`, `POST /rag-query` → OpenRouter |
| 11 | frontend | React+TS | 5173 | 7 pages (unchanged) |

Infra: postgres 5432 · kafka 9092 · prometheus 9090 · grafana 3000 (dev-only, never demo-critical).

Go libs: `chi`, `pgx`, `segmentio/kafka-go` or `franz-go`, `client_golang`, `golang-migrate`.
Frontend: TanStack Query, Recharts, React Flow (or SVG), Tailwind.

## 4. End-to-end flow (unchanged except Step 10)

Steps 1–9 identical to master doc §3 (request → saga → outbox → observe → ML predict →
guarded decide → execute → saga recover → verify). Step 10 changes:

- **OLD Step 10:** stop containers → start Ollama → generate from persisted data.
- **NEW Step 10:** on incident close, `genai-service` assembles the evidence bundle from
  Postgres (`predictions`, `decisions`, `incident_evidence`, `saga_steps`), retrieves top-k
  doc chunks from pgvector, calls OpenRouter chat completions, saves markdown to
  `generated_reports`, returns it. **Same session, no container restarts.**
- Step 11 (dashboard) unchanged.

## 5. Kafka contract (inherited, summarized)

Topics (1–3 partitions, ~2 h retention): `order.events`, `inventory.events`,
`payment.events`, `risk.events`, `transaction.events`, `reliability.events`,
`dead-letter.events`. Key = `aggregate_id` (order ID) → ordering per order.
One consumer group per consuming service; commit offsets after processing;
envelope `{event_id, event_type, occurred_at, transaction_id, aggregate_id,
correlation_id, source, region, version, payload}`. Full event-type list and rules:
master doc §8 / `docs/EVENT_CONTRACTS.md`.

Broker (lean): `apache/kafka:latest`, `KAFKA_PROCESS_ROLES=broker,controller`,
`KAFKA_HEAP_OPTS="-Xmx512m -Xms512m"`, no ZooKeeper/Connect/Registry/Debezium.
Contingency: Redpanda image swap, zero code change (same protocol, clients, topics).

## 6. Database contract (inherited + 2 deltas)

One Postgres instance, one schema per service, no cross-schema reads.
`outbox`, `processed_events(event_id, consumer_name)`, `saga_steps`,
`predictions`, `decisions(decision_uid unique, action, reason, score, rule_blocked)`,
`incidents`, `incident_evidence` — all per master doc §9.

**Delta 1 — embeddings dimension (OpenRouter, not nomic):**
```sql
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE knowledge_chunks(
  id bigserial PRIMARY KEY, source text, chunk_text text,
  embedding vector(1536), created_at timestamptz DEFAULT now());
CREATE INDEX ON knowledge_chunks USING hnsw (embedding vector_cosine_ops);
```
1536 = `openai/text-embedding-3-small` via OpenRouter embeddings API.

**Delta 2 — report audit columns:**
```sql
CREATE TABLE generated_reports(
  id uuid PRIMARY KEY, incident_id uuid, markdown text,
  model text, prompt_version text, evidence_bundle_id text,
  prompt_tokens int, completion_tokens int, cost_usd numeric,
  generated_at timestamptz DEFAULT now());
```

## 7. Saga (inherited)

States `PENDING → RUNNING → SUCCEEDED / FAILED → COMPENSATING → COMPENSATED`.
Happy path Reserve → Authorize → Risk → COMPLETED. Compensation in reverse
(refund → release) → clean FAILED. Idempotent compensations, bounded retries with
backoff+jitter, resume stuck RUNNING/COMPENSATING on startup. Full detail: master doc §10.

## 8. ML layer (inherited)

Model A XGBoost regressor (load t+5/t+15); Model B XGBoost classifier
(failure_within_10m + top features). One shared feature pipeline, one FastAPI process.
Training data from chaos runs + load generator; label 10-min pre-failure windows;
**time-based split only**; beat trivial `CPU > 85%` baseline; report precision/recall/
F1/ROC-AUC/false-alarms/lead-time. Full detail: master doc §11.

## 9. Reliability controller (inherited)

Hard rules first (never exceed MAX_REPLICAS; never scale out when `db_conn_pct` over
threshold; never fail over to unhealthy target; never re-execute a `decision_uid`),
then `score = w1*latency + w2*risk + w3*cost + w4*instability`, lowest passing action wins.
Action space: `NO_ACTION | SCALE_UP | SCALE_DOWN | STOP_SCALING | ROUTE_AWAY | FAILOVER_REGION`.
Single Action Executor (`docker compose --scale` + router health flips), decision logged
before execution. Showcase: DB-slowdown scenario where baseline scales and worsens,
controller returns STOP_SCALING + reroute.

## 10. GenAI layer — OpenRouter (THE NEW SECTION)

### 10.1 Jobs (same two, new runtime)
- **Job 1 (core):** incident postmortem markdown (Summary, Timeline, Root Cause, Evidence,
  Actions Taken, Recommendation) from the structured evidence bundle.
- **Job 2 (core):** RAG root-cause Q&A over team docs + past reports + live evidence,
  every claim labeled `FACT / PREDICTION / INFERENCE / RECOMMENDATION`.
- Job 3 (propose next chaos) stays cut.

### 10.2 Models
| Use | Primary (cheap paid) | Fallback chain (:free) |
|-----|----------------------|------------------------|
| Generation | `openai/gpt-4o-mini` or `google/gemini-flash-1.5` | `meta-llama/llama-3.1-8b-instruct:free` → `mistralai/mistral-7b-instruct:free` |
| Embeddings | `openai/text-embedding-3-small` (1536-d) | same (embeddings are pennies; no fallback needed) |

Exact IDs pinned in `genai-service` config; model + prompt version recorded per report.

### 10.3 API shape
- `POST https://openrouter.ai/api/v1/chat/completions`
  headers: `Authorization: Bearer $OPENROUTER_API_KEY`, `HTTP-Referer`, `X-Title`.
  Body: `{model, messages: [system(pinned template), user(evidence JSON + chunks)], temperature: 0.2, max_tokens: 2000}`.
- `POST https://openrouter.ai/api/v1/embeddings` body: `{model, input: [chunks]}`.
- Retry: 3 attempts, exponential backoff, then next model in fallback chain.
  Timeout 60 s generation / 30 s embeddings. Failures never block transactions —
  report endpoint returns `503 {retryable: true}` and the incident stays open.

### 10.4 RAG pipeline
1. Chunk `ARCHITECTURE.md`, `RUNBOOK.md`, `EVENT_CONTRACTS.md`, past reports (500-token,
   50 overlap) → embed via OpenRouter → `knowledge_chunks`.
2. At query/report time: embed question (Job 2) or incident summary (Job 1) →
   `ORDER BY embedding <=> $q LIMIT 8` → inject chunks + evidence bundle into prompt.
3. Real embedding + vector-store step preserved (pgvector HNSW); never prompt-stuffing.

### 10.5 Prompt skeleton (`ai/prompts/`, versioned)
System: "You are an SRE assistant. Use ONLY the evidence provided. Never invent a metric,
timestamp, or service name. Label every claim FACT/PREDICTION/INFERENCE/RECOMMENDATION.
If evidence is insufficient, say so."
User: `<evidence bundle JSON> <retrieved chunks> Write the incident report.`

### 10.6 Guardrails (unchanged, restated)
LLM emits text/JSON only — zero infra permissions. Only the deterministic controller acts.
Controller and LLM read the SAME evidence tables. Every report stores model, prompt
version, evidence bundle ID, token counts, cost.

### 10.7 Cost control
Env: `OPENROUTER_API_KEY` (gitignored `.env` only), `OPENROUTER_GEN_MODEL`,
`OPENROUTER_FALLBACK_MODELS`, `OPENROUTER_EMBED_MODEL`, monthly cap
(e.g. $10), per-report cap (e.g. $0.05 — skip retrieval expansion beyond it).
Token/cost counters in `generated_reports`; dashboard shows spend on Reports page.

### 10.8 Offline contingency (venue WiFi dies)
`GENAI_OFFLINE_MOCK=true` serves the newest cached report for the incident + labels the
UI badge "cached"; report explicitly states it is a fallback. Never claim live generation
when serving cache.

## 11. RAM budget (rewritten — no local LLM)

Measure on the worst laptop in week 1 (`docker stats` at each step):

| Component | Expectation |
|-----------|-------------|
| Docker/WSL2 idle | 1.5–3 GB (cap WSL2 via `.wslconfig` memory=5GB) |
| PostgreSQL + pgvector | 150–250 MB |
| Kafka KRaft 512 m heap | 700 MB–1 GB (biggest non-AI item) |
| 8 Go services @ ~20 MB | ~160 MB |
| Prometheus | 150–200 MB |
| Grafana | ~150 MB — dev-only, first to stop |
| ml-service (Python+XGBoost) | 200–250 MB |
| genai-service (Python, no local model) | 100–200 MB |
| **Steady-state containers** | **~1.5–2 GB — fits, no sequential mode** |
| OpenRouter (cloud) | 0 MB local |

If still tight: Redpanda swap, drop Grafana, done. No model-size juggling anymore.

## 12. API surface (inherited)

Transaction `POST /api/orders` (+Idempotency-Key), `GET /api/orders/:id`,
`GET /api/transactions[/:id]`, `POST /api/transactions/:id/retry`;
reliability `GET /api/system/health|/services|/predictions|/decisions|/incidents[/:id]|/metrics/summary`;
chaos `POST /api/chaos/...` (ENV=local only); AI `POST /api/ai/generate-report`,
`GET /api/ai/incidents/:id/report`, `POST /api/ai/query`;
internal `ml-service:9000 /forecast /risk`, `genai-service:9001 /generate-postmortem /rag-query`.
Full shapes: master doc §7.

## 13. Chaos, frontend, experiments, demo (inherited, one edit)

- Chaos A–F unchanged (kill, DB slowdown showcase, consumer lag, risk outage,
  region fail, traffic spike). Master doc §13.
- Frontend 7 pages unchanged. Master doc §14. Reports page adds model + cost line.
- Experiments baseline-vs-proposed (N≥5, means+spread) unchanged; headline chart =
  DB slowdown. Master doc §20.
- Demo ~13 min unchanged EXCEPT 10:00 slot becomes: "generate the postmortem LIVE
  via OpenRouter for the incident just caused; read the FACT/PREDICTION/INFERENCE/
  RECOMMENDATION lines." No container stops. Rehearse twice on the demo machine;
  `make demo` one command; close other apps.

## 14. Engineering practices (inherited checklist)

Timeouts everywhere; bounded retries + jitter; idempotency keys; consumer dedupe;
correlation IDs end-to-end; persisted state machines; graceful shutdown; circuit breaker
on risk call; Kafka keying/groups/DLQ/envelope-versioning; versioned migrations;
one-schema-per-service; explicit pool limits as metrics; env config; `.env.example`
+ gitignored `.env` (**now also holding `OPENROUTER_API_KEY`**); `/healthz`+`/readyz`;
structured JSON logs; Makefile; chaos/AI endpoints ENV=local only; trunk-based PRs + CI;
ADR per decision; contract changes documented in-PR.

## 15. Repo layout (inherited, two deletions)

Per master doc §15, with: DELETE `deploy/docker-compose.ai.yml`'s Ollama service
(keep the file only if used for genai-service alone, renamed `docker-compose.genai.yml`
or merged into core compose — genai-service is light enough to run always);
DELETE `nomic-embed-text` bootstrap. ADD `ai/prompts/` pinned templates +
`docs/adr/0011-openrouter-over-ollama.md`. Secrets: committed `.env.example`
(documents `OPENROUTER_*` keys without values), real `.env` gitignored.

## 16. ADRs

Reuse 0001–0010 from master doc §15 (0009 sequential-demo-mode marked SUPERSEDED by 0011).
New: 0011 OpenRouter over Ollama. Contingency 0004 (Redpanda) stays.

## 17. Limitations to state openly

Master doc §22 items 1–3, 5–9 unchanged. Item 4 ("GenAI runs sequentially") is REPLACED by:
"GenAI depends on OpenRouter network + key; bounded by API cost caps; offline fallback is
cached reports only. Embeddings/training data are synthetic-fault-derived."
