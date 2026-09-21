AUTONOMOUS CLOUD RELIABILITY & TRANSACTION CONTINUITY PLATFORM
FINAL END-TO-END PROJECT REPORT — AUDITED BY TWO-AGENT DESIGN REVIEW
=========================================================================
This version supersedes all earlier drafts. It was produced by: (1) an
ADVOCATE pass defending/refining the plan against 4 hard constraints, (2) a
SKEPTIC pass interrogating every decision and catching a Windows/RAM issue
and a work-split issue the advocate missed, (3) this consolidated final
version reconciling both. Where the two disagreed, the stricter/safer call
was kept.

HARD CONSTRAINTS (restated, drive every decision below)
---------------------------------------------------------
1. Zero money spent. No paid cloud, no paid API keys, no paid tools.
2. Must be a genuinely full END-TO-END working system on demo day — not
   slides, not stubs pretending to be features.
3. No heavy laptop. Assume ~8GB RAM machines, no dedicated GPU, mid-range
   CPU. On Windows, Docker Desktop's WSL2 VM alone can reserve 2-3GB before
   a single container starts — so the real usable budget for containers is
   closer to 4-5GB, not 8GB. Verify this on the actual laptops in week 1.
4. Exactly 3 team members. Work split must not create a single-person
   bottleneck that blocks the other two for the whole semester.

=========================================================================
1. WHAT THE PROJECT IS (unchanged from earlier drafts)
=========================================================================
A small distributed order-processing system (Order -> Inventory -> Payment
-> Risk) coordinated with a Saga pattern for transaction correctness, an
event backbone for async communication, a lightweight ML layer that
forecasts load and predicts failure risk, a guarded decision engine that
reacts (scale / reroute / fail over), chaos scripts that deliberately break
the system to prove all of this works, and a local GenAI layer that
performs real analysis/writing work (not chat) using the evidence the
system generates. Everything runs on Docker Compose, $0 cost.

=========================================================================
2. FINAL LOCKED DECISIONS (post-audit)
=========================================================================

DECISION 1 — LANGUAGE FOR CONTROL PLANE
Go for the 7 transactional/control-plane services, IF at least one team
member is genuinely comfortable with Go already.
  - Audit finding: budgeting a "1-2 week Go crash course" into a 3-person,
    one-semester project is a real cost against constraint 2, even though
    it's not money. Do NOT plan a language-learning phase.
  - FINAL RULE: use whichever backend language the team already knows best
    (Go if someone knows it well; otherwise plain Node.js/TypeScript or
    Python+FastAPI for the control plane is equally acceptable). The
    architecture, patterns, and this document apply regardless of language
    choice. Pick the language in week 0, in one conversation, based on
    actual existing skill — do not choose it for resume value.
  - Python stays reserved for the two AI sidecars (`ml-service`,
    `genai-service`) regardless of which language the control plane uses.

DECISION 2 — NO KUBERNETES, DOCKER COMPOSE ONLY
  - `docker compose --scale <service>=N` gives real multiple replicas —
    genuine horizontal scaling with zero control-plane overhead.
  - A small hand-written reverse-proxy/router service keeps a health map of
    backend instances and reroutes away from unhealthy ones — this replaces
    k8s ingress/HPA behavior and is fully explainable line-by-line in a viva.
  - "Region A / Region B" = two separate Compose projects on the same
    machine, connected through the router. Document explicitly as a logical
    simulation, not real geo-redundancy.
  - FINAL RULE (new, from audit): before writing any service code, build a
    per-container RAM budget table (see Section 8) and validate it on the
    WORST/lowest-spec laptop on the team, not the average one. Do this in
    week 1, not after hitting a wall in week 8.

