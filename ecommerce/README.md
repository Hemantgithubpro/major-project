# E-commerce storefront branch (extra, not part of the viva demo)

This branch (`feature/ecommerce-simulation`) adds a storefront-style interface over the
**existing** safety-constrained autonomy testbed. It is deliberately separate from the
operator console used for the viva (`frontend/index.html`, port 5173). Do not show this in
the viva unless asked.

## Run

```bash
cd /Users/hemant/College/semester-7/major-project/try2
docker compose up -d --build genai simulation storefront
open http://localhost:5174
```

- Storefront: **http://localhost:5174**
- Simulation API: **http://localhost:8088** (`GET /scenarios`, `POST /run {"scenario":"..."}`)
- GenAI advisory endpoint: `POST http://localhost:9001/explain-simulation`

## What is real vs static

- The **catalogue is static demo data** (no product database).
- `Checkout` creates a **real order** through `gateway POST /api/orders`.
- Every simulation case drives the real stack: real metrics, real XGBoost `/risk`, real
  safety shield vetoes, real ledger rows, real 503s from load shedding.
- The AI explanation and postmortem are **live OpenRouter calls** with no mock path. If the
  LLM is unavailable, the endpoint returns an explicit error — it never fabricates text.

## Cases

| Scenario | What it does |
|---|---|
| `happy` | Places 10 real orders under normal load; risk stays low, no remediation needed. |
| `risk_outage` | Injects the existing risk outage; one order is compensated; generates a live postmortem. |
| `db_bottleneck` | Injects real DB latency (`pg_sleep`), drives 20 rps, waits for `SCALE_UP` to be vetoed on `INV-O2`, then `SHED_LOAD` produces real 503s. |
| `traffic_spike` | Drives 35 rps without a fault; shows the controller holding `NO_ACTION`. |

Each run resets fault flags first and restores them afterwards, so it does not leave the
testbed in a broken state.

## Honest limits

No product service, inventory catalogue, payment integration beyond the existing testbed,
auth, recommendation engine, or persistent cart. This is an interface over the reliability
testbed, not a production e-commerce system.
