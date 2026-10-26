#!/usr/bin/env bash
# scripts/demo/down.sh – Tear down demo state (prescaler, predictor container).
# Usage: bash scripts/demo/down.sh
#
# What this does:
#   1. Scale prescaler to 0
#   2. Release any active boost (reset ScaledObject annotations)
#   3. Stop and remove predictor container
#   4. Leave the kind cluster intact (never kind delete)

set -euo pipefail
KC="kubectl --context kind-prescale-demo"
NS=predictive
SO=checkout
PREDICTOR_NAME=prescale-predictor

log(){ echo "[$(date -u +%H:%M:%S)] $*"; }

log "=== demo/down: cleaning up demo state ==="

# 1. Scale prescaler to 0
log "Scaling prescaler to 0..."
$KC scale deploy/prescaler -n $NS --replicas=0 2>/dev/null || true

# 2. Release any active boost
ORIG_ANN=$($KC get scaledobject $SO -n $NS \
  -o jsonpath="{.metadata.annotations['prescale\.io/original-min-replicas']}" 2>/dev/null || echo "")
if [ -n "$ORIG_ANN" ]; then
  log "Releasing active boost (original min was $ORIG_ANN)..."
  $KC patch scaledobject $SO -n $NS --type=merge -p \
    '{"spec":{"minReplicaCount":3},"metadata":{"annotations":{"prescale.io/original-min-replicas":null,"prescale.io/boosted-until":null}}}' \
    2>/dev/null || true
else
  log "No active boost to release."
fi

# 3. Stop predictor container
if docker inspect $PREDICTOR_NAME &>/dev/null; then
  log "Stopping and removing predictor container..."
  docker rm -f $PREDICTOR_NAME 2>/dev/null || true
else
  log "Predictor container not found."
fi

# 4. Clean up any k6 containers left from a prior run
docker rm -f k6-ab-baseline k6-ab-predictive 2>/dev/null || true

log ""
log "=== demo/down complete ==="
log "  Prescaler   : 0 replicas"
log "  Boost       : released"
log "  Predictor   : stopped"
log "  Kind cluster: intact (NOT deleted)"
