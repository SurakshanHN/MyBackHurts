# REPO_CONTEXT.md

*Generated: 2026-10-26*

---

## 1. Overview

This repository contains **prescale**, a Go-based Kubernetes operator built with Kubebuilder (v4.16.0). It watches HPA scale-up events, queries Prometheus for metrics around the event, evaluates whether the scale-up **helped**, was **useless**, **harmful**, or **unknown** using a counterfactual baseline (ordinary least-squares linear regression on pre-scale P99 latency and error rate), and in `enforce` mode caps the HPA's `maxReplicas` to prevent repeated harmful scale-ups. Every evaluation is recorded as a `ScaleDecision` CR.

**Critical discrepancy with project background:** The background describes an "ML predictive autoscaler" with a trained `.pkl` model that *predicts* traffic surges *before* they happen. **This repo contains no Python, no `.pkl` files, no `requirements.txt`, no training scripts, and no ML model.** The detection logic is purely heuristic (statistical), implemented in Go, and runs *after* a scale-up to evaluate its impact — not before. The nested `prescale/prescale/` directory is a near-duplicate of the project (stale/diverged).

---

## 2. Directory Tree (annotated)

```
/home/unhealthycoder/MyBackHurts/
├── .devcontainer/              # DevContainer config (VS Code Remote)
├── .github/workflows/          # CI workflows (lint.yml, test.yml, test-e2e.yml) — only in nested prescale/
├── api/
│   └── v1alpha1/
│       ├── groupversion_info.go      # Scheme registration
│       ├── scaledecision_types.go    # ScaleDecision CRD (verdict, confidence, report, action, from/to replicas)
│       ├── scaleguard_types.go       # ScaleGuard CRD spec (TargetHPA, Mode, TTL, Floor, WarmupSeconds, MinSamples, Step, Dependencies, thresholds)
│       └── zz_generated.deepcopy.go  # Auto-generated (DO NOT EDIT)
├── bin/                        # Committed test binaries (etcd, kube-apiserver, kubectl, controller-gen, setup-envtest)
├── cmd/
│   ├── main.go                 # Manager entry: registers ScaleGuardReconciler, webhooks, metrics, healthz
│   ├── prescale-demo/main.go   # CLI: runs 3 hardcoded synthetic scenarios, prints detector.Judge reports
│   ├── prescale-eval/main.go   # CLI: 5-arm evaluation harness (plain HPA, fixed cap, threshold guard, simple rule, PreScale) — 200 scenarios × 8 types = 1600 total
│   └── prescale-live/main.go   # CLI: queries real Prometheus, auto-detects scale-up, calls detector.Judge, writes CSV
├── config/
│   ├── crd/bases/              # Generated CRDs (DO NOT EDIT)
│   ├── default/                # Manager deployment + certs + metrics service
│   ├── manager/                # Manager deployment base
│   ├── network-policy/         # Allow metrics traffic
│   ├── prometheus/             # PodMonitor/ServiceMonitor for controller metrics
│   ├── rbac/                   # ClusterRoles/RoleBindings (manager-role, scaleguard_*, scaledecision_*)
│   └── samples/                # Example CRs (scaleguard-sample.yaml is a TODO placeholder)
├── demo/                       # Demo application (checkout + fakedb) — MOSTLY EMPTY STUBS
│   ├── checkout/               # main.go, Dockerfile, main_test.go — ALL 0 BYTES
│   ├── fakedb/                 # main.go, Dockerfile, main_test.go — ALL 0 BYTES
│   ├── k6/surge.js             # k6 load test script (ramping-arrival-rate 20→120→20 RPS, MODE=contention|healthy)
│   └── k8s/                    # checkout.yaml, fakedb.yaml, namespace.yaml — ALL 0 BYTES; monitoring.yaml exists (PodMonitor)
├── Dockerfile                  # Multi-stage: golang:1.26 builder → distroless static
├── go.mod / go.sum             # Go 1.26.0 module github.com/prescale/prescale
├── hack/boilerplate.go.txt     # License header for generated code
├── internal/
│   ├── actuator/
│   │   ├── actuator.go         # Cap/Restore/Expired logic; uses annotations prescale.io/original-max-replicas, capped-until, capped-by
│   │   ├── plan.go             # Pure planning logic: PlanCap (clamp to floor/current, save original), PlanRestore
│   │   └── *_test.go           # Unit tests
│   ├── controller/
│   │   ├── decide.go           # Decide rules (priority: metricsErr/Unknown → observe mode → expired cap → enforce+Harmful → None)
│   │   ├── scaleguard_controller.go  # Reconcile loop: detect scale-up, wait warmup, fetch metrics, Judge, Decide, Actuate, write ScaleDecision
│   │   └── *_test.go           # Unit tests (envtest)
│   ├── detector/
│   │   ├── judge.go            # Core evaluation: FitMetric (OLS), prediction intervals, verdict rules, alternatives, confidence scoring
│   │   ├── types.go            # Sample, DepSample, Config, Input, Finding, Alternative, Result, Verdict constants
│   │   ├── baseline.go         # fitLinear, FitMetric, Fit.Predict (OLS with 95% prediction interval)
│   │   ├── alternatives.go     # checkTrafficAlreadyDegrading, checkWarmup, checkOtherDependencies
│   │   ├── ceiling.go          # Connection ceiling detector
│   │   ├── changepoint.go      # CUSUM changepoint detection
│   │   ├── confidence.go       # Confidence scoring
│   │   └── *_test.go           # Unit tests
│   ├── metrics/
│   │   ├── types.go            # Source interface (Series, DepSeries), Target, DepRef, ErrStale, ErrNoData
│   │   ├── prometheus.go       # Prometheus /api/v1/query_range client; aligns 5 service + 2 dep metrics by timestamp
│   │   ├── fake.go             # In-memory Source for tests
│   │   └── *_test.go
│   └── report/
│       └── report.go           # Plain-English rendering of detector.Result
├── Makefile                    # Standard Kubebuilder targets: test, build, run, docker-build, deploy, install, test-e2e (kind), lint-fix, build-installer
├── PROJECT                     # Kubebuilder metadata (multigroup: false, resources: ScaleGuard, ScaleDecision)
├── prescale/prescale/          # NESTED DUPLICATE PROJECT (diverged: has hpa.go, suite_test.go, source.go; missing decide.go, fake.go, etc.)
├── test/
│   ├── e2e/                    # Ginkgo e2e tests (requires Kind cluster, deploys manager via make)
│   └── utils/utils.go          # Test utilities
└── README.md                   # Documentation (describes ScaleGuard spec with prometheusURL/queries — NOT in actual API types)
```

