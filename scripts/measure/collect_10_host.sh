#!/usr/bin/env bash
# Collects host ground-truth for Part 4D (step 1b).
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"
source scripts/measure/evidence.sh
OUT="docs/reports/data/part4d/10_host.txt"
: > "$OUT"

run which -a go
run go version
run go env GOTOOLCHAIN GOROOT
run head -5 go.mod
run free -h
run grep -E 'MemTotal|MemAvailable' /proc/meminfo
run nproc
run docker info --format '{{.MemTotal}} {{.NCPU}}'
run docker ps --format '{{.Names}} {{.Image}} {{.Status}}'
run kind get clusters
