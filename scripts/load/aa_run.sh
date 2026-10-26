#!/usr/bin/env bash
# scripts/load/aa_run.sh – Symmetric A/A load test replay on baseline & predictive sides.
# Arguments: RUN_ID (e.g. R1, R2, R3) and NO_REUSE (0 or 1).
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

RUN_ID="${1:-R1}"
NO_REUSE="${2:-0}"

KC="kubectl --context kind-prescale-demo"
K6_IMG=$(cat loadtest/K6_IMAGE | tr -d ' \n')
OUT_DIR="docs/reports/data/part5b"
mkdir -p "$OUT_DIR"
chmod 777 "$OUT_DIR"

LOG="${OUT_DIR}/aa_${RUN_ID}_run.log"
SUMMARY_TXT="${OUT_DIR}/aa_${RUN_ID}_summary.txt"
CSV_ALL="${OUT_DIR}/aa_summary.csv"

: > "$LOG"
: > "$SUMMARY_TXT"

log() {
    local msg="[$(date +'%Y-%m-%dT%H:%M:%S%z')] $*"
    echo "$msg" | tee -a "$LOG"
}

log "=== Starting A/A Run ${RUN_ID} (NO_REUSE=${NO_REUSE}) ==="

# 1. Load SLO parameters
if [ -f loadtest/slo.env ]; then
    source loadtest/slo.env
else
    SLO_P99_MS=300
    SLO_ERR_PCT=1
fi
log "SLO: SLO_P99_MS=${SLO_P99_MS}, SLO_ERR_PCT=${SLO_ERR_PCT}"

# Verify sysctl values
tcp_tw_reuse=$(sysctl -n net.ipv4.tcp_tw_reuse 2>/dev/null || echo "unknown")
ip_local_port_range=$(sysctl -n net.ipv4.ip_local_port_range 2>/dev/null || echo "unknown")
log "Host sysctls: net.ipv4.tcp_tw_reuse=${tcp_tw_reuse}, net.ipv4.ip_local_port_range=${ip_local_port_range}"

# 2. Pre-check both sides: SO min 3, HPA desired 3, 3/3 Ready, stable for 60 s (timeout 600 s)
log "Pre-checking both sides for 60s stability at 3 ready pods (timeout 600s)..."
consecutive_settled_seconds=0
check_deadline=$(($(date +%s) + 600))
settled=false