---

## 3. Tech Stack

| Layer | Technology |
|-------|------------|
| Language | Go 1.26 (go.mod: `go 1.26.0`; binary: `go1.26.10-X:nodwarf5`) |
| Framework | Kubebuilder 4.16.0 (`cliVersion` in PROJECT), controller-runtime v0.25.0 |
| K8s APIs | k8s.io/api/apimachinery/client-go v0.37.0 |
| Testing | Ginkgo v2.27.4 + Gomega v1.39.0 (unit + e2e via envtest/Kind) |
| Metrics | Prometheus client_golang v1.24.0 (indirect) |
| Load Testing | k6 (JavaScript) — `demo/k6/surge.js` |
| Cluster Tooling | Kind (e2e), Helm (kube-prometheus-stack for demo) |
| Container | Multi-stage Dockerfile: `golang:1.26` builder → `gcr.io/distroless/static:nonroot` |
| Linting | golangci-lint v2.13.1 with custom plugins (`.custom-gcl.yml`) |
| **Missing** | Python, pip/requirements.txt, pyproject.toml, package.json, .pkl files, ML libraries |

---

## 4. Entry Points & How to Run

| Entry Point | Command | Status |
|-------------|---------|--------|
| **Manager (controller)** | `make build` → `./bin/manager` or `make run` | **Works** (standard Kubebuilder) |
| **Manager (Docker)** | `make docker-build IMG=...` → `make deploy IMG=...` | **Works** (standard) |
| **Install CRDs** | `make install` | **Works** |
| **Unit tests** | `make test` (uses envtest) | **Works** (go test ./... excluding e2e) |
| **E2E tests** | `make test-e2e` (creates Kind cluster `prescale-test-e2e`) | **Partial** — requires Kind, Prometheus, demo apps |
| **Demo CLI (synthetic)** | `go run ./cmd/prescale-demo` | **Works** — prints 3 scenario reports |
| **Eval CLI (5-arm)** | `go run ./cmd/prescale-eval` | **Works** — prints comparison tables, Wilson CIs, sample reports |
| **Live CLI (real Prom)** | `go run ./cmd/prescale-live --prom http://... --ns demo --name checkout` | **Partial** — needs running Prometheus + checkout deployment |
| **Helm chart** | `kubebuilder edit --plugins=helm/v2-alpha` then `make helm-deploy` | **Missing** — not generated; README mentions but no chart dir |
| **Build installer** | `make build-installer IMG=...` → `dist/install.yaml` | **Works** (Kustomize) |

