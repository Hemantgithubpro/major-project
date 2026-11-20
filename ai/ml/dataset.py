"""Generate a reproducible, time-ordered telemetry dataset for the ML pipeline.

The data is synthetic by design (the project is a testbed), but the features and the
pre-failure labelling follow the real control loop: rows in the 10 minutes before an
injected database bottleneck are labelled 1, healthy rows are 0, and ambiguous rows
during/just after the incident are discarded.
"""

import numpy as np
import pandas as pd

from features import FEATURES, TARGET

INTERVAL_S = 10
N_ROWS = 6000
SEED = 42
PRE_ROWS = 60  # 10 minutes at 10s resolution
POST_ROWS = 12  # 2 minutes of ambiguous recovery


def build_dataset(n_rows: int = N_ROWS, seed: int = SEED) -> pd.DataFrame:
    rng = np.random.default_rng(seed)
    ts = pd.date_range("2026-11-01", periods=n_rows, freq=f"{INTERVAL_S}s")

    # Base load: smooth sinusoid plus noise, clipped to a sane floor.
    base = 40 + 25 * np.sin(np.arange(n_rows) / 180.0) + rng.normal(0, 5, n_rows)
    base = np.clip(base, 5, None)

    # Incident start indices, spaced far enough that pre-windows do not overlap.
    starts = []
    pos = 300
    while pos < n_rows - 250:
        starts.append(pos)
        pos += int(rng.integers(300, 500))

    phase = np.array(["healthy"] * n_rows, dtype=object)
    for s in starts:
        e = min(n_rows, s + int(rng.integers(90, 180)))
        phase[max(0, s - PRE_ROWS):s] = "pre"
        phase[s:e] = "incident"
        phase[e:min(n_rows, e + POST_ROWS)] = "post"

    requests = base + rng.normal(0, 3, n_rows)
    p95 = np.zeros(n_rows)
    err = np.zeros(n_rows)
    conn = np.zeros(n_rows)
    waiting = np.zeros(n_rows)
    saga = np.zeros(n_rows)
    injected = np.zeros(n_rows)

    for name in ("healthy", "pre", "incident"):
        mask = phase == name
        cnt = int(mask.sum())
        if cnt == 0:
            continue
        if name == "healthy":
            p95[mask] = rng.uniform(20, 80, cnt)
            err[mask] = rng.uniform(0.0, 0.015, cnt)
            conn[mask] = rng.uniform(0.10, 0.50, cnt)
            waiting[mask] = rng.integers(0, 2, cnt)
            saga[mask] = rng.uniform(0.0, 0.015, cnt)
        elif name == "pre":
            p95[mask] = rng.uniform(60, 350, cnt)
            err[mask] = rng.uniform(0.005, 0.10, cnt)
            conn[mask] = rng.uniform(0.40, 0.85, cnt)
            waiting[mask] = rng.integers(0, 7, cnt)
            saga[mask] = rng.uniform(0.005, 0.06, cnt)
            injected[mask] = rng.uniform(20, 250, cnt)
        else:
            p95[mask] = rng.uniform(300, 900, cnt)
            err[mask] = rng.uniform(0.10, 0.40, cnt)
            conn[mask] = rng.uniform(0.85, 0.98, cnt)
            waiting[mask] = rng.integers(3, 11, cnt)
            saga[mask] = rng.uniform(0.05, 0.30, cnt)
            injected[mask] = rng.uniform(200, 500, cnt)

    # Real systems are noisy: healthy rows can spike, and pre-failure rows can look
    # deceptively calm. This overlap keeps the classifier honest.
    healthy_idx = np.where(phase == "healthy")[0]
    if len(healthy_idx):
        spike = rng.choice(healthy_idx, size=max(1, int(0.05 * len(healthy_idx))), replace=False)
        p95[spike] = rng.uniform(150, 400, len(spike))
        err[spike] = rng.uniform(0.02, 0.10, len(spike))
        conn[spike] = rng.uniform(0.50, 0.80, len(spike))
        waiting[spike] = rng.integers(1, 5, len(spike))
        saga[spike] = rng.uniform(0.01, 0.06, len(spike))
        injected[spike] = rng.uniform(50, 150, len(spike))

    pre_idx = np.where(phase == "pre")[0]
    if len(pre_idx):
        quiet = rng.choice(pre_idx, size=max(1, int(0.12 * len(pre_idx))), replace=False)
        p95[quiet] = rng.uniform(30, 80, len(quiet))
        err[quiet] = rng.uniform(0.0, 0.02, len(quiet))
        conn[quiet] = rng.uniform(0.20, 0.45, len(quiet))
        waiting[quiet] = rng.integers(0, 2, len(quiet))
        saga[quiet] = rng.uniform(0.0, 0.02, len(quiet))
        injected[quiet] = rng.uniform(0, 50, len(quiet))

    # 5-minute slope of p95, expressed per minute (10s interval -> x6).
    slope = np.gradient(p95) * 6.0

    # Minutes until the incident starts, for rows in the pre-failure window.
    minutes_to_failure = np.full(n_rows, np.nan)
    for s in starts:
        lo = max(0, s - PRE_ROWS)
        minutes_to_failure[lo:s] = (s - np.arange(lo, s)) * (INTERVAL_S / 60.0)

    df = pd.DataFrame({
        "ts": ts,
        "requests_per_min": requests,
        "p95_latency_ms": p95,
        "error_rate": err,
        "db_conn_pct": conn,
        "db_pool_waiting": waiting,
        "saga_failure_rate": saga,
        "db_latency_injected_ms": injected,
        "risk_slope_5m": slope,
        "minutes_to_failure": minutes_to_failure,
        "phase": phase,
        TARGET: (phase == "pre").astype(int),
    })

    # Forecast targets: load 5 and 15 minutes ahead.
    df["requests_t5"] = df["requests_per_min"].shift(-30)
    df["requests_t15"] = df["requests_per_min"].shift(-90)

    # Keep only healthy and pre-incident rows; drop ambiguous incident/recovery rows.
    df = df[df["phase"].isin(["healthy", "pre"])].copy()
    df = df.dropna(subset=["requests_t5", "requests_t15"]).reset_index(drop=True)
    return df


def main():
    df = build_dataset()
    print(f"rows={len(df)} positives={int(df[TARGET].sum())} "
          f"({df[TARGET].mean() * 100:.1f}%)")
    print(df[FEATURES].describe().round(3).to_string())


if __name__ == "__main__":
    main()
