# AGENTS.md

Operating guide for coding agents working in this repository.

## 1. What this project is

A laptop-runnable distributed order platform used as a **testbed for safety-constrained
autonomy**:

> A prediction-driven controller proposes remediations. A deterministic **safety shield**
> vetoes any action that would violate transactional or operational invariants.

The contribution is the invariant model + shield + decision ledger, demonstrated by the shield
refusing to scale into a database bottleneck. Everything else (saga, Kafka, GenAI) exists to
support that story.

**Read before changing anything, in this order:**

1. `docs/TOMORROW.md` — frozen interfaces, phases, demo script, cut list. This is the execution
   authority for the current build.
2. `PLAN.md` — long-term architecture, invariant catalog (T1–T5, O1–O7), data model, API surface.
3. `docs/adr/0012`–`0014` — thesis, removal of regions/router, shield-as-a-service.
4. `PROJECT_STATUS.md` — known gaps. Treat it as possibly stale; verify against code.

If code and docs disagree, say so and fix the docs (or the code). Do not silently drift.

## 2. Current state (verify, don't assume)

| Component | Path | State |
|---|---|---|
| gateway | `apps/gateway` | exists — orders, transactions, risk chaos, health |
| order / inventory / payment / risk / saga | `apps/*` | exist, working |
| GenAI postmortem + RAG | `ai/service` | exists, live OpenRouter |
| frontend | `frontend/index.html` | static single page, nginx |
| migrations | `migrations/001_init.sql` | 6 schemas; outbox/processed_events exist but unused |
| metrics | `shared/httpx/httpx.go` | `/metrics` is a **stub** returning `up 1` |
| safety-shield | `apps/safety-shield` | **empty dir — not built** |
| reliability-controller | `apps/reliability-controller` | **empty dir — not built** |
| ml-service | `ai/ml/service` | **empty dir — not built** |
| metrics package | `shared/metrics` | **empty dir — not built** |

Known gaps: no real metrics, no ML, no controller, no shield, no ledger, no consumers/dedupe,
no idempotency enforcement, no retries/crash-resume, one chaos scenario only.

## 3. Service map and ports

| Service | Port | Notes |
|---|---|---|
| gateway | 8080 | public entry; CORS; correlation ID |
| order | 8081 | `POST /orders`, `GET /orders/{id}` |
| inventory | 8082 | `POST /reserve`, `POST /release` |
| payment | 8083 | `POST /authorize`, `POST /refund` |
| risk | 8084 | `POST /check`, `POST /chaos/fail` |
| saga | 8085 | `POST /saga/run`, `GET /sagas/recent`, `GET /saga/{tx}` |
| reliability-controller | 8086 | **planned** |
| safety-shield | 8087 | **planned** |
| ml-service | 9000 | **planned** |
| genai | 9001 | FastAPI, `POST /generate-postmortem`, RAG query |
| frontend | 5173 → 80 | static page |
| postgres+pgvector | 5432 | system of record |
| kafka (KRaft) | 9092 | `transaction.events`; no consumers yet |

Schemas: `order_svc`, `inventory_svc`, `payment_svc`, `risk_svc`, `saga_svc`, `ai_svc`.
Planned: `safety_svc`, `reliability_svc`.

## 4. Commands

```bash
make up            # docker compose up -d --build, then ps
make down
make ps
make logs
make smoke         # scripts/smoke.sh
make demo          # scripts/demo.sh (happy order, risk outage, postmortem)
```

```bash
go build ./...
go test ./...
curl -s localhost:8080/healthz
curl -s localhost:8080/api/transactions/recent | jq
```

Migrations are mounted at `/docker-entrypoint-initdb.d`, so they **only run on a fresh volume**.
To apply a new migration without wiping data:

```bash
docker compose exec -T postgres psql -U postgres -d mvp < migrations/002_safety_reliability.sql
```

Wiping is `docker compose down -v` — destructive, ask before doing it.

## 5. Non-negotiable design rules

These are the thesis. Violating them breaks the project, not just the build.

1. **Shield is pure.** `apps/safety-shield` must have no DB, no clock, no randomness, no network.
   The caller supplies `now` and `decision_uid`. Same input → same verdict, always.
2. **Shield is the only veto.** The controller may propose; it must never execute an action the
   shield did not allow. No "allow because the shield was slow."
3. **Fail closed.** If the shield is unreachable or returns malformed output, the controller
   chooses `NO_ACTION` and records why. Never bypass.
4. **Prediction is a signal, never authority.** ML output feeds scoring and invariant checks.
   It cannot directly trigger an action.
