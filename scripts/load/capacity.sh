#!/usr/bin/env bash
# scripts/load/capacity.sh – Capacity probe on baseline checkout service.
# Measures sustained RPS of 3 checkout pods within SLO.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

KC="kubectl --context kind-prescale-demo"
K6_IMG=$(cat loadtest/K6_IMAGE | tr -d ' \n')
OUT_DIR="docs/reports/data/part5"
mkdir -p "$OUT_DIR"
chmod 777 "$OUT_DIR"

CSV="${OUT_DIR}/capacity.csv"
SUMMARY="${OUT_DIR}/capacity_summary.txt"
LOG="${OUT_DIR}/capacity_run.log"

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
ORIG_MIN=$($KC get scaledobject checkout -n baseline -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo 3)
ORIG_MAX=$($KC get scaledobject checkout -n baseline -o jsonpath='{.spec.maxReplicaCount}' 2>/dev/null || echo 10)
log "Original baseline ScaledObject: min=${ORIG_MIN}, max=${ORIG_MAX}"

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
echo "target_rps,achieved_rps,dropped,p50_ms,p95_ms,p99_ms,fail_pct,pod_cpu_m_avg,loadavg1,mem_avail_mib,fakedb_peak_conns,slo_ok" > "$CSV"

STAGES=(30 60 90 120 150 180 210 240 270 300)
consecutive_slo_breaches=0
stop_rule_fired="none"

parse_cpu_m() {
    # Takes "125m" -> 125, "1" -> 1000
    local val="$1"
    if [[ "$val" =~ ([0-9]+)m ]]; then
        echo "${BASH_REMATCH[1]}"
    elif [[ "$val" =~ ^[0-9]+$ ]]; then
        echo "$((val * 1000))"
    else
        echo 0
    fi
}

for idx in "${!STAGES[@]}"; do
    rate="${STAGES[$idx]}"
    log "=================================================="
    log "Starting Stage: ${rate} RPS (duration 40s)"
    log "=================================================="

    # Check MemAvailable before stage
    mem_avail_kb=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
    if [ "$mem_avail_kb" -lt 1048576 ]; then
        stop_rule_fired="MemAvailable < 1 GiB (${mem_avail_kb} kB)"
        log "STOP RULE FIRED: ${stop_rule_fired}"
        break
    fi

    # Launch k6 stage in background
    stage_json="${OUT_DIR}/stage_${rate}.json"
    rm -f "$stage_json"

    docker run --rm --user "$(id -u):$(id -g)" --network host \
        -v "$PWD/loadtest:/loadtest:ro,z" \
        -v "$PWD/${OUT_DIR}:/out:z" \
        "$K6_IMG" run \
        -e TARGET_URL="http://localhost:8081" \
        -e RATE="$rate" \
        -e DURATION="40s" \
        --summary-export "/out/stage_${rate}.json" \
        /loadtest/capacity.js >> "$LOG" 2>&1 &
    k6_pid=$!

    # Sample top pods at ~25s into stage
    sleep 25
    top_sample1=$($KC top pods -n baseline -l app=checkout --no-headers 2>/dev/null || true)
    log "top pods sample 1 (~25s):"
    echo "$top_sample1" | tee -a "$LOG"

    # Sample top pods at ~38s into stage (13s later)
    sleep 13
    top_sample2=$($KC top pods -n baseline -l app=checkout --no-headers 2>/dev/null || true)
    log "top pods sample 2 (~38s):"
    echo "$top_sample2" | tee -a "$LOG"

    # Wait for k6 to finish
    wait $k6_pid || log "k6 exited with status $?"

    # Post-stage measurements
    loadavg1=$(awk '{print $1}' /proc/loadavg)
    mem_avail_kb=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
    mem_avail_mib=$(python3 -c "print(f'{$mem_avail_kb / 1024.0:.1f}')")
    
    current_restarts=$(get_checkout_restarts)
    log "Loadavg1: ${loadavg1}, MemAvailable: ${mem_avail_mib} MiB, Restarts: ${current_restarts}"

    # Query fakedb peak connections from VictoriaMetrics
    fakedb_resp=$(curl -s -G 'http://localhost:8428/api/v1/query' \
        --data-urlencode 'query=max_over_time(db_active_connections{namespace="baseline",app="fakedb"}[40s])' 2>/dev/null)
    fakedb_peak_conns=$(echo "$fakedb_resp" | python3 -c \
        "import sys, json; d=json.load(sys.stdin); r=d.get('data',{}).get('result',[]); print(r[0]['value'][1] if r else '0')" 2>/dev/null || echo 0)
    log "fakedb peak active connections (40s): ${fakedb_peak_conns}"

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
reqs = m.get("http_reqs", {}).get("values", {})
achieved_rps = reqs.get("rate", 0.0)

