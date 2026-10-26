# prescale

**prescale** is a Kubernetes operator that protects your workloads from harmful Horizontal Pod Autoscaler (HPA) scale-ups by evaluating their actual impact on latency, error rates, and throughput.

When an HPA scales up a Deployment, prescale correlates the scaling event with Prometheus metrics to determine if the scale-up **helped**, was **useless**, or **harmed** performance. In `enforce` mode, it can automatically cap the HPA's `maxReplicas` to prevent repeated harmful scale-ups.

## How It Works

1. **Watch** — prescale monitors `ScaleGuard` resources, each linked to an HPA by name
2. **Detect** — On each HPA scale-up, prescale queries Prometheus (via the configured `queries`) for a time window around the event
3. **Evaluate** — The detector classifies the scale-up as:
   - **helped** — Latency/error rate improved or stayed stable
   - **useless** — No measurable improvement (wasted resources)
   - **harmful** — Latency/error rate degraded significantly
   - **unknown** — Insufficient data to decide
4. **Act** — In `enforce` mode, harmful scale-ups trigger an HPA `maxReplicas` cap (with a configurable floor and max freeze duration). In `observe` mode, only records the decision.
5. **Record** — Every evaluation creates a `ScaleDecision` CR for audit trail and dashboards.

## Core Concepts

### ScaleGuard (CRD)
The primary configuration resource. Each `ScaleGuard` binds to one HPA and defines:
- **Mode**: `observe` (record only) or `enforce` (cap HPA on harmful scale-ups)
- **Prometheus URL**: Where to query metrics
- **Queries**: PromQL for RPS, P99 latency, and error rate
- **Dependencies**: Optional downstream services to check for saturation (e.g., database, cache)
- **Warmup Window**: Time to wait after scale-up before evaluating (default: 2m)
- **Max Freeze Duration**: How long to keep a cap before auto-restore (default: 30m)
- **Min Replicas Floor**: Safety floor — caps never go below this

### ScaleDecision (CRD)
Immutable audit record created for every scale-up evaluation:
- Verdict (`helped` / `useless` / `harmful` / `unknown`)
- Confidence score (0–1)
- From/to replica counts
- Culprit dependency (if any)
- Human-readable reasons
- Action taken (`capped` / `restored` / `observed`)

## Quickstart

### Prerequisites
- Go 1.24+
- Docker 17.03+
- kubectl 1.11.3+
- Kubernetes 1.11.3+ cluster with HPA and Prometheus

### Deploy

```bash
# Build and push the controller image
export IMG=<your-registry>/prescale:v0.1.0
make docker-build docker-push IMG=$IMG

# Install CRDs
make install

# Deploy the controller
make deploy IMG=$IMG
```

### Create a ScaleGuard

```yaml
# config/samples/autoscaling_v1alpha1_scaleguard.yaml
apiVersion: autoscaling.prescale.io/v1alpha1
kind: ScaleGuard
metadata:
  name: my-app-guard
  namespace: production
spec:
  hpaName: my-app-hpa                    # Must match existing HPA name
  mode: enforce                          # or "observe"
  prometheusURL: "http://prometheus:9090"
  queries:
    rps: 'sum(rate(http_requests_total{namespace="production",service="my-app"}[1m]))'
    p99Millis: 'histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{namespace="production",service="my-app"}[5m])) by (le)) * 1000'
    errorRate: 'sum(rate(http_requests_total{namespace="production",service="my-app",code=~"5.."}[5m])) / sum(rate(http_requests_total{namespace="production",service="my-app"}[5m]))'
  dependencies:
    - name: postgres
      saturationQuery: 'pg_stat_database_max_connections / pg_settings_max_connections * 100'
      thresholdPercent: 85
  warmupWindow: 2m
  maxFreezeDuration: 30m
  minReplicasFloor: 2
```

Apply it:
```bash
kubectl apply -f config/samples/autoscaling_v1alpha1_scaleguard.yaml
```

### Verify

```bash
# Watch ScaleGuard status
kubectl get scaleguard my-app-guard -n production -w

# See recorded decisions
kubectl get scaledecision -n production
```

## Configuration Reference

