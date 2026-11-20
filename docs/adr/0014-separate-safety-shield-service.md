# ADR 0014 — Safety Shield as a Separate, Stateless Service

- Status: Accepted.
- Date: 2026-11-18.

## Context

The safety shield must be trusted. If it lives inside the controller as an ordinary function,
its verdicts are entangled with policy state and harder to test, version, and audit. The
original plan also floated an independent safety monitor; that would be a fourth always-on
service producing evidence that overlaps with the shield's own verdicts.

## Decision

- Implement `safety-shield` as a **separate Go service on :8087**.
- It is **stateless and deterministic**: no DB, no clock, no randomness, no network. Given the
  same `{action, state}` it always returns the same verdict. The controller supplies `now` and
  the decision id in the request.
- Input contract: `POST /evaluate {decision_uid, action, state}` →
  `{allow, invariant_id, reason, invariant_version}`.
- Invariants are versioned in code and mirrored in `safety_svc.invariants`. Evaluation returns
  the first violation in a fixed priority order.
- **Fail-closed at the caller:** if the shield is unreachable, the controller treats it as
  `BLOCKED` and selects `NO_ACTION`; it never bypasses the shield.
- No independent monitor service. A read-only **invariant audit** runs inside the controller
  (T1–T5, O1/O7 against the database) and writes `reliability_svc.invariant_audit`.

## Consequences

- Good: the safety claim is visible and independently testable; the shield can be unit-tested
  with table-driven allow/block cases and a determinism test; the controller can be replicated
  without replicating safety state.
- Bad: one more small service in Compose (~10–20 MB RAM); the controller must handle shield
  failure explicitly.
- The shield has no credentials and no infrastructure permissions; it cannot execute anything.
- The decision ledger, not the shield, is the source of truth for "what was allowed and why".

## Considered options

- Shield as a library inside the controller — rejected: weaker auditability and test isolation.
- Shield as a sidecar process beside the controller — rejected: same trust problem as a library.
- Separate safety monitor in addition to the shield — rejected: redundant evidence, extra
  service, and no added guarantee; the read-only audit covers the same invariants.
