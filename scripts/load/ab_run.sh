#!/usr/bin/env bash
# scripts/load/ab_run.sh – A/B load test: baseline (HPA only) vs predictive (HPA + prescaler)
# Arguments: RUN_ID (e.g. B1, B2). Uses NO_REUSE=1, PEAK_RPS=490.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

RUN_ID="${1:-B1}"
NO_REUSE=1
PEAK_RPS=490
BASE_RPS=60

KC="kubectl --context kind-prescale-demo"
K6_IMG=$(cat loadtest/K6_IMAGE | tr -d ' \n')
OUT_DIR="docs/reports/data/part7"
mkdir -p "$OUT_DIR"
chmod 777 "$OUT_DIR"

LOG="${OUT_DIR}/ab_${RUN_ID}_run.log"
SUMMARY_TXT="${OUT_DIR}/ab_${RUN_ID}_summary.txt"
CSV_ALL="${OUT_DIR}/ab_summary.csv"

: > "$LOG"
: > "$SUMMARY_TXT"

log() {
    local msg="[$(date +'%Y-%m-%dT%H:%M:%S%z')] $*"
    echo "$msg" | tee -a "$LOG"
}

log "=== Starting A/B Run ${RUN_ID} (NO_REUSE=${NO_REUSE}, PEAK_RPS=${PEAK_RPS}) ==="

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

# 2. Pre-check both sides: SO min 3, 3/3 Ready pods (timeout 600s)
log "Pre-checking both sides for 3 ready pods and SO min 3 (max wait 600s)..."
t_precheck_start=$(date +%s)
check_deadline=$((t_precheck_start + 600))
settled=false

while [ "$(date +%s)" -lt "$check_deadline" ]; do
    b_min=$($KC get scaledobject checkout -n baseline -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo 0)
    p_min=$($KC get scaledobject checkout -n predictive -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo 0)
    b_ready=$($KC get deploy checkout -n baseline -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
    p_ready=$($KC get deploy checkout -n predictive -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)

    if [ "$b_min" = "3" ] && [ "$p_min" = "3" ] && [ "$b_ready" = "3" ] && [ "$p_ready" = "3" ]; then
        settled=true
        break
    fi
    sleep 3
done

t_precheck_end=$(date +%s)
wait_duration=$((t_precheck_end - t_precheck_start))
log "Pre-check wait duration: ${wait_duration}s (settled: ${settled})"

if [ "$settled" != "true" ]; then
    log "WARNING: Pre-check timed out after 600s without full settlement. Proceeding anyway per rules."
fi

# Capture predictive ScaledObject annotations BEFORE prescaler is started
ann_before=$($KC get scaledobject checkout -n predictive -o jsonpath='{.metadata.annotations}' 2>/dev/null || echo "{}")
log "Predictive ScaledObject annotations BEFORE run: ${ann_before}"

# Clean up any lingering k6 containers
docker rm -f k6-ab-baseline k6-ab-predictive 2>/dev/null || true

# 3. Scale prescaler to 1 and wait for one evaluation interval (15s)
log "Scaling prescaler to 1 in predictive namespace..."
$KC scale deploy prescaler -n predictive --replicas=1
log "Waiting 15s for one prescaler evaluation interval..."
sleep 15

# 4. Schedule surge and launch both k6 containers CONCURRENTLY
T_LAUNCH=$(date +%s)
SURGE_START=$((T_LAUNCH + 60))
log "T_LAUNCH: ${T_LAUNCH} (SURGE_START: ${SURGE_START} / T_LAUNCH + 60s)"

# Set predictor schedule: surge_start_epoch = SURGE_START
sched_resp=$(python3 -c '
import urllib.request, json, sys
url = "http://127.0.0.2:8000/schedule"
payload = json.dumps({"surge_start_epoch": int(sys.argv[1])}).encode("utf-8")
req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})
with urllib.request.urlopen(req, timeout=5) as resp:
    print(resp.read().decode())
' "$SURGE_START")
log "Predictor schedule set: ${sched_resp}"

json_base="${OUT_DIR}/summary_baseline_${RUN_ID}.json"
json_pred="${OUT_DIR}/summary_predictive_${RUN_ID}.json"
k6_log_base="${OUT_DIR}/k6_baseline_${RUN_ID}.log"
k6_log_pred="${OUT_DIR}/k6_predictive_${RUN_ID}.log"
so_timeline_file="${OUT_DIR}/ab_${RUN_ID}_so_timeline.csv"

rm -f "$json_base" "$json_pred" "$k6_log_base" "$k6_log_pred" "$so_timeline_file"
echo "epoch,rel_launch,min_replicas,ready_replicas,annotations" > "$so_timeline_file"

