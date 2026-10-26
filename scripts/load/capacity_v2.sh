#!/usr/bin/env bash
# scripts/load/capacity_v2.sh – Capacity probe v2 on baseline checkout service.
# Measures sustained RPS of 3 checkout pods within SLO across higher stages.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

KC="kubectl --context kind-prescale-demo"
K6_IMG=$(cat loadtest/K6_IMAGE | tr -d ' \n')
OUT_DIR="docs/reports/data/part5b"
mkdir -p "$OUT_DIR"
chmod 777 "$OUT_DIR"

CSV="${OUT_DIR}/capacity_v2.csv"
SUMMARY="${OUT_DIR}/capacity_v2_summary.txt"
LOG="${OUT_DIR}/capacity_v2_run.log"

: > "$LOG"
: > "$SUMMARY"

log() {
    local msg="[$(date +'%Y-%m-%dT%H:%M:%S%z')] $*"
    echo "$msg" | tee -a "$LOG"
}

# 1. Load SLO parameters
if [ -f loadtest/slo.env ]; then
    source loadtest/slo.env
else
    SLO_P99_MS=300
    SLO_ERR_PCT=1
fi
log "SLO Parameters: SLO_P99_MS=${SLO_P99_MS}, SLO_ERR_PCT=${SLO_ERR_PCT}"

# 2. Hold replicas fixed at 3
ORIG_MIN=3
ORIG_MAX=10
log "Target baseline ScaledObject restoration bounds: min=${ORIG_MIN}, max=${ORIG_MAX}"

log "Patching baseline ScaledObject to min=3, max=3..."
$KC patch scaledobject checkout -n baseline --type merge -p '{"spec":{"minReplicaCount":3,"maxReplicaCount":3}}' >> "$LOG" 2>&1