**Broken:** `go build ./...` fails because `demo/checkout/main.go` and `demo/fakedb/main.go` are empty (0 bytes, "expected 'package', found 'EOF'"). The demo apps cannot be built.

---

## 5. Data

**No real datasets exist.** All data is synthetic, generated in-code at runtime.

### Synthetic Generators
| File | Scenarios | Samples per Scenario | Step | Time Range | Key Fields |
|------|-----------|---------------------|------|------------|------------|
| `cmd/prescale-demo/main.go` | 3 (Scenario 3 contention, control, healthy surge) | ~47 each (20 pre + 7 warmup + 20 post) | 10s | Fixed t0=2026-01-01, scaleUpAt=t0+200s | RPS, P99Ms, ErrRate, Replicas, Ready, dep Saturation, dep LatencyMs |
| `cmd/prescale-eval/main.go` | 8 types × 200 = 1600 total | ~47 each | 10s | Same fixed timeline | Same + ground-truth `Harmful` bool |
| `cmd/prescale-live/main.go` | N/A (real) | Variable | Configurable (`-step`, default 5s) | `-last` window (default 20m) | Fetched from Prometheus via query_range |

**Schema (detector.Sample):** `T time.Time`, `Replicas int`, `Ready int`, `RPS float64`, `P99Ms float64`, `ErrRate float64`  
**Schema (detector.DepSample):** `T time.Time`, `Saturation float64 (0–1)`, `LatencyMs float64`

**Preprocessing:** 
- Prometheus client drops NaN/Inf, requires all 5 service metrics at same timestamp, rejects if >20% dropped or newest point older than 2×step (`metrics/prometheus.go:150-168, 340-344`)
- Detector splits pre/post at `ScaleUpAt`, waits `WarmupSeconds`, requires `MinSamples` (default 5) each side (`judge.go:56-62`)

---

## 6. ML Model

**NONE.** There is no ML model, no `.pkl` file, no training script, no notebook, no `requirements.txt`, no `pyproject.toml`, no Python code anywhere in the repository.

### What Exists Instead: Heuristic Detector
- **Algorithm:** Ordinary least-squares linear regression (`internal/detector/baseline.go:36-64`) on pre-scale samples (T < ScaleUpAt) for P99Ms and ErrRate separately.
- **Counterfactual:** `Fit.Predict(t, z=1.96)` returns mean + 95% prediction interval for each post-warmup timestamp.
- **Verdict Rules** (`judge.go:284-294`):
  - `Harmful`: (latencyWorse OR errWorse) AND throughputGain < MinThroughputGain (default 5%) AND dependency evidence (saturation ≥ 90% OR dep latency shift co-timed with service shift)
  - `Helped`: throughputGain ≥ MinThroughputGain
  - `Useless`: neither helped nor harmful
  - `Unknown`: insufficient data, warmup not finished, invalid values, or confidence < MinConfidence
- **Confidence:** Heuristic score (not probability) based on signal-to-noise (excess/halfWidth), sample count, missing data fraction, and confirming signals (`judge.go:328-424`).
- **Alternatives checked** (downgrade Harmful → Unknown if not ruled out): traffic already degrading pre-scale, warmup not finished, other dependency not ruled out (`alternatives.go`).

### Problems Noticed
| Issue | Location |
|-------|----------|
| No ML model at all — contradicts project background | N/A |
| Train/serve skew: synthetic generators use fixed t0=2026-01-01, 10s step; live uses real Prometheus with variable step | `demo/main.go` vs `live/main.go` |
| Hardcoded fallback dependency list `["dep","db","redis"]` | `scaleguard_controller.go:220` |
| ScaleGuardSpec fields (TTL, Floor, WarmupSeconds...) differ from README (prometheusURL, queries, mode...) | `scaleguard_types.go` vs `README.md` |
| No feature engineering / preprocessing objects saved (no scaler/encoder) | N/A |
| No versioning of detector logic (config parsed from strings at reconcile time) | `buildDetectorConfig` |

