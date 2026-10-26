#!/usr/bin/env bash
# Collects cluster ground-truth for Part 4D (step 1c).
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"
source scripts/measure/evidence.sh
KC="kubectl --context kind-prescale-demo"
OUT="docs/reports/data/part4d/11_cluster.txt"
: > "$OUT"

run $KC get nodes -o wide
run $KC get pods -A -o wide
run $KC get pods -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{" "}{range .spec.containers[*]}{.image}{" "}{end}{"\n"}{end}'
run $KC get deploy,sts,ds -A
run $KC top nodes
run $KC top pods -A
run $KC get scaledobject -A -o yaml
run $KC get hpa -A -o yaml
run $KC get deploy checkout -n baseline -o yaml
run $KC get deploy checkout -n predictive -o yaml

# Scrape configmaps
printf '$ kubectl get cm -A | grep -iE scrape|vm|prometheus\n' | tee -a "$OUT"
$KC get cm -A 2>&1 | grep -iE 'scrape|vm|prometheus' | tee -a "$OUT"
printf '[exit %d]\n\n' "${PIPESTATUS[0]}" | tee -a "$OUT"

# Prescaler deploy check
printf '$ kubectl get deploy -A | grep -i prescal\n' | tee -a "$OUT"
$KC get deploy -A 2>&1 | grep -i prescal | tee -a "$OUT" || true
printf '[exit done]\n\n' | tee -a "$OUT"

# vmagent pod check
printf '$ kubectl get pods -A | grep -i vmagent\n' | tee -a "$OUT"
$KC get pods -A 2>&1 | grep -i vmagent | tee -a "$OUT" || true
printf '[exit done]\n\n' | tee -a "$OUT"

# Observability namespace
printf '$ kubectl get pods -A | grep observ\n' | tee -a "$OUT"
$KC get pods -A 2>&1 | grep -i observ | tee -a "$OUT" || true
printf '[exit done]\n\n' | tee -a "$OUT"