while [ "$(date +%s)" -lt "$check_deadline" ]; do
    b_min=$($KC get scaledobject checkout -n baseline -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo 0)
    p_min=$($KC get scaledobject checkout -n predictive -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo 0)
    b_hpa=$($KC get hpa keda-hpa-checkout -n baseline -o jsonpath='{.status.desiredReplicas}' 2>/dev/null || echo 0)
    p_hpa=$($KC get hpa keda-hpa-checkout -n predictive -o jsonpath='{.status.desiredReplicas}' 2>/dev/null || echo 0)
    b_ready=$($KC get deploy checkout -n baseline -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
    p_ready=$($KC get deploy checkout -n predictive -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)

    if [ "$b_min" = "3" ] && [ "$p_min" = "3" ] && \
       [ "$b_hpa" = "3" ] && [ "$p_hpa" = "3" ] && \
       [ "$b_ready" = "3" ] && [ "$p_ready" = "3" ]; then
        consecutive_settled_seconds=$((consecutive_settled_seconds + 5))
        if [ "$consecutive_settled_seconds" -ge 60 ]; then
            log "Pre-check passed: both sides stable for 60s at min=3, desired=3, ready=3"
            settled=true
            break
        fi
    else
        consecutive_settled_seconds=0
    fi
    sleep 5
done

if [ "$settled" != "true" ]; then
    log "ABORTED: Pre-check timed out after 600s without 60s stability."
    echo "STATUS: ABORTED (pre-check timeout 600s)" > "$SUMMARY_TXT"
    exit 1
fi

# Clean up any old containers with same name
docker rm -f k6-aa-baseline k6-aa-predictive 2>/dev/null || true

# 3. Launch both k6 containers CONCURRENTLY
T_LAUNCH=$(date +%s)
SURGE_START=$((T_LAUNCH + 60))
log "T_LAUNCH: ${T_LAUNCH} (SURGE_START: ${SURGE_START} / T_LAUNCH + 60s)"

json_base="${OUT_DIR}/summary_baseline_${RUN_ID}.json"
json_pred="${OUT_DIR}/summary_predictive_${RUN_ID}.json"
k6_log_base="${OUT_DIR}/k6_baseline_${RUN_ID}.log"
k6_log_pred="${OUT_DIR}/k6_predictive_${RUN_ID}.log"

rm -f "$json_base" "$json_pred" "$k6_log_base" "$k6_log_pred"

docker run --name "k6-aa-baseline" --memory 1g --user "$(id -u):$(id -g)" --network host \
    -v "$PWD/loadtest:/loadtest:ro,z" \
    -v "$PWD/${OUT_DIR}:/out:z" \
    "$K6_IMG" run \
    -e TARGET_URL="http://localhost:8081" \
    -e BASE_RPS=60 \
    -e PEAK_RPS=490 \
    -e NO_REUSE="$NO_REUSE" \
    --summary-export "/out/summary_baseline_${RUN_ID}.json" \
    /loadtest/surge_demo.js > "$k6_log_base" 2>&1 &
k6_base_pid=$!

docker run --name "k6-aa-predictive" --memory 1g --user "$(id -u):$(id -g)" --network host \
    -v "$PWD/loadtest:/loadtest:ro,z" \
    -v "$PWD/${OUT_DIR}:/out:z" \
    "$K6_IMG" run \
    -e TARGET_URL="http://localhost:8082" \
    -e BASE_RPS=60 \
    -e PEAK_RPS=490 \
    -e NO_REUSE="$NO_REUSE" \
    --summary-export "/out/summary_predictive_${RUN_ID}.json" \
    /loadtest/surge_demo.js > "$k6_log_pred" 2>&1 &
k6_pred_pid=$!

log "Launched k6-aa-baseline (pid $k6_base_pid) and k6-aa-predictive (pid $k6_pred_pid)"

# 4. Watchdog every 5s: loadavg, MemAvailable (< 800 MiB -> kill ONLY those two containers)
max_loadavg1=0.0
min_mem_avail_mib=999999.0
aborted=false

while kill -0 $k6_base_pid 2>/dev/null || kill -0 $k6_pred_pid 2>/dev/null; do
    l1=$(awk '{print $1}' /proc/loadavg)
    mem_avail_kb=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
    mem_mib=$(python3 -c "print(f'{$mem_avail_kb / 1024.0:.1f}')")

    max_loadavg1=$(python3 -c "print(max(float('$max_loadavg1'), float('$l1')))")
    min_mem_avail_mib=$(python3 -c "print(min(float('$min_mem_avail_mib'), float('$mem_mib')))")

    if [ "$mem_avail_kb" -lt 819200 ]; then
        log "CRITICAL WATCHDOG: MemAvailable < 800 MiB (${mem_mib} MiB). Aborting!"
        docker kill k6-aa-baseline k6-aa-predictive 2>/dev/null || true
        aborted=true
        break
    fi
    sleep 5
done

wait $k6_base_pid 2>/dev/null || true
wait $k6_pred_pid 2>/dev/null || true
T_FINISH=$(date +%s)
log "Both k6 containers exited. T_FINISH: ${T_FINISH} (run duration: $((T_FINISH - T_LAUNCH))s)"

if [ "$aborted" = "true" ]; then
    log "ABORTED: Memory threshold breached."
    echo "STATUS: ABORTED (MemAvailable < 800 MiB)" > "$SUMMARY_TXT"
    docker rm -f k6-aa-baseline k6-aa-predictive 2>/dev/null || true
    exit 1
fi

# Clean up docker containers
docker rm -f k6-aa-baseline k6-aa-predictive 2>/dev/null || true

# Check TIME-WAIT count at end
timewait_count=$(ss -s | grep -i timewait | awk -F'timewait ' '{print $2}' | awk '{print $1}' | tr -d ')' || echo "0")
log "ss -s TIME-WAIT count at end: ${timewait_count}"

# Wait 10s for VictoriaMetrics ingestion lag
log "Waiting 10s for VictoriaMetrics ingestion lag..."
sleep 10

# 5. Query VictoriaMetrics and parse results
python3 - << EOF | tee -a "$SUMMARY_TXT"
import json
import urllib.parse
import urllib.request
import csv
import os

t_launch = int("$T_LAUNCH")
t_finish = int("$T_FINISH")
surge_start = int("$SURGE_START")
run_id = "$RUN_ID"
no_reuse = "$NO_REUSE"
out_dir = "$OUT_DIR"
slo_p99_ms = float("$SLO_P99_MS")
max_load1 = float("$max_loadavg1")
min_mem = float("$min_mem_avail_mib")
tw_count = "$timewait_count"
tcp_tw_reuse = "$tcp_tw_reuse"
ip_local_port_range = "$ip_local_port_range"

sides = ["baseline", "predictive"]

def vm_query(query, time_sec=None):
    params = {"query": query}
    if time_sec:
        params["time"] = str(time_sec)
    url = f"http://localhost:8428/api/v1/query?{urllib.parse.urlencode(params)}"
    try:
        with urllib.request.urlopen(url, timeout=10) as resp:
            return json.loads(resp.read().decode())
    except Exception as e:
        return {"status": "error", "error": str(e)}

def vm_query_range(query, start_sec, end_sec, step="5s"):
    params = {
        "query": query,
        "start": str(start_sec),
        "end": str(end_sec),
        "step": step
    }
    url = f"http://localhost:8428/api/v1/query_range?{urllib.parse.urlencode(params)}"
    try:
        with urllib.request.urlopen(url, timeout=10) as resp:
            return json.loads(resp.read().decode())
    except Exception as e:
        return {"status": "error", "error": str(e)}

print(f"=== A/A Run {run_id} Results (NO_REUSE={no_reuse}) ===")
print(f"Bucket boundaries: [0.005, 0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5, 10, +Inf]")
print(f"No bucket is exactly 0.3 s; smallest bucket >= 0.3 s is 0.32 s.")

csv_all_path = os.path.join(out_dir, "aa_summary.csv")
write_header = not os.path.exists(csv_all_path)

all_rows = []

for side in sides:
    k6_json_file = os.path.join(out_dir, f"summary_{side}_{run_id}.json")
    k6_log_file = os.path.join(out_dir, f"k6_{side}_{run_id}.log")

    # Parse k6 summary
    http_reqs = 0
    achieved_rps = 0.0
    failed_pct = 0.0
    dropped_iters = 0
    client_p95 = 0.0
    client_p99 = 0.0

    if os.path.exists(k6_json_file):
        with open(k6_json_file) as f:
            d = json.load(f)
        m = d.get("metrics", {})
        reqs = m.get("http_reqs", {})
        http_reqs = reqs.get("values", {}).get("count", reqs.get("count", 0))
        achieved_rps = reqs.get("values", {}).get("rate", reqs.get("rate", 0.0))

        dropped_obj = m.get("dropped_iterations", {})
        dropped_iters = dropped_obj.get("values", {}).get("count", dropped_obj.get("count", 0))

        dur = m.get("http_req_duration", {})
        dur_vals = dur.get("values", dur)
        client_p95 = dur_vals.get("p(95)", 0.0)
        client_p99 = dur_vals.get("p(99)", dur_vals.get("max", 0.0))

        failed = m.get("http_req_failed", {})
        fail_rate = failed.get("values", {}).get("rate", failed.get("value", failed.get("rate", 0.0)))
        failed_pct = fail_rate * 100.0

    # Parse connect errors in k6 log
    err_addr = 0
    err_refused = 0
    err_timeout = 0
    if os.path.exists(k6_log_file):
        with open(k6_log_file) as f:
            for line in f:
                ll = line.lower()
                if "cannot assign requested address" in ll:
                    err_addr += 1
                if "connection refused" in ll:
                    err_refused += 1
                if "i/o timeout" in ll:
                    err_timeout += 1

    # Query range from VictoriaMetrics (step 5s)
    # 1. Ready replicas
    q_ready = f'kube_deployment_status_replicas_ready{{namespace="{side}",deployment="checkout"}}'
    r_ready = vm_query_range(q_ready, t_launch, t_finish, "5s")
    ready_series = {}
    for res in r_ready.get("data", {}).get("result", []):
        for ts, val in res.get("values", []):
            ready_series[int(ts)] = float(val)

    # 2. Total RPS
    q_rps = f'sum(rate(http_requests_total{{namespace="{side}",app="checkout"}}[20s]))'
    r_rps = vm_query_range(q_rps, t_launch, t_finish, "5s")
    rps_series = {}
    for res in r_rps.get("data", {}).get("result", []):
        for ts, val in res.get("values", []):
            rps_series[int(ts)] = float(val)

    # 3. Server-side p99 (histogram_quantile in ms)
    q_p99 = f'histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket{{namespace="{side}",app="checkout"}}[20s]))) * 1000'
    r_p99 = vm_query_range(q_p99, t_launch, t_finish, "5s")
    p99_series = {}
    for res in r_p99.get("data", {}).get("result", []):
        for ts, val in res.get("values", []):
            p99_series[int(ts)] = float(val) if val != "NaN" else 0.0

    # 4. Non-2xx rps
    q_non2xx = f'sum(rate(http_requests_total{{namespace="{side}",app="checkout",code!~"2.."}}[20s])) or vector(0)'
    r_non2xx = vm_query_range(q_non2xx, t_launch, t_finish, "5s")
    non2xx_series = {}
    for res in r_non2xx.get("data", {}).get("result", []):
        for ts, val in res.get("values", []):
            non2xx_series[int(ts)] = float(val)

    # 5. Write timeseries CSV
    ts_csv = os.path.join(out_dir, f"aa_{run_id}_{side}_timeseries.csv")
    all_timestamps = sorted(list(set(list(ready_series.keys()) + list(rps_series.keys()) + list(p99_series.keys()))))
    with open(ts_csv, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["timestamp", "rel_sec", "ready_replicas", "rps", "p99_ms", "non_2xx_rps"])
        for ts in all_timestamps:
            rel = ts - t_launch
            writer.writerow([
                ts,
                rel,
                ready_series.get(ts, 0.0),
                f"{rps_series.get(ts, 0.0):.2f}",
                f"{p99_series.get(ts, 0.0):.2f}",
                f"{non2xx_series.get(ts, 0.0):.2f}"
            ])

    # Derive server-side metrics
    breach_steps = sum(1 for val in p99_series.values() if val > slo_p99_ms)
    breach_seconds = breach_steps * 5
    peak_p99 = max(p99_series.values()) if p99_series else 0.0

    # Total non-2xx requests across window
    total_non2xx_q = vm_query(f'sum(increase(http_requests_total{{namespace="{side}",app="checkout",code!~"2.."}}[{t_finish - t_launch}s]))', t_finish)
    total_non2xx = 0
    try:
        total_non2xx = int(float(total_non2xx_q.get("data", {}).get("result", [])[0]["value"][1]))
    except:
        total_non2xx = 0

    # Ready replicas progression:
    # seconds from SURGE_START to first ready > 3
    # seconds from SURGE_START to max ready
    # max ready
    max_ready = int(max(ready_series.values())) if ready_series else 3
    sec_to_first_scale = "none"
    sec_to_max_ready = "none"

    sorted_ready = sorted(ready_series.items(), key=lambda x: x[0])
    for ts, r_val in sorted_ready:
        if ts >= surge_start and r_val > 3.0:
            sec_to_first_scale = str(ts - surge_start)
            break

    for ts, r_val in sorted_ready:
        if ts >= surge_start and int(r_val) == max_ready and max_ready > 3:
            sec_to_max_ready = str(ts - surge_start)
            break

    # pod_seconds = 5s * sum of ready replicas over the run window
    pod_seconds = int(sum(ready_series.values()) * 5)

    # Per-pod RPS min/max among ready pods at SURGE_START + 110s and + 140s
    def get_pod_rps_bounds(target_time):
        q = f'sum by (pod)(rate(http_requests_total{{namespace="{side}",app="checkout"}}[20s]))'
        res = vm_query(q, target_time)
        results = res.get("data", {}).get("result", [])
        vals = [float(r["value"][1]) for r in results if float(r["value"][1]) > 0.0]
        if vals:
            return min(vals), max(vals), (max(vals) / min(vals)) if min(vals) > 0 else 0.0
        return 0.0, 0.0, 0.0

    p_min_110, p_max_110, p_ratio_110 = get_pod_rps_bounds(surge_start + 110)
    p_min_140, p_max_140, p_ratio_140 = get_pod_rps_bounds(surge_start + 140)

    # Fakedb peak connections
    fakedb_q = vm_query(f'max_over_time(db_active_connections{{namespace="{side}",app="fakedb"}}[{t_finish - t_launch}s])', t_finish)
    fakedb_conns = 0
    try:
        fakedb_conns = int(float(fakedb_q.get("data", {}).get("result", [])[0]["value"][1]))
    except:
        fakedb_conns = 0

    print(f"\n--- Side: {side} ---")
    print(f"k6 http_reqs: {http_reqs}, achieved avg rps: {achieved_rps:.2f}, failed %: {failed_pct:.2f}%, dropped_iters: {dropped_iters}")
    print(f"client p95: {client_p95:.2f} ms, client p99: {client_p99:.2f} ms")
    print(f"server breach_seconds: {breach_seconds} s (p99 > {slo_p99_ms} ms), peak p99: {peak_p99:.2f} ms, total non-2xx: {total_non2xx}")
    print(f"ready replicas: sec_to_first_scale={sec_to_first_scale}s, sec_to_max_ready={sec_to_max_ready}s, max_ready={max_ready}")
    print(f"pod_seconds: {pod_seconds} pod*s")
    print(f"per-pod rps at +110s: min={p_min_110:.2f}, max={p_max_110:.2f}, ratio={p_ratio_110:.2f}")
    print(f"per-pod rps at +140s: min={p_min_140:.2f}, max={p_max_140:.2f}, ratio={p_ratio_140:.2f}")
    print(f"fakedb peak connections: {fakedb_conns}")
    print(f"connect errors: cannot_assign={err_addr}, conn_refused={err_refused}, io_timeout={err_timeout}")
    print(f"max loadavg1: {max_load1:.2f}, min MemAvailable: {min_mem:.1f} MiB")
    print(f"ss -s TIME-WAIT count: {tw_count}")

    row = [
        run_id, side, no_reuse, http_reqs, f"{achieved_rps:.2f}", f"{failed_pct:.2f}", dropped_iters,
        f"{client_p95:.2f}", f"{client_p99:.2f}", breach_seconds, f"{peak_p99:.2f}", total_non2xx,
        sec_to_first_scale, sec_to_max_ready, max_ready, pod_seconds, fakedb_conns,
        f"{p_min_110:.2f}", f"{p_max_110:.2f}", f"{p_ratio_110:.2f}",
        f"{p_min_140:.2f}", f"{p_max_140:.2f}", f"{p_ratio_140:.2f}",
        f"{max_load1:.2f}", f"{min_mem:.1f}", err_addr, err_refused, err_timeout, tw_count
    ]
    all_rows.append(row)

# Append to aa_summary.csv
with open(csv_all_path, "a", newline="") as f:
    writer = csv.writer(f)
    if write_header:
        writer.writerow([
            "run_id", "side", "no_reuse", "http_reqs", "achieved_rps", "fail_pct", "dropped_iters",
            "client_p95_ms", "client_p99_ms", "breach_seconds", "peak_p99_ms", "total_non_2xx",
            "sec_to_first_scale", "sec_to_max_ready", "max_ready", "pod_seconds", "fakedb_peak_conns",
            "spread_110s_min", "spread_110s_max", "spread_110s_ratio",
            "spread_140s_min", "spread_140s_max", "spread_140s_ratio",
            "max_loadavg1", "min_mem_avail_mib", "err_addr", "err_refused", "err_timeout", "timewait_count"
        ])
    for row in all_rows:
        writer.writerow(row)

EOF

log "A/A Run ${RUN_ID} complete."
