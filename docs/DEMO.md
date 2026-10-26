# prescale — Demo Guide

> **Forecaster**: The stand-in oracle predictor (`prescale-predictor:0.1.1`) is used.
> It emits a pre-scheduled surge at a fixed time offset; it is **not** a trained ML model.

## Prerequisites

| Requirement | Check |
|---|---|
| `kind` cluster `prescale-demo` | `kubectl --context kind-prescale-demo cluster-info` |
| Docker available | `docker info` |
| Images loaded | `prescale/prescaler:dev`, `prescale-predictor:0.1.1` in kind |
| k6 image tag | `cat loadtest/K6_IMAGE` |
| `baseline` namespace | checkout deploy + ScaledObject (HPA-only) |
| `predictive` namespace | checkout deploy + ScaledObject + prescaler deploy |

## One-command demo

```bash
# 1. Start predictor + prescaler
bash scripts/demo/up.sh

# 2. Run A/B load test (baseline HPA vs predictive HPA+prescaler)
bash scripts/demo/run.sh [RUN_ID]   # default: D1

# 3. Tear down
bash scripts/demo/down.sh
```

## What `up.sh` does

1. Starts `prescale-predictor:0.1.1` container on Docker network `kind` at `172.19.0.100:8000`
2. Scales `deploy/prescaler` in `predictive` namespace to 1 replica
3. Waits for both pods to be Running

## What `run.sh` does

Thin wrapper around `scripts/load/ab_run.sh`. Runs concurrent k6 load against:
- `baseline` namespace (HPA only)
- `predictive` namespace (HPA + prescaler)

Profile: `NO_REUSE=1`, `PEAK_RPS=490`, surge matches the oracle predictor's schedule.

Output saved to `docs/reports/data/part7/`.

## What `down.sh` does

1. Scales prescaler to 0
2. Releases any active boost (clears ScaledObject annotations)
3. Stops and removes the predictor container
4. Leaves the kind cluster intact

## Known limits (from Part 5B / Part 7)

These are reported; not investigated in this demo:

- Per-pod RPS and CPU metrics do not reconcile with k6 client-side RPS
- k6 drops iterations at `PEAK_RPS=490` with `NO_REUSE=1`
- k6 p99 latency improvement may be modest (prescaler boosts minimum pods before surge)

## Failure behavior

The prescaler releases a boost automatically when:

| Trigger | Mechanism |
|---|---|
| Predictor unreachable × 3 ticks | `consecutiveFailures >= maxFailures` → `action=release` |
| Boost TTL expired | `now >= boostedUntil` → `action=release` |
| Desired ≤ original min | Demand dropped → `action=release` |
| Pod restart | New pod reads annotations; releases if lease expired |

To test failure behavior: `bash scripts/failure_test.sh`

## Grafana dashboard

Import: `scripts/grafana_import.sh`  
Dashboard: `deploy/grafana/prescale-demo.json`

## Directory layout

```
scripts/demo/
  up.sh           Start predictor + prescaler
  run.sh          Run A/B load test
  down.sh         Tear down demo state
scripts/
  failure_test.sh  F1-F5 failure scenario tests
  load/ab_run.sh   Core A/B concurrent k6 runner
  grafana_import.sh  Grafana dashboard import
loadtest/
  surge_demo.js    k6 script (surge profile)
  K6_IMAGE         k6 Docker image tag
  slo.env          SLO thresholds (p99, error%)
docs/
  DEMO.md          This file
  BACKUP.md        Backup/restore cluster state
  reports/         All part reports + raw data
predictor/
  server.py        Stand-in oracle predictor source
  Dockerfile
deploy/prescaler/  Kubernetes manifests (deployment, rbac, etc.)
```
