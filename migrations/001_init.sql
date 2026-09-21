CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS order_svc;
CREATE SCHEMA IF NOT EXISTS inventory_svc;
CREATE SCHEMA IF NOT EXISTS payment_svc;
CREATE SCHEMA IF NOT EXISTS risk_svc;
CREATE SCHEMA IF NOT EXISTS saga_svc;
CREATE SCHEMA IF NOT EXISTS ai_svc;

CREATE TABLE IF NOT EXISTS order_svc.orders(
  id uuid PRIMARY KEY,
  customer_ref text NOT NULL,
  status text NOT NULL DEFAULT 'PENDING',
  total_amount numeric NOT NULL DEFAULT 0,
  created_at timestamptz DEFAULT now(),
  updated_at timestamptz DEFAULT now()
);
CREATE TABLE IF NOT EXISTS order_svc.idempotency_keys(
  key text PRIMARY KEY,
  order_id uuid NOT NULL,
  response_body jsonb,
  created_at timestamptz DEFAULT now()
);
CREATE TABLE IF NOT EXISTS order_svc.outbox(
  id bigserial PRIMARY KEY,
  aggregate_id uuid NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  published boolean DEFAULT false,
  created_at timestamptz DEFAULT now(),
  published_at timestamptz
);

CREATE TABLE IF NOT EXISTS inventory_svc.reservations(
  id uuid PRIMARY KEY,
  order_id uuid NOT NULL,
  item text NOT NULL DEFAULT 'widget',
  qty int NOT NULL DEFAULT 1,
  status text NOT NULL DEFAULT 'reserved',
  created_at timestamptz DEFAULT now()
);
CREATE TABLE IF NOT EXISTS inventory_svc.outbox(
  id bigserial PRIMARY KEY,
  aggregate_id uuid NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  published boolean DEFAULT false,
  created_at timestamptz DEFAULT now(),
  published_at timestamptz
);
CREATE TABLE IF NOT EXISTS inventory_svc.processed_events(
  event_id uuid NOT NULL,
  consumer_name text NOT NULL,
  processed_at timestamptz DEFAULT now(),
  PRIMARY KEY (event_id, consumer_name)
);

CREATE TABLE IF NOT EXISTS payment_svc.payments(
  id uuid PRIMARY KEY,
  order_id uuid NOT NULL,
  amount numeric NOT NULL DEFAULT 0,
  status text NOT NULL DEFAULT 'authorized',
  created_at timestamptz DEFAULT now()
);
CREATE TABLE IF NOT EXISTS payment_svc.outbox(
  id bigserial PRIMARY KEY,
  aggregate_id uuid NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  published boolean DEFAULT false,
  created_at timestamptz DEFAULT now(),
  published_at timestamptz
);
CREATE TABLE IF NOT EXISTS payment_svc.processed_events(
  event_id uuid NOT NULL,
  consumer_name text NOT NULL,
  processed_at timestamptz DEFAULT now(),
  PRIMARY KEY (event_id, consumer_name)
);

CREATE TABLE IF NOT EXISTS risk_svc.risk_checks(
  id uuid PRIMARY KEY,
  order_id uuid NOT NULL,
  verdict text NOT NULL DEFAULT 'approved',
  created_at timestamptz DEFAULT now()
);
CREATE TABLE IF NOT EXISTS risk_svc.outbox(
  id bigserial PRIMARY KEY,
  aggregate_id uuid NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  published boolean DEFAULT false,
  created_at timestamptz DEFAULT now(),
  published_at timestamptz
);

CREATE TABLE IF NOT EXISTS saga_svc.sagas(
  id uuid PRIMARY KEY,
  transaction_id uuid UNIQUE NOT NULL,
  order_id uuid NOT NULL,
  status text NOT NULL DEFAULT 'RUNNING',
  current_step text NOT NULL DEFAULT 'reserve',
  created_at timestamptz DEFAULT now(),
  updated_at timestamptz DEFAULT now()
);
CREATE TABLE IF NOT EXISTS saga_svc.saga_steps(
  id bigserial PRIMARY KEY,
  saga_id uuid NOT NULL REFERENCES saga_svc.sagas(id),
  step_name text NOT NULL,
  step_order int NOT NULL,
  status text NOT NULL DEFAULT 'RUNNING',
  attempt int NOT NULL DEFAULT 1,
  started_at timestamptz DEFAULT now(),
  finished_at timestamptz,
  error text
);
CREATE TABLE IF NOT EXISTS saga_svc.outbox(
  id bigserial PRIMARY KEY,
  aggregate_id uuid NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL,
  published boolean DEFAULT false,
  created_at timestamptz DEFAULT now(),
  published_at timestamptz
);

CREATE TABLE IF NOT EXISTS ai_svc.incidents(
  id uuid PRIMARY KEY,
  scenario text NOT NULL,
  transaction_id uuid,
  status text NOT NULL DEFAULT 'open',
  started_at timestamptz DEFAULT now(),
  resolved_at timestamptz
);
CREATE TABLE IF NOT EXISTS ai_svc.incident_evidence(
  id bigserial PRIMARY KEY,
  incident_id uuid NOT NULL REFERENCES ai_svc.incidents(id),
  kind text NOT NULL,
  data jsonb NOT NULL,
  captured_at timestamptz DEFAULT now()
);
CREATE TABLE IF NOT EXISTS ai_svc.knowledge_chunks(
  id bigserial PRIMARY KEY,
  source text NOT NULL,
  chunk_text text NOT NULL,
  embedding vector(1536),
  created_at timestamptz DEFAULT now()
);
CREATE INDEX IF NOT EXISTS knowledge_chunks_hnsw
  ON ai_svc.knowledge_chunks USING hnsw (embedding vector_cosine_ops);
CREATE TABLE IF NOT EXISTS ai_svc.generated_reports(
  id uuid PRIMARY KEY,
  incident_id uuid NOT NULL REFERENCES ai_svc.incidents(id),
  markdown text NOT NULL,
  model text NOT NULL,
  prompt_version text NOT NULL DEFAULT 'v1',
  evidence_bundle_id text,
  prompt_tokens int DEFAULT 0,
  completion_tokens int DEFAULT 0,
  cost_usd numeric DEFAULT 0,
  cached boolean DEFAULT false,
  generated_at timestamptz DEFAULT now()
);