---

## 7. Serving / Integration Layer

The integration is the **controller reconciliation loop** (`internal/controller/scaleguard_controller.go:49-327`):

1. **Watch** ScaleGuard CRs (owns ScaleDecision, watches HPA via replica count change detection)
2. **Detect scale-up:** `currentReplicas > lastRecordedReplicas` (or from ScaleDecision history)
3. **Wait:** `WarmupSeconds + MinSamples × Step` after scale-up timestamp
4. **Fetch metrics:** `metrics.Source.Series()` + `DepSeries()` for dependencies (Prometheus or Fake)
5. **Evaluate:** `detector.Judge(Input)` → `Result{Verdict, Confidence, Culprit, Reasons, Findings, Alternatives}`
6. **Decide:** `controller.Decide(mode, result, capped, expired, ...)` → `Action` (Cap/Restore/RecordOnly/None)
7. **Actuate:** `actuator.Cap()` or `Restore()` — patches HPA `maxReplicas`, stores original in annotation `prescale.io/original-max-replicas`, sets `capped-until` (TTL), `capped-by`
8. **Record:** Create `ScaleDecision` CR with verdict, confidence, report, action, from/to replicas
9. **Requeue:** After TTL if capped, else idle

**Scale-up decision from prediction:** Only `Harmful` verdict + `enforce` mode + not already capped → `Cap{Proposed: fromReplicas}` (clamped by floor and current replicas). The "prediction" is the counterfactual expected P99Ms/ErrRate; the decision is a binary cap/no-cap.

---

## 8. Kubernetes / Infra

| Component | Details |
|-----------|---------|
| **CRDs** | `ScaleGuard`, `ScaleDecision` (namespaced, `config/crd/bases/`) |
| **RBAC** | `manager-role` (HPA get/list/watch/update/patch, ScaleGuard/ScaleDecision full, events create/patch, leader election) |
| **Manager Deployment** | `config/manager/manager.yaml` (image `controller:latest`, leader election, metrics on :8443 HTTPS) |
| **Prometheus** | `config/prometheus/monitor.yaml` (PodMonitor for controller metrics), `monitor_tls_patch.yaml` |
| **Dockerfile** | Multi-stage, distroless, nonroot (UID 65532) |
| **Helm Chart** | **Not generated** — `kubebuilder edit --plugins=helm/v2-alpha` never run |
| **Samples** | `config/samples/autoscaling_v1alpha1_scaleguard.yaml` — placeholder `# TODO(user): Add fields here` |
| **HPA Config** | No HPA manifest in repo; demo `checkout.yaml` is empty. README example uses CPU > 50% target. |
| **Assumed Cluster** | Kind (e2e), or any K8s 1.11.3+ with Prometheus + metrics-server + HPA controller |
| **Network Policy** | `config/network-policy/allow-metrics-traffic.yaml` |

---

## 9. Load Generation / Traffic Simulation

| File | Tool | Pattern |
|------|------|---------|
| `demo/k6/surge.js` | k6 (JS) | `ramping-arrival-rate`: 20 RPS × 1m → ramp to 120 RPS × 1m → hold 120 RPS × 2m → ramp down to 20 RPS × 1m |
| | | `MODE=contention`: fakedb latency = 10ms × (1 + active/20)²; `MODE=healthy`: flat ~10ms |
| | | Sets fakedb mode via `POST /mode?mode=...` in `setup()` |
| `demo/fakedb/` | Go (empty stub) | Intended: simulates DB with `MAX_CONNS=100`, exposes `/metrics` with `db_active_connections`, `db_max_connections`, `db_query_duration_seconds` |
| `demo/checkout/` | Go (empty stub) | Intended: HTTP service calling fakedb `/query`, ~5ms CPU/request, HPA scales on CPU > 50% |

**Status:** The actual demo applications (`checkout`, `fakedb`) are **empty files (0 bytes)** — cannot run. Only the k6 script and PodMonitor exist.

---

## 10. Demo Readiness Assessment

