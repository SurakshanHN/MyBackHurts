# prescale – AI Agent Guide

> **Updated Part 9**: Kubebuilder scaffold was removed in Part 2.
> This project is a hand-written Go daemon, not a controller-manager.
> The Kubebuilder sections below are stale and do not apply.

## Project Structure

```
cmd/prescaler/main.go          Daemon entry point (flags, ticker loop)
internal/
  metrics/                     VictoriaMetrics query client
  predictor/                   HTTP client for the prediction service
  scaledobject/
    client.go                  KEDA ScaledObject read/patch via dynamic client
    plan.go                    Pure planning function (PlanDecision): no I/O
predictor/
  server.py                    Stand-in oracle predictor (Python stdlib HTTPServer)
  Dockerfile                   Image: prescale-predictor:0.1.1
deploy/
  prescaler/                   Kubernetes manifests (Deployment, RBAC, etc.)
  grafana/                     Grafana dashboard JSON
  keda/                        KEDA install manifests
  observability/               VictoriaMetrics kustomize
  kind/                        kind cluster config
loadtest/
  surge_demo.js                k6 load test (surge profile matching oracle)
  surge.js                     k6 raw surge script
  capacity.js                  k6 capacity test
  K6_IMAGE                     k6 Docker image tag (no :latest)
  slo.env                      SLO: SLO_P99_MS, SLO_ERR_PCT
scripts/
  demo/up.sh                   Start predictor + prescaler
  demo/run.sh                  Run A/B load test (wraps ab_run.sh)
  demo/down.sh                 Tear down demo state
  load/ab_run.sh               Concurrent A/B k6 runner
  failure_test.sh              F1-F5 failure scenario tests
  grafana_import.sh            Import Grafana dashboard via API
docs/
  DEMO.md                      One-command demo guide
  BACKUP.md                    Backup / restore guide
  reports/                     Per-part reports + raw data
```

## Critical Rules (current state)

- **No Kubebuilder scaffold**: No CRDs, no controller-manager, no `make manifests/generate`.
- **No PROJECT file** and no `config/crd/` — removed in Part 2.
- **Never edit**: auto-generated files that no longer exist (nothing to protect here).
- **Kind cluster**: `prescale-demo` only — never `prescale-dev`. Never kind create/delete.
- **Every kubectl**: must use `--context kind-prescale-demo`.
- **Images**: no `:latest`. Use `prescale/prescaler:dev` and `prescale-predictor:0.1.1`.
- **No Helm**.

## After Making Changes

```bash
# After editing Go files
go build ./...        # verify compilation
make test             # run unit tests (uses standard go test, no envtest)
make lint-fix         # auto-fix code style

# After editing deploy/ manifests
kubectl --context kind-prescale-demo apply -k deploy/prescaler/

# After editing predictor/server.py
docker build -t prescale-predictor:0.1.1 predictor/
kind load docker-image prescale-predictor:0.1.1 --name prescale-demo  # if needed in-cluster
```

## Prescaler Daemon Design

### Flags (`cmd/prescaler/main.go`)

| Flag | Default | Purpose |
|---|---|---|
| `--victoria-url` | — | VictoriaMetrics query endpoint |
| `--predictor-url` | `http://predictor.predictive.svc.cluster.local:8000` | Stand-in predictor |
| `--namespace` | — | Namespace of the ScaledObject |
| `--scaled-object` | — | Name of the ScaledObject |
| `--interval` | `15s` | Tick interval |
| `--window` | `5m` | Historical window sent to predictor |
| `--min-replicas` | `3` | Baseline minimum |
| `--max-replicas` | `10` | Safety ceiling |
| `--conns-per-pod` | `80` | RPS→replicas ratio |
| `--headroom` | `1.0` | Headroom multiplier on desired |
| `--boost-ttl` | `60s` | Lease duration per tick |
| `--max-failures` | `3` | Consecutive failure threshold |
| `--max-step` | `3` | Max replicas raised per tick |
| `--cooldown` | `30s` | Cooldown after release |
| `--dry-run` | `false` | Log only, no patches |

### Planning Logic (`internal/scaledobject/plan.go`)

`PlanDecision(state, cfg, now, desired, tickErr)` is a pure function:

1. **tick error** → increment `consecutiveFailures`; if `>= maxFailures` and boosted → release
2. **lease expired** → release
3. **desired ≤ original** → release (demand dropped)
4. **cooldown** → noop
5. **desired > original** → raise (clamped by ceiling and maxStep), renew lease TTL

### Predictor Contract (`docs/predictor-contract.md`)

```
POST /predict
Content-Type: application/json
{"window": [<rps_float>, ...]}   ← 5-minute window of RPS samples

200 OK
{"predicted_rps": <float>, "horizon_seconds": <int>, ...}
```

The stand-in oracle (`prescale-predictor:0.1.1`) accepts this shape and returns a
pre-scheduled surge profile. It is **not** a trained ML model.

## Cluster Layout

```
kind cluster: prescale-demo
  namespace: predictive      ← main demo namespace
    deploy/checkout          ← workload (HPA + prescaler)
    deploy/fakedb
    deploy/prescaler         ← the prescaler daemon
    scaledobject/checkout    ← KEDA SO, minReplicaCount patched by prescaler
  namespace: baseline        ← A/B comparison (HPA only, no prescaler)
    deploy/checkout
    deploy/fakedb
    scaledobject/checkout
  namespace: monitoring
    VictoriaMetrics (vmsingle)
    Grafana
  namespace: keda
    KEDA operator
```

## Deployment Workflow

```bash
# 1. Build and load prescaler image
docker build -t prescale/prescaler:dev .
kind load docker-image prescale/prescaler:dev --name prescale-demo

# 2. Apply manifests
kubectl --context kind-prescale-demo apply -k deploy/prescaler/

# 3. Run demo
bash scripts/demo/up.sh
bash scripts/demo/run.sh D1
bash scripts/demo/down.sh
```

## Testing

```bash
make test        # unit tests (plan.go has table-driven tests)
make lint-fix    # golangci-lint auto-fix
```

## Logging conventions

Follow Kubernetes logging style (same as original AGENTS.md):
- Capital letter start, no trailing period
- Active voice: `"Could not patch ScaledObject"`
- Specify object: `"Released boost"` not `"Released"`
- Balanced key-value pairs: `log.Info("Released boost", "object", name, "from", old, "to", new)`

## References

- controller-runtime ScaledObject dynamic client pattern: `internal/scaledobject/client.go`
- KEDA ScaledObject API: https://keda.sh/docs/latest/concepts/scaling-deployments/
- VictoriaMetrics instant query: `http://<vm>:8428/api/v1/query`
- k6 docs: https://grafana.com/docs/k6/
