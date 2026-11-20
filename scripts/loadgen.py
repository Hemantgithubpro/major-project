#!/usr/bin/env python3
"""Small load generator for the order gateway.

Places orders at a target rate and reports latency percentiles and error counts.
No third-party dependencies.

Usage:
  python3 scripts/loadgen.py --rps 5 --duration 60
  python3 scripts/loadgen.py --rps 10 --duration 120 --url http://localhost:8080
"""

import argparse
import json
import time
import urllib.error
import urllib.request
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait


def one_order(base_url: str, amount: float, customer: str, timeout: float):
    payload = json.dumps({"customer_ref": customer, "amount": amount}).encode()
    req = urllib.request.Request(
        base_url.rstrip("/") + "/api/orders",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            resp.read()
            return time.perf_counter() - start, resp.status
    except urllib.error.HTTPError as exc:
        return time.perf_counter() - start, exc.code
    except Exception:
        return time.perf_counter() - start, 0


def percentile(values, pct):
    if not values:
        return 0.0
    ordered = sorted(values)
    idx = min(len(ordered) - 1, int(round((pct / 100.0) * (len(ordered) - 1))))
    return ordered[idx]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://localhost:8080")
    parser.add_argument("--rps", type=float, default=5.0)
    parser.add_argument("--duration", type=float, default=60.0)
    parser.add_argument("--workers", type=int, default=8)
    parser.add_argument("--amount", type=float, default=99.5)
    parser.add_argument("--customer", default="loadgen")
    parser.add_argument("--timeout", type=float, default=10.0)
    args = parser.parse_args()

    if args.rps <= 0:
        raise SystemExit("--rps must be > 0")
    if args.duration <= 0:
        raise SystemExit("--duration must be > 0")

    start = time.time()
    next_at = start
    submitted = 0
    results = []
    pending = set()
    last_report = start

    with ThreadPoolExecutor(max_workers=max(1, args.workers)) as pool:
        while time.time() - start < args.duration:
            now = time.time()
            if now < next_at:
                time.sleep(min(next_at - now, 0.05))
                continue

            while len(pending) >= args.workers * 2:
                done, pending = wait(pending, return_when=FIRST_COMPLETED)
                results.extend(f.result() for f in done)

            pending.add(pool.submit(one_order, args.url, args.amount, args.customer, args.timeout))
            submitted += 1
            next_at += 1.0 / args.rps

            if now - last_report >= 5:
                last_report = now
                done = [r for r in results if r[1] == 200]
                print(f"[loadgen] t={now - start:5.1f}s submitted={submitted} "
                      f"completed={len(results)} ok={len(done)}", flush=True)

        while pending:
            done, pending = wait(pending, return_when=FIRST_COMPLETED)
            results.extend(f.result() for f in done)

    elapsed = time.time() - start
    latencies = [r[0] for r in results]
    ok = sum(1 for r in results if r[1] == 200)
    errors = len(results) - ok
    print("\n[loadgen] summary")
    print(f"  duration_s   : {elapsed:.2f}")
    print(f"  submitted    : {submitted}")
    print(f"  completed    : {len(results)}")
    print(f"  ok           : {ok}")
    print(f"  errors       : {errors}")
    print(f"  actual_rps   : {len(results) / elapsed:.2f}")
    print(f"  p50_ms       : {percentile(latencies, 50) * 1000:.1f}")
    print(f"  p95_ms       : {percentile(latencies, 95) * 1000:.1f}")
    print(f"  p99_ms       : {percentile(latencies, 99) * 1000:.1f}")


if __name__ == "__main__":
    main()