| Component | Status | Reason |
|-----------|--------|--------|
| **Data (synthetic)** | Partial | In-code generators exist (demo, eval, live); no persisted datasets, no real data files |
| **ML Model** | Missing | No .pkl, no training, no Python; heuristic OLS only |
| **Inference Service** | Partial | `detector.Judge` is a pure Go function; no HTTP/gRPC serving, no model server |
| **K8s Integration (controller)** | Partial | Reconciler + actuator + CRDs + RBAC complete and unit-tested; not deployed; depends on real Prometheus |
| **Load Test (k6)** | Partial | Script exists; demo apps missing; k8s manifests mostly empty |
| **Metrics/Visualization** | Partial | PromQL queries documented (README, live/main.go); PodMonitor exists; no Grafana dashboards |

---

## 11. Mess & Tech Debt

| Issue | File(s) |
|-------|---------|
| **Nested duplicate project** `prescale/prescale/` (diverged: has `hpa.go`, `suite_test.go`, `source.go`; missing `decide.go`, `fake.go`, `plan.go`, `alternatives.go`, etc.) | `prescale/prescale/**` |
| **Empty demo stubs** (0 bytes): `demo/checkout/main.go`, `Dockerfile`, `main_test.go`; `demo/fakedb/main.go`, `Dockerfile`, `main_test.go`; `demo/k8s/checkout.yaml`, `fakedb.yaml`, `namespace.yaml` | `demo/**` |
| **`go build ./...` fails** due to empty demo mains | Root cause above |
| **README vs API drift**: README documents `prometheusURL`, `queries.rps/p99Millis/errorRate`, `dependencies[].saturationQuery`, `mode` — **none exist in `ScaleGuardSpec`** (has TTL, Floor, WarmupSeconds, MinSamples, Step, Dependencies[]string, HarmMargin, SaturationPct, MinConfidence, MinThroughputGain) | `README.md` vs `scaleguard_types.go` |
| **Sample CR is placeholder** | `config/samples/autoscaling_v1alpha1_scaleguard.yaml:9` |
| **No LICENSE file** (README references `LICENSE`) | Missing |
| **Committed binaries** in `bin/` (etcd, kube-apiserver, kubectl, controller-gen, setup-envtest) | `bin/` |
| **Committed coverage output** | `cover.out` |
| **Hardcoded defaults**: fallback dep list `["dep","db","redis"]` | `scaleguard_controller.go:220` |
| **Hardcoded detector config defaults** in multiple places | `demo/main.go:13`, `eval/main.go:157`, `live/main.go:24`, `scaleguard_controller.go:337` |
| **No tests for cmd/prescale-*** | Only internal packages tested |
| **Custom golangci-lint plugin build** (`.custom-gcl.yml`) | May cause CI drift |

---

## 12. TODO/FIXME/HACK Comments

| File | Line | Comment |
|------|------|---------|
| `cmd/main.go` | 148 | `// TODO(user): If you enable certManager, uncomment the following lines:` |
| `test/e2e/e2e_test.go` | 273 | `// TODO: Customize the e2e test suite with scenarios specific to your project.` |
| `test/e2e/e2e_suite_test.go` | 59 | `// TODO(user): If you want to change the e2e test vendor from Kind,` |
| `prescale/prescale/test/e2e/e2e_test.go` | 273 | (same as above, in nested copy) |
| `prescale/prescale/test/e2e/e2e_suite_test.go` | 59 | (same as above) |
| `prescale/prescale/internal/controller/suite_test.go` | 59 | `ctx, cancel = context.WithCancel(context.TODO())` |
| `prescale/prescale/internal/controller/scaleguard_controller_test.go` | 57, 64, 83 | Scaffold TODOs (spec details, cleanup, assertions) |
| `prescale/prescale/internal/controller/scaleguard_controller.go` | 42, 52 | Scaffold TODOs (Reconcile logic) |
| `prescale/prescale/cmd/main.go` | 148 | (same certManager TODO) |
| `config/samples/autoscaling_v1alpha1_scaleguard.yaml` | 9 | `# TODO(user): Add fields here` |

**No FIXME, HACK, XXX, BUG comments found** in root project (only in nested copy).

---

## 13. Git Summary (last 15 commits — all 2026-10-26)

