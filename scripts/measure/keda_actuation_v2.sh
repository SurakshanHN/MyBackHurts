#!/usr/bin/env bash
# keda_actuation_v2.sh – Re-measure KEDA actuation timing for Part 4D.
# Runs 2 up+down trials. Writes raw CSV and derived offsets.
# Usage: bash scripts/measure/keda_actuation_v2.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"
KC="kubectl --context kind-prescale-demo"
CSV="docs/reports/data/part4d/60_keda_poll.csv"
DERIVED="docs/reports/data/part4d/61_keda_derived.txt"

# Setup: record prescaler original replicas and scale to 0
PRESCALER_ORIG=$($KC get deploy prescaler -n predictive -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 0)
echo "prescaler original replicas: $PRESCALER_ORIG" > "$DERIVED"

# Baseline ScaledObject min and ready replicas (control)
BASE_SO_MIN_INIT=$($KC get scaledobject checkout -n baseline -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo -1)
BASE_READY_INIT=$($KC get deploy checkout -n baseline -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo -1)
echo "baseline initial control: so_min=$BASE_SO_MIN_INIT ready=$BASE_READY_INIT" >> "$DERIVED"

if [ "$PRESCALER_ORIG" -gt 0 ]; then
    $KC scale deploy prescaler -n predictive --replicas=0
    $KC rollout status deploy prescaler -n predictive --timeout=60s
    echo "prescaler scaled to 0 and confirmed" >> "$DERIVED"
fi

# CSV header
echo "epoch_ms,trial,phase,so_min,hpa_min,hpa_desired,hpa_current,deploy_spec,deploy_status_replicas,deploy_ready,pods_total,pods_ready_true,pods_running,baseline_so_min,baseline_ready" > "$CSV"

poll_once() {
    local trial="$1" phase="$2"
    local now_ms
    now_ms=$(date +%s%3N)

    # Call 1: ScaledObject predictive
    local so_min
    so_min=$($KC get scaledobject checkout -n predictive -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo -1)

    # Call 2: HPA predictive
    local hpa_info hpa_min hpa_desired hpa_current
    hpa_info=$($KC get hpa keda-hpa-checkout -n predictive -o jsonpath='{.spec.minReplicas} {.status.desiredReplicas} {.status.currentReplicas}' 2>/dev/null || echo "-1 -1 -1")
    hpa_min=$(echo "$hpa_info" | awk '{print $1}')
    hpa_desired=$(echo "$hpa_info" | awk '{print $2}')
    hpa_current=$(echo "$hpa_info" | awk '{print $3}')

    # Call 3: Deployment predictive
    local deploy_info deploy_spec deploy_status deploy_ready
    deploy_info=$($KC get deploy checkout -n predictive -o jsonpath='{.spec.replicas} {.status.replicas} {.status.readyReplicas}' 2>/dev/null || echo "-1 -1 -1")
    deploy_spec=$(echo "$deploy_info" | awk '{print $1}')
    deploy_status=$(echo "$deploy_info" | awk '{print $2}')
    deploy_ready=$(echo "$deploy_info" | awk '{print $3}')

    # Call 4: Pods predictive (total, ready_true with no deletionTimestamp, running)
    local pod_stats pods_total pods_ready_true pods_running
    pod_stats=$($KC get pods -n predictive -l app=checkout -o jsonpath='{range .items[*]}{.status.phase}{"|"}{.metadata.deletionTimestamp}{"|"}{range .status.conditions[?(@.type=="Ready")]}{.status}{end}{"\n"}{end}' 2>/dev/null | \
        awk -F'|' 'NF>0 && $1!="" {total++; if ($2=="" && $3=="True") ready++; if ($1=="Running") running++} END {print total+0, ready+0, running+0}')
    pods_total=$(echo "$pod_stats" | awk '{print $1}')
    pods_ready_true=$(echo "$pod_stats" | awk '{print $2}')
    pods_running=$(echo "$pod_stats" | awk '{print $3}')

    # Call 5: Baseline control
    local base_info baseline_so_min baseline_ready
    base_info=$($KC get scaledobject,deploy checkout -n baseline -o jsonpath='{.items[0].spec.minReplicaCount} {.items[1].status.readyReplicas}' 2>/dev/null || echo "-1 -1")
    baseline_so_min=$(echo "$base_info" | awk '{print $1}')
    baseline_ready=$(echo "$base_info" | awk '{print $2}')

    echo "${now_ms},${trial},${phase},${so_min},${hpa_min:-0},${hpa_desired:-0},${hpa_current:-0},${deploy_spec:-0},${deploy_status:-0},${deploy_ready:-0},${pods_total},${pods_ready_true},${pods_running},${baseline_so_min},${baseline_ready}" >> "$CSV"
}