DECISION 3 — KAFKA AS THE EVENT BACKBONE (UPDATED — team's final call)
  - Team decision: use Kafka for all asynchronous events, not NATS. Reason
    given: Kafka is the standard, well-known tool for async event handling
    and the team wants direct experience with it.
  - RAM consequence (be upfront about this, it is the real trade-off of this
    choice): run Kafka in **KRaft mode, single broker, no ZooKeeper**
    (`KAFKA_PROCESS_ROLES=broker,controller`) — this is the leanest possible
    Kafka setup. Even so, expect roughly 800MB-1.5GB RAM for the broker JVM
    (tune `KAFKA_HEAP_OPTS`, e.g. `-Xmx512m -Xms512m`, to keep it near the
    lower end). This is 15-40x heavier than NATS would have been — it is the
    single biggest RAM line item in the whole stack after Ollama, so the
    Section 8/13 RAM budget table MUST include it explicitly and be
    validated on the worst laptop in week 1 before assuming it fits.
  - Do NOT run ZooKeeper, do NOT run a multi-broker cluster, do NOT run
    Kafka Connect/Debezium/Schema Registry — single KRaft broker only. Any
    of those extras will very likely blow the 8GB budget once Postgres +
    control-plane services + ml-service + (sequential) Ollama are counted.
  - Outbox pattern (unchanged in shape): service writes business row +
    outbox row in one Postgres transaction -> small poller publishes to
    Kafka -> consumers dedupe via a `processed_events` table (idempotent
    consumers), using consumer groups for each subscribing service.
  - Document as an ADR: "Kafka (single-broker KRaft mode) chosen for async
    events over NATS for team familiarity with the industry-standard tool;
    trade-off is materially higher RAM usage, mitigated by running the
    leanest possible single-broker configuration and validating the RAM
    budget in week 1."

DECISION 4 — XGBOOST-ONLY ML, ONE FEATURE PIPELINE
  - One Python/FastAPI `ml-service`: XGBoost regressor (load forecast) +
    XGBoost classifier (failure-within-10-min risk), sharing one feature
    extraction pipeline, exposed as `/forecast` and `/risk`.
  - Audit finding: no deep learning needed and no long justification needed
    either — trains in seconds on CPU, genuinely feeds the decision engine
    (not decorative), and the "is this real AI" concern is already answered
    by the GenAI component (Decision 5). Do not relitigate model choice with
    the mentor; state it as a one-line design decision.

DECISION 5 — GENAI: REAL WORK, RUN IN "SEQUENTIAL DEMO MODE"
This is the most re-worked section after audit — the original plan
under-estimated RAM cost of running an LLM ALONGSIDE the full transactional
stack on an 8GB machine.

  What GenAI actually does (unchanged, still real work not chat):
    JOB 1 (core): after a chaos incident resolves, auto-generate a full
      incident postmortem (markdown) from structured evidence — metrics
      timeline, ML predictions with timestamps, the decision engine's
      chosen action + why, saga compensation steps, outcome. Sections:
      Summary, Timeline, Root Cause, Evidence, Actions Taken, Recommendation.
    JOB 2 (core): RAG over the team's own docs + past incident reports +
      the live telemetry snapshot, producing root-cause explanations that
      explicitly separate FACT / PREDICTION / INFERENCE / RECOMMENDATION.
      Must use a REAL embedding + vector-store step (even a simple one) —
      not prompt-stuffing raw text, or the "RAG" claim is fake.
    JOB 3 (cut): "propose next chaos scenario" stretch goal — REMOVED. Not
      needed to prove GenAI does real work; jobs 1+2 already prove it, and
      it adds risk for no end-to-end credit.

  RAM reality check (the audit's key catch):
    A capable local model for writing + RAG (7-8B class, e.g. llama3.1:8b)
    needs ~5-6GB resident RAM at Q4 quantization — this will NOT fit next
    to Postgres + NATS + 7-9 other services + Docker Desktop's own overhead
    on an 8GB machine. Even a small model (phi3:mini / qwen2.5:1.5b,
    ~1.5-2GB) is tight if everything else is running simultaneously,
    especially once real total-vs-free RAM on Windows is accounted for.

  FINAL RULE — SEQUENTIAL DEMO MODE (do this, don't hand-wave it):
    1. Run the chaos/failover scenario live with the full transactional
       stack up; persist all incident evidence to Postgres/files as it
       normally would.
    2. Stop non-essential containers (Grafana, duplicate region services,
       anything not needed for the next step) — `docker compose down` or
       pause them.
    3. Start `genai-service` + Ollama (small quantized model, e.g.
       qwen2.5:1.5b or phi3:mini) fresh, and generate the postmortem from
       the ALREADY-PERSISTED incident data (not live telemetry at that
       moment).
    4. Show the generated report.
    This is honest end-to-end (real data in, real generation out) without
    requiring simultaneous residency of the entire stack plus an LLM. State
    this explicitly in the architecture doc as "sequential demo mode" so it
    reads as a deliberate design decision, not a workaround being hidden.
    Alternative if one teammate's laptop is stronger: run genai-service on
    that machine and call it over REST from the main demo machine during
    presentation — still $0 cost, no code change, just demo logistics.
  ACTION ITEM: in week 1, check ACTUAL total RAM (not "8GB-ish") on all
  three laptops, and measure Docker Desktop's idle overhead before
  finalizing which model size is realistic.

DECISION 6 — GENAI/ML STAY PYTHON SIDECARS (CONTRADICTION RESOLVED)
Earlier drafts briefly floated rewriting `genai-service` in pure Go with
`chromem-go` to avoid Python entirely. That note is REMOVED. Final,
locked design: `ml-service` and `genai-service` are both small Python
(FastAPI) HTTP sidecars, called by the control-plane services over plain
REST — exactly like calling any external API. This is a standard,
defensible architecture pattern (AI workloads in the ecosystem built for
them; transactional/control-plane code in whatever language the team knows
best), not a compromise.

=========================================================================
3. GUARDRAILS THAT MUST BE REAL, NOT DECORATIVE (audit finding, section 7)
=========================================================================
The two agents agreed these four things separate a real project from a
slideware one — none of them are optional:
  1. Region A/B failover must be demoed LIVE with an actual `docker kill`
     or container-stop during the presentation, not just described.
  2. RAG must have a real embedding + vector-store step (any simple local
     option is fine — SQLite+cosine-similarity, a pure-library vector
     store, etc.) — never raw prompt-stuffing pretending to be RAG.
  3. The deterministic decision engine and the GenAI/LLM path must both
     route through the SAME action executor / evidence store. If the LLM's
     explanation and the controller's actual action aren't provably looking
     at the same data, the "LLM never touches infra directly" guardrail is
     decorative, not real.
  4. Chaos scenarios must run live in the demo, in the same session as the
     rest of the story — not as separate, disconnected pre-recorded clips.

=========================================================================
4. FINAL SERVICE LIST
=========================================================================
  1. gateway                 - entry point, correlation IDs, routing to region
  2. order-service           - order state machine
  3. inventory-service       - reserve/release stock
  4. payment-service         - authorize/refund (simulated)
  5. risk-service            - simulated fraud check, intentionally failable
  6. saga-orchestrator       - workflow state machine + compensation
  7. router/lb               - health-aware reverse proxy (replaces k8s ingress/HPA)
  8. reliability-controller  - reads metrics, calls ml-service, applies rules, triggers actions
  9. ml-service   (Python/FastAPI) - XGBoost forecast + risk endpoints
 10. genai-service (Python/FastAPI) - postmortem generation + RAG (Ollama), sequential demo mode
 11. frontend (React + TS)  - dashboard: system map, transactions, chaos lab, decision timeline, reports

Services 1-8 are in the team's chosen control-plane language (Go, or
whatever the team already knows best per Decision 1).

Infra (all via Docker Compose, all free):
  - PostgreSQL           - system of record, separate schemas per service
  - NATS + JetStream      - event backbone
  - Prometheus            - metrics (Grafana optional/dev-only if RAM is tight,
                            see Section 8 — do not make it demo-critical)
  - Ollama                - local LLM runtime, run in sequential demo mode

Cut entirely (documented as deliberate scope limitation, a strength not a
weakness in the report): Kubernetes, kind, Helm, Kafka, Debezium,
Redis/Valkey, Terraform, ArgoCD, Chaos Mesh, Jaeger/Loki/Tempo/OTel
collector, LSTM/Transformer/RL, gRPC, service mesh, GenAI's "propose next
chaos scenario" stretch job.

=========================================================================
5. TRANSACTION CONSISTENCY (unchanged, was already correct)
=========================================================================
Saga orchestration + transactional outbox + idempotency keys + consumer-side
dedupe table. Local DB transaction writes business row + outbox row
together; a poller publishes outbox rows to NATS; consumers record
processed event_id to avoid double-processing. No 2PC, no distributed
transaction across services.

=========================================================================
6. CHAOS SCENARIOS (plain Docker, no Chaos Mesh needed)
=========================================================================
  - Kill a container: `docker kill <container>` from a chaos-runner script.
  - DB slowdown: a feature-flag toggle in payment-service adding artificial
    latency to DB calls.
  - NATS consumer slowdown: sleep injected in a consumer handler via flag.
  - Risk-service outage: a `/chaos/fail` endpoint returning 500s.
  - Region failure: mark all instances in one compose project unhealthy in
    the router's health map -> router redirects new traffic to the other
    region's compose project. Must be demoed live (see Section 3).

=========================================================================
7. WORK SPLIT (3 people) — REBALANCED AFTER AUDIT
=========================================================================
Audit finding: the original split gave Person A 6 services + saga + outbox
+ NATS — 2-3x the scope of B or C, and everyone else's work was blocked
until A's services existed. Fixed with two changes: (a) rebalance service
ownership, (b) stub-first integration so B and C are never blocked waiting
on A's real implementation.

  WEEK 1 (all three, together): freeze the API contract, the event
  schema/envelope, and the DB schema conventions. Person A ships a RUNNABLE
  STUB immediately — a fake gateway / mock event emitter that publishes
  canned NATS messages matching the frozen schema. B and C build and test
  against this stub from day one; A swaps in the real implementation later
  without B/C needing to change anything on their end.

  Person A — Transaction core: saga-orchestrator, gateway, reliability-
    controller (the genuinely hard coordination logic), plus code review
    on order/inventory/payment/risk.
  Person B — Simpler transactional services + ML + chaos: order-service,
    inventory-service (or payment/risk — split evenly with A so no one
    person owns 6 services), ml-service, chaos scripts, experiments.
  Person C — Router/frontend/GenAI: router/lb, React dashboard (system map,
    transaction explorer, chaos lab, decision timeline, reports viewer),
    genai-service + RAG setup, documentation/ADRs/demo script.

  Rule of thumb going forward: no single person should own more than ~40%
  of the total services. If in doubt, count services per person weekly and
  rebalance early rather than in week 10.

=========================================================================
8. RESOURCE FOOTPRINT — PLAN THIS BEFORE CODING, NOT AFTER
=========================================================================
Do this literally in week 1, on the WORST laptop on the team:
  1. Note actual TOTAL system RAM (not "8GB-ish" — check Task Manager).
  2. Install Docker Desktop, run nothing, check RAM used at idle (WSL2 VM
     overhead on Windows is commonly 2-3GB by itself).
  3. Bring up Postgres + NATS only, measure with `docker stats`.
  4. Add control-plane services one at a time, measure again.
  5. Add ml-service, measure.
  6. Only THEN decide whether Grafana is affordable as demo-critical or
     should be dev-only/optional.
  7. Test Ollama + genai-service SEPARATELY in "sequential demo mode"
     (Section 2, Decision 5) — never assume it runs concurrently with
     everything else until measured.

Rough starting estimates (verify, do not trust blindly):
  Postgres ~100-200MB, NATS ~20-50MB, each small control-plane service
  ~15-30MB, Prometheus ~150-200MB, Grafana ~150MB, ml-service (Python +
  XGBoost) ~150-250MB, Ollama + small quantized model (1.5B-3.8B) ~1.5-2.5GB
  while loaded. Docker Desktop/WSL2 idle overhead on Windows: budget 2-3GB
  before any of the above. This is why K8s and Kafka were dropped, and why
  GenAI runs in sequential demo mode rather than continuously.

=========================================================================
9. BUILD ORDER (realistic for 3 people, one semester)
=========================================================================
Phase 0: Repo, docs, API/event/DB contracts, ADRs, Person A's stub gateway/
         mock event emitter running, RAM budget table validated on the
         worst laptop.
Phase 1: Real Order/Inventory/Payment/Risk + Saga + outbox + idempotency,
         happy path + one failure/compensation path, end-to-end.
Phase 2: NATS wiring for real (replacing the stub) + Prometheus metrics +
         optional Grafana dashboards.
Phase 3: Router/LB with health checks + `docker compose --scale` demo +
         logical region A/B + live manual failover working.
Phase 4: Chaos scripts + telemetry export -> labeled dataset for ML.
Phase 5: ml-service (XGBoost forecast + risk classifier) wired into
         reliability-controller with rule-based decisions + guardrails,
         sharing the same action executor referenced in Section 3.
Phase 6: genai-service (RAG + Ollama) in sequential demo mode, generating
         real postmortems from Phase 4/5 incident evidence.
Phase 7: React dashboard: system map, transaction explorer, decision
         timeline, chaos lab buttons, generated reports viewer.
Phase 8: Baseline (no ML/controller) vs proposed (full system) experiments,
         metrics, report, demo script rehearsed end-to-end at least twice.

=========================================================================
10. ONE-LINE PITCH
=========================================================================
"A Docker-Compose-based, zero-cost distributed order-processing platform
with Saga-coordinated transactions over a NATS event backbone, XGBoost-based
failure prediction driving a guarded decision engine that scales/reroutes/
fails over across simulated regions, and a local GenAI layer that
automatically writes evidence-grounded incident postmortems from real chaos
experiments — engineered end-to-end to run on ordinary laptops."

=========================================================================
11. AUDIT METHODOLOGY NOTE (for the report's engineering-process section)
=========================================================================
This plan was produced through a structured two-pass review: an ADVOCATE
pass defended and refined each architectural decision against the four
constraints above, then a SKEPTIC pass independently interrogated every
decision, catching two issues the advocate missed or under-weighted (the
Windows/Docker Desktop RAM overhead problem, and the insufficiency of
"freeze the schema" alone without a runnable stub for unblocking work
split). The consolidated decisions in this document reflect where both
passes agreed, with the stricter/safer position kept wherever they
diverged. This process itself is worth one paragraph in the project report
as evidence of engineering rigor.