docker run --name "k6-ab-baseline" --memory 1g --user "$(id -u):$(id -g)" --network host \
    -v "$PWD/loadtest:/loadtest:ro,z" \
    -v "$PWD/${OUT_DIR}:/out:z" \
    "$K6_IMG" run \
    -e TARGET_URL="http://localhost:8081" \
    -e BASE_RPS="$BASE_RPS" \
    -e PEAK_RPS="$PEAK_RPS" \
    -e NO_REUSE="$NO_REUSE" \
    --summary-export "/out/summary_baseline_${RUN_ID}.json" \
    /loadtest/surge_demo.js > "$k6_log_base" 2>&1 &
k6_base_pid=$!

docker run --name "k6-ab-predictive" --memory 1g --user "$(id -u):$(id -g)" --network host \
    -v "$PWD/loadtest:/loadtest:ro,z" \
    -v "$PWD/${OUT_DIR}:/out:z" \
    "$K6_IMG" run \
    -e TARGET_URL="http://localhost:8082" \
    -e BASE_RPS="$BASE_RPS" \
    -e PEAK_RPS="$PEAK_RPS" \
    -e NO_REUSE="$NO_REUSE" \
    --summary-export "/out/summary_predictive_${RUN_ID}.json" \
    /loadtest/surge_demo.js > "$k6_log_pred" 2>&1 &
k6_pred_pid=$!

log "Launched k6-ab-baseline (pid $k6_base_pid) and k6-ab-predictive (pid $k6_pred_pid)"

# 5. Background monitoring loop: poll ScaledObject and watchdog host resources every 2s
max_loadavg1=0.0
min_mem_avail_mib=999999.0
aborted=false

while kill -0 $k6_base_pid 2>/dev/null || kill -0 $k6_pred_pid 2>/dev/null; do
    now_epoch=$(date +%s)
    rel_launch=$((now_epoch - T_LAUNCH))

    # Poll predictive ScaledObject
    so_info=$($KC get scaledobject checkout -n predictive -o jsonpath='{.spec.minReplicaCount}{"|"}{.metadata.annotations}' 2>/dev/null || echo "3|{}")
    cur_min=$(echo "$so_info" | cut -d'|' -f1)
    cur_ann=$(echo "$so_info" | cut -d'|' -f2-)
    p_ready=$($KC get deploy checkout -n predictive -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)

    # Clean json for csv
    clean_ann=$(echo "$cur_ann" | tr ',' ';')
    echo "${now_epoch},${rel_launch},${cur_min},${p_ready},\"${clean_ann}\"" >> "$so_timeline_file"

    # Host watchdog
    l1=$(awk '{print $1}' /proc/loadavg)
    mem_avail_kb=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
    mem_mib=$(python3 -c "print(f'{$mem_avail_kb / 1024.0:.1f}')")

    max_loadavg1=$(python3 -c "print(max(float('$max_loadavg1'), float('$l1')))")
    min_mem_avail_mib=$(python3 -c "print(min(float('$min_mem_avail_mib'), float('$mem_mib')))")

    if [ "$mem_avail_kb" -lt 819200 ]; then
        log "CRITICAL WATCHDOG: MemAvailable < 800 MiB (${mem_mib} MiB). Aborting!"
        docker kill k6-ab-baseline k6-ab-predictive 2>/dev/null || true
        aborted=true
        break
    fi
    sleep 2
done

wait $k6_base_pid 2>/dev/null || true
wait $k6_pred_pid 2>/dev/null || true
T_FINISH=$(date +%s)
log "Both k6 containers exited. T_FINISH: ${T_FINISH} (run duration: $((T_FINISH - T_LAUNCH))s)"

# Capture predictive annotations AFTER k6 finishes
ann_after=$($KC get scaledobject checkout -n predictive -o jsonpath='{.metadata.annotations}' 2>/dev/null || echo "{}")
log "Predictive ScaledObject annotations AFTER run: ${ann_after}"

# Scale prescaler to 0
log "Scaling prescaler to 0 in predictive namespace..."
$KC scale deploy prescaler -n predictive --replicas=0

# Clean up docker containers
docker rm -f k6-ab-baseline k6-ab-predictive 2>/dev/null || true

if [ "$aborted" = "true" ]; then
    log "ABORTED: Memory threshold breached."
    echo "STATUS: ABORTED (MemAvailable < 800 MiB)" > "$SUMMARY_TXT"
    exit 1
fi

# Check TIME-WAIT count at end
timewait_count=$(ss -s | grep -i timewait | awk -F'timewait ' '{print $2}' | awk '{print $1}' | tr -d ')' || echo "0")
log "ss -s TIME-WAIT count at end: ${timewait_count}"

# Wait 10s for VictoriaMetrics ingestion lag
log "Waiting 10s for VictoriaMetrics ingestion lag..."
sleep 10

# 6. Query VictoriaMetrics, timeline and parse results
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
ann_before_raw = """$ann_before"""
ann_after_raw = """$ann_after"""
wait_duration = int("$wait_duration")

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

