# ADR 0012 — Safety-Constrained Autonomy as the Project Thesis

- Status: Accepted.
- Date: 2026-11-18.

## Context

The original plan was a broad "autonomous reliability platform": many services, many chaos
scenarios, and a long feature list. At the mid-viva the project was read as breadth without a
sharp contribution. The team also observed that autonomous remediation can *harm*: a naive
reactive scaler, seeing CPU rise during a database bottleneck, will `SCALE_UP` and add more
connections to an already-saturated database, making P95 and error rate worse.

## Decision

Reframe the project around one research question:

> Can autonomous remediation be made safe by explicit, machine-checkable invariants?

The architecture becomes a **prediction-driven controller** that proposes remediations, and a
**deterministic safety shield** that vetoes any proposed action violating a catalog of
transactional (T1–T5) and operational (O1–O7) invariants. The contribution is the invariant
model, the shield, and an empirical baseline-vs-proposed comparison showing the shield prevents
harmful remediation while preserving transaction correctness.

Concretely:

- The controller never acts directly; every candidate action passes through `safety-shield`.
- Correctness is a hard floor: zero incorrect final states in both baseline and proposed modes.
- The headline experiment is the database-bottleneck scenario: baseline chooses `SCALE_UP`
  (harmful); the shield blocks it on INV-O2 and the policy falls through to `SHED_LOAD` /
  `ENABLE_CIRCUIT_BREAKER`.
- GenAI stays as an advisory explanation layer over the same evidence tables, with claims
  labeled `FACT / PREDICTION / INFERENCE / RECOMMENDATION`.

## Consequences

- Good: one defensible novelty claim; a deterministic, unit-testable safety artifact; a clear
  demo narrative; the existing platform (saga, outbox, Kafka, GenAI) becomes testbed evidence
  rather than the contribution.
- Bad: requires the shield + controller + experiment to actually work before the final viva;
  breadth features (extra regions, extra scenarios) are deprioritized.
- Mid-viva pivot is stated explicitly in the report/slides: the project was *sharpened*, not
  restarted; saga compensation, Kafka/outbox and GenAI postmortem remain as prototype evidence.
- Claim boundaries are stated openly (see `PLAN.md` §2.4): synthetic testbed, injected fault
  modes, no cloud failover, no production SPOF claims.

## Considered options

- Keep the broad platform story — rejected: no single novelty, hard to defend in a group viva.
- Safety monitor as a separate always-on service — rejected: the shield already produces
  verdicts; a read-only invariant audit inside the controller covers verification without a
  fourth service.
- Full safe-RL learned shield — rejected: non-determinism would undermine the core safety claim.