log "Waiting for HPA min=3, max=3 and 3 ready pods (max 120s)..."
deadline=$(($(date +%s) + 120))
while [ "$(date +%s)" -lt "$deadline" ]; do
    hpa_min=$($KC get hpa keda-hpa-checkout -n baseline -o jsonpath='{.spec.minReplicas}' 2>/dev/null || echo 0)
    hpa_max=$($KC get hpa keda-hpa-checkout -n baseline -o jsonpath='{.spec.maxReplicas}' 2>/dev/null || echo 0)
    ready_pods=$($KC get deploy checkout -n baseline -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
    if [ "$hpa_min" = "3" ] && [ "$hpa_max" = "3" ] && [ "$ready_pods" = "3" ]; then
        log "Baseline settled at min=3, max=3, ready=3"
        break
    fi
    sleep 2
done

# Initial checkout pod restarts
get_checkout_restarts() {
    $KC get pods -n baseline -l app=checkout -o jsonpath='{range .items[*]}{.status.containerStatuses[*].restartCount}{" "}{end}' 2>/dev/null | \
        awk '{sum=0; for(i=1;i<=NF;i++) sum+=$i; print sum}'
}

INITIAL_RESTARTS=$(get_checkout_restarts)
log "Initial checkout pod restarts: ${INITIAL_RESTARTS}"

# CSV header
echo "target_rps,achieved_rps,dropped,p50_ms,p95_ms,p99_ms,fail_pct,restarts,loadavg1,mem_avail_mib,cpu_source,pod_cpu_m_avg,pod_rps_min,pod_rps_max,fakedb_peak_conns,mcpu_per_rps_per_pod,slo_ok" > "$CSV"

STAGES=(330 360 390 420 450 480)
stop_rule_fired="none"
first_breach_stage=""

for idx in "${!STAGES[@]}"; do
    rate="${STAGES[$idx]}"
    log "=================================================="
    log "Starting Stage: ${rate} RPS (duration 40s)"
    log "=================================================="

    # Check MemAvailable before stage
    mem_avail_kb=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
    if [ "$mem_avail_kb" -lt 1048576 ]; then
        stop_rule_fired="MemAvailable < 1 GiB before stage (${mem_avail_kb} kB)"
        log "STOP RULE FIRED: ${stop_rule_fired}"
        break
    fi

    # Launch k6 stage in background
    stage_json="${OUT_DIR}/stage_${rate}.json"
    rm -f "$stage_json"

    docker run --name "k6-capacity-v2" --rm --user "$(id -u):$(id -g)" --network host \
        -v "$PWD/loadtest:/loadtest:ro,z" \
        -v "$PWD/${OUT_DIR}:/out:z" \
        "$K6_IMG" run \
        -e TARGET_URL="http://localhost:8081" \
        -e RATE="$rate" \
        -e DURATION="40s" \
        --summary-export "/out/stage_${rate}.json" \
        /loadtest/capacity.js >> "$LOG" 2>&1 &
    k6_pid=$!

    # Wait for k6 to finish
    wait $k6_pid || log "k6 exited with status $?"

    # Wait 7 seconds for VM metric ingestion
    log "Waiting 7s for VictoriaMetrics ingestion lag..."
    sleep 7

    # Post-stage measurements
    loadavg1=$(awk '{print $1}' /proc/loadavg)
    mem_avail_kb=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
    mem_avail_mib=$(python3 -c "print(f'{$mem_avail_kb / 1024.0:.1f}')")
    current_restarts=$(get_checkout_restarts)

    # Query VictoriaMetrics over a 20s window
    # 1. CPU per pod (mCPU)
    cpu_source="container_cpu_usage_seconds_total"
    cpu_resp=$(curl -s -G 'http://localhost:8428/api/v1/query' \
        --data-urlencode 'query=rate(container_cpu_usage_seconds_total{namespace="baseline",container="checkout"}[20s]) * 1000' 2>/dev/null)

    cpu_stats=$(echo "$cpu_resp" | python3 -c '
import sys, json
try:
    d = json.load(sys.stdin)
    results = d.get("data", {}).get("result", [])
    vals = [float(r["value"][1]) for r in results if "value" in r]
    if vals:
        avg = sum(vals) / len(vals)
        print(f"{avg:.2f}")
    else:
        print("0.0")
except Exception as e:
    print("0.0")
')

    # Fallback check
    if [ "$cpu_stats" = "0.0" ]; then
        proc_resp=$(curl -s -G 'http://localhost:8428/api/v1/query' \
            --data-urlencode 'query=rate(process_cpu_seconds_total{namespace="baseline",app="checkout"}[20s]) * 1000' 2>/dev/null)
        proc_stats=$(echo "$proc_resp" | python3 -c '
import sys, json
try:
    d = json.load(sys.stdin)
    results = d.get("data", {}).get("result", [])
    vals = [float(r["value"][1]) for r in results if "value" in r]
    if vals:
        print(f"{sum(vals)/len(vals):.2f}")
    else:
        print("0.0")
except:
    print("0.0")
')
        if [ "$proc_stats" != "0.0" ]; then
            cpu_source="process_cpu_seconds_total"
            pod_cpu_m_avg="$proc_stats"
        else
            cpu_source="kubectl_top_LOW-RES"
            top_out=$($KC top pods -n baseline -l app=checkout --no-headers 2>/dev/null || true)
            pod_cpu_m_avg=$(echo "$top_out" | awk '{val=$2; gsub("m","",val); sum+=val; c++} END {if(c>0) print sum/c; else print 0}')
        fi
    else
        pod_cpu_m_avg="$cpu_stats"
    fi

    # 2. Per-pod RPS (min and max across the 3 pods)
    rps_resp=$(curl -s -G 'http://localhost:8428/api/v1/query' \
        --data-urlencode 'query=sum by (pod)(rate(http_requests_total{namespace="baseline",app="checkout"}[20s]))' 2>/dev/null)

    pod_rps_bounds=$(echo "$rps_resp" | python3 -c '
import sys, json
try:
    d = json.load(sys.stdin)
    results = d.get("data", {}).get("result", [])
    vals = [float(r["value"][1]) for r in results if "value" in r]
    if vals:
        print(f"{min(vals):.2f} {max(vals):.2f}")
    else:
        print("0.00 0.00")
except Exception as e:
    print("0.00 0.00")
')
    pod_rps_min=$(echo "$pod_rps_bounds" | awk '{print $1}')
    pod_rps_max=$(echo "$pod_rps_bounds" | awk '{print $2}')

    # 3. Peak fakedb connections
    fakedb_resp=$(curl -s -G 'http://localhost:8428/api/v1/query' \
        --data-urlencode 'query=max_over_time(db_active_connections{namespace="baseline",app="fakedb"}[20s])' 2>/dev/null)
    fakedb_peak_conns=$(echo "$fakedb_resp" | python3 -c \
        "import sys, json; d=json.load(sys.stdin); r=d.get('data',{}).get('result',[]); print(r[0]['value'][1] if r else '0')" 2>/dev/null || echo 0)

    # Parse k6 summary json
    if [ ! -f "$stage_json" ]; then
        log "ERROR: ${stage_json} was not generated!"
        break
    fi

    stage_metrics=$(python3 - << EOF
import json

with open("${stage_json}") as f:
    d = json.load(f)

m = d.get("metrics", {})
reqs = m.get("http_reqs", {})
achieved_rps = reqs.get("values", {}).get("rate", reqs.get("rate", 0.0))

dropped_obj = m.get("dropped_iterations", {})
dropped = dropped_obj.get("values", {}).get("count", dropped_obj.get("count", 0))

dur = m.get("http_req_duration", {})
dur_vals = dur.get("values", dur)
p50 = dur_vals.get("p(50)", dur_vals.get("med", 0.0))
p95 = dur_vals.get("p(95)", 0.0)
p99 = dur_vals.get("p(99)", dur_vals.get("max", 0.0))

failed = m.get("http_req_failed", {})
fail_rate = failed.get("values", {}).get("rate", failed.get("value", failed.get("rate", 0.0)))
fail_pct = fail_rate * 100.0

print(f"{achieved_rps:.2f} {dropped} {p50:.2f} {p95:.2f} {p99:.2f} {fail_pct:.2f}")
EOF
)

    achieved_rps=$(echo "$stage_metrics" | awk '{print $1}')
    dropped=$(echo "$stage_metrics" | awk '{print $2}')
    p50_ms=$(echo "$stage_metrics" | awk '{print $3}')
    p95_ms=$(echo "$stage_metrics" | awk '{print $4}')
    p99_ms=$(echo "$stage_metrics" | awk '{print $5}')
    fail_pct=$(echo "$stage_metrics" | awk '{print $6}')

    # Calculate mCPU per RPS per pod
    # per-pod RPS = achieved_rps / 3.0
    mcpu_per_rps_per_pod=$(python3 -c "
achieved = float('$achieved_rps')
cpu = float('$pod_cpu_m_avg')
pod_rps = achieved / 3.0
if pod_rps > 0:
    print(f'{cpu / pod_rps:.2f}')
else:
    print('0.00')
")

    # SLO evaluation:
    # Breach = p99 > SLO_P99_MS or failed % > SLO_ERR_PCT or dropped_iterations > 1% of target
    target_iterations=$((rate * 40))
    dropped_limit=$(python3 -c "print(f'{$target_iterations * 0.01:.1f}')")
    
    breach_reason=""
    is_breached=$(python3 -c "
p99 = float('$p99_ms')
fail = float('$fail_pct')
dropped = int('$dropped')
target_iters = int('$target_iterations')
slo_p99 = float('$SLO_P99_MS')
slo_err = float('$SLO_ERR_PCT')

reasons = []
if p99 > slo_p99:
    reasons.append(f'p99 {p99:.2f}ms > {slo_p99}ms')
if fail > slo_err:
    reasons.append(f'fail {fail:.2f}% > {slo_err}%')
if dropped > target_iters * 0.01:
    reasons.append(f'dropped {dropped} > 1% target ({target_iters * 0.01:.0f})')

if reasons:
    print('true;' + '; '.join(reasons))
else:
    print('false;none')
")
    slo_breached=$(echo "$is_breached" | cut -d';' -f1)
    breach_desc=$(echo "$is_breached" | cut -d';' -f2-)

    if [ "$slo_breached" = "true" ]; then
        slo_ok="false"
        first_breach_stage="${rate} RPS (${breach_desc})"
    else
        slo_ok="true"
    fi

    log "Results: achieved_rps=${achieved_rps}, dropped=${dropped}/${dropped_limit}, p99=${p99_ms}ms, fail_pct=${fail_pct}%, cpu=${pod_cpu_m_avg}m (${cpu_source}), pod_rps=[${pod_rps_min}, ${pod_rps_max}], fakedb_conns=${fakedb_peak_conns}, m/rps/pod=${mcpu_per_rps_per_pod}, slo_ok=${slo_ok}"

    # Append to CSV
    echo "${rate},${achieved_rps},${dropped},${p50_ms},${p95_ms},${p99_ms},${fail_pct},${current_restarts},${loadavg1},${mem_avail_mib},${cpu_source},${pod_cpu_m_avg},${pod_rps_min},${pod_rps_max},${fakedb_peak_conns},${mcpu_per_rps_per_pod},${slo_ok}" >> "$CSV"

    # Stop Rules:
    # 1. First breached stage
    if [ "$slo_ok" = "false" ]; then
        stop_rule_fired="First breached stage: ${first_breach_stage}"
        log "STOP RULE FIRED: ${stop_rule_fired}"
        break
    fi

    # 2. Failed % > 30
    if python3 -c "exit(0 if float('$fail_pct') > 30.0 else 1)"; then
        stop_rule_fired="failed % > 30 (got ${fail_pct}%)"
        log "STOP RULE FIRED: ${stop_rule_fired}"
        break
    fi

    # 3. MemAvailable < 1 GiB
    if [ "$mem_avail_kb" -lt 1048576 ]; then
        stop_rule_fired="MemAvailable < 1 GiB (${mem_avail_kb} kB)"
        log "STOP RULE FIRED: ${stop_rule_fired}"
        break
    fi

    # 4. Checkout pod restart
    if [ "$current_restarts" -gt "$INITIAL_RESTARTS" ]; then
        stop_rule_fired="checkout pod restart detected (from ${INITIAL_RESTARTS} to ${current_restarts})"
        log "STOP RULE FIRED: ${stop_rule_fired}"
        break
    fi

    # Remaining idle time (20s idle total: 7s already spent in lag wait, remaining 13s)
    if [ "$idx" -lt $((${#STAGES[@]} - 1)) ]; then
        log "Resting 13s (total 20s idle)..."
        sleep 13
    fi
done

log "Probe stages completed. Stop rule fired: ${stop_rule_fired}"

# Restore baseline ScaledObject
log "Restoring baseline ScaledObject to min=${ORIG_MIN}, max=${ORIG_MAX}..."
$KC patch scaledobject checkout -n baseline --type merge -p "{\"spec\":{\"minReplicaCount\":${ORIG_MIN},\"maxReplicaCount\":${ORIG_MAX}}}" >> "$LOG" 2>&1

log "Waiting for baseline ScaledObject & HPA restoration..."
restore_deadline=$(($(date +%s) + 120))
while [ "$(date +%s)" -lt "$restore_deadline" ]; do
    h_min=$($KC get hpa keda-hpa-checkout -n baseline -o jsonpath='{.spec.minReplicas}' 2>/dev/null || echo 0)
    h_max=$($KC get hpa keda-hpa-checkout -n baseline -o jsonpath='{.spec.maxReplicas}' 2>/dev/null || echo 0)
    r_pods=$($KC get deploy checkout -n baseline -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
    if [ "$h_min" = "${ORIG_MIN}" ] && [ "$h_max" = "${ORIG_MAX}" ] && [ "$r_pods" = "3" ]; then
        log "Baseline restored: min=${ORIG_MIN}, max=${ORIG_MAX}, ready=3"
        break
    fi
    sleep 2
done

# Derived calculations across Part 5 and Part 5B
python3 - << 'EOF' | tee -a "$SUMMARY"
import csv

# Part 5 stages
part5_stages = []
try:
    with open("docs/reports/data/part5/capacity.csv") as f:
        reader = csv.DictReader(f)
        for row in reader:
            part5_stages.append(row)
except Exception as e:
    pass

# Part 5B stages
part5b_stages = []
try:
    with open("docs/reports/data/part5b/capacity_v2.csv") as f:
        reader = csv.DictReader(f)
        for row in reader:
            part5b_stages.append(row)
except Exception as e:
    pass

all_stages = []
for s in part5_stages:
    all_stages.append((int(s["target_rps"]), s["slo_ok"] == "true", float(s["achieved_rps"]), s))
for s in part5b_stages:
    all_stages.append((int(s["target_rps"]), s["slo_ok"] == "true", float(s["achieved_rps"]), s))

highest_slo_ok_stage = None
has_higher_breach = False

# Sort by target rps
all_stages.sort(key=lambda x: x[0])

for target, ok, achieved, row in all_stages:
    if ok:
        highest_slo_ok_stage = (target, achieved, row)

if highest_slo_ok_stage:
    knee_target = highest_slo_ok_stage[0]
    for target, ok, achieved, row in all_stages:
        if target > knee_target and not ok:
            has_higher_breach = True
            break

print("\n=== Capacity Probe v2 Derived Summary ===")
if highest_slo_ok_stage:
    knee_target, knee_achieved, knee_row = highest_slo_ok_stage
    bracketed = "yes" if has_higher_breach else "no"
    print(f"KNEE_3: {knee_target} RPS (achieved {knee_achieved:.2f} RPS) (MEASURED)")
    print(f"BRACKETED: {bracketed}")
else:
    print("KNEE_3: none")
    print("BRACKETED: no")

EOF

echo "Stop rule fired: ${stop_rule_fired}" >> "$SUMMARY"
log "capacity_v2.sh completed successfully."
