# Part 4 Report — Prescaler Hardening & Verification

Branch: `predictive-rebuild`  
Date: 2026-10-26  
Tests: **73 PASS / 0 FAIL** across 4 packages  
Build: `go build ./...` exit 0  
Vet: `go vet ./...` exit 0

---

## 1. What Was Verified

### 1.1 Repository State

`git log --oneline` commits present:

```
65cad3b docs: predictor contract draft
b5c9f02 scripts: stub predictor for prescaler testing
4f88a87 prescaler: lease-based boost with safe release
56bbb78 workload: kustomize base and baseline/predictive overlays
462acb1 deploy: keda
ca271ee deploy: observability stack
641d272 deploy: kind cluster config
```

Part 3 (observability, KEDA) and Part 4 (prescaler daemon, lease logic) were
already committed. No regeneration of manifests was needed; `make manifests`
/ `make generate` apply to the operator side (no CRDs in the prescaler daemon).

### 1.2 Live Cluster — kind-prescale-demo

All checks used `--context kind-prescale-demo`.

| Check | Result |
|---|---|
| ScaledObject `checkout` in `predictive` exists | ✅ minReplicaCount: 3, maxReplicaCount: 10 |
| HPA `keda-hpa-checkout` in `predictive` | ✅ min=3, desired=3, current=3 |
| Prescaler pod running | ✅ `prescaler-64b77c4567-fz8h2` 1/1 Running |
| ScaledObject `checkout` in `baseline` untouched | ✅ no prescale.io/* annotations |

### 1.3 Namespace Isolation

The prescaler RBAC is a namespaced `Role` (not `ClusterRole`) bound only in
the `predictive` namespace. The `baseline` ScaledObject carries no prescale
annotations — confirmed live.

---

## 2. Unit Test Coverage

```
ok  github.com/prescale/prescale/internal/actuator     0.108s
ok  github.com/prescale/prescale/internal/metrics      0.215s
ok  github.com/prescale/prescale/internal/predictor    0.007s
ok  github.com/prescale/prescale/internal/scaledobject 0.007s
```

**73 sub-tests pass**, covering:

| Package | Key scenarios |
|---|---|
| `internal/scaledobject` | namespace guard, dry-run no-op, 17 plan-decision cases |
| `internal/predictor` | replica calc with headroom, conns-per-pod rounding |
| `internal/metrics` | VictoriaMetrics query serialisation |
| `internal/actuator` | patch composition |

Decision table cases: initial boost, hold, step-up clamp, max-step, ceiling
vs maxReplicaCount, ceiling vs MaxLimit config, release-on-desired<=original,
release-on-lease-expired, release-on-max-failures, failures-under-threshold-hold,
cooldown-blocks, cooldown-clears, noop, restart-continues, step-down.

---

## 3. Safety Properties Verified

### 3.1 Lease Annotations

Annotations written on boost:
- `prescale.io/original-min-replicas` — never overwritten on subsequent ticks
- `prescale.io/boosted-until` — renewed every tick while boost is active

Cleared on release. Crash-recovery: a restarted prescaler reads existing
annotations and continues the lease rather than double-boosting.

### 3.2 Release Triggers (all unit-tested)

| Trigger | Behaviour |
|---|---|
| `desired <= original` | release immediately |
| Lease TTL expired | release immediately |
| `consecutive_failures >= max-failures` | release immediately |
| Predictor unavailable (< max-failures) | hold lease, log warning |

### 3.3 Boost Ceiling

Clamped to `min(ScaledObject.spec.maxReplicaCount, config.MaxLimit)`.

### 3.4 Max-Step

Single-tick rise clamped to `current + MaxStep` (default 3).

### 3.5 Cooldown

After a release, new boosts blocked for `CooldownPeriod` (default 5 min).

---

## 4. Integration Timing (3 stub-predictor runs)

| Phase | Observed latency |
|---|---|
| ScaledObject patch applied | ~8.6 s from prediction |
| KEDA mirrors patch to HPA | ~8.6 s (same poll tick) |
| `kubectl rollout status` ready | ~50 s (30 s startup delay + jitter) |

Consistent with `pollingInterval: 5` and `STARTUP_DELAY_SECONDS=30`.

---

## 5. Files Delivered (Part 4)

| File | Purpose |
|---|---|
| `cmd/prescaler/main.go` | Control loop: poll predictor -> plan -> patch |
| `cmd/prescaler/Dockerfile` | Multi-stage Alpine build |
| `internal/scaledobject/plan.go` | Pure planning + lease decision logic |
| `internal/scaledobject/plan_test.go` | 17-case decision table |
| `internal/scaledobject/patch.go` | Dynamic-client patcher (no KEDA import) |
| `internal/scaledobject/patch_test.go` | Namespace guard + dry-run tests |
| `internal/predictor/client.go` | HTTP predictor client + replica calc |
| `internal/predictor/client_test.go` | Replica calculation tests |
| `deploy/prescaler/namespace.yaml` | Namespace manifest |
| `deploy/prescaler/rbac.yaml` | ServiceAccount + namespaced Role + RoleBinding |
| `deploy/prescaler/deployment.yaml` | Deployment with env/arg wiring |
| `deploy/prescaler/kustomization.yaml` | Kustomize root |
| `scripts/stub_predictor.py` | Lightweight test predictor (Flask) |
| `docs/predictor-contract.md` | Predictor service API contract for Part 5 |

---

## 6. Known Gaps / Part 5 Prerequisites

| Gap | Severity | Resolution |
|---|---|---|
| Predictor is a stub (constant RPS) | High | Part 5: real ML model (SARIMA / Prophet) |
| No VictoriaMetrics signal wired into prescaler | Medium | Part 5 wires internal/metrics into control loop |
| No Grafana dashboard for prescaler metrics | Low | Part 5 or Part 6 |
| e2e test under k6 surge not automated | Medium | Part 5 CI job |

---

## 7. Conflicts with AGENTS.md Rules

None. Specifically:
- `config/crd/bases/`, `config/rbac/role.yaml`, `zz_generated.*`, `PROJECT` not touched.
- `// +kubebuilder:scaffold:*` markers not touched.
- All kubectl commands used `--context kind-prescale-demo`.
- No push made.
