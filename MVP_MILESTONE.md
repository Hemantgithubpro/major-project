# MVP Milestone — 30% Presentable Slice (LOCKED)

> Locked scope for discussion before building further. Full vision stays in
> `ARCHITECTURE_OPENROUTER.md` / `PHASES_OPENROUTER.md` / `project_report_final.md`.

## Included (P0 + P1 + P2-lite + P3-lite)

- P0: Postgres (pgvector) + Kafka KRaft single-broker + Env/Compose/Makefile + OpenRouter key smoke test.
- P1: gateway + order + inventory + payment + risk + saga-orchestrator with happy path + compensation, outbox + idempotency + dedupe.
- P2-lite: `/healthz`, `/metrics` stub, structured logs, correlation IDs, two chaos paths (risk-outage flag, `docker kill`), load script.
- P3-lite: genai-service (Python) → OpenRouter chat + embeddings → pgvector RAG-lite → live postmortem saved to `generated_reports`.
- Minimal dashboard (single page): place order, transaction timeline, chaos buttons, generate-report + report view.

## Demo story (5 min)

1. Place order → COMPLETED with saga timeline.
2. Enable risk-outage → place order → reserve + authorize succeed → risk FAILS → refund + release → FAILED clean.
3. `POST /api/ai/generate-report` → live OpenRouter markdown with FACT/PREDICTION/INFERENCE/RECOMMENDATION from DB rows.

## Deferred (P4, NOT in this milestone)

Router/regions failover, XGBoost ml-service + controller, Prometheus-full/Grafana, 6-scenario lab, baseline-vs-proposed experiments, auth, full 7-page frontend.

## Run

```bash
cp .env.example .env   # add OPENROUTER_API_KEY
make up                # builds + starts everything
make demo              # order → risk-outage order → report
make down
```

## Exit criteria

- `docker compose ps` all healthy; `make demo` ends with one COMPLETED + one FAILED (compensated) transaction + one markdown report citing real DB rows.
- No code changes needed to discuss next slice.