print(f"=== A/B Run {run_id} Results (NO_REUSE={no_reuse}, PEAK_RPS=490) ===")
print(f"Pre-check wait duration: {wait_duration}s")
print(f"T_LAUNCH: {t_launch}, SURGE_START: {surge_start}, T_FINISH: {t_finish}")

csv_all_path = os.path.join(out_dir, "ab_summary.csv")
write_header = not os.path.exists(csv_all_path)

# Parse timeline for predictive side
boost_epoch = None
boosted_min_val = 3
release_epoch = None

timeline_path = os.path.join(out_dir, f"ab_{run_id}_so_timeline.csv")
if os.path.exists(timeline_path):
    with open(timeline_path) as f:
        reader = csv.DictReader(f)
        for row in reader:
            ep = int(row["epoch"])
            m_val = int(row["min_replicas"])
            if m_val > 3 and boost_epoch is None:
                boost_epoch = ep
                boosted_min_val = m_val
            if m_val > boosted_min_val:
                boosted_min_val = m_val
            if boost_epoch is not None and ep > boost_epoch and m_val == 3 and release_epoch is None:
                release_epoch = ep

boost_offset = (boost_epoch - surge_start) if boost_epoch is not None else "none"
release_offset = (release_epoch - surge_start) if release_epoch is not None else "none"

# Check annotations cleaned
cleaned_yes_no = "yes"
if "prescale.io/original-min-replicas" in ann_after_raw or "prescale.io/boosted-until" in ann_after_raw:
    cleaned_yes_no = "no"

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

    # 5. Timeseries CSV
    ts_csv = os.path.join(out_dir, f"ab_{run_id}_{side}_timeseries.csv")
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

    # Derive server-side metrics (EXACT 5B definitions)
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

    pod_seconds = int(sum(ready_series.values()) * 5)

    # Replicas ready at SURGE_START and at SURGE_START + 30s
    ready_at_surge = "unknown"
    ready_at_surge_30 = "unknown"
    for ts, r_val in sorted_ready:
        if abs(ts - surge_start) <= 3 and ready_at_surge == "unknown":
            ready_at_surge = int(r_val)
        if abs(ts - (surge_start + 30)) <= 3 and ready_at_surge_30 == "unknown":
            ready_at_surge_30 = int(r_val)

    print(f"\n--- Side: {side} ---")
    print(f"client p99: {client_p99:.2f} ms, server p99: {peak_p99:.2f} ms")
    print(f"breach_seconds: {breach_seconds} s, failed %: {failed_pct:.2f}%, non-2xx: {total_non2xx}")
    print(f"dropped_iterations: {dropped_iters}, max ready replicas: {max_ready}")
    print(f"first scale-out epoch offset from SURGE_START: {sec_to_first_scale} s, pod_seconds: {pod_seconds} pod*s")

    if side == "predictive":
        print(f"PREDICTIVE EXTRA:")
        print(f"  boost epoch offset: {boost_offset} s")
        print(f"  boosted min value: {boosted_min_val}")
        print(f"  release offset: {release_offset} s")
        print(f"  annotations cleaned: {cleaned_yes_no}")
        print(f"  replicas ready at SURGE_START: {ready_at_surge}")
        print(f"  replicas ready at SURGE_START+30s: {ready_at_surge_30}")

    row = [
        run_id, side, no_reuse, http_reqs, f"{achieved_rps:.2f}", f"{failed_pct:.2f}", dropped_iters,
        f"{client_p95:.2f}", f"{client_p99:.2f}", breach_seconds, f"{peak_p99:.2f}", total_non2xx,
        sec_to_first_scale, sec_to_max_ready, max_ready, pod_seconds,
        boost_offset if side == "predictive" else "-",
        boosted_min_val if side == "predictive" else "-",
        release_offset if side == "predictive" else "-",
        cleaned_yes_no if side == "predictive" else "-",
        ready_at_surge if side == "predictive" else "-",
        ready_at_surge_30 if side == "predictive" else "-"
    ]
    all_rows.append(row)

# Append to ab_summary.csv
with open(csv_all_path, "a", newline="") as f:
    writer = csv.writer(f)
    if write_header:
        writer.writerow([
            "run_id", "side", "no_reuse", "http_reqs", "achieved_rps", "fail_pct", "dropped_iters",
            "client_p95_ms", "client_p99_ms", "breach_seconds", "peak_p99_ms", "total_non_2xx",
            "sec_to_first_scale", "sec_to_max_ready", "max_ready", "pod_seconds",
            "boost_offset_s", "boosted_min_val", "release_offset_s", "annotations_cleaned",
            "ready_at_surge", "ready_at_surge_30"
        ])
    for row in all_rows:
        writer.writerow(row)

EOF

log "A/B Run ${RUN_ID} complete."
