=========================================================================
AUTONOMOUS CLOUD RELIABILITY & TRANSACTION CONTINUITY PLATFORM
THE MASTER DOCUMENT — COMPLETE END-TO-END BUILD REFERENCE
=========================================================================
This single file is everything the team needs: what the project is, the use
case, the full flow, the architecture, the final tech stack with reasons,
the contracts (API / events / database), the ML and GenAI design, the
repository layout, the week-by-week plan, the 3-person work split, the demo
script, the experiments, the limitations, and a viva question bank.

If any older document disagrees with this file, THIS FILE WINS.

Version: FINAL (post two-agent audit + Kafka decision)
Event backbone: APACHE KAFKA (team's final call — see Section 5.4)

-------------------------------------------------------------------------
TABLE OF CONTENTS
-------------------------------------------------------------------------
  0.  Constraints that shaped every decision
  1.  What this project is (plain English)
  2.  The use case and the story it tells
  3.  End-to-end flow, step by step
  4.  System architecture
  5.  Final tech stack — what each tool does and why it was chosen
  6.  Engineering best practices that make this "world class"
  7.  Contracts: API endpoints
  8.  Contracts: Kafka topics and the event envelope
  9.  Contracts: database schema
  10. The Saga: states, happy path, and compensation
  11. The ML layer: features, training data, models
  12. The GenAI layer: two real jobs, prompts, and guardrails
  13. Chaos engineering scenarios
  14. The frontend dashboard
  15. Repository structure
  16. Running it: Docker Compose, ports, and the RAM budget
  17. Testing strategy and CI
  18. Week-by-week build plan (14 weeks)
  19. Work split for 3 people (non-blocking)
  20. Experiments: baseline vs proposed, and metrics
  21. Demo script (minute by minute)
  22. Limitations to state openly
  23. Viva / defense question bank
  24. Explicit non-goals
  25. What this audit changed and why

=========================================================================
0. CONSTRAINTS THAT SHAPED EVERY DECISION
=========================================================================
  C1. ZERO MONEY. No paid cloud, no paid APIs, no paid tools. Everything
      free and open-source, running locally.
  C2. GENUINELY END-TO-END. A real working system demoed live — not
      slides, not stubs dressed up as features.
  C3. NO HEAVY LAPTOP. Assume ~8GB RAM, no GPU, mid-range CPU. On Windows,
      Docker Desktop's WSL2 layer can reserve 2-3GB before a single
      container starts, so the realistic container budget is ~4-5GB.
  C4. EXACTLY 3 PEOPLE. The work must split so that no one person blocks
      the other two for the semester.

Every decision in this document is traceable to one or more of these. If a
future idea cannot be justified against them, it does not go in.

=========================================================================
1. WHAT THIS PROJECT IS (PLAIN ENGLISH)
=========================================================================

ONE SENTENCE
A small online order-processing system that is deliberately engineered to
survive failure — and to explain itself when failure happens.

THE LONGER VERSION
When a customer places an order, that single action must succeed across
four independent services that do not share a database: reserve stock, take
payment, run a fraud/risk check, confirm the order. If one step fails
halfway through, the system must not be left in a broken state. It must
automatically undo the steps that already succeeded — refund the payment,
release the reserved stock — so the customer and the business end up
consistent.

On top of that correctness layer, the system watches its own health. A
machine-learning layer forecasts load and predicts which service is likely
to fail in the next few minutes. A decision engine reacts safely — adding
capacity, refusing to scale when scaling would make a database bottleneck
worse, or rerouting traffic to a healthy region. Finally, once an incident
is over, a local AI model reads the real evidence the system produced and
writes an actual incident report, the kind a Site Reliability Engineer
writes by hand today.

WHY THIS IS A STRONG PROJECT
  - It spans five subject areas — distributed systems, databases,
    event-driven architecture, DevOps/observability, and AI (both classical
    ML and GenAI) — and every one of them is load-bearing. Nothing is there
    just for decoration.
  - It demos brilliantly. You can kill a running container in front of the
    examiner and watch the system detect it, decide what to do, recover,
    and then write up what happened.
  - It has a real, measurable research claim (Section 20), not just "we
    built an app."
  - It runs entirely free, on an ordinary laptop.

WHAT IT IS NOT
  - Not a real payment system or bank. Payments are simulated.
  - Not literally multi-region. "Regions" are two Docker Compose projects
    on one laptop. This is stated openly and is a strength (honest scoping),
    never hidden.
  - Not Kubernetes, not deep learning. Both were evaluated and deliberately
    rejected for concrete, statable reasons (Section 5).

=========================================================================
2. THE USE CASE AND THE STORY IT TELLS
=========================================================================

PRIMARY USE CASE
"Order placement that stays correct even when parts of the system fail."

THE CONCRETE SCENARIO THE ENTIRE PROJECT IS BUILT TO DEMONSTRATE

  1. A customer places an order.
  2. The system reserves inventory, authorizes payment, and runs a risk
     check — three independent services, three independent data stores.
  3. Normally all three succeed and the order completes.
  4. Now inject a failure: the risk service goes down (one button in the
     Chaos Lab). The order must NOT be left half-finished. The system
     automatically refunds the payment, releases the reserved inventory,
     and marks the order cleanly FAILED — not stuck, not silently wrong,
     not double-charged.
  5. Meanwhile, before anything even broke, the ML layer was forecasting
     load and scoring each service's risk of failing in the next ten
     minutes. When risk rises (say, database connections climbing), the
     decision engine picks a safe action: add an instance, or REFUSE to
     add instances because more instances would open more database
     connections and make the real bottleneck worse, or shift traffic away
     from an unhealthy region.
  6. Once the incident resolves, the GenAI layer reads the actual evidence
     — the metric timeline, the ML predictions with timestamps, the
     decision taken and why, the compensation steps, the outcome — and
     writes a real incident report that cites which signal moved first and
     separates fact from prediction from inference from recommendation.

SUPPORTING SCENARIOS THAT PROVE IT WORKS
  - Traffic spike: does the forecaster see it coming before it lands?
  - Database slowdown: does the controller avoid the classic mistake of
    adding more replicas when the database is the bottleneck?
  - Region outage: does traffic genuinely reroute to the backup?

WHO WOULD CARE IN THE REAL WORLD (motivation section of the report)
Any team running a distributed e-commerce, fintech, or logistics backend,
where a partial failure mid-transaction is worse than a clean total
failure, and where writing up "why did this break" currently costs an
engineer hours after every incident.

=========================================================================
3. END-TO-END FLOW, STEP BY STEP
=========================================================================

STEP 1 — REQUEST ENTERS
  The React dashboard sends POST /api/orders with an Idempotency-Key
  header. The Gateway attaches a correlation ID and routes the request to
  the currently healthy region via the Router.

STEP 2 — THE SAGA BEGINS
  The Saga Orchestrator creates a transaction record (status = PENDING) in
  its own database, then executes steps in order:
      Reserve Inventory -> Authorize Payment -> Risk Check
  Every step's state is persisted before and after execution, so the system
  always knows exactly where a transaction stands, even after a crash.

STEP 3 — SERVICES WORK AND PUBLISH EVENTS RELIABLY (OUTBOX PATTERN)
  Each service writes its business state change AND an outbox row inside
  the SAME local database transaction. A small poller then reads
  unpublished outbox rows and publishes them to Kafka, marking them
  published. This guarantees an event is never lost because the process
  died between "database committed" and "message sent" — the classic
  dual-write bug that this pattern exists to eliminate.

STEP 4 — EVERYTHING IS OBSERVED
  Every service exposes Prometheus metrics: request rate, latency
  histogram, error rate, in-flight requests, database pool usage, Kafka
  consumer lag, restart count. Every log line and every event carries the
  correlation ID and transaction ID, so a single order can be followed
  across all services.

STEP 5 — ML PREDICTS BEFORE FAILURE HAPPENS
  The reliability-controller periodically sends a telemetry window to the
  ML service, which returns:
      (a) a short-term load forecast (XGBoost regression), and
      (b) a failure-risk probability for the next ~10 minutes (XGBoost
          classification) with the top contributing features, e.g.
          "payment-service: 0.87 risk, top signal: db_connection_pct".

STEP 6 — THE DECISION ENGINE DECIDES, SAFELY
  The controller does NOT act on the model's number alone. It applies hard
  safety rules first, then scores the remaining candidate actions:

      HARD RULES (examples)
        - never exceed MAX_REPLICAS for a service
        - never scale out a service when its database connection pool is
          above the safe threshold (scaling would worsen the bottleneck)
        - never fail over to a target that is itself unhealthy
        - never execute the same decision ID twice

      SCORING
        score(action) = w1*predicted_latency + w2*failure_risk
                      + w3*resource_cost + w4*instability_penalty
        choose the lowest-scoring action that passes every hard rule.

      ACTION SPACE
        NO_ACTION | SCALE_UP | SCALE_DOWN | STOP_SCALING
        ROUTE_AWAY | FAILOVER_REGION

  The ML model is a signal, never an authority. This separation is what
  makes the system explainable and safe, and it is a key talking point.

STEP 7 — THE ACTION IS EXECUTED
  A single Action Executor component performs the chosen action: call the
  Router to shift traffic or flip a region's health, or run
  `docker compose --scale` to add/remove real replicas. Every action is
  written to a decisions table with its reason and score before execution.

STEP 8 — THE SAGA RECOVERS INDEPENDENTLY
  Transaction recovery is completely separate from infrastructure
  reaction. If a step genuinely failed, the Saga Orchestrator reads its own
  durable state and runs compensations in reverse order (refund payment,
  release inventory), ending at a consistent FAILED state. It never
  performs a blind global rollback and never depends on the ML layer.

STEP 9 — RECOVERY IS VERIFIED
  After acting, the controller re-checks latency, error rate, and risk to
  confirm the system actually improved, and closes the loop by recording:
  prediction -> decision -> action -> outcome. This record is the evidence
  bundle for Step 10.

STEP 10 — GENAI WRITES THE INCIDENT REPORT
  When an incident closes, the GenAI service is handed the structured
  evidence from Steps 4-9, retrieves relevant context from the team's own
  docs and past incidents (RAG over pgvector), and generates a Markdown
  incident report with FACT / PREDICTION / INFERENCE / RECOMMENDATION
  clearly separated. Because of the laptop RAM ceiling this runs in
  "sequential demo mode" (Section 5.7) — real data in, real report out,
  just not co-resident with the entire stack.

STEP 11 — IT IS ALL VISIBLE ON ONE SCREEN
  The React dashboard shows the live system map, the transaction explorer
  with the Saga timeline, the ML risk view, the decision timeline, the
  Chaos Lab, the experiments comparison, and the generated reports.

=========================================================================
4. SYSTEM ARCHITECTURE
=========================================================================

TWO PLANES, ONE DASHBOARD

  TRANSACTION PLANE                  RELIABILITY PLANE
  (does the business work,           (watches, predicts, decides,
   guarantees correctness)            and reacts safely)
  ------------------------------     ------------------------------
  Gateway                            Prometheus (telemetry)
  Order / Inventory / Payment        ML Service (forecast + risk)
  Risk                               Reliability Controller
  Saga Orchestrator                  Action Executor
  PostgreSQL (schema per service)    Router / Load Balancer
  Kafka (event backbone)             GenAI Service (reports + RAG)
  Outbox + idempotency + dedupe

  Both planes write their evidence to PostgreSQL, and the React dashboard
  reads from both. Crucially, the controller and the GenAI service read the
  SAME evidence tables — so the explanation and the action can never drift
  apart into two different stories.

FULL LIFECYCLE DIAGRAM

    React Dashboard
          |
          v
      Gateway ------------------> Router / LB (health map)
          |                             |
          v                             | (reroute / failover)
    Saga Orchestrator                   |
          |                             |
    +-----+--------+---------+          |
    v              v         v          |
 Inventory      Payment    Risk         |
    |              |         |          |
    +--- outbox rows (same DB tx) ---+  |
                   |                    |
                   v                    |
                 KAFKA                  |
                   |                    |
    all services --+--> Prometheus      |
                            |           |
                            v           |
                    ML Service (XGBoost)|
                            |           |
                            v           |
              Reliability Controller    |
                            |           |
                            v           |
                     Action Executor ---+
                            |
                            v
                Verify recovery + decisions table
                            |
                            v
              GenAI Service (Ollama + pgvector RAG)
                            |
                            v
                  Incident Report (Markdown)
                            |
                            v
                     React Dashboard

REGIONS
"Region A" and "Region B" are two Docker Compose projects on the same
machine, each running a copy of the transactional services, both behind the
Router. This proves the failover LOGIC — health detection, traffic
shifting, recovery — not geographic redundancy. Say this plainly in the
report and in the viva; examiners respect precision far more than
overclaiming.

=========================================================================
5. FINAL TECH STACK — WHAT EACH TOOL DOES AND WHY
=========================================================================

5.1 CONTROL-PLANE LANGUAGE: GO (or the team's strongest language)
  USED FOR: gateway, order, inventory, payment, risk, saga-orchestrator,
  router/lb, reliability-controller.
  WHY: tiny memory footprint per service (~15-30MB), so running 8+ services
  plus replicas stays affordable under C3. Static binaries mean small,
  fast-starting containers. Good standard-library HTTP and concurrency
  support, so no heavyweight framework is needed.
  IMPORTANT CAVEAT: do NOT budget a "learn Go" phase into the semester. If
  nobody on the team is already comfortable with Go, use the language the
  team genuinely knows (Node.js/TypeScript or Python/FastAPI are fine) —
  every pattern in this document is language-independent. Risking C2 (a
  working system) to learn a language is a bad trade. Decide in week 0.
  LIBRARIES (if Go): net/http or chi for routing, pgx for PostgreSQL,
  segmentio/kafka-go or franz-go for Kafka (both pure Go, no cgo),
  prometheus/client_golang for metrics, golang-migrate for schema
  migrations.

5.2 DOCKER + DOCKER COMPOSE (no Kubernetes)
  USED FOR: running every component; real horizontal scaling via
  `docker compose up --scale payment-service=4`; simulating two regions as
  two Compose projects.
  WHY NOT KUBERNETES: a local cluster (kind/minikube) consumes 1-2GB+ RAM
  before your application starts, which directly violates C3, and it buys
  nothing the project needs. Compose gives real containers and real
  replicas at effectively zero control-plane cost.
  WHAT REPLACES HPA/INGRESS: a small hand-written Router service that keeps
  a health map of instances and routes away from unhealthy ones. This is
  roughly 150 lines, and in a viva you can explain every line of your own
  failover logic — which is worth more than pointing at YAML you did not
  write.

5.3 POSTGRESQL (with the pgvector extension)
  USED FOR: the system of record for every service (one schema per
  service), the outbox tables, saga state, the predictions and decisions
  logs, incident records, AND the vector store for RAG embeddings.
  WHY: free, reliable, and one database the whole team learns instead of
  three. Using pgvector for RAG means the AI layer adds ZERO extra
  infrastructure — no Chroma, no FAISS server, no extra container, no extra
  RAM. Use the `pgvector/pgvector:pg16` image.
  RULE: services never read each other's schemas. Cross-service data moves
  by API call or Kafka event only. No cross-schema joins.

5.4 APACHE KAFKA — THE EVENT BACKBONE (team's final decision)
  USED FOR: all asynchronous events between services — OrderCreated,
  InventoryReserved, PaymentAuthorized, RiskFailed, and so on — published
  through the transactional outbox and consumed idempotently.
  WHY KAFKA: it is the industry-standard event backbone, the team wants
  hands-on experience with it, and it is what a reviewer or interviewer
  will recognise immediately. It gives durable, replayable, partitioned
  logs with consumer groups and ordering guarantees per key — genuinely the
  right conceptual fit for an event-driven transaction system.

  THE HONEST TRADE-OFF (do not skip this — it is the one real cost):
  Kafka runs on the JVM and is substantially heavier than the alternatives.
  Mitigate it deliberately:
    - Run KRaft mode, SINGLE BROKER, NO ZooKeeper. Modern Kafka does not
      need ZooKeeper at all. Use the official `apache/kafka` image with
      combined roles: KAFKA_PROCESS_ROLES=broker,controller.
    - Cap the heap: KAFKA_HEAP_OPTS="-Xmx512m -Xms512m". Expect roughly
      700MB-1GB resident. Without this cap it will happily take more.
    - Use few partitions (1-3 per topic) and a short retention
      (e.g. 1-2 hours) — you are not storing history, you are moving events.
    - Do NOT add ZooKeeper, a multi-broker cluster, Schema Registry, Kafka
      Connect, or Debezium. Every one of those is another JVM or another
      container, and any of them will break the RAM budget under C3.

  THE SANCTIONED FALLBACK (important, and costs nothing to keep in reserve):
  If the week-1 RAM measurement shows Kafka does not fit comfortably, swap
  the Kafka container for REDPANDA. Redpanda implements the Kafka wire
  protocol, so your producer and consumer code, your client libraries, your
  topic names, your consumer groups, and your partition keys all stay
  EXACTLY the same — you change the image and the broker address in
  docker-compose.yml and nothing else. It is a single C++ binary with no
  JVM and runs comfortably in a few hundred MB. You still legitimately say
  "the system uses Kafka" because it is the Kafka protocol and Kafka
  clients throughout. Document this in an ADR as a planned contingency so
  it reads as foresight, not panic.

5.5 PROMETHEUS (+ Grafana, optional)
  USED FOR: scraping every service's metrics; feeding both the dashboard
  and the ML feature pipeline.
  WHY GRAFANA IS OPTIONAL: it costs ~150MB for visualisation that your own
  React dashboard largely duplicates. Keep it for development, but never
  let the live demo depend on it — if RAM is tight on demo day, it is the
  first thing you stop.

5.6 XGBOOST, SERVED BY A PYTHON/FASTAPI ml-service
  USED FOR: (a) an XGBoost regressor forecasting near-term load, and (b) an
  XGBoost classifier predicting failure probability in the next ~10
  minutes. Both share one feature-extraction function and are served from
  one FastAPI process on /forecast and /risk, so the rest of the system
  sees a single "ML brain" with one contract.
  WHY NOT DEEP LEARNING OR REINFORCEMENT LEARNING: the data here is small
  and tabular, inference must be fast on a CPU with no GPU, and the
  training set is generated from your own chaos runs. XGBoost trains in
  seconds, predicts in milliseconds, and provides feature importances you
  can actually show in the UI and explain in a viva. An LSTM or a
  Transformer would add training time, RAM, and fragility with no benefit
  to the demonstration. Reinforcement learning would need a stable
  simulator, a reward function, many episodes, and protection against
  unsafe exploration — months of work before you get a controller you can
  trust. Rules plus scoring is the correct first implementation and is
  defensible as an engineering decision, not a shortcut.
  WHY PYTHON HERE: the ML and GenAI ecosystems live in Python. These two
  services are small REST sidecars called over HTTP, exactly like any
  external API. The transactional core stays in one language; the AI slice
  uses the right tool. State this as a deliberate architectural boundary.

5.7 OLLAMA + A SMALL LOCAL LLM, SERVED BY A PYTHON/FASTAPI genai-service
  USED FOR: two real jobs (full detail in Section 12) — automatically
  writing incident postmortems from real evidence, and answering
  root-cause questions grounded in retrieved evidence. Not a chatbot.
  WHY LOCAL: C1 forbids paid APIs. Ollama is free, runs offline, needs no
  API key, and keeps the whole project reproducible on any laptop.
  MODELS: generation — `qwen2.5:1.5b` (~1GB) or `phi3:mini` (~2.2GB) or
  `llama3.2:3b` (~2GB), all Q4-quantised. Embeddings — `nomic-embed-text`
  (~275MB), which is small enough to keep resident.
  WHY "SEQUENTIAL DEMO MODE": a 7-8B model needs ~5-6GB resident, which
  cannot co-exist with Postgres + Kafka + the services + Docker's own
  overhead on an 8GB machine. Even a small model is tight when everything
  else is up. So the demo explicitly:
      (1) runs the chaos scenario with the full stack live, persisting all
          evidence to PostgreSQL;
      (2) stops the components not needed next (Grafana, the duplicate
          region, and Kafka itself — which alone frees ~1GB);
      (3) starts Ollama + genai-service and generates the report from the
          already-persisted evidence;
      (4) displays the result.
  This is honest end-to-end behaviour — real data in, real generated output
  — simply not co-resident. Document it as a deliberate, constraint-driven
  design decision in an ADR. It reads as engineering maturity. Hiding it
  and having the demo machine swap to death on stage does not.
  ALTERNATIVE: run genai-service on whichever teammate's laptop has the
  most RAM and call it over REST during the demo. Still free, still no code
  change — pure logistics.
  HARD GUARDRAIL: the LLM never calls an infrastructure API. It cannot
  scale, stop, start, or route anything. It only produces text and JSON.
  Only the deterministic controller acts.

5.8 REACT + TYPESCRIPT + VITE
  USED FOR: the single operations dashboard (Section 14).
  WHY: free, standard, and it lets the entire story — transactions, ML,
  decisions, chaos, GenAI — be told from one screen in a 12-minute demo.
  LIBRARIES: TanStack Query for server state, Recharts for charts, React
  Flow (or hand-drawn SVG) for the system map, Tailwind for styling.

5.9 EVERYTHING DELIBERATELY EXCLUDED, AND WHY
  Kubernetes (RAM, no benefit here) · ZooKeeper (obsolete in KRaft mode) ·
  Debezium / Kafka Connect / Schema Registry (extra JVMs, and the
  application-level outbox poller is simpler and sufficient) · Redis /
  Valkey (no caching requirement in the MVP; PostgreSQL is fast enough at
  this scale) · Terraform / ArgoCD (nothing to provision — it is all local
  Compose) · Chaos Mesh (needs Kubernetes; plain `docker kill` and feature
  flags do the same job) · Jaeger / Loki / Tempo / OTel Collector (each is
  another container; Prometheus plus structured logs with correlation IDs
  covers what the project actually needs) · service mesh · gRPC (REST plus
  Kafka is enough, and REST is trivially debuggable from a browser) · full
  event sourcing · LSTM / Transformers / RL · the GenAI "propose the next
  chaos experiment" idea (cut — jobs 1 and 2 already prove GenAI does real
  work; this only added risk).
  Present this list in the report as evidence of deliberate scoping. Every
  entry has a one-line reason. "We considered it and rejected it for reason
  X" is a much stronger answer than silence.

=========================================================================
6. ENGINEERING BEST PRACTICES THAT MAKE THIS "WORLD CLASS"
=========================================================================
These cost almost nothing in RAM or time but are what separate a student
project from professional work. Adopt all of them from week 1.

DISTRIBUTED SYSTEMS
  - Timeouts on every network call. No unbounded waits, ever.
  - Bounded retries with exponential backoff AND jitter (jitter prevents
    synchronised retry storms — a real cause of cascading failure).
  - Idempotency keys on every externally retryable write operation.
  - Consumer-side deduplication via a processed_events table.
  - Correlation ID generated at the Gateway, propagated through every HTTP
    call, every Kafka message, and every log line.
  - Explicit state machines with persisted transitions — never infer state.
  - Graceful shutdown: stop accepting new work, finish in-flight work,
    close the Kafka consumer cleanly, then exit.
  - A circuit breaker on the risk-service call (the one designed to fail)
    so a dead dependency degrades instead of hanging every request.

KAFKA
  - One fixed event envelope for every message (Section 8).
  - Key every message by aggregate_id (or transaction_id) so all events for
    one order land on the same partition and stay ordered.
  - One consumer group per consuming service.
  - Commit offsets AFTER successful processing, not before — at-least-once
    delivery plus idempotent consumers is the correct combination.
  - A dead-letter topic for messages that fail repeatedly, so a single
    poison message never blocks a partition.
  - Version field in the envelope so the schema can evolve.

DATABASE
  - Versioned migrations from day one (golang-migrate or equivalent).
    Never hand-edit a running schema.
  - One schema per service; no cross-service joins; no service reads
    another's tables.
  - Connection pool limits set explicitly and exported as a metric — this
    is literally the signal the database-bottleneck demo depends on.

TWELVE-FACTOR / OPERATIONS
  - All configuration via environment variables. No hardcoded hosts,
    ports, or credentials.
  - No secrets in the repository. A committed .env.example documents the
    variables; the real .env is gitignored.
  - /healthz (liveness) and /readyz (readiness) on every service.
  - Structured JSON logs including correlation_id, service, and level.
  - A Makefile so the whole system starts with one command
    (`make up`, `make demo`, `make chaos-region-fail`). On demo day you do
    not want to be typing long docker commands.

SECURITY AND SAFETY (proportionate to a local project, but stated)
  - Chaos and AI endpoints are enabled only when ENV=local.
  - The LLM has no infrastructure permissions whatsoever.
  - Input validation at the Gateway boundary.
  - Never log full payloads containing personal data; log IDs instead.

TEAM PROCESS
  - Trunk-based development: short-lived branches, small PRs, one reviewer,
    CI must pass before merge, protected main branch.
  - An ADR (Architecture Decision Record) for every significant decision.
    They take ten minutes to write and they are exactly what you will need
    when the examiner asks "why did you choose this?"
  - API and event contract changes are documented in the same PR as the
    code change.

=========================================================================
7. CONTRACTS: API ENDPOINTS
=========================================================================
Keep the surface small and stable. Freeze this in week 1.

TRANSACTION
  POST   /api/orders                  (header: Idempotency-Key, required)
  GET    /api/orders/:id
  GET    /api/transactions            (list, paginated)
  GET    /api/transactions/:id        (includes the full saga step timeline)
  POST   /api/transactions/:id/retry

RELIABILITY
  GET    /api/system/health
  GET    /api/services                (instances, health, replica counts)
  GET    /api/predictions             (recent ML predictions)
  GET    /api/decisions               (the controller's action log)
  GET    /api/incidents
  GET    /api/incidents/:id
  GET    /api/metrics/summary

CHAOS  (ENV=local only)
  POST   /api/chaos/payment-kill
  POST   /api/chaos/db-latency        { service, added_ms }
  POST   /api/chaos/consumer-lag      { service, sleep_ms }
  POST   /api/chaos/risk-outage       { enabled: true|false }
  POST   /api/chaos/region-failover   { region: "a"|"b" }
  POST   /api/chaos/traffic-spike     { rps, duration_s }

AI
  POST   /api/ai/generate-report      { incident_id }
  GET    /api/ai/incidents/:id/report
  POST   /api/ai/query                { question, incident_id? }

ml-service (internal, port 9000)
  POST /forecast
       in : { service, window: [ {ts, requests, cpu_pct, mem_pct,
                                   p95_latency_ms, db_conn_pct,
                                   consumer_lag}, ... ] }
       out: { requests_5m, requests_15m, cpu_pct_5m, db_conn_pct_5m }

  POST /risk
       in : { service, features: { ... see Section 11 ... } }
       out: { failure_probability, risk_level, horizon_minutes,
              predicted_cause, top_features: [ {name, contribution}, ... ] }

genai-service (internal, port 9001)
  POST /generate-postmortem   { incident_id } -> { markdown, sections }
  POST /rag-query             { question, incident_id? }
                              -> { answer, evidence: [...], citations: [...] }

=========================================================================
8. CONTRACTS: KAFKA TOPICS AND THE EVENT ENVELOPE
=========================================================================

TOPICS (create explicitly at startup; 1-3 partitions each, short retention)
  order.events
  inventory.events
  payment.events
  risk.events
  transaction.events        (saga-level state changes)
  reliability.events        (predictions, decisions, actions)
  dead-letter.events        (poison messages)

MESSAGE KEY
  Always key by aggregate_id (the order ID), or transaction_id for
  transaction.events. This guarantees that all events for a single order go
  to one partition and are therefore consumed in order. This is the single
  most important Kafka detail in the project — say it in the viva.

THE ENVELOPE (every message, no exceptions)
  {
    "event_id":       "uuid",
    "event_type":     "PaymentAuthorized",
    "occurred_at":    "2026-01-01T12:00:00Z",
    "transaction_id": "uuid",
    "aggregate_id":   "order-uuid",
    "correlation_id": "uuid",
    "source":         "payment-service",
    "region":         "region-a",
    "version":        1,
    "payload":        { }
  }

EVENT TYPES (the complete list — do not invent others ad hoc)
  OrderCreated · OrderCompleted · OrderFailed
  InventoryReserved · InventoryReleased · InventoryReservationFailed
  PaymentAuthorized · PaymentRefunded · PaymentFailed
  RiskApproved · RiskRejected · RiskCheckFailed
  SagaStepStarted · SagaStepSucceeded · SagaStepFailed
  SagaCompensationStarted · SagaCompensated
  PredictionMade · DecisionTaken · ActionExecuted · RecoveryVerified

RULE: no service invents its own message shape. Any new event type is added
to docs/EVENT_CONTRACTS.md and reviewed by all three members before merge.

=========================================================================
9. CONTRACTS: DATABASE SCHEMA
=========================================================================
One PostgreSQL instance, one schema per service. Sketch:

  -- ORDER SCHEMA
  orders(id uuid pk, public_id text unique, customer_ref text,
         status text, total_amount numeric, created_at, updated_at)
  idempotency_keys(key text pk, order_id uuid, response_body jsonb,
         created_at)
  outbox(id bigserial pk, aggregate_id uuid, event_type text,
         payload jsonb, published bool default false, created_at,
         published_at)

  -- INVENTORY / PAYMENT / RISK SCHEMAS (each mirrors this shape)
  reservations | payments | risk_checks  (domain table)
  outbox(...)                            (same shape as above)
  processed_events(event_id uuid, consumer_name text, processed_at,
         primary key (event_id, consumer_name))

  -- SAGA SCHEMA
  sagas(id uuid pk, transaction_id uuid unique, order_id uuid,
        status text, current_step text, created_at, updated_at)
  saga_steps(id bigserial pk, saga_id uuid, step_name text,
        step_order int, status text, attempt int,
        started_at, finished_at, error text)

  -- RELIABILITY SCHEMA
  predictions(id bigserial pk, service text, failure_probability numeric,
        risk_level text, horizon_minutes int, predicted_cause text,
        top_features jsonb, created_at)
  decisions(id bigserial pk, decision_uid uuid unique, service text,
        action text, reason text, score numeric, rule_blocked text,
        created_at, executed_at, execution_result text)
  incidents(id uuid pk, scenario text, started_at, resolved_at,
        status text, summary text)
  incident_evidence(id bigserial pk, incident_id uuid, kind text,
        data jsonb, captured_at)

  -- AI SCHEMA (pgvector)
  CREATE EXTENSION IF NOT EXISTS vector;
  knowledge_chunks(id bigserial pk, source text, chunk_text text,
        embedding vector(768), created_at)
  generated_reports(id uuid pk, incident_id uuid, markdown text,
        model text, generated_at)

THE THREE TABLES THAT MAKE THE CORRECTNESS STORY REAL
  outbox (no lost events) + processed_events (no duplicate processing) +
  saga_steps (durable, inspectable transaction state). If an examiner asks
  "how do you actually guarantee consistency?", these three tables plus the
  compensation flow are the entire answer.

SINGLE SOURCE OF TRUTH MAP
  Order / Inventory / Payment / Risk state -> that service's own schema
  Saga / workflow state                    -> saga schema
  Events                                   -> Kafka (transport & replay log,
                                              NOT business truth)
  Metrics                                  -> Prometheus
  Model artifacts                          -> Git (ml/models/)
  RAG knowledge                            -> versioned Markdown in Git,
                                              embedded into pgvector
  Infrastructure config                    -> Git (compose files)
  Never make the frontend, Kafka, or the ML model a source of business truth.

=========================================================================
10. THE SAGA: STATES, HAPPY PATH, AND COMPENSATION
=========================================================================

STEP STATES
  PENDING -> RUNNING -> SUCCEEDED
                     -> FAILED -> COMPENSATING -> COMPENSATED

HAPPY PATH
  OrderCreated (PENDING)
    -> Reserve Inventory      SUCCEEDED
    -> Authorize Payment      SUCCEEDED
    -> Risk Check             SUCCEEDED
    -> Order COMPLETED

COMPENSATION PATH (the one you must demo live)
  OrderCreated
    -> Reserve Inventory      SUCCEEDED
    -> Authorize Payment      SUCCEEDED
    -> Risk Check             FAILED        <- chaos-injected
    -> COMPENSATING
       -> Refund Payment      COMPENSATED
       -> Release Inventory   COMPENSATED
    -> Order FAILED (consistent, not stuck, not double-charged)

RULES
  - Compensations run in REVERSE order of completion.
  - Every compensation is idempotent — running it twice is harmless.
  - A step that times out is retried with backoff up to a bounded limit
    before being marked FAILED.
  - The saga_steps table is written before and after each attempt, so a
    crash mid-transaction is recoverable: on startup the orchestrator scans
    for sagas stuck in RUNNING or COMPENSATING and resumes them.
  - Show this resume-after-crash behaviour if you have time; it is a
    genuinely impressive thirty-second addition to the demo.

=========================================================================
11. THE ML LAYER: FEATURES, TRAINING DATA, MODELS
=========================================================================

MODEL A — LOAD FORECAST (XGBoost regressor)
  Features: requests_per_min at t, t-1, t-5, t-10, t-15; rolling mean and
  slope over 5 and 15 minutes; cpu_pct; mem_pct; consumer_lag;
  minute-of-run (synthetic seasonality from the scripted load pattern).
  Targets: requests_per_min at t+5 and t+15.

MODEL B — FAILURE RISK (XGBoost classifier)
  Features: cpu_pct, mem_pct, p95_latency_ms, error_rate, retry_rate,
  kafka_consumer_lag, db_connection_pct, restart_count, request_rate,
  plus the 5-minute slope of latency, error rate, and db_connection_pct
  (the slopes matter more than the absolute values — trend is what predicts
  failure).
  Target: failure_within_10m (0/1).
  Output: probability, risk level, and the top contributing features from
  XGBoost's feature importance, which the dashboard displays directly.

HOW TO CREATE THE TRAINING DATA (free, reproducible, and a good story)
  1. Write a load generator producing several patterns: steady, ramp,
     spike, sustained-high.
  2. Run every chaos scenario from Section 13, several times each, under
     each load pattern.
  3. Export the Prometheus telemetry for each run to CSV, tagged with the
     scenario name and the exact time the failure occurred.
  4. Label: rows in the 10 minutes BEFORE a failure get 1; rows in healthy
     periods get 0. Discard ambiguous rows immediately after recovery.
  5. Split by TIME, never randomly. A random split leaks information from
     the same incident into both train and test and will give you a
     gorgeous, meaningless accuracy score. Say this in the report — it
     shows you understand evaluation, which most student ML projects do not.
  6. Train, evaluate (precision, recall, F1, ROC-AUC, false-alarm rate, and
     mean prediction lead time), and save artifacts to ml/models/ in Git.

EVALUATION HONESTY
  Always compare the model against a trivial baseline — for example
  "predict failure whenever CPU > 85%". If XGBoost cannot beat that, say
  so. A documented negative result is respectable science; an unexamined
  0.99 accuracy is a red flag to any examiner who knows the field.

=========================================================================
12. THE GENAI LAYER: TWO REAL JOBS, PROMPTS, AND GUARDRAILS
=========================================================================
The mentor's requirement is that GenAI does legitimate work, not chat. It
is satisfied by giving the model a WRITING responsibility over real
evidence, producing artifacts a human would otherwise have to produce.

JOB 1 — AUTOMATIC INCIDENT POSTMORTEM (the centrepiece)
  Trigger: an incident closes (chaos scenario resolved).
  Input: a structured evidence bundle assembled by genai-service from the
  database — the metric timeline before/during/after, every prediction with
  its timestamp and top features, every decision with its reason and the
  rule that fired, every saga step and compensation, and the recovery
  verification result.
  Output: a Markdown report saved to generated_reports and displayed in the
  UI, with sections: Summary, Timeline, Root Cause, Evidence, Actions
  Taken, Recommendation.
  Why this is real work: this is exactly the document a Site Reliability
  Engineer writes by hand after every production incident. By the end of
  the semester your repository contains a genuine corpus of these reports,
  generated from real experiments — that is a tangible project artifact.

JOB 2 — EVIDENCE-GROUNDED ROOT CAUSE ANSWERING (RAG)
  Corpus: the team's own ARCHITECTURE.md, RUNBOOK.md, EVENT_CONTRACTS.md,
  every past generated report, and the incident evidence records.
  Pipeline: chunk the documents -> embed with nomic-embed-text via Ollama
  -> store in pgvector -> at query time embed the question, retrieve the
  top-k chunks by cosine similarity, and pass them plus the live evidence
  bundle to the generation model.
  Required output discipline: every answer labels its claims.
      FACT:           db_connection_pct reached 92% at 14:02:03.
      PREDICTION:     the model gave payment-service 0.81 failure risk.
      INFERENCE:      database saturation preceded the latency rise by 36s,
                      so it is the leading candidate cause.
      RECOMMENDATION: block further scale-out; reduce pool size per replica.
  This separation is the single most defensible thing about the design: the
  model is never allowed to present an inference as a fact.

PROMPT SKELETON (keep it in ai/prompts/, versioned in Git)
  System: "You are an SRE assistant. You may ONLY use the evidence
  provided. Never invent a metric, timestamp, or service name. Every claim
  must be labelled FACT, PREDICTION, INFERENCE, or RECOMMENDATION. If the
  evidence is insufficient to determine the root cause, say so explicitly
  rather than guessing."
  User: "<structured evidence bundle as JSON>  <retrieved doc chunks>
  Write the incident report."

CUT: the "propose the next chaos experiment" job. Jobs 1 and 2 already
prove GenAI does real work; the third added scope and risk with no marginal
credit.

HARD GUARDRAILS (state these in the report and the viva)
  - The LLM has NO infrastructure permissions. It cannot scale, restart,
    route, or fail over anything. It emits text and JSON only.
  - Only the deterministic rule-plus-scoring controller executes actions.
  - The controller and the LLM read the SAME evidence tables, so the
    written explanation always corresponds to the action actually taken.
  - Model, prompt version, and the evidence bundle ID are recorded with
    every generated report, so any output is reproducible and auditable.

=========================================================================
13. CHAOS ENGINEERING SCENARIOS
=========================================================================
All achievable with plain Docker and feature flags. No Chaos Mesh needed.

  A. KILL A SERVICE INSTANCE
     Mechanism: `docker kill <container>`.
     Expect: restart count rises, latency rises, risk score rises, the
     controller acts, the system recovers.

  B. DATABASE SLOWDOWN  (the showcase experiment)
     Mechanism: a feature flag in payment-service adds artificial latency
     to its database calls.
     Expect: db_connection_pct climbs, latency climbs. The BASELINE sees
     high CPU and adds replicas, each opening more connections, making
     things worse. The PROPOSED controller's hard rule blocks scale-out and
     shifts traffic instead. This contrast is the best single moment in the
     entire demo — rehearse it.

  C. KAFKA CONSUMER LAG
     Mechanism: a configurable sleep injected into a consumer handler.
     Expect: consumer lag grows, transaction completion time grows, the
     controller responds.

  D. RISK-SERVICE OUTAGE  (the transaction-correctness proof)
     Mechanism: POST /api/chaos/risk-outage makes it return 500s.
     Expect: Inventory reserved, Payment authorized, Risk failed ->
     compensation refunds the payment, releases the inventory, and marks
     the order FAILED cleanly.

  E. REGION FAILURE
     Mechanism: stop Region A's containers and/or mark them unhealthy in
     the Router's health map.
     Expect: the Router detects unhealthy instances and routes all new
     traffic to Region B, with a visible blip and recovery.

  F. TRAFFIC SPIKE
     Mechanism: the load generator ramps request rate sharply.
     Expect: the forecaster predicts the rise before it peaks; the
     controller pre-scales.

Every scenario records an incidents row with start time, end time, and
evidence, which is what Section 12 Job 1 later turns into a report.

=========================================================================
14. THE FRONTEND DASHBOARD
=========================================================================
It must look like an operations control centre, not a CRUD admin panel.

  PAGE 1 — SYSTEM OVERVIEW
    Status cards: system health, active transactions, P95 latency, current
    failure risk, Kafka consumer lag, SLO status.
    Live system map: Gateway -> Region A / Region B -> services, with each
    node coloured healthy / degraded / failing / recovered, updating live.

  PAGE 2 — TRANSACTION EXPLORER
    Table of transactions (id, status, current step, region, duration).
    Click one to see the full saga step timeline including compensation
    steps. This page is the visual proof of the correctness claim.

  PAGE 3 — RELIABILITY / ML
    Per-service failure probability with the horizon, a bar chart of the
    top contributing features, and the decision the controller made with
    the reason and the rule that fired.

  PAGE 4 — DECISION TIMELINE
    A chronological feed: forecast rose -> risk crossed threshold ->
    controller evaluated N actions -> chose X because Y -> executed ->
    metrics recovered. Timestamps throughout.

  PAGE 5 — CHAOS LAB
    One button per scenario in Section 13. Each shows injected-at,
    detected-at, predicted-at, action-taken, recovered-at.

  PAGE 6 — EXPERIMENTS
    Baseline vs Proposed, side by side: P95 latency, SLO violations,
    recovery time, replica count over time, transaction failures, and the
    ML precision/recall summary.

  PAGE 7 — INCIDENT REPORTS
    The list of GenAI-generated postmortems, each viewable in full, plus an
    ad-hoc question box for Job 2.

FRONTEND RULES
  Server state goes through TanStack Query; keep local state minimal. Type
  every API response. Every page needs explicit loading, error, and empty
  states. Never let the frontend become a source of truth.

=========================================================================
15. REPOSITORY STRUCTURE
=========================================================================
  autonomous-cloud-reliability/
  |
  +-- apps/
  |   +-- gateway/
  |   +-- order-service/
  |   +-- inventory-service/
  |   +-- payment-service/
  |   +-- risk-service/
  |   +-- saga-orchestrator/
  |   +-- router/
  |   +-- reliability-controller/
  |
  +-- ml/
  |   +-- training/            (train_forecast.py, train_risk.py)
  |   +-- service/             (FastAPI app: /forecast, /risk)
  |   +-- features/            (shared feature extraction)
  |   +-- models/              (committed model artifacts)
  |   +-- datasets/            (exported labelled CSVs)
  |
  +-- ai/
  |   +-- service/             (FastAPI app: postmortem + RAG)
  |   +-- retrieval/           (chunking, embedding, pgvector queries)
  |   +-- prompts/             (versioned prompt templates)
  |
  +-- frontend/
  |
  +-- deploy/
  |   +-- docker-compose.yml           (core stack, region A)
  |   +-- docker-compose.region-b.yml  (second logical region)
  |   +-- docker-compose.ai.yml        (Ollama + genai-service, started
  |   |                                 separately for sequential mode)
  |   +-- prometheus/
  |   +-- grafana/
  |
  +-- chaos/                   (scenario scripts + load generator)
  +-- experiments/             (run scripts, results, charts)
  +-- migrations/              (versioned SQL per service schema)
  +-- docs/
  |   +-- ARCHITECTURE.md
  |   +-- SSOT.md
  |   +-- EVENT_CONTRACTS.md
  |   +-- API_SPEC.yaml
  |   +-- ML_EXPERIMENTS.md
  |   +-- CHAOS_SCENARIOS.md
  |   +-- RUNBOOK.md
  |   +-- DEMO_SCRIPT.md
  |   +-- LIMITATIONS.md
  |   +-- adr/
  |       +-- 0001-language-choice.md
  |       +-- 0002-postgresql-as-business-state.md
  |       +-- 0003-kafka-kraft-single-broker.md
  |       +-- 0004-redpanda-contingency.md
  |       +-- 0005-saga-over-2pc.md
  |       +-- 0006-rules-and-scoring-over-rl.md
  |       +-- 0007-compose-over-kubernetes.md
  |       +-- 0008-llm-advisory-only.md
  |       +-- 0009-sequential-demo-mode.md
  |       +-- 0010-pgvector-over-separate-vector-db.md
  |
  +-- .github/workflows/ci.yml
  +-- Makefile
  +-- .env.example
  +-- README.md

=========================================================================
16. RUNNING IT: COMPOSE, PORTS, AND THE RAM BUDGET
=========================================================================

PORTS
  gateway 8080 · order 8081 · inventory 8082 · payment 8083 · risk 8084
  saga 8085 · reliability-controller 8086 · router 8000
  ml-service 9000 · genai-service 9001
  postgres 5432 · kafka 9092 · prometheus 9090 · grafana 3000
  ollama 11434 · frontend 5173

KAFKA CONTAINER (lean, KRaft, no ZooKeeper)
  image: apache/kafka:latest
  environment:
    KAFKA_NODE_ID: 1
    KAFKA_PROCESS_ROLES: broker,controller
    KAFKA_CONTROLLER_QUORUM_VOTERS: 1@kafka:9093
    KAFKA_LISTENERS: PLAINTEXT://:9092,CONTROLLER://:9093
    KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:9092
    KAFKA_CONTROLLER_LISTENER_NAMES: CONTROLLER
    KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: 1
    KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR: 1
    KAFKA_LOG_RETENTION_HOURS: 2
    KAFKA_HEAP_OPTS: "-Xmx512m -Xms512m"
  (If you swap to Redpanda later, only this block and the broker address
   change. No application code changes.)

RAM BUDGET — MEASURE IT IN WEEK 1, ON THE WORST LAPTOP
  Procedure, in this order, recording `docker stats` at each step:
    1. Record TOTAL system RAM from Task Manager. Not "about 8".
    2. Start Docker Desktop with nothing running. Record the idle cost.
       WINDOWS TIP: cap WSL2 by creating %UserProfile%\.wslconfig with
         [wsl2]
         memory=5GB
         processors=4
       This stops WSL2 from ballooning and is the single most useful
       Windows-specific fix for this project.
    3. Start PostgreSQL + Kafka only. Measure.
    4. Add the control-plane services one at a time. Measure.
    5. Add Prometheus, then ml-service. Measure.
    6. Decide from the numbers whether Grafana stays or becomes dev-only.
    7. Separately, start Ollama + genai-service and measure the model.
       Never assume it fits alongside everything else — that is exactly the
       assumption that breaks on demo day.

  ROUGH EXPECTATIONS (verify, do not trust)
    Docker Desktop / WSL2 idle ............ 1.5-3 GB  (cap it, see above)
    PostgreSQL + pgvector ................. 150-250 MB
    Kafka (KRaft, 512m heap) .............. 700 MB - 1 GB   <- biggest
                                                               non-AI item
    8 control-plane services @ ~20 MB ..... ~160 MB
    Prometheus ............................ 150-200 MB
    Grafana (optional) .................... ~150 MB
    ml-service (Python + XGBoost) ......... 200-250 MB
    ---- steady-state total (no AI) ....... roughly 1.5-2 GB of containers
    Ollama + small quantised model ........ 1.5-2.5 GB   (sequential mode)

  This is why the AI layer runs sequentially: the steady-state stack is
  comfortable, but adding a language model on top of it is not. Stopping
  Kafka before the AI step alone frees around a gigabyte.

  IF IT STILL DOES NOT FIT: switch the Kafka container to Redpanda (zero
  code change, Section 5.4), drop Grafana, and use qwen2.5:1.5b instead of
  phi3:mini. Those three moves together free roughly 1.5 GB.

=========================================================================
17. TESTING STRATEGY AND CI
=========================================================================
Keep it small but real. Three layers:

  UNIT — the saga state machine, the decision engine's rules and scoring,
  the feature extraction. These are pure logic and are the highest-value
  tests in the project. The decision engine especially: given a fixed
  input, it must always produce the same action. Determinism is a claim you
  are making, so test it.

  INTEGRATION — one test per service against a real PostgreSQL and a real
  Kafka (Testcontainers, or just the Compose stack in CI). Specifically
  test: the outbox actually publishes, a duplicate event is ignored by the
  consumer, and an idempotency key returns the original response instead of
  creating a second order.

  END-TO-END — one test that places an order and asserts it completes, and
  one that triggers the risk outage and asserts the compensation ran and
  the final state is FAILED with the payment refunded. If only two tests in
  the whole project are green, make it these two.

  CI (GitHub Actions, free for public repositories)
    on: pull_request
    jobs: lint -> unit tests -> build containers -> integration tests
  Keep CI under ten minutes. Protect main; require CI to pass.

  NEVER delete or weaken a test to make the build green. If a test fails
  near the deadline, document why and fix the cause.

=========================================================================
18. WEEK-BY-WEEK BUILD PLAN (14 WEEKS)
=========================================================================
  W0  Language decision. Repo skeleton. Docker Compose with PostgreSQL and
      Kafka starting cleanly. First ADRs written.
  W1  Freeze the three contracts (API, event envelope, DB schema). Person A
      ships a RUNNABLE STUB gateway + mock event producer. Run the full RAM
      budget measurement on the worst laptop and record the numbers in
      docs/. Decide Kafka vs Redpanda from those numbers.
  W2  Order + Inventory services, migrations, outbox table and poller,
      first real events flowing into Kafka.
  W3  Payment + Risk services. Idempotency keys. Consumer deduplication.
  W4  Saga orchestrator: happy path end to end, then the compensation path.
      ** Milestone: the core correctness story works. **
  W5  Prometheus metrics on every service. Structured logging with
      correlation IDs. Grafana if the budget allows.
  W6  Router with health checks. `--scale` demonstration. Region B Compose
      project. Live manual failover working.
  W7  Chaos scripts (all six scenarios) plus the load generator. Begin
      exporting telemetry.
  W8  Generate and label the dataset. Train both XGBoost models. Evaluate
      properly with a time-based split against a trivial baseline.
  W9  ml-service serving /forecast and /risk. Reliability controller:
      rules, scoring, Action Executor, decision logging.
      ** Milestone: closed loop — predict, decide, act, verify. **
  W10 genai-service: chunking, embeddings into pgvector, RAG retrieval,
      postmortem generation. Sequential demo mode scripted in the Makefile.
  W11 Frontend: system map, transaction explorer, decision timeline.
  W12 Frontend: chaos lab, experiments page, reports viewer.
  W13 Run the full baseline-vs-proposed experiment suite. Collect metrics.
      Produce the charts.
  W14 Write the report. Rehearse the demo end to end at least twice on the
      actual demo machine. Buffer for the things that break.

  If you fall behind, cut in this order: Grafana, then the experiments
  page, then GenAI Job 2, then region failover. Never cut the saga
  compensation path or the chaos lab — they are the project.

=========================================================================
19. WORK SPLIT FOR 3 PEOPLE (NON-BLOCKING)
=========================================================================
THE CRITICAL RULE, ESTABLISHED IN WEEK 1
Freezing the contracts on paper is necessary but NOT sufficient. Person A
must also ship a RUNNABLE STUB in week 1 — a fake gateway that serves the
real API shapes and a mock producer that publishes correctly-shaped events
to Kafka on demand. B and C then build and test against the stub from day
one and swap to the real implementation later without changing their code.
Without this, A becomes a bottleneck for the entire semester.

  PERSON A — TRANSACTION CORE
    Owns: saga-orchestrator, gateway, the outbox/idempotency pattern,
    Kafka topic setup, migrations, and the week-1 stub.
    Also: reviews the four domain services.
    Owns the correctness claim in the report.

  PERSON B — SERVICES, ML, AND EXPERIMENTS
    Owns: two or three of the domain services (order/inventory/payment/
    risk — split evenly with A so nobody owns six), ml-service, the
    training pipeline, all chaos scripts, the load generator, and the
    baseline-vs-proposed experiment suite.
    Owns the research/evaluation claim in the report.

  PERSON C — ROUTER, FRONTEND, AND AI
    Owns: the router/load balancer, the entire React dashboard,
    genai-service with RAG, the documentation set, the ADRs, and the demo
    script.
    Owns the presentation and the AI claim.

  SHARED
    The reliability-controller is written by A and B together — it is the
    seam between the two planes and both need to understand it.
    Integration testing and the final demo rehearsal are all three.

  BALANCE RULE: nobody owns more than about 40% of the services. Count them
  weekly. Rebalance in week 5, not week 12.

  CROSS-CUTTING RULE: every feature should touch at least two people's
  areas. B builds the risk model, C visualises it, A emits the metrics that
  feed it. This prevents three disconnected mini-projects.

=========================================================================
20. EXPERIMENTS: BASELINE VS PROPOSED, AND METRICS
=========================================================================

THE RESEARCH QUESTION
  Can a telemetry-driven, guarded controller — combining workload
  forecasting, failure-risk prediction, and saga-based transaction recovery
  — reduce SLO violations and recovery time compared with a conventional
  reactive baseline, while preserving transaction correctness, on commodity
  hardware at zero infrastructure cost?

BASELINE SYSTEM
  Same application, same saga. Reactive only: a simple CPU-threshold
  autoscaler and restart-on-failure. No forecasting, no risk model, no
  guard rules.

PROPOSED SYSTEM
  Forecast + risk model + hard safety rules + action scoring + region
  failover + the same saga.

PROTOCOL
  Run every chaos scenario from Section 13, under the same load traces,
  N times each (N >= 5) against both systems. Automate the runs — do not
  do this by hand at 2am in week 14. Report means and spread, not single
  runs.

METRICS
  Reliability   : SLO violation rate, P95 and P99 latency, error rate,
                  mean time to recovery.
  Correctness   : incomplete transactions, duplicate operations, incorrect
                  final states, failed compensations. (Target: zero
                  incorrect final states in both systems — correctness is
                  a floor, not a variable.)
  ML quality    : precision, recall, F1, ROC-AUC, false-alarm rate, mean
                  prediction lead time before failure.
  Controller    : number of actions, number of harmful actions, scaling
                  oscillation, time to stabilise.
  Efficiency    : CPU-seconds, memory-seconds, replica-minutes, average
                  replica count. Label these clearly as a LOCAL COST PROXY.
                  Do not present them as cloud money.

EXPECTED HEADLINE RESULT
  The database-slowdown scenario (13.B) should show the baseline scaling
  itself into a worse state while the proposed controller refuses and
  reroutes. If you get one clean chart out of this project, make it that one.

=========================================================================
21. DEMO SCRIPT (MINUTE BY MINUTE, ~13 MINUTES)
=========================================================================
  0:00  Normal operation. Dashboard live, orders flowing, all nodes green.
        One sentence on what the system does.
  1:30  Place one order manually. Open it in the Transaction Explorer and
        walk the saga timeline. "This is the correctness layer."
  3:00  Traffic spike from the Chaos Lab. Show the forecast rising ahead of
        the actual load, then CPU following it.
  4:30  Database slowdown. Show the risk score climbing, the top
        contributing feature being db_connection_pct, and the controller
        choosing STOP_SCALING with its reason displayed. Contrast with the
        baseline chart on the Experiments page: "the naive system scales
        here, and makes it worse."
  6:30  Fail the risk service. Return to the Transaction Explorer:
        Inventory reserved, Payment authorized, Risk failed, then refund
        and release, ending FAILED and consistent. This is the money shot
        for distributed-transaction correctness.
  8:30  Fail Region A with a real `docker kill` on screen. Show the Router
        health map flip and traffic move to Region B. Nothing pre-recorded.
  10:00 Switch to sequential mode on screen — stop the non-essential
        containers, start Ollama and genai-service, generate the postmortem
        for the incident you just caused. Read out the FACT / PREDICTION /
        INFERENCE / RECOMMENDATION lines. Point out that every number in it
        came from the system's own tables.
  12:00 Experiments page: baseline vs proposed summary charts.
  12:30 Close with the one-line pitch and the honest limitations slide.

  RULES FOR DEMO DAY
    - Rehearse twice on the actual machine you will present from.
    - `make demo` should bring the whole thing up in one command.
    - Have a pre-generated incident report as a fallback if the LLM is slow
      — but say so if you use it. Never claim live generation you did not do.
    - Close every other application. Chrome with twenty tabs will cost you
      more RAM than Kafka.

=========================================================================
22. LIMITATIONS TO STATE OPENLY
=========================================================================
Stating these makes the project stronger, not weaker. Examiners find the
gaps anyway; finding them yourself first is the difference between a team
that understands its system and one that does not.

  1. Logical regions are two Compose projects on one machine, not
     geographically independent infrastructure. Network partitions between
     real regions are not simulated.
  2. Training data comes from synthetic fault injection and may not
     represent the distribution of real production failures.
  3. Resource measurements are a local proxy, not cloud cost.
  4. The GenAI layer runs sequentially rather than continuously, due to
     laptop memory limits — a deliberate, documented constraint.
  5. The LLM's output is evidence-grounded but still generative; it is not
     guaranteed correct root-cause analysis and is positioned as an
     assistant, never an authority.
  6. The controller acts over a small hand-designed action space, not a
     learned policy.
  7. Single Kafka broker and single PostgreSQL instance: the message bus
     and the database are themselves single points of failure in this
     deployment. Real deployments would replicate both.
  8. Throughput figures reflect a laptop, not a cluster. Report the numbers
     you actually measured and never extrapolate.
  9. This is a research and teaching prototype, not a production financial
     system.

=========================================================================
23. VIVA / DEFENSE QUESTION BANK
=========================================================================
Prepare and rehearse these. Each answer is one or two sentences.

  Q: Why Kafka and not a lighter message broker?
  A: It is the standard event backbone with durable partitioned logs,
     consumer groups, and per-key ordering, which is exactly what an
     event-driven transaction system needs, and we wanted genuine
     experience with it. We run it as a single KRaft broker with a capped
     heap to fit our hardware, and we documented Redpanda as a
     protocol-compatible fallback requiring no code change.

  Q: How do you guarantee an event is never lost?
  A: The transactional outbox. The business row and the event row commit in
     the same local transaction, and a poller publishes afterwards. There is
     no window where the database has changed but the event does not exist.

  Q: How do you guarantee an event is never processed twice?
  A: At-least-once delivery plus idempotent consumers. Every consumer
     records the event_id in processed_events and ignores repeats.

  Q: Why Saga instead of two-phase commit?
  A: The services own separate databases by design. 2PC needs a coordinator
     holding locks across all of them, which destroys availability and does
     not work across independent services. Saga trades atomicity for
     eventual consistency with explicit, testable compensations.

  Q: Why no Kubernetes?
  A: It costs 1-2GB of RAM before our application starts and adds nothing
     the research question needs. Compose gives real containers and real
     replicas, and we wrote our own health-aware router so we can explain
     every line of our failover logic.

  Q: Why XGBoost and not a neural network?
  A: Small tabular data, CPU-only inference, and a need for explainable
     feature importances. XGBoost trains in seconds and beats the trivial
     baseline; a deep model would add cost and fragility with no gain here.

  Q: Is the GenAI part just a wrapper around ChatGPT?
  A: No. It runs a local open-weight model with no API, and its job is to
     write incident postmortems from our system's own persisted evidence,
     with retrieval over our own documents. It has no infrastructure
     permissions and cannot take any action.

  Q: How do you stop the LLM from hallucinating a metric?
  A: It only receives a structured evidence bundle and retrieved chunks,
     it is instructed to label every claim as FACT, PREDICTION, INFERENCE,
     or RECOMMENDATION, and the numbers in the report are traceable to rows
     in the database. Anything it cannot support, it must decline.

  Q: Why does the AI not run at the same time as everything else?
  A: Memory. We measured it. Running a language model alongside the full
     stack does not fit in 8GB, so the report is generated from persisted
     evidence after the incident, which is also how a real postmortem
     works — after the fact, from the record.

  Q: What is your actual contribution?
  A: A reproducible, zero-cost testbed plus a controlled comparison showing
     that a guarded, prediction-driven controller avoids a specific class
     of harmful autoscaling decision — the database-bottleneck case — while
     preserving transaction correctness.

  Q: What would you do next with more time and resources?
  A: Real multi-region deployment, Debezium-based CDC instead of the
     polling outbox, an offline reinforcement-learning comparison against
     our rule-based controller, and drift monitoring on the models.

=========================================================================
24. EXPLICIT NON-GOALS — DO NOT LET THESE CREEP BACK IN
=========================================================================
  Real cloud deployment · multi-cloud · real payment gateways · real fraud
  detection · service mesh · full event sourcing · reinforcement learning
  as the live controller · GPU training · large language models ·
  authentication/authorization systems · geo-distributed databases ·
  multi-cluster Kubernetes · Terraform · ArgoCD · Chaos Mesh · a large
  microservice count.

  A smaller system that is measured, reproducible, and correctly engineered
  beats a large one with half-finished parts. Put that sentence in your
  conclusion, because it is the actual lesson of the project.

=========================================================================
25. WHAT THIS AUDIT CHANGED AND WHY
=========================================================================
For transparency, and because the reasoning is worth a paragraph in the
report's engineering-process section:

  1. EVENT BACKBONE -> KAFKA, with eyes open. The team chose Kafka for
     industry relevance. Accepted, with the JVM cost stated plainly, the
     leanest possible configuration specified (single KRaft broker, capped
     heap, no ZooKeeper, no Connect/Registry/Debezium), and Redpanda
     documented as a zero-code-change fallback if the week-1 measurement
     says it does not fit. A decision with a named contingency is a strong
     decision; one with an unexamined risk is not.

  2. VECTOR STORE -> pgvector. Previous drafts implied a separate vector
     database. Reusing the PostgreSQL you already run costs zero extra
     containers and zero extra RAM, which matters more here than any
     feature a dedicated vector store would add.

  3. ONE ACTION EXECUTOR. The controller and the GenAI layer now provably
     read the same evidence tables. Without this the "the LLM never acts"
     guardrail is a claim rather than a property of the design.

  4. STUB-FIRST, NOT SCHEMA-FIRST. Freezing contracts in week 1 is not
     enough to unblock a three-person team; someone must ship a runnable
     fake the same week. This was the single biggest scheduling risk in the
     earlier plan.

  5. RAM MEASURED, NOT ASSUMED. The week-1 measurement procedure, the
     .wslconfig cap, and sequential demo mode replace an optimistic
     assumption that everything would simply fit.

  6. TIME-BASED TRAIN/TEST SPLIT, and comparison against a trivial
     threshold baseline. A random split across incident windows leaks and
     produces meaningless accuracy.

  7. SCOPE CUTS: the GenAI "propose the next experiment" job, Grafana as a
     demo dependency, and any language-learning phase. Each was removed
     because it consumed the scarcest resources — time and memory —
     without strengthening the core claim.

=========================================================================
ONE-LINE PITCH
=========================================================================
"A zero-cost, laptop-runnable distributed order-processing platform with
saga-coordinated transactions over a Kafka event backbone, XGBoost failure
prediction driving a guarded decision engine that scales, reroutes, and
fails over across simulated regions, and a local GenAI layer that writes
evidence-grounded incident postmortems from real chaos experiments."

=========================================================================
END OF MASTER DOCUMENT
=========================================================================
