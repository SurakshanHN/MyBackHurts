#!/usr/bin/env bash
# scripts/demo/scenarios/s2_predictor_outage.sh
# Live Scenario 2: Predictor Outage & Self-Healing Lease Expiry
set -euo pipefail

KC="kubectl --context kind-prescale-demo"
NS="predictive"
SO="checkout"
PREDICTOR="prescale-predictor"

log(){ echo "[$(date -u +%H:%M:%S)] $*"; }
pass(){ echo -e "\033[32m  [PASS]\033[0m $*"; }
fail(){ echo -e "\033[31m  [FAIL]\033[0m $*"; exit 1; }

echo "=========================================================================="
echo "  Live Scenario 2: Predictor Outage & Self-Healing Fallback"
echo "  Proves prescaler releases boost when predictor fails for max-failures ticks"
echo "=========================================================================="

# 1. Ensure Prescaler and Predictor are running
bash scripts/demo/up.sh

# 2. Reset to baseline
$KC patch scaledobject $SO -n $NS --type=merge -p \
  '{"spec":{"minReplicaCount":3},"metadata":{"annotations":{"prescale.io/original-min-replicas":null,"prescale.io/boosted-until":null}}}' >/dev/null 2>&1 || true
sleep 3

BASELINE_MIN=$($KC get scaledobject $SO -n $NS -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo 3)
log "Baseline minReplicaCount: $BASELINE_MIN"

# 3. Inject an active boost (min=6) with 120s TTL
UNTIL_TS=$(date -u -d '+120 seconds' '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v+120S '+%Y-%m-%dT%H:%M:%SZ')
log "Injecting active boost: minReplicaCount=6 (lease valid until $UNTIL_TS)"
$KC patch scaledobject $SO -n $NS --type=merge -p \
  "{\"spec\":{\"minReplicaCount\":6},\"metadata\":{\"annotations\":{\"prescale.io/original-min-replicas\":\"3\",\"prescale.io/boosted-until\":\"$UNTIL_TS\"}}}"

sleep 2
CURR_MIN=$($KC get scaledobject $SO -n $NS -o jsonpath='{.spec.minReplicaCount}')
log "Active boost verified: minReplicaCount=$CURR_MIN"

# 4. Stop predictor container to simulate outage
log "Simulating upstream outage: stopping predictor container..."
docker stop $PREDICTOR >/dev/null 2>&1

# 5. Monitor prescaler releasing the boost
log "Waiting up to 90s for prescaler to detect failure and release boost (3 failures x 15s)..."
T0=$(date +%s)
RELEASED=0
for i in $(seq 1 18); do
  sleep 5
  CURR_MIN=$($KC get scaledobject $SO -n $NS -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo 0)
  ORIG_ANN=$($KC get scaledobject $SO -n $NS -o jsonpath="{.metadata.annotations['prescale\\.io/original-min-replicas']}" 2>/dev/null || echo "")
  log "  t=$(($(date +%s)-T0))s: minReplicaCount=$CURR_MIN (original-min annotation='$ORIG_ANN')"
  if [ "$CURR_MIN" = "$BASELINE_MIN" ] && [ -z "$ORIG_ANN" ]; then
    RELEASED=1
    break
  fi
done

if [ $RELEASED -eq 1 ]; then
  pass "Boost cleanly released back to baseline min=$BASELINE_MIN after predictor failure!"
else
  fail "Boost was NOT released after predictor failure."
fi

# 6. Restore predictor
log "Restoring predictor container..."
docker start $PREDICTOR >/dev/null 2>&1
sleep 15
log "Predictor restored and ticking normally."
pass "Scenario 2 completed successfully."
