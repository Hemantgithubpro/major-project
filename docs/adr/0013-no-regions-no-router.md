# ADR 0013 — Remove Regions and the Router/Load Balancer

- Status: Accepted.
- Date: 2026-11-18.

## Context

An earlier architecture included Region A / Region B, a router/LB on :8000, and
`FAILOVER_REGION` / `ROUTE_AWAY` actions. In a single-host Compose deployment these added
conceptual overhead without real failure isolation: both regions shared one Postgres and one
Kafka broker, so "failover" could not be demonstrated honestly and the shared-DB failure mode
made region semantics ambiguous.

## Decision

- Remove the router/LB service and the Region A/B concept.
- Remove `FAILOVER_REGION` and `ROUTE_AWAY` from the action space.
- Scaling is expressed as `docker compose --scale <service>=n`; the gateway performs
  client-side round-robin across the resolved replicas.
- Cross-region failover is explicitly out of scope and not claimed.

## Consequences

- Good: removes a shared-DB/region muddle; frees RAM and demo time; keeps the safety story
  crisp (actions operate on one logical region).
- Bad: no failover claim; `docker compose scale` is the only scaling mechanism (documented).
- The gateway must resolve the current replica set (DNS/`extra_hosts` or an explicit service
  list) and rotate targets; a sticky or fixed single target is not acceptable.
- `kafkax.Envelope.Region` may remain in the schema for wire compatibility but is set to a
  constant `region-a` and is no longer semantically meaningful.

## Considered options

- Keep regions but make them share nothing — rejected: impossible on one laptop without
  nested virtualization; would dominate the remaining time budget.
- Keep the router as a reverse proxy only — rejected: adds a hop with no research value; the
  gateway already proxies.