5. **LLM is advisory.** GenAI explains and labels claims (`FACT / PREDICTION / INFERENCE /
   RECOMMENDATION`). It has no infrastructure permissions and never sits in the control path.
6. **One controller replica.** The controller is a singleton by design; document this. Do not
   add leader election.
7. **Baseline mode must not call the shield.** It exists to demonstrate unconstrained harm.
8. **Ledger before action.** No execution without a `decision_uid` persisted in
   `reliability_svc.decisions`.
9. **Exact action strings:** `NO_ACTION`, `SCALE_UP`, `SCALE_DOWN`, `SHED_LOAD`,
   `ENABLE_CIRCUIT_BREAKER`. Do not invent variants.
10. **No regions, no router, no failover claims.** Removed by ADR 0013.

## 6. Frozen interfaces

`docs/TOMORROW.md` §1 is authoritative. Do not change these without updating that document
first and telling the user.

- Metric names: `http_requests_total`, `http_request_duration_seconds_bucket`,
  `db_query_duration_seconds_bucket`, `db_pool_acquired|idle|waiting|max`,
  `db_latency_injected_ms`, `saga_steps_total`, `orders_total`, `risk_checks_total`.
- `ml-service`: `POST /risk`, `POST /forecast` (payloads in §1.2).
- `safety-shield`: `POST /evaluate`, `GET /invariants`, `GET /healthz` (§1.3).
- Ledger tables: `reliability_svc.decisions`, `.actions`, `.verifications`.
- Invariant priority: freshness → idempotency → replica bounds → DB ceiling → cooldown →
  budget → transaction non-regression.

## 7. Conventions

**Go**
- Module is `mvp`; Go 1.23; stdlib `net/http` with method-prefixed patterns (`"POST /x"`).
- Use `shared/httpx`: `Env`, `WriteJSON`, `CorrID`, `Log`, `Healthz`, `CORS`.
- Use `shared/db.Connect`; it sets `MaxConns = 5`.
- Propagate `X-Correlation-ID` on every outbound request.
- Dockerfile pattern (multi-stage, build from repo root):

```dockerfile
FROM golang:1.23-alpine AS b
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY shared ./shared
COPY apps/<svc> ./apps/<svc>
RUN go build -o /app ./apps/<svc>
FROM alpine:3.20
COPY --from=b /app /app
CMD ["/app"]
```

- Register every new service in `docker-compose.yml` with the same `context: .` pattern and
  `depends_on` the services it actually needs.

**Python**
- FastAPI + `psycopg` + `httpx`; configuration from environment with safe defaults.
- Never hardcode secrets. `OPENROUTER_API_KEY` lives in `.env` (gitignored).
- ML artifacts go under `ai/ml/models/`; do not commit large binaries without asking.

**SQL**
- One schema per service; numbered migrations (`002_...sql`).
- Application code does not read another service's schema, except the documented read-only
  invariant audit.

**Frontend**
- Today: static `frontend/index.html`. Planned React/TS is not built — do not describe it as
  existing.

## 8. Testing and verification

- Shield: table-driven tests for every invariant, plus a determinism test (same input twice).
- Go: `go test ./...` must pass before you claim a phase is done.
- Every phase has an exit criterion in `docs/TOMORROW.md` §2. Verify it with a real command and
  paste the output; do not mark it done from reading code.
- Integration claims require the stack running: `make up`, then the actual curl/loadgen flow.
- If something is flaky, say it is flaky. Do not tune the test to hide it.

## 9. Do not do / do not claim

- Do not add mock LLM paths to the demo. `GENAI_OFFLINE_MOCK` exists for offline fallback only
  and must be labeled if used.
- Do not fabricate metrics, verdicts, experiment results, or screenshots.
- Do not claim: full N≥5 experiment suite, outbox/consumers/idempotency, regions/failover, RAG
  corpus, production readiness, real cloud scaling. See `docs/TOMORROW.md` §9.
- Do not expose chaos, admin, or AI endpoints publicly. `ENV=local` only.
- Do not commit `.env`, keys, or generated data dumps.
- Do not run destructive chaos (`docker kill`, volume wipes) without explicit confirmation.
- Do not rewrite or delete files that look like in-progress work without checking `git status`
  and asking.

## 10. Definition of done (current milestone)

A live closed loop on one Mac:

```
loadgen → services → real /metrics → controller → real XGBoost /risk
        → policy proposes SCALE_UP → shield blocks (INV-O2)
        → SHED_LOAD executes → metrics recover → ledger + verification
        → live GenAI postmortem
```

Two clean rehearsals, under six minutes each, with no manual DB edits. Full checklist:
`docs/TOMORROW.md` §0.
