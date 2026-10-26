#!/usr/bin/env python3
import argparse, concurrent.futures, time, urllib.request, urllib.error

def worker(url, end_time, interval):
    latencies, errors, total = [], 0, 0
    next_time = time.time()
    while time.time() < end_time:
        now = time.time()
        if now < next_time:
            time.sleep(max(0.0, next_time - now))
        next_time += interval
        total += 1
        start = time.time()
        try:
            req = urllib.request.Request(url, data=b"{}", headers={"Content-Type": "application/json"}, method="POST")
            with urllib.request.urlopen(req, timeout=5) as resp:
                resp.read()
                if resp.status != 200:
                    errors += 1
                latencies.append((time.time() - start) * 1000)
        except Exception:
            errors += 1
    return total, errors, latencies

def run_side(name, url, rps, duration):
    end_time = time.time() + duration
    threads = min(max(1, rps), 20)
    per_interval = 1.0 / (float(rps) / threads)
    totals, errors, all_lats = 0, 0, []
    with concurrent.futures.ThreadPoolExecutor(max_workers=threads) as ex:
        futs = [ex.submit(worker, url, end_time, per_interval) for _ in range(threads)]
        for f in concurrent.futures.as_completed(futs):
            t, e, l = f.result()
            totals += t
            errors += e
            all_lats.extend(l)
    all_lats.sort()
    p50 = all_lats[int(len(all_lats) * 0.5)] if all_lats else 0
    p99 = all_lats[int(len(all_lats) * 0.99)] if all_lats else 0
    print(f"[{name}] Total: {totals}, Errors: {errors}, p50: {p50:.2f}ms, p99: {p99:.2f}ms")

def main():
    p = argparse.ArgumentParser(description="Smoke traffic generator")
    p.add_argument("--rps", type=int, default=20, help="Target RPS per side")
    p.add_argument("--duration", type=int, default=60, help="Duration in seconds")
    p.add_argument("--url-baseline", default="http://localhost:8081/", help="Baseline URL")
    p.add_argument("--url-predictive", default="http://localhost:8082/", help="Predictive URL")
    a = p.parse_args()
    print(f"Running smoke traffic at {a.rps} RPS per side for {a.duration}s...")
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as ex:
        f1 = ex.submit(run_side, "baseline", a.url_baseline, a.rps, a.duration)
        f2 = ex.submit(run_side, "predictive", a.url_predictive, a.rps, a.duration)
        f1.result()
        f2.result()

if __name__ == "__main__":
    main()