### ScaleGuard Spec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `hpaName` | string | Yes | Name of the HPA to guard (same namespace) |
| `mode` | enum (`observe`, `enforce`) | Yes | Observe only, or actively cap HPA |
| `prometheusURL` | string | Yes | Prometheus query endpoint |
| `queries.rps` | string | Yes | PromQL returning requests/second |
| `queries.p99Millis` | string | Yes | PromQL returning P99 latency in milliseconds |
| `queries.errorRate` | string | Yes | PromQL returning error rate (0–1) |
| `dependencies[]` | array | No | Downstream services to check for saturation |
| `dependencies[].name` | string | Yes | Identifier for the dependency |
| `dependencies[].saturationQuery` | string | Yes | PromQL returning saturation percentage (0–100) |
| `dependencies[].thresholdPercent` | int | Yes | Saturation % above which the dependency is a culprit |
| `warmupWindow` | Duration | Yes | Time to wait post-scale-up before evaluating (e.g., `2m`) |
| `maxFreezeDuration` | Duration | Yes | Max time to keep a cap before auto-restore (e.g., `30m`) |
| `minReplicasFloor` | int32 | Yes | Safety floor — caps never reduce maxReplicas below this |

### ScaleGuard Status

| Field | Description |
|-------|-------------|
| `phase` | Current phase: `Evaluating`, `Capped`, `Restored`, `Idle` |
| `lastVerdict` | Most recent verdict: `helped`, `useless`, `harmful`, `unknown` |
| `lastConfidence` | Confidence of last verdict (0–1) |
| `lastMessage` | Human-readable summary |
| `cap` | Active cap details (if any): `originalMaxReplicas`, `cappedAt`, `since`, `reason` |
| `trustScore` | Running trust score (0–100) — increases on `helped`, decreases on `harmful` |
| `conditions` | Standard Kubernetes conditions (`Ready`, `PrometheusReachable`, `HPAExists`) |

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   HPA       │────▶│  ScaleGuard │────▶│  Prometheus │
│  (scales up)│     │ Controller  │     │  (metrics)  │
└─────────────┘     └──────┬──────┘     └─────────────┘
                           │
                    ┌──────▼──────┐
                    │  Detector   │  (helped / useless / harmful / unknown)
                    └──────┬──────┘
                           │
                    ┌──────▼──────┐     ┌─────────────┐
                    │  Actuator   │────▶│    HPA      │
                    │ (cap/restore)│     │ (maxReplicas)│
                    └─────────────┘     └─────────────┘
                           │
                    ┌──────▼──────┐
                    │ ScaleDecision│  (audit record)
                    └─────────────┘
```

### Detector (`internal/detector/`)
Implements the causal inference logic:
- Compares post-scale-up metrics against a counterfactual baseline (pre-scale-up trend)
- Checks dependency saturation to identify culprits
- Outputs verdict, confidence, culprit, and plain-English reasons

### Actuator (`internal/actuator/`)
Safely modifies HPA `maxReplicas`:
- Stores original `maxReplicas` in annotation (`prescale.io/original-max-replicas`)
- Applies cap with `minReplicasFloor` safety bound
- Auto-restores after `maxFreezeDuration`
- Idempotent and conflict-safe (re-fetches before update)

## Modes

| Mode | Behavior |
|------|----------|
| `observe` | Records `ScaleDecision` for every scale-up. Never modifies HPA. Use for validation and tuning. |
| `enforce` | Same as observe, **plus** caps HPA `maxReplicas` on `harmful` verdicts. Auto-restores after `maxFreezeDuration`. |

## Development

```bash
# Run unit tests (uses envtest)
make test

# Run locally against your cluster (uses ~/.kube/config)
make run

# Lint & format
make lint-fix

# Generate manifests & deepcopy code after API changes
make manifests generate
```

## Distribution

### YAML Bundle (Kustomize)
```bash
make build-installer IMG=<registry>/prescale:v0.1.0
# Outputs dist/install.yaml — users install with:
# kubectl apply -f dist/install.yaml
```

### Helm Chart
```bash
# Generate chart (first time)
kubebuilder edit --plugins=helm/v2-alpha

# Deploy via Helm (development)
make helm-deploy IMG=<registry>/prescale:v0.1.0

# Package for distribution
helm package dist/chart/
```

## RBAC

The controller requires permissions to:
- Read/write `ScaleGuard` and `ScaleDecision` (own CRDs)
- Read `HPA` and `Deployment` (to watch scale events and patch maxReplicas)
- Create `Events` (for visibility)
- Leader election (configmap/lease)

See `config/rbac/` for generated roles.

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.