dropped = m.get("dropped_iterations", {}).get("values", {}).get("count", 0)

dur = m.get("http_req_duration", {}).get("values", {})
p50 = dur.get("p(50)", dur.get("med", 0.0))
p95 = dur.get("p(95)", 0.0)
p99 = dur.get("p(99)", dur.get("max", 0.0))

failed = m.get("http_req_failed", {}).get("values", {})
fail_rate = failed.get("rate", 0.0)
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

    # Calculate pod_cpu_m_avg across the 2 samples (average per pod)
    calc_cpu_avg() {
        local sample="$1"
        echo "$sample" | awk '
        NF>=2 {
            val=$2
            gsub("m","",val)
            sum+=val
            count++
        }
        END {
            if (count>0) print sum/count; else print 0
        }'
    }

    cpu1=$(calc_cpu_avg "$top_sample1")
    cpu2=$(calc_cpu_avg "$top_sample2")
    pod_cpu_m_avg=$(python3 -c "print(f'{($cpu1 + $cpu2) / 2.0:.1f}')")
    log "pod_cpu_m_avg: ${pod_cpu_m_avg} mCPU (sample1=${cpu1}, sample2=${cpu2})"

    # SLO evaluation
    slo_ok=$(python3 -c "
p99 = float('$p99_ms')
fail = float('$fail_pct')
slo_p99 = float('$SLO_P99_MS')
slo_err = float('$SLO_ERR_PCT')
if p99 <= slo_p99 and fail <= slo_err:
    print('true')
else:
    print('false')
")

    log "Results: achieved_rps=${achieved_rps}, p99=${p99_ms}ms, fail_pct=${fail_pct}%, slo_ok=${slo_ok}"

    # Append to CSV
    echo "${rate},${achieved_rps},${dropped},${p50_ms},${p95_ms},${p99_ms},${fail_pct},${pod_cpu_m_avg},${loadavg1},${mem_avail_mib},${fakedb_peak_conns},${slo_ok}" >> "$CSV"

    # Check Stop Rules:
    # 1. Pod restarts
    if [ "$current_restarts" -gt "$INITIAL_RESTARTS" ]; then
        stop_rule_fired="checkout pod restart detected (from ${INITIAL_RESTARTS} to ${current_restarts})"
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

    # 4. 2 consecutive stages breaching SLO
    if [ "$slo_ok" = "false" ]; then
        consecutive_slo_breaches=$((consecutive_slo_breaches + 1))
        log "SLO breach count: ${consecutive_slo_breaches}"
        if [ "$consecutive_slo_breaches" -ge 2 ]; then
            stop_rule_fired="2 consecutive stages breached SLO"
            log "STOP RULE FIRED: ${stop_rule_fired}"
            break
        fi
    else
        consecutive_slo_breaches=0
    fi

    # 20s idle cooldown between stages
    if [ "$idx" -lt $((${#STAGES[@]} - 1)) ]; then
        log "Resting 20s between stages..."
        sleep 20
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

# Derived calculations
python3 - << 'EOF' | tee -a "$SUMMARY"
import csv

stages = []
with open("docs/reports/data/part5/capacity.csv") as f:
    reader = csv.DictReader(f)
    for row in reader:
        stages.append(row)

knee_stage = None
for s in stages:
    if s["slo_ok"] == "true":
        knee_stage = s

print("\n=== Capacity Probe Derived Summary ===")
if knee_stage:
    knee_rps_3pods = float(knee_stage["achieved_rps"])
    target_knee = int(knee_stage["target_rps"])
    knee_per_pod = knee_rps_3pods / 3.0
    pod_cpu_m_avg = float(knee_stage["pod_cpu_m_avg"])
    mcpu_per_rps = pod_cpu_m_avg / knee_per_pod if knee_per_pod > 0 else 0.0

    print(f"KNEE_RPS_3PODS: {knee_rps_3pods:.2f} RPS (target stage {target_knee} RPS)")
    print(f"KNEE_PER_POD: {knee_per_pod:.2f} RPS per pod")
    print(f"Pod CPU at knee (pod_cpu_m_avg): {pod_cpu_m_avg:.1f} mCPU")
    print(f"MCPU_PER_RPS: {mcpu_per_rps:.2f} mCPU per RPS")
else:
    print("No stage passed SLO!")

print("\n=== Theoretical Comparison ===")
print("THEORETICAL: limit 500m / 5ms CPU burn = 100 RPS per pod")
print("THEORETICAL 3 pods: 300 RPS (ignoring fakedb and network overhead)")
EOF

echo "Stop rule fired: ${stop_rule_fired}" >> "$SUMMARY"
log "capacity.sh completed successfully."
