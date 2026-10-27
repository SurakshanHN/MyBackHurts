#!/usr/bin/env bash
# scripts/demo/scenarios/s3_ttl_expiry.sh
# Live Scenario 3: Boost TTL Expiry & Automatic Cleanup
set -euo pipefail

KC="kubectl --context kind-prescale-demo"
NS="predictive"
SO="checkout"

log(){ echo "[$(date -u +%H:%M:%S)] $*"; }
pass(){ echo -e "\033[32m  [PASS]\033[0m $*"; }
fail(){ echo -e "\033[31m  [FAIL]\033[0m $*"; exit 1; }

echo "=========================================================================="
echo "  Live Scenario 3: Boost Lease TTL Expiration"
echo "  Proves prescaler automatically releases expired boosts without manual intervention"
echo "=========================================================================="

bash scripts/demo/up.sh

BASELINE_MIN=3
PAST_TS="2000-01-01T00:00:00Z"

log "Injecting boost with already expired lease TTL ($PAST_TS, min=7)..."
$KC patch scaledobject $SO -n $NS --type=merge -p \
  "{\"spec\":{\"minReplicaCount\":7},\"metadata\":{\"annotations\":{\"prescale.io/original-min-replicas\":\"3\",\"prescale.io/boosted-until\":\"$PAST_TS\"}}}"

sleep 2
log "Injected: min=$($KC get scaledobject $SO -n $NS -o jsonpath='{.spec.minReplicaCount}')"

log "Waiting for prescaler tick to detect expiry and release (up to 30s)..."
T0=$(date +%s)
RELEASED=0
for i in $(seq 1 8); do
  sleep 4
  CURR_MIN=$($KC get scaledobject $SO -n $NS -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo 0)
  ORIG_ANN=$($KC get scaledobject $SO -n $NS -o jsonpath="{.metadata.annotations['prescale\\.io/original-min-replicas']}" 2>/dev/null || echo "")
  log "  t=$(($(date +%s)-T0))s: minReplicaCount=$CURR_MIN"
  if [ "$CURR_MIN" = "$BASELINE_MIN" ] && [ -z "$ORIG_ANN" ]; then
    RELEASED=1
    break
  fi
done

if [ $RELEASED -eq 1 ]; then
  pass "Expired TTL boost detected and cleanly released back to baseline $BASELINE_MIN!"
else
  fail "Expired lease was NOT released."
fi

pass "Scenario 3 completed successfully."
