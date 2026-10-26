#!/usr/bin/env bash
# Collects VictoriaMetrics and HPA sync data for Part 4D (steps 1d-1e).
set -uo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"
source scripts/measure/evidence.sh

# Step 1d: HPA sync period
OUT="docs/reports/data/part4d/12_hpa_sync.txt"
: > "$OUT"
# grep exits 1 if no match; record that
docker exec prescale-demo-control-plane grep -n horizontal-pod-autoscaler \
    /etc/kubernetes/manifests/kube-controller-manager.yaml > "$OUT" 2>&1 \
  && printf '[exit 0]\n\n' >> "$OUT" \
  || printf '[exit %d (no match or error)]\n\n' "$?" >> "$OUT"

# Step 1e: VM flags and metrics
OUT="docs/reports/data/part4d/13_vm.txt"
: > "$OUT"

run curl -s 'localhost:8428/flags'

printf '$ curl -s localhost:8428/flags | grep latencyOffset\n' | tee -a "$OUT"
curl -s 'localhost:8428/flags' 2>&1 | grep 'latencyOffset' | tee -a "$OUT" || true
printf '[exit done]\n\n' | tee -a "$OUT"

printf '$ curl -s localhost:8428/flags | grep retentionPeriod\n' | tee -a "$OUT"
curl -s 'localhost:8428/flags' 2>&1 | grep 'retentionPeriod' | tee -a "$OUT" || true
printf '[exit done]\n\n' | tee -a "$OUT"

printf '$ curl -s localhost:8428/flags | grep promscrape\n' | tee -a "$OUT"
curl -s 'localhost:8428/flags' 2>&1 | grep 'promscrape' | tee -a "$OUT" || true
printf '[exit done]\n\n' | tee -a "$OUT"

run curl -s 'localhost:8428/api/v1/targets'

printf '$ curl targets | grep -c health.up\n' | tee -a "$OUT"
curl -s 'localhost:8428/api/v1/targets' 2>&1 | grep -c '"health":"up"' | tee -a "$OUT" || echo 0 | tee -a "$OUT"
printf '[exit done]\n\n' | tee -a "$OUT"

printf '$ curl targets | grep -c health.down\n' | tee -a "$OUT"
curl -s 'localhost:8428/api/v1/targets' 2>&1 | grep -c '"health":"down"' | tee -a "$OUT" || echo 0 | tee -a "$OUT"
printf '[exit done]\n\n' | tee -a "$OUT"

printf '$ metric names filtered\n' | tee -a "$OUT"
curl -s 'localhost:8428/api/v1/label/__name__/values' 2>&1 | tr ',' '\n' | \
    grep -iE 'requests_total|http_request|db_' | tee -a "$OUT" || true
printf '[exit done]\n\n' | tee -a "$OUT"

# Full metric names list
run curl -s 'localhost:8428/api/v1/label/__name__/values'

# Scrape config from the ConfigMap found in step 1c
KC="kubectl --context kind-prescale-demo"
OUT="docs/reports/data/part4d/13_vm.txt"
printf '$ kubectl get cm vmsingle-scrape-config -n monitoring -o yaml\n' | tee -a "$OUT"
$KC get cm vmsingle-scrape-config -n monitoring -o yaml 2>&1 | tee -a "$OUT" || true
printf '[exit done]\n\n' | tee -a "$OUT"
