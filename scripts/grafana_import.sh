#!/usr/bin/env bash
# scripts/grafana_import.sh – Idempotent import of prescale-demo dashboard to Grafana
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

GRAFANA_PORT=3000
GRAFANA_URL="http://127.0.0.1:${GRAFANA_PORT}"
PF_PID=""

cleanup() {
    if [ -n "$PF_PID" ]; then
        kill "$PF_PID" 2>/dev/null || true
        wait "$PF_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

# Verify connectivity to Grafana; start port-forward if not already listening
if ! curl -s --max-time 2 "${GRAFANA_URL}/api/health" | grep -q '"database":\s*"ok"'; then
    echo "Starting port-forward to svc/grafana on port 33000..."
    kubectl --context kind-prescale-demo port-forward svc/grafana -n monitoring 33000:3000 > /dev/null 2>&1 &
    PF_PID=$!
    GRAFANA_URL="http://127.0.0.1:33000"
    for i in {1..30}; do
        if curl -s --max-time 1 "${GRAFANA_URL}/api/health" | grep -q '"database":\s*"ok"'; then
            break
        fi
        sleep 1
    done
fi

# Build payload with overwrite=true
PAYLOAD=$(python3 -c '
import json
with open("deploy/grafana/dashboard.json") as f:
    dash = json.load(f)
print(json.dumps({
    "dashboard": dash,
    "overwrite": True,
    "message": "Imported via scripts/grafana_import.sh"
}))
')

mkdir -p docs/reports/data/part8
RESP=$(curl -s -X POST "${GRAFANA_URL}/api/dashboards/db" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json" \
    -d "$PAYLOAD")

echo "$RESP" | tee docs/reports/data/part8/import.txt
