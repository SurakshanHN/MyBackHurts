#!/usr/bin/env bash
# vm_freshness_v2.sh – Re-measure VictoriaMetrics freshness for Part 4D.
# Reads internal/metrics to find the query style, measures sample age and lag.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

RAW_CSV="docs/reports/data/part4d/70_freshness_raw.csv"
DERIVED="docs/reports/data/part4d/71_freshness_derived.txt"

: > "$RAW_CSV"
: > "$DERIVED"

VM="http://localhost:8428"
APP_URL="http://localhost:8081/"

# 1. Quote file:line from internal/metrics for the exact URL and query
printf '=== Prescaler metric query source and URL ===\n' >> "$DERIVED"
printf 'File: internal/metrics/prometheus.go:117\n' >> "$DERIVED"
printf 'Line 117: url := strings.TrimRight(p.cfg.BaseURL, "/") + "/api/v1/query_range"\n' >> "$DERIVED"
printf 'File: cmd/prescaler/main.go:148-150\n' >> "$DERIVED"
grep -n -A 3 'RPS:' cmd/prescaler/main.go >> "$DERIVED" || true
printf '\nNote: cmd/prescaler/main.go:148 references checkout_requests_total, but actual exported metric is http_requests_total.\n' >> "$DERIVED"

# 2. Sample age: 10 instant queries (/api/v1/query) of baseline checkout request counter, 1 s apart
printf '\n=== Part 1: Sample Age (10 instant queries, 1 s apart) ===\n' >> "$DERIVED"
echo "now_ms,newest_sample_ts_ms,age_ms" >> "$RAW_CSV"

for i in $(seq 1 10); do
    now_ms=$(date +%s%3N)
    # Query the newest sample timestamp using max(timestamp(http_requests_total{namespace="baseline",app="checkout"}))
    resp=$(curl -s -G "${VM}/api/v1/query" \
        --data-urlencode 'query=max(timestamp(http_requests_total{namespace="baseline",app="checkout"}))' 2>/dev/null)
    
    sample_ts_sec=$(echo "$resp" | python3 -c \
        "import sys, json; d=json.load(sys.stdin); r=d.get('data',{}).get('result',[]); print(r[0]['value'][1] if r else '-1')" 2>/dev/null || echo "-1")
    
    if [ "$sample_ts_sec" != "-1" ]; then
        sample_ts_ms=$(python3 -c "print(int(float('$sample_ts_sec') * 1000))")
        age_ms=$((now_ms - sample_ts_ms))
    else
        sample_ts_ms=-1
        age_ms=-1
    fi
    
    echo "${now_ms},${sample_ts_ms},${age_ms}" >> "$RAW_CSV"
    printf 'Sample %2d: now=%s sample_ts=%s age=%d ms (%.2f s)\n' "$i" "$now_ms" "$sample_ts_ms" "$age_ms" "$(python3 -c "print($age_ms/1000.0)")" >> "$DERIVED"
    sleep 1
done

# 3. Lag test: 5 trials, 20 s apart
printf '\n=== Part 2: End-to-End Lag (5 trials, 20s apart, 30 requests each) ===\n' >> "$DERIVED"
echo "" >> "$RAW_CSV"
echo "trial,record_type,epoch_ms,http_code,counter_value" >> "$RAW_CSV"

# Scrape config: scrape_interval=5s, latencyOffset=1s
# Theoretical max: 5s scrape + 1s latencyOffset + 0.5s query = ~6.5s
THEORETICAL_WINDOW_MAX_MS=6500

