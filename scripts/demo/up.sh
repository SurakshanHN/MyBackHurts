#!/usr/bin/env bash
# scripts/demo/up.sh – Bring up all demo prerequisites.
# Usage: bash scripts/demo/up.sh
#
# Prerequisites:
#   - kind cluster "prescale-demo" exists (do NOT create/delete)
#   - Docker available
#   - prescale/prescaler:dev image loaded in kind
#   - prescale-predictor:0.1.1 image built locally
#
# What this does:
#   1. Start the stand-in predictor container on the kind network (172.19.0.100)
#   2. Scale the prescaler deployment to 1 replica
#   3. Wait for everything to be Ready
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KC="kubectl --context kind-prescale-demo"
NS=predictive
PREDICTOR_NAME=prescale-predictor
PREDICTOR_IP=172.19.0.100
PREDICTOR_IMAGE=prescale-predictor:0.1.1

log(){ echo "[$(date -u +%H:%M:%S)] $*"; }

log "=== demo/up: starting prerequisites ==="

# 1. Start predictor container
if docker inspect $PREDICTOR_NAME &>/dev/null && \
   [ "$(docker inspect -f '{{.State.Running}}' $PREDICTOR_NAME 2>/dev/null)" = "true" ]; then
  log "Predictor container already running."
else
  docker rm -f $PREDICTOR_NAME 2>/dev/null || true
  log "Starting predictor container ($PREDICTOR_IMAGE) at $PREDICTOR_IP..."
  docker run -d \
    --name $PREDICTOR_NAME \
    --network kind \
    --ip $PREDICTOR_IP \
    $PREDICTOR_IMAGE
  sleep 3
fi

# Smoke-test predictor
PRED_RESP=$(curl -sf http://$PREDICTOR_IP:8000/predict \
  -H 'Content-Type: application/json' \
  -d '{"window":[60,60,60,60,60]}' 2>/dev/null) || true
if [ -n "$PRED_RESP" ]; then
  log "Predictor health check OK: $PRED_RESP"
else
  log "WARNING: predictor not responding yet (may need more time)"
fi

# 2. Scale prescaler to 1
log "Scaling prescaler to 1..."
$KC scale deploy/prescaler -n $NS --replicas=1

# 3. Wait for prescaler pod Running
log "Waiting for prescaler pod..."
retries=0
until $KC get pods -n $NS -l app=prescaler --field-selector=status.phase=Running 2>/dev/null | grep -q Running; do
  sleep 3; retries=$((retries+1))
  [ $retries -ge 30 ] && { log "ERROR: Prescaler pod not running after 90s"; exit 1; }
done

# 4. Wait for checkout deployment ready
log "Waiting for checkout deployment to be ready (min 3)..."
$KC rollout status deploy/checkout -n $NS --timeout=120s

log ""
log "=== demo/up complete ==="
log "  Predictor : http://$PREDICTOR_IP:8000/predict (oracle/stand-in)"
log "  Prescaler : $($KC get pods -n $NS -l app=prescaler -o name 2>/dev/null | head -1)"
log "  Checkout  : $($KC get deploy checkout -n $NS -o jsonpath='{.status.readyReplicas}') ready replicas"
log ""
log "Next: bash scripts/demo/run.sh"
