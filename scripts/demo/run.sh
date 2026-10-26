#!/usr/bin/env bash
# scripts/demo/run.sh – Run one full A/B demo (baseline vs predictive).
# Usage: bash scripts/demo/run.sh [RUN_ID]
#
# Requires:
#   - demo/up.sh already run
#   - baseline namespace with HPA-only checkout + scaledobject
#   - predictive namespace with prescaler running
#
# Known limits (from Part 5B / Part 7, not investigated here):
#   - Per-pod RPS and CPU metrics do not reconcile with k6 client-side RPS
#   - k6 drops iterations at PEAK_RPS=490 with NO_REUSE=1
#   - These are expected; do not investigate in demo

set -uo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

RUN_ID="${1:-D1}"
bash scripts/load/ab_run.sh "$RUN_ID"