| Hash | Date | Message |
|------|------|---------|
| bd0ac46 | 2026-10-26 | start cooking |
| 3879adb | 2026-10-26 | live: judge real prometheus data |
| 4b5f3ff | 2026-10-26 | demo: checkout app, fake db, k6, manifests |
| d9c7c7c | 2026-10-26 | demo: scenario 3 CLI |
| b6891b4 | 2026-10-26 | eval: verdict breakdown |
| 7bfffc4 | 2026-10-26 | eval: harder scenarios, single run |
| 016d4a9 | 2026-10-26 | eval: 5-arm comparison |
| ff69ab9 | 2026-10-26 | revert accidental formatting of protected file prescale/prescale/internal/detector/types.go |
| 75b3b59 | 2026-10-26 | controller: scaleguard reconcile loop and rbac |
| bf9a644 | 2026-10-26 | controller: pure decision rules |
| 1d404bb | 2026-10-26 | metrics: source interface, fake, prometheus client |
| b5b382f | 2026-10-26 | actuator: cap and restore HPA |
| d459533 | 2026-10-26 | actuator: pure cap planning with safety rules |
| a052491 | 2026-10-26 | report: deterministic plain-English report |
| fb378ec | 2026-10-26 | detector: connection ceiling |

**Pattern:** All commits on same day (2026-10-26). Rapid scaffold → detector → actuator → controller → metrics → eval → demo → live. Suggests single-day development sprint; demo files left as empty stubs; nested `prescale/prescale/` appears to be an earlier scaffold attempt that was copied but not cleaned up.

---

## 14. Open Questions

1. **Why is there a nested `prescale/prescale/` duplicate?** Which copy is canonical? The root has more detector files (`alternatives.go`, `ceiling.go`, `changepoint.go`, `confidence.go`, `fake.go`, `plan.go`, `decide.go`); the nested has `hpa.go`, `suite_test.go`, `source.go`. They diverge.

2. **The project background describes an ML predictive autoscaler with a `.pkl` model.** Is that a separate planned direction, or is this the wrong repository? The current code has zero ML.

3. **Are the empty demo files (`demo/checkout/main.go`, `demo/fakedb/main.go`, `demo/k8s/*.yaml`) intentional placeholders to be filled, or were they lost?**

4. **Which ScaleGuard spec is canonical?** The Go types (`TTL`, `Floor`, `WarmupSeconds`, `MinSamples`, `Step`, `Dependencies[]string`, threshold strings) or the README (`prometheusURL`, `queries.{rps,p99Millis,errorRate}`, `dependencies[].{name,saturationQuery,thresholdPercent}`, `mode`, `warmupWindow`, `maxFreezeDuration`, `minReplicasFloor`)? They don't match.

5. **Where do PromQL query templates live for the controller?** `ScaleGuardSpec` has no query fields; `metricsConfig()` in `live/main.go` hardcodes them. The controller's `metrics.Source` (Prometheus) requires a `Config` with templates — but `ScaleGuardReconciler` never constructs one (it passes `nil` for `Metrics` in `main.go:185`).

6. **Is the controller meant to be deployed standalone, or only as part of the demo?** No Helm chart, no installer tested.

7. **What cluster is the demo intended for?** Kind (per e2e), but demo README says `kind create cluster --name prescale-demo` and install `kube-prometheus-stack` via Helm — no automation.

8. **Is there a real dataset / training pipeline planned for the `.pkl` model mentioned in the background?** If so, where?

9. **Why does `ScaleGuardSpec.Dependencies` use `[]string` (just names) while README shows objects with `saturationQuery` and `thresholdPercent`?** The controller falls back to `["dep","db","redis"]` (hardcoded).

10. **The `detector.Config` is parsed from string fields at reconcile time (`buildDetectorConfig`).** Is this intentional vs. strongly-typed fields?

---

## Summary

This repo is a **Go Kubebuilder operator (prescale)** that **evaluates HPA scale-ups post-hoc** using statistical counterfactuals (OLS regression), not an ML predictive autoscaler. It has a complete controller/actuator/detector stack with unit tests, a 5-arm evaluation CLI, a live Prometheus CLI, and a synthetic demo CLI — but **the demo applications and manifests are empty stubs**, the **README documents a different API than the code implements**, and there is a **stale nested duplicate project**. No ML artifacts exist. To make a working demo, the empty demo files must be implemented, the ScaleGuardSpec/READMe drift resolved, and a real Prometheus + workload deployed.

---

**REPO_CONTEXT.md written to /home/unhealthycoder/MyBackHurts/REPO_CONTEXT.md**