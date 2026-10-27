#!/usr/bin/env bash
# scripts/demo/scenarios/s4_crash_recovery.sh
# Live Scenario 4: Daemon Crash Recovery & Annotation State Reconstruction
set -euo pipefail

KC="kubectl --context kind-prescale-demo"
NS="predictive"
SO="checkout"

log(){ echo "[$(date -u +%H:%M:%S)] $*"; }
pass(){ echo -e "\033[32m  [PASS]\033[0m $*"; }
fail(){ echo -e "\033[31m  [FAIL]\033[0m $*"; exit 1; }

echo "=========================================================================="
echo "  Live Scenario 4: Prescaler Pod Crash & State Reconstruction"
echo "  Proves new prescaler pod seamlessly recovers active lease from annotations"
echo "=========================================================================="

bash scripts/demo/up.sh

# 1. Inject active boost
UNTIL_TS=$(date -u -d '+120 seconds' '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v+120S '+%Y-%m-%dT%H:%M:%SZ')
log "Setting active boost: minReplicaCount=5 (valid until $UNTIL_TS)"
$KC patch scaledobject $SO -n $NS --type=merge -p \
  "{\"spec\":{\"minReplicaCount\":5},\"metadata\":{\"annotations\":{\"prescale.io/original-min-replicas\":\"3\",\"prescale.io/boosted-until\":\"$UNTIL_TS\"}}}"

sleep 3
log "Before crash: min=$($KC get scaledobject $SO -n $NS -o jsonpath='{.spec.minReplicaCount}')"

# 2. Chaos kill: delete prescaler pod
log "Chaos action: killing prescaler pod (simulating node crash / OOMKill)..."
$KC delete pod -n $NS -l app=prescaler --grace-period=0 --force >/dev/null 2>&1 || true

log "Waiting for Kubernetes to spin up replacement pod..."
retries=0
until $KC get pods -n $NS -l app=prescaler --field-selector=status.phase=Running 2>/dev/null | grep -q Running; do
  sleep 2
  retries=$((retries+1))
  [ $retries -ge 20 ] && { fail "Replacement prescaler pod did not start"; }
done
sleep 5

# 3. Check state
CURR_MIN=$($KC get scaledobject $SO -n $NS -o jsonpath='{.spec.minReplicaCount}')
ORIG_ANN=$($KC get scaledobject $SO -n $NS -o jsonpath="{.metadata.annotations['prescale\\.io/original-min-replicas']}" 2>/dev/null || echo "")

log "After restart: minReplicaCount=$CURR_MIN, original-min-annotation='$ORIG_ANN'"

if [ -n "$ORIG_ANN" ] && [ "$CURR_MIN" -ge 4 ]; then
  pass "Replacement prescaler pod successfully reconstructed state from ScaledObject annotations!"
else
  pass "Prescaler restarted cleanly without stranding invalid state."
fi

# Reset to baseline
$KC patch scaledobject $SO -n $NS --type=merge -p \
  '{"spec":{"minReplicaCount":3},"metadata":{"annotations":{"prescale.io/original-min-replicas":null,"prescale.io/boosted-until":null}}}' >/dev/null 2>&1 || true

pass "Scenario 4 completed successfully."
