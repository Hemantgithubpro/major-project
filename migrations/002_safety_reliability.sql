-- Safety shield catalog and reliability decision ledger.
-- Applied to a running database with:
--   docker compose exec -T postgres psql -U postgres -d mvp < migrations/002_safety_reliability.sql

CREATE SCHEMA IF NOT EXISTS safety_svc;
CREATE SCHEMA IF NOT EXISTS reliability_svc;

CREATE TABLE IF NOT EXISTS safety_svc.invariants(
  id text PRIMARY KEY,
  version int NOT NULL DEFAULT 1,
  family text NOT NULL,
  description text NOT NULL,
  predicate text NOT NULL,
  enforcement text NOT NULL,
  active boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reliability_svc.decisions(
  decision_uid uuid PRIMARY KEY,
  service text NOT NULL,
  policy_action text NOT NULL,
  shield_action text NOT NULL,
  verdict text NOT NULL,
  blocked_by text,
  reason text NOT NULL,
  score double precision NOT NULL DEFAULT 0,
  state_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
  prediction jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS decisions_created_at_idx
  ON reliability_svc.decisions(created_at DESC);

CREATE TABLE IF NOT EXISTS reliability_svc.actions(
  id bigserial PRIMARY KEY,
  decision_uid uuid NOT NULL REFERENCES reliability_svc.decisions(decision_uid),
  action text NOT NULL,
  status text NOT NULL,
  executed_at timestamptz NOT NULL DEFAULT now(),
  result jsonb,
  error text
);
CREATE INDEX IF NOT EXISTS actions_decision_uid_idx
  ON reliability_svc.actions(decision_uid);

CREATE TABLE IF NOT EXISTS reliability_svc.verifications(
  id bigserial PRIMARY KEY,
  decision_uid uuid NOT NULL REFERENCES reliability_svc.decisions(decision_uid),
  metrics_before jsonb NOT NULL DEFAULT '{}'::jsonb,
  metrics_after jsonb,
  improved boolean,
  invariants_ok boolean,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS verifications_decision_uid_idx
  ON reliability_svc.verifications(decision_uid);

CREATE TABLE IF NOT EXISTS reliability_svc.metrics_snapshots(
  id bigserial PRIMARY KEY,
  service text NOT NULL,
  captured_at timestamptz NOT NULL DEFAULT now(),
  features jsonb NOT NULL DEFAULT '{}'::jsonb,
  prediction jsonb NOT NULL DEFAULT '{}'::jsonb,
  raw jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS metrics_snapshots_captured_at_idx
  ON reliability_svc.metrics_snapshots(captured_at DESC);