run_trial() {
    local trial=$1
    printf '\n=== Starting Trial %d ===\n' "$trial" >> "$DERIVED"

    # Pre-patch logging 5s before T0
    for i in 1 2 3 4 5; do
        poll_once "$trial" "pre"
        sleep 1
    done

    # Abort check
    local last_ready last_so
    last_ready=$(tail -1 "$CSV" | awk -F, '{print $12}')
    last_so=$(tail -1 "$CSV" | awk -F, '{print $4}')
    if [ "$last_ready" != "3" ] || [ "$last_so" != "3" ]; then
        printf 'ABORT: pods_ready_true=%s so_min=%s (not 3/3 at T0)\n' "$last_ready" "$last_so" >> "$DERIVED"
        return 1
    fi

    # T0 = epoch_ms immediately before the patch
    local T0_MS
    T0_MS=$(date +%s%3N)
    printf 'T0_MS=%s\n' "$T0_MS" >> "$DERIVED"

    # UP patch
    $KC patch scaledobject checkout -n predictive --type merge -p '{"spec":{"minReplicaCount":6}}'
    local patch_ret
    patch_ret=$(date +%s%3N)
    printf 'patch minReplicaCount=6 returned at +%d ms\n' "$((patch_ret - T0_MS))" >> "$DERIVED"

    # Poll until deploy_ready == 6 and pods_ready_true == 6 on 2 consecutive rows, or 180 s
    local consec=0
    local deadline=$((T0_MS + 180000))
    while [ "$(date +%s%3N)" -lt "$deadline" ]; do
        poll_once "$trial" "up"
        local row
        row=$(tail -1 "$CSV")
        local cur_deploy_ready cur_pods_ready
        cur_deploy_ready=$(echo "$row" | awk -F, '{print $10}')
        cur_pods_ready=$(echo "$row" | awk -F, '{print $12}')
        if [ "$cur_deploy_ready" = "6" ] && [ "$cur_pods_ready" = "6" ]; then
            consec=$((consec + 1))
            if [ $consec -ge 2 ]; then
                printf 'UP reached 6/6 ready consecutive at +%d ms\n' "$(($(date +%s%3N) - T0_MS))" >> "$DERIVED"
                break
            fi
        else
            consec=0
        fi
        sleep 1
    done

    # Save pod timestamps after up phase
    printf '\n=== Pod timestamps after UP trial %d ===\n' "$trial" >> "$DERIVED"
    $KC get pods -n predictive -l app=checkout -o jsonpath=\
'{range .items[*]}{.metadata.name}{" creationTimestamp="}{.metadata.creationTimestamp}{" startTime="}{.status.startTime}{" conditions="}{range .status.conditions[*]}{.type}{"="}{.status}{"@"}{.lastTransitionTime}{";"}{end}{"\n"}{end}' \
        >> "$DERIVED" 2>&1 || true

    # Describe HPA
    printf '\n=== Describe HPA after UP trial %d ===\n' "$trial" >> "$DERIVED"
    $KC describe hpa keda-hpa-checkout -n predictive >> "$DERIVED" 2>&1 || true

    # DOWN patch
    local T0_DOWN_MS
    T0_DOWN_MS=$(date +%s%3N)
    printf '\n=== Trial %d DOWN (T0_DOWN=%s) ===\n' "$trial" "$T0_DOWN_MS" >> "$DERIVED"
    $KC patch scaledobject checkout -n predictive --type merge -p '{"spec":{"minReplicaCount":3}}'
    printf 'patch minReplicaCount=3 returned at +%d ms\n' "$(($(date +%s%3N) - T0_DOWN_MS))" >> "$DERIVED"

    # Poll until deploy_status_replicas == 3 and pods_total == 3, or 240 s
    local down_deadline=$((T0_DOWN_MS + 240000))
    while [ "$(date +%s%3N)" -lt "$down_deadline" ]; do
        poll_once "$trial" "down"
        local down_row
        down_row=$(tail -1 "$CSV")
        local cur_status_rep cur_pods_tot
        cur_status_rep=$(echo "$down_row" | awk -F, '{print $9}')
        cur_pods_tot=$(echo "$down_row" | awk -F, '{print $11}')
        if [ "$cur_status_rep" = "3" ] && [ "$cur_pods_tot" = "3" ]; then
            printf 'DOWN finished (status_replicas=3, pods_total=3) at +%d ms\n' "$(($(date +%s%3N) - T0_DOWN_MS))" >> "$DERIVED"
            break
        fi
        sleep 2
    done

    # Settle at 3 ready and 3 total between trials
    printf 'Settle check (ready=3, total=3)...\n' >> "$DERIVED"
    local settle_deadline=$(($(date +%s%3N) + 60000))
    while [ "$(date +%s%3N)" -lt "$settle_deadline" ]; do
        poll_once "$trial" "settle"
        local s_row
        s_row=$(tail -1 "$CSV")
        local s_ready s_tot
        s_ready=$(echo "$s_row" | awk -F, '{print $12}')
        s_tot=$(echo "$s_row" | awk -F, '{print $11}')
        if [ "$s_ready" = "3" ] && [ "$s_tot" = "3" ]; then
            printf 'Settled at ready=3 total=3\n' >> "$DERIVED"
            break
        fi
        sleep 2
    done
}

