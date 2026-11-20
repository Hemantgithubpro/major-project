"""Shared feature contract for the ML service and the training pipeline."""

FEATURES = [
    "requests_per_min",
    "p95_latency_ms",
    "error_rate",
    "db_conn_pct",
    "db_pool_waiting",
    "saga_failure_rate",
    "db_latency_injected_ms",
    "risk_slope_5m",
]

TARGET = "failure_within_10m"
HORIZON_MINUTES = 10
MODEL_VERSION = "1.0.0"
