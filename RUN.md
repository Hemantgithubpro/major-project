# Run It — clone to demo in 5 minutes

Needs: Docker + Docker Compose, one OpenRouter key (free-tier works).

## 1. Clone and configure

```bash
git clone <repo-url>
cd <repo-dir>
cp .env.example .env
# edit .env, set OPENROUTER_API_KEY=sk-or-v1-...
```

Without a key everything still runs, but incident reports come back as
`offline-mock` instead of a live LLM postmortem.

## 2. Start everything

```bash
docker compose up -d --build
docker compose ps
```

Wait until all 10 containers are up (~30s first boot — Kafka takes longest).
Dashboard: http://localhost:5173

| Service | Port |
|---|---|
| dashboard | 5173 |
| gateway | 8080 |
| order / inventory / payment / risk / saga | 8081–8085 |
| genai | 9001 |
| postgres / kafka | 5432 / 9092 |

## 3. Verify (happy + chaos + report)

```bash
./scripts/smoke.sh
```

Expected: one `COMPLETED` order, one `FAILED` (compensated) order, `SMOKE OK`.

Or the full story with a live postmortem:

```bash
./scripts/demo.sh
```

This places an order, fails risk on the next one (refund + release, clean FAILED),
then generates the incident report from real DB rows.

## 4. Manual API tour

```bash
# happy order
curl -X POST localhost:8080/api/orders \
  -H 'Content-Type: application/json' \
  -d '{"customer_ref":"demo","amount":99.5}'

# enable risk outage, order again (expect FAILED + compensated + incident_id)
curl -X POST localhost:8080/api/chaos/risk-outage \
  -H 'Content-Type: application/json' -d '{"enabled":true}'

curl -X POST localhost:8080/api/orders \
  -H 'Content-Type: application/json' \
  -d '{"customer_ref":"demo","amount":99.5}'

# disable outage
curl -X POST localhost:8080/api/chaos/risk-outage \
  -H 'Content-Type: application/json' -d '{"enabled":false}'

# live postmortem (replace <incident_id>)
curl -X POST localhost:9001/generate-postmortem \
  -H 'Content-Type: application/json' \
  -d '{"incident_id":"<incident_id>"}'
```

## 5. Stop / reset

```bash
docker compose down            # stop, keep data
docker compose down -v         # stop + wipe DB (migrations re-run next boot)
docker compose logs -f --tail=100   # follow logs
```

## Troubleshooting

- **First boot slow**: Kafka needs ~20s. Check `docker compose logs kafka --tail 5`
  for `Kafka Server started` before judging failures.
- **Port taken**: edit the left side of the port mapping in `docker-compose.yml`
  (e.g. `"8081:8081"` → `"18081:8081"`).
- **`offline-mock` report**: `OPENROUTER_API_KEY` missing in `.env`, or no credits.
  Free-tier keys work only with `:free` models (defaults in `.env.example`).
  After editing `.env`: `docker compose up -d genai`.
- **Chaos flag stuck on**: if every order FAILs, run the disable call above
  (`{"enabled":false}`).
- **Fresh DB**: `docker compose down -v && docker compose up -d --build`.