run_trial 1
echo "Resting 10s between trials..."
sleep 10
run_trial 2

# Restore: ScaledObject min 3, prescaler replicas back to original
$KC patch scaledobject checkout -n predictive --type merge -p '{"spec":{"minReplicaCount":3}}' 2>/dev/null || true
if [ "$PRESCALER_ORIG" -gt 0 ]; then
    $KC scale deploy prescaler -n predictive --replicas="$PRESCALER_ORIG"
    printf 'prescaler restored to %d replicas\n' "$PRESCALER_ORIG" >> "$DERIVED"
fi

# Run python derivation script
python3 - << 'EOF' >> "$DERIVED"
import csv

trials = {}
with open("docs/reports/data/part4d/60_keda_poll.csv") as f:
    reader = csv.DictReader(f)
    for row in reader:
        tr = row.get("trial")
        if tr not in trials:
            trials[tr] = []
        trials[tr].append(row)

for tr in sorted(trials.keys()):
    rows = trials[tr]
    print(f"\n=== Trial {tr} Derived Offsets ===")
    up_rows = [r for r in rows if r["phase"] == "up"]
    down_rows = [r for r in rows if r["phase"] == "down"]
    
    if up_rows:
        t0 = int(up_rows[0]["epoch_ms"])
        print(f"T0 up: {t0}")
        
        def find_first(predicate, desc):
            for r in up_rows:
                if predicate(r):
                    offset = int(r["epoch_ms"]) - t0
                    print(f"  {desc}: +{offset} ms ({offset/1000.0:.2f} s)")
                    return int(r["epoch_ms"]), offset
            print(f"  {desc}: NOT REACHED within test window")
            return None, None

        find_first(lambda r: int(r["so_min"]) == 6, "so_min == 6")
        find_first(lambda r: int(r["hpa_min"]) == 6, "hpa_min == 6")
        find_first(lambda r: int(r["hpa_desired"]) == 6, "hpa_desired == 6")
        find_first(lambda r: int(r["deploy_spec"]) == 6, "deploy_spec == 6")
        find_first(lambda r: int(r["pods_total"]) > 3, "pods_total > 3")
        find_first(lambda r: int(r["pods_ready_true"]) > 3, "pods_ready_true > 3")
        _, pr6_offset = find_first(lambda r: int(r["pods_ready_true"]) == 6, "pods_ready_true == 6")
        
        if pr6_offset is not None and pr6_offset < 30000:
            print(f"  CONTRADICTION: pods_ready_true reached 6 at +{pr6_offset} ms (<30 s). Configured STARTUP_DELAY_SECONDS=30!")
    
    if down_rows:
        t0d = int(down_rows[0]["epoch_ms"])
        print(f"T0 down: {t0d}")
        def find_down(predicate, desc):
            for r in down_rows:
                if predicate(r):
                    offset = int(r["epoch_ms"]) - t0d
                    print(f"  {desc}: +{offset} ms ({offset/1000.0:.2f} s)")
                    return int(r["epoch_ms"]), offset
            print(f"  {desc}: NOT REACHED within test window")
            return None, None

        find_down(lambda r: int(r["hpa_min"]) == 3, "hpa_min == 3")
        find_down(lambda r: int(r["hpa_desired"]) == 3, "hpa_desired == 3")
        find_down(lambda r: int(r["deploy_spec"]) == 3, "deploy_spec == 3")
        find_down(lambda r: int(r["deploy_status_replicas"]) == 3, "deploy_status_replicas == 3")

# Poll gaps
print("\n=== Poll Gap Analysis ===")
with open("docs/reports/data/part4d/60_keda_poll.csv") as f:
    reader = csv.DictReader(f)
    prev = None
    gaps = []
    for r in reader:
        cur = int(r["epoch_ms"])
        if prev is not None:
            gaps.append(cur - prev)
        prev = cur
if gaps:
    gaps.sort()
    med = gaps[len(gaps)//2]
    print(f"Total rows: {len(gaps)+1}, Gaps: {len(gaps)}")
    print(f"Median gap: {med} ms ({med/1000.0:.2f} s)")
    print(f"Min gap: {min(gaps)} ms ({min(gaps)/1000.0:.2f} s)")
    print(f"Max gap: {max(gaps)} ms ({max(gaps)/1000.0:.2f} s)")

# Baseline control check
print("\n=== Baseline Control Check ===")
with open("docs/reports/data/part4d/60_keda_poll.csv") as f:
    reader = csv.DictReader(f)
    changed = False
    for r in reader:
        if r["baseline_so_min"] != "3" or r["baseline_ready"] != "3":
            print(f"Baseline changed at {r['epoch_ms']}: so_min={r['baseline_so_min']} ready={r['baseline_ready']}")
            changed = True
    if not changed:
        print("Baseline control remained stable: so_min=3 ready=3 across all polls.")
EOF

echo "keda_actuation_v2.sh completed successfully" >> "$DERIVED"
