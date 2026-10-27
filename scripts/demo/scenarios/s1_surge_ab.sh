#!/usr/bin/env bash
# scripts/demo/scenarios/s1_surge_ab.sh
# Live Scenario 1: A/B Load Test (Baseline Reactive HPA vs Predictive Prescaler)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"

RUN_ID="${1:-DEMO_$(date +%H%M%S)}"

echo "=========================================================================="
echo "  Live Scenario 1: Pre-Emptive Traffic Surge (A/B Test)"
echo "  Comparing Reactive KEDA/HPA (baseline) vs Predictive Prescale"
echo "=========================================================================="

# Ensure environment is up
bash scripts/demo/up.sh

echo ""
echo "Running concurrent k6 load test (Run ID: $RUN_ID)..."
bash scripts/demo/run.sh "$RUN_ID"

echo ""
echo "A/B Run $RUN_ID completed successfully."