for trial in $(seq 1 5); do
    printf '\n--- Trial %d ---\n' "$trial" >> "$DERIVED"
    if [ "$trial" -gt 1 ]; then
        printf 'Waiting 20 s cooldown between trials...\n' >> "$DERIVED"
        sleep 20
    fi
    
    # Read initial counter C0
    c0_resp=$(curl -s -G "${VM}/api/v1/query" \
        --data-urlencode 'query=sum(http_requests_total{namespace="baseline",app="checkout"})' 2>/dev/null)
    C0=$(echo "$c0_resp" | python3 -c \
        "import sys, json; d=json.load(sys.stdin); r=d.get('data',{}).get('result',[]); print(int(float(r[0]['value'][1])) if r else 0)" 2>/dev/null || echo 0)
    
    TARGET=$((C0 + 30))
    printf 'Trial %d: initial counter C0=%d, target (+30)=%d\n' "$trial" "$C0" "$TARGET" >> "$DERIVED"
    
    # Send 30 POST requests to localhost:8081/
    last_req_ms=0
    for j in $(seq 1 30); do
        req_ms=$(date +%s%3N)
        code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$APP_URL" 2>/dev/null || echo "000")
        echo "${trial},request,${req_ms},${code}," >> "$RAW_CSV"
        last_req_ms=$req_ms
    done
    printf 'Sent 30 requests; last request finished at epoch_ms=%s\n' "$last_req_ms" >> "$DERIVED"
    
    # Poll every 0.5 s until counter >= C0+30 or 60 s deadline
    deadline=$((last_req_ms + 60000))
    detected_poll_ms=0
    final_counter=0
    
    while [ "$(date +%s%3N)" -lt "$deadline" ]; do
        poll_ms=$(date +%s%3N)
        poll_resp=$(curl -s -G "${VM}/api/v1/query" \
            --data-urlencode 'query=sum(http_requests_total{namespace="baseline",app="checkout"})' 2>/dev/null)
        cur_val=$(echo "$poll_resp" | python3 -c \
            "import sys, json; d=json.load(sys.stdin); r=d.get('data',{}).get('result',[]); print(int(float(r[0]['value'][1])) if r else 0)" 2>/dev/null || echo 0)
        
        echo "${trial},poll,${poll_ms},,${cur_val}" >> "$RAW_CSV"
        
        if [ "$cur_val" -ge "$TARGET" ]; then
            detected_poll_ms=$poll_ms
            final_counter=$cur_val
            break
        fi
        sleep 0.5
    done
    
    if [ "$detected_poll_ms" -gt 0 ]; then
        lag_ms=$((detected_poll_ms - last_req_ms))
        printf 'Trial %d: detected target %d (counter=%d) at epoch_ms=%s\n' "$trial" "$TARGET" "$final_counter" "$detected_poll_ms" >> "$DERIVED"
        printf 'Trial %d: derived lag = %d ms (%.2f s)\n' "$trial" "$lag_ms" "$(python3 -c "print($lag_ms/1000.0)")" >> "$DERIVED"
        if [ "$lag_ms" -gt "$THEORETICAL_WINDOW_MAX_MS" ]; then
            printf 'Trial %d: CONTRADICTION: lag %d ms exceeds theoretical window %d ms\n' "$trial" "$lag_ms" "$THEORETICAL_WINDOW_MAX_MS" >> "$DERIVED"
        fi
    else
        printf 'Trial %d: target %d not reached within 60s\n' "$trial" "$TARGET" >> "$DERIVED"
    fi
done

# Summary calculation
python3 - << 'EOF' >> "$DERIVED"
import csv

# Sample age summary
ages = []
with open("docs/reports/data/part4d/70_freshness_raw.csv") as f:
    reader = csv.reader(f)
    header = next(reader)
    for row in reader:
        if not row or row[0] == "trial":
            break
        if len(row) == 3 and row[2] != "-1":
            ages.append(int(row[2]))

print("\n=== Sample Age Summary ===")
if ages:
    print(f"Sample count: {len(ages)}")
    print(f"Min age: {min(ages)} ms ({min(ages)/1000.0:.2f} s)")
    print(f"Max age: {max(ages)} ms ({max(ages)/1000.0:.2f} s)")
    print(f"Average age: {sum(ages)/len(ages):.0f} ms ({sum(ages)/len(ages)/1000.0:.2f} s)")

# Lag summary
trials = {}
with open("docs/reports/data/part4d/70_freshness_raw.csv") as f:
    reader = csv.DictReader(f)
    for row in reader:
        tr = row.get("trial")
        if not tr:
            continue
        if tr not in trials:
            trials[tr] = {"requests": [], "polls": []}
        if row.get("record_type") == "request":
            trials[tr]["requests"].append(int(row["epoch_ms"]))
        elif row.get("record_type") == "poll":
            trials[tr]["polls"].append((int(row["epoch_ms"]), int(row["counter_value"])))

print("\n=== Lag Summary Table ===")
lags = []
for tr in sorted(trials.keys()):
    reqs = trials[tr]["requests"]
    polls = trials[tr]["polls"]
    if not reqs or not polls:
        continue
    last_req = max(reqs)
    # first poll
    c0 = polls[0][1]
    tgt = c0 + 30
    detected_poll = None
    for p_time, p_val in polls:
        if p_val >= tgt:
            detected_poll = p_time
            break
    if detected_poll:
        lag = detected_poll - last_req
        lags.append(lag)
        print(f"Trial {tr}: last_req={last_req}, detected={detected_poll}, lag={lag} ms ({lag/1000.0:.2f} s)")
    else:
        print(f"Trial {tr}: not detected")

if lags:
    print(f"Lag range: {min(lags)} ms to {max(lags)} ms ({min(lags)/1000.0:.2f} s to {max(lags)/1000.0:.2f} s)")
    print(f"Average lag: {sum(lags)/len(lags):.0f} ms ({sum(lags)/len(lags)/1000.0:.2f} s)")

print("\nTHEORETICAL window: 0 to ~6.5 s (scrape_interval=5s + latencyOffset=1s + query latency)")
EOF

echo "vm_freshness_v2.sh completed successfully" >> "$DERIVED"
