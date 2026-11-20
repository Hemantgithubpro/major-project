# Viva Q bank — safety-constrained autonomy

Short, conceptual answers. Do not walk through code during the viva; use the demo and this
script. Ground every claim in something the demo actually shows.

## 1. What is the problem you are solving?

Autonomous remediation can make an incident worse. A classic threshold autoscaler sees rising
latency and adds replicas; each new replica opens more database connections, so a DB bottleneck
gets worse. We need a way to let a controller propose actions while formally refusing the unsafe
ones.

## 2. What was missing in prior solutions?

- Threshold autoscalers: no notion of *why* latency rose, no safety check on the action.
- Chaos tools: break things, but do not decide what to do about it.
- LLM "SRE assistant" tools: explain after the fact, have no enforcement power, and can be wrong.
- None of them produce an auditable record of what was proposed, what was vetoed, and why.

## 3. What is your contribution?

A deterministic **safety shield** that is the sole veto over a prediction-driven controller,
plus a versioned invariant catalog, a decision ledger (proposal → verdict → action →
verification), and fail-closed behaviour when any dependency is unavailable. The demo shows the
shield refusing `SCALE_UP` on `INV-O2` and the controller falling through to `SHED_LOAD`.

## 4. Why is the shield a separate service?

It is pure: no DB, no clock, no randomness, no network. The caller supplies `now` and
`decision_uid`, so the same input always gives the same verdict. Separating it makes the safety
argument small, testable and independent of the controller's heuristics.

## 5. Why does determinism matter?

A safety check must be replayable and auditable. Given the same state snapshot, the verdict is
reproducible, so an examiner can re-run `/evaluate` and get the same answer. We have a dedicated
determinism test.

## 6. Is ML allowed to act on its own?

No. `ml-service` returns a failure probability and feature contributions. It cannot call an
executor. The controller turns that signal into an ordered list of candidate actions; the shield
decides which are allowed. Prediction is a signal, never authority.

## 7. What if the shield or the ML service is down?

Fail closed. The controller records `FAIL_CLOSED` with `NO_ACTION` and executes nothing. There is
no "allow because the shield was slow" path.

## 8. What is `decision_uid` for?

It makes a decision idempotent. The shield checks that one is present; the ledger's primary key
enforces uniqueness; and the controller persists the ledger row **before** executing anything.
A repeated cycle cannot execute the same decision twice.

## 9. Why `SHED_LOAD` rather than `SCALE_UP` here?

`INV-O2` says that when DB connections are at/above the ceiling, adding replicas would add more
connections. Shedding load reduces incoming work without touching the database pool. The demo
shows the gateway returning real 503s and p95 dropping afterwards.

## 10. Why XGBoost, and is the evaluation honest?

It is fast on tabular telemetry and gives feature contributions. We used a time-based split and
compared against a trivial `db_conn_pct >= 0.55` rule: classifier F1 **0.960 / ROC-AUC 0.996**
versus baseline F1 **0.660 / ROC-AUC 0.767**. Training data is **simulated** — we say so openly.

## 11. What role does the LLM play?

Advisory only. It reads real incident and saga evidence and writes a postmortem with claims
labelled FACT / PREDICTION / INFERENCE / RECOMMENDATION. It has no infrastructure permissions and
is not in the control path. No mock LLM is used.

## 12. What are the main limitations?

One Mac, one testbed, one controller replica, injected faults and simulated training data. No
real cloud scaling, no outbox poller/consumers/idempotency enforcement/crash-resume, no regions
or failover, no RAG corpus, no N≥5 experiment suite, no production claims. The decision ledger is
not yet in the LLM's evidence bundle.

## 13. What is genuinely novel versus assembled?

The assembled parts (saga, metrics, XGBoost, LLM) are standard. The contribution is the
composition: **prediction proposes, a pure invariant shield vetoes, a ledger records, and the
LLM only explains.** That separation is the thesis.

## 14. Walk me through the 2-minute demo

1. "Orders flow through a saga; real `/metrics` show p95 and DB connections low, risk low."
2. "I inject DB latency. Connections saturate, p95 rises, the XGBoost risk score climbs."
3. "The controller proposes `SCALE_UP`. The shield blocks it on `INV-O2`; the ledger shows the
   veto; the controller executes `SHED_LOAD`."
4. "New orders get real 503s and latency recovers. Verification is written to the ledger."
5. "A live OpenRouter postmortem explains the incident — advisory only, never in the control path."
6. "A threshold-only scaler would have scaled up here. The shield refused."

## 15. If a component fails during the viva

Use the fallbacks in `docs/TOMORROW.md` §6 and the real screenshots in `docs/screenshots/`.
Never fabricate a verdict or a result.
