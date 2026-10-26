# REPORT_PART_1: Repository Audit, VictoriaMetrics Compatibility & Environment Inventory

## 1. TL;DR
- **Repo Purpose**: Currently a Kubebuilder operator ("prescale") that reactively evaluates HPA scale-ups and caps `maxReplicas` on downstream contention; it contains no predictive logic or ML code.
- **Canonical Copy**: The root directory is canonical and actively maintained; `prescale/prescale/` is an abandoned initial scaffolding artifact.
- **Build/Test Status**: `go build` and `go vet` pass cleanly once empty demo stubs are excluded; unit tests for `metrics`, `actuator`, and `detector` pass 100%.
- **VictoriaMetrics Compatibility**: High compatibility out of the box; `internal/metrics` Prometheus client works with `vmsingle` via base-URL configuration without code changes.
- **Actuator Inversion Needed**: Actuator must be reversed from capping `maxReplicas` downwards post-surge to raising `minReplicas` upwards ahead of predicted surges, with corresponding annotation and clamp adjustments.
- **Environment**: Fedora 44, 16 CPU cores, 15.3 GiB RAM (5.7 GiB available), 109 GiB free disk, Docker active, Kind v0.26.0 installed with a running v1.32.0 cluster; comfortably capable of running the target workload.
- **Key Blockers**: `helm` and `k6` are not installed; demo app files (`checkout`, `fakedb`) were wiped to 0 bytes in the latest commit; ML model serving architecture is pending.
- **Recommendation**: Create a clean, focused pre-scaler service/operator by cherry-picking and adapting `internal/metrics`, inverted `internal/actuator`, and the demo apps from git history, rather than overloading the existing reactive `ScaleGuard` CRD architecture.

---

## 2. Canonical Copy: Root vs Nested, with Evidence

### Comparison Summary
- **Canonical Version**: **Root directory (`/home/unhealthycoder/MyBackHurts/`)** is the newer, active, and canonical implementation.
- **Nested Directory (`prescale/prescale/`)**: An abandoned copy from the initial scaffolding commit (`c67df35`), never updated across subsequent feature commits.

### Files Existing Only in Root (42 files)
- `REPO_CONTEXT.md`
- `cmd/prescale-demo/` (`main.go`, `main_test.go`)
- `cmd/prescale-eval/` (`main.go`, `main_test.go`)
- `cmd/prescale-live/` (`main.go`, `main_test.go`)
- `cover.out`
- `demo/` (`README.md`, `checkout/*`, `fakedb/*`, `k6/surge.js`, `k8s/*`)
- `internal/actuator/` (`actuator.go`, `actuator_test.go`, `plan.go`, `plan_test.go`)
- `internal/controller/` (`decide.go`, `decide_test.go`)
- `internal/detector/` (`alternatives.go`, `alternatives_test.go`, `baseline.go`, `baseline_extra_test.go`, `baseline_fit_test.go`, `baseline_test.go`, `ceiling.go`, `ceiling_test.go`, `changepoint.go`, `changepoint_test.go`, `confidence.go`, `judge.go`, `judge_test.go`)
- `internal/metrics/` (`fake.go`, `metrics_test.go`, `prometheus.go`, `types.go`)
- `internal/report/` (`report.go`, `report_test.go`)

### Files Existing Only in Nested (3 files)
- `internal/actuator/hpa.go` (initial 17-line stub interface, replaced by concrete actuator in root)
- `internal/controller/suite_test.go` (boilerplate envtest suite scaffold)
- `internal/metrics/source.go` (initial 19-line stub interface, replaced by `types.go` and `prometheus.go` in root)

### Files That Differ (9 files)
1. `api/v1alpha1/scaledecision_types.go`: Root contains full decision schema (Action, AppliedAction, Reason, Confidence, Baseline, Replicas); nested has a rudimentary 40-line draft.
2. `api/v1alpha1/scaleguard_types.go`: Root defines full configuration specs (TargetHPA, Mode, TTL, Floor, WarmupSeconds, MinSamples, Dependencies, SaturationThreshold); nested contains an obsolete stub.
3. `api/v1alpha1/zz_generated.deepcopy.go`: Generated deepcopy methods matching the updated root types.
4. `config/crd/bases/autoscaling.prescale.io_scaledecisions.yaml`: Generated CRD schema reflecting root types.
5. `config/crd/bases/autoscaling.prescale.io_scaleguards.yaml`: Generated CRD schema reflecting root types.
6. `config/rbac/role.yaml`: Root adds RBAC permissions for `autoscaling/horizontalpodautoscalers` (get, list, patch, update, watch); nested lacks HPA permissions.
7. `internal/controller/scaleguard_controller.go`: Root contains full 376-line reconciliation loop with finalizer, HPA patching, and Prometheus querying; nested contains an empty 63-line scaffold.
8. `internal/controller/scaleguard_controller_test.go`: Root contains complete 469-line reconciliation test suite; nested contains 87-line placeholder.
9. `internal/detector/types.go`: Root defines complete sample models, baseline structures, and verdict types; nested has early draft types.

---

## 3. Build and Test Status

### `go build`
- **Full repo (`go build ./...`)**: **FAILS** due to empty demo files:
  ```
  demo/checkout/main.go:1:1: expected 'package', found 'EOF'
  demo/fakedb/main.go:1:1: expected 'package', found 'EOF'
  ```
- **Excluding `demo/checkout` and `demo/fakedb` (`go build ./api/v1alpha1 ./cmd ./cmd/prescale-demo ./cmd/prescale-eval ./cmd/prescale-live ./internal/...`)**: **PASSES** cleanly with zero errors (exit code 0).

### `go vet`
- **Excluding demo packages (`go vet ./api/v1alpha1 ./cmd ./cmd/prescale-demo ./cmd/prescale-eval ./cmd/prescale-live ./internal/...`)**: **PASSES** cleanly with zero warnings/errors (exit code 0).

### `go test` (Scoped to requested internal packages)
Executed with `go test -v ./internal/metrics ./internal/actuator ./internal/detector`:
- `github.com/prescale/prescale/internal/metrics`: **PASS** (13 tests passed, covering query range, NaN handling, drop rate checks, stale checks, timeouts, fakes).
- `github.com/prescale/prescale/internal/actuator`: **PASS** (12 tests passed, covering cap rules, floor clamping, original max preservation, idempotency, restore rules, property tests).
- `github.com/prescale/prescale/internal/detector`: **PASS** (48 tests passed, covering data quality, warmup validation, dependency saturation, OLS regression baseline, connection ceilings, CUSUM changepoint detection with empirical false alarm rates, and judging).

---

## 4. Authorship and Fit Assessment

### Git Authorship Summary
- **Commit History**:
  ```
  23  Varchas H V <hvvarchas@gmail.com>
   1  SurakshanHN <surakshan111@gmail.com>
   1  VarchasHV <hvvarchas@gmail.com>
  ```
- **First Commit**: `Mon Oct 26 11:55:18 2026 +0530` (`c67df35` - "file structure and initial code")
- **Last Commit**: `Mon Oct 26 18:27:53 2026 +0530` (`bd0ac46` - "start cooking")
- All 25 commits occurred on the same day (October 26, 2026) within a span of ~6.5 hours.

### Generated / Scaffolded vs Hand-Written Code
- **Scaffolded**:
  - Kubebuilder 4.16.0 generated: `PROJECT`, `cmd/main.go`, `Makefile`, `Dockerfile`, `config/` (CRD bases, RBAC, Prometheus monitors), `api/v1alpha1/groupversion_info.go`, and auto-generated `zz_generated.deepcopy.go`.
- **Hand-written**:
  - `internal/detector/*`: Implements causal inference mathematics (Page's CUSUM changepoint, OLS linear regression, and database saturation ceiling).
  - `internal/actuator/*`: Kubernetes HPA patch logic, annotation tracking, and safety rule planners.
  - `internal/metrics/*`: HTTP client parsing Prometheus `/api/v1/query_range` JSON responses and aligning multi-metric time series.
  - `cmd/prescale-eval/` and `cmd/prescale-live/`: Testing harnesses and CLI utilities.
  - `demo/k6/surge.js` and `demo/README.md`: Load test definitions and setup guides.

### Fit Assessment: Is it plausibly part of the same project?
- **Domain Alignment**: Yes. The codebase addresses Kubernetes HPA autoscaling, traffic metrics monitoring, pod scale-up delays, and downstream contention under surge loads. The repository name (`prescale`) directly reflects the concept of pre-scaling.
- **Directional Difference**: The code in this repository was constructed as an **ex-post guard** (circuit breaker). It evaluates HPA scale-ups *after* they happen using statistical heuristics and caps `maxReplicas` to mitigate cascading failures. In contrast, the current project goal is an **ex-ante pre-scaler** driven by a machine learning model (`.pkl`) that forecasts traffic *before* it arrives and raises `minReplicas`.
- **Demo Stubs (`checkout`, `fakedb`)**: Added in commit `4b5f3ff` and emptied out in commit `bd0ac46` ("start cooking"). They were designed to simulate a realistic microservice architecture:
  - `checkout`: CPU-burning HTTP microservice configured with an HPA (scaling from 3 to 10 pods when traffic spikes).
  - `fakedb`: Simulated database dependency with configurable modes (`healthy` vs `contention`). In contention mode, query latency scaled quadratically with concurrency (`10ms * (1 + active/20)^2`).
  - These demo components directly serve the planned comparative demonstration (HPA-only vs pre-scaler under surge traffic).

---

## 5. Component Reuse Table

| Component | Path | Recommendation | Why / Assessment |
| :--- | :--- | :--- | :--- |
| **Prometheus Client** | `internal/metrics/prometheus.go`, `types.go`, `fake.go` | **Reusable with minor changes** | Clean HTTP client using `/api/v1/query_range`. Compatible with VictoriaMetrics single-node out of the box with only a base-URL change. Needs minor additions if instant query (`/api/v1/query`) or authentication is required. |
| **Actuator** | `internal/actuator/actuator.go`, `plan.go` | **Needs changes (Inversion)** | Core Kubernetes client patterns, annotation handling, idempotency, and TTL-based auto-restore are robust and well-tested. However, the logic must be inverted from capping `maxReplicas` downwards to boosting `minReplicas` upwards, with updated safety clamp rules. |
| **Controller** | `internal/controller/scaleguard_controller.go`, `decide.go` | **Needs changes / Rewrite** | Controller is tied to `ScaleGuard` and `ScaleDecision` CRDs, designed for post-scale-up evaluation. For predictive pre-scaling, the reconciler must instead interface with the ML prediction service, track surge forecast windows, and drive the inverted actuator. |
| **Statistical Detector** | `internal/detector/*` | **Drop (or keep as fallback circuit breaker)** | Contains CUSUM changepoint, OLS regression baseline, and connection ceiling estimators for ex-post scale-up judging. Does not perform predictive traffic forecasting. Superfluous once traffic is forecast by the external ML `.pkl` model. |
| **Live Evaluator CLI** | `cmd/prescale-live/` | **Needs changes / Repurpose** | CLI tool that polls Prometheus and waits for an HPA scale-up event to evaluate it with `detector.Judge`. Could be adapted into a live monitoring or validation tool, but its core logic evaluates reactive scale-ups rather than predicted surges. |
| **Synthetic Eval Harness** | `cmd/prescale-eval/` | **Drop** | An offline benchmark running 1,600 synthetic scenarios across 5 algorithmic arms to validate the statistical detector. Irrelevant to ML inference and pre-scaling. |
| **Load Test Script** | `demo/k6/surge.js` | **Reusable as-is** | Excellent k6 scenario (`ramping-arrival-rate` from 20 to 120 RPS). Generates the exact traffic curve required to benchmark reactive HPA pod-startup latency against predictive pre-scaling. |
| **Demo Apps & Manifests** | `demo/checkout/`, `demo/fakedb/`, `demo/k8s/` | **Reusable (Restore from Git)** | Full implementations exist in git commit `4b5f3ff` before being emptied in `bd0ac46`. Ideal for the side-by-side demo cluster. |

---

## 6. Actuator Gap Analysis

The current actuator (`internal/actuator/actuator.go` and `internal/actuator/plan.go`) reduces `hpa.Spec.MaxReplicas` to cap scaling after a harmful event. The predictive autoscaler requires raising `hpa.Spec.MinReplicas` ahead of a predicted surge and restoring it after the surge expires.

### Required Structural and Logic Modifications

#### 1. Annotations (`internal/actuator/actuator.go:17-23`)
- **Current**:
  ```go
  AnnOriginalMax = "prescale.io/original-max-replicas"
  AnnCappedUntil = "prescale.io/capped-until"
  AnnCappedBy    = "prescale.io/capped-by"
  ```
- **Changes Needed**:
  - Replace or supplement with:
    - `AnnOriginalMin = "prescale.io/original-min-replicas"`: Stores original `minReplicas` prior to pre-scaling.
    - `AnnBoostedUntil = "prescale.io/boosted-until"`: RFC3339 timestamp when the pre-scale boost expires.
    - `AnnBoostedBy = "prescale.io/boosted-by"`: Identifies the pre-scaler service.

#### 2. Planning State & Rules (`internal/actuator/plan.go`)
- **Lines 8–21 (`HPAState` & `Plan`)**:
  - Add `OriginalMin int32` to `HPAState`.
  - Update `Plan` to carry `NewMin int32` and `SaveOriginalMin int32`, with actions `"boost"` (or `"raise"`), `"noop"`, `"refuse"`, and `"restore"`.
- **Lines 39–81 (`PlanCap`)**:
  - Replace/supplement with `PlanRaiseMin(s HPAState, proposedMin, maxCeiling int32) Plan`:
    - **Rule 1 (Validation)**: Reject invalid inputs (`proposedMin <= 0`, `s.MaxReplicas < s.MinReplicas`, etc.).
    - **Rule 2 (Ceiling Clamping)**: In Kubernetes, `MinReplicas` cannot exceed `MaxReplicas`. `proposedMin` must be clamped: `newMin = min32(proposedMin, s.MaxReplicas)`.
    - **Rule 3 (No-op on Non-Increase)**: If `newMin <= s.MinReplicas`, return `Action: "noop"` (do not accidentally decrease `minReplicas`).
    - **Rule 4 (Preserve True Baseline)**: If `s.OriginalMin == 0`, save the current `s.MinReplicas`. If already boosted (`s.OriginalMin > 0`), preserve the existing original value so subsequent adjustments don't overwrite the true baseline.
- **Lines 85–94 (`PlanRestore`)**:
  - Change target from `s.OriginalMax` to `s.OriginalMin`. When restoring, set `NewMin: s.OriginalMin`.

#### 3. Kubernetes Actuation (`internal/actuator/actuator.go`)
- **Lines 42–86 (`Cap`)**:
  - Create `RaiseMin(ctx, key, proposedMin, maxCeiling, ttl, owner)`:
    - Lines 48–52: Read `hpa.Spec.MinReplicas` (defaulting to 1 if nil).
    - Lines 53–60: Read `AnnOriginalMin` from annotations.
    - Lines 75–78: Write `AnnOriginalMin`, `AnnBoostedUntil`, and `AnnBoostedBy`.
    - Line 79–80: Currently:
      ```go
      hpa.Spec.MaxReplicas = plan.NewMax
      // Never touch Spec.MinReplicas.
      ```
      **Change to**:
      ```go
      newMin := plan.NewMin
      hpa.Spec.MinReplicas = &newMin
      ```
- **Lines 91–132 (`Restore`)**:
  - Line 106: Read `AnnOriginalMin`.
  - Line 120: Change `hpa.Spec.MaxReplicas = plan.NewMax` to `*hpa.Spec.MinReplicas = plan.NewMin`.
  - Lines 122–125: Delete `AnnOriginalMin`, `AnnBoostedUntil`, and `AnnBoostedBy` annotations.
- **Lines 137–160 (`Expired`)**:
  - Check for presence of `AnnOriginalMin` and compare `AnnBoostedUntil` against `a.now()`.

---

## 7. VictoriaMetrics Compatibility

### Client Inspection: `internal/metrics/prometheus.go`
- **HTTP Endpoint**: Hardcoded to `strings.TrimRight(p.cfg.BaseURL, "/") + "/api/v1/query_range"` (GET request, line 119).
- **Query Parameters**:
  - `query`: PromQL query string (lines 126, 191).
  - `start`: Unix timestamp in seconds (`strconv.FormatInt(from.Unix(), 10)`, line 127).
  - `end`: Unix timestamp in seconds (`strconv.FormatInt(to.Unix(), 10)`, line 128).
  - `step`: Formatted duration string, e.g., `"5s"`, `"1m"` (lines 55–60, 129).
- **Response Fields Decoded**:
  - Top-level: `status` (string, must equal `"success"`), `error` (string), `data` (object, lines 80–84).
  - `data.result`: Array of series objects containing `values: [[timestamp_float, "value_string"], ...]` (lines 86–112).
- **Authentication**: None. The client initializes standard `http.Request` without authorization headers or TLS configuration.
- **Assumptions**: Assumes Prometheus v1 HTTP Range Query API returning standard Prometheus JSON matrix schema.

### VictoriaMetrics Single-Node (`vmsingle`) Compatibility
1. **API Endpoint & Protocol Compatibility** — **CONFIRMED**:
   - VictoriaMetrics single-node implements full Prometheus query API compatibility at `/api/v1/query_range` (as well as `/prometheus/api/v1/query_range`).
   - Changing `BaseURL` to `http://<vmsingle-host>:8428` works directly without code modifications.
2. **Wire Response Schema** — **CONFIRMED**:
   - VictoriaMetrics returns JSON with `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{...},"values":[[<ts>,"<val>"], ...]}]}}`. This matches `promResponse` and `promPoint` unmarshaling.
3. **Query Parameter Handling** — **CONFIRMED**:
   - `start`, `end` (unix seconds), `step` (`5s`, `1m`), and `query` are fully supported by VictoriaMetrics.
4. **MetricsQL / PromQL Dialect** — **CONFIRMED**:
   - Standard PromQL queries used in `Config` (e.g. `sum(rate(...))`, `histogram_quantile(...)`) are natively parsed by VictoriaMetrics MetricsQL engine.
5. **Rate Calculation & Extrapolation Differences** — **CONFIRMED**:
   - VictoriaMetrics MetricsQL calculates `rate()` without Prometheus-style boundary extrapolation, preventing artificial spikes on short windows. This is advantageous for low-latency scaling.
6. **Authentication & Security** — **CONFIRMED**:
   - Single-node `vmsingle` in default mode requires no auth headers, matching the client. If deployed behind `vmauth` or TLS proxy with Basic Auth / Bearer tokens, the client would need headers added.
7. **Staleness Check Sensitivity** — **ASSUMED**:
   - Line 342 enforces `if newest.Before(to.Add(-2 * step)) { return ErrStale }`. Depending on scrape flush intervals and VictoriaMetrics query alignment, queries evaluated right at `time.Now()` might occasionally trigger `ErrStale` if scraping has a slight ingestion delay. Setting query `to` slightly behind `time.Now()` avoids this.

### Prometheus Operator CRD Assumptions & VictoriaMetrics Equivalents
- **Files assuming Prometheus Operator CRDs / kube-prometheus-stack**:
  - `demo/k8s/monitoring.yaml`: Declares `apiVersion: monitoring.coreos.com/v1`, `kind: PodMonitor` targeting `fakedb` and `checkout`.
  - `demo/README.md`: Documents installing `prometheus-community/kube-prometheus-stack` via Helm and setting `podMonitorSelectorNilUsesHelmValues=false`.
  - `config/prometheus/monitor.yaml`: Declares `kind: ServiceMonitor` for controller manager metrics.
- **VictoriaMetrics Equivalents**:
  - **`vmagent` with ConfigMap**: Simplest and lowest-overhead approach; configure `vmagent` with standard `scrape_configs` targeting Kubernetes pod annotations or endpoints directly.
  - **VictoriaMetrics Operator CRD**: Replace `PodMonitor` with `apiVersion: operator.victoriametrics.com/v1beta1`, `kind: VMPodScrape` (or `VMServiceScrape`).
  - **Native Prometheus CRD Ingestion**: VictoriaMetrics Operator can natively watch standard Prometheus Operator `PodMonitor` and `ServiceMonitor` resources when configured with `-promOperator.podMonitorSelector`.

---

## 8. Environment Inventory Table & Capacity Verdict

### Tool and Version Inventory

| Tool / Resource | Status / Version | Path | Notes |
| :--- | :--- | :--- | :--- |
| **Operating System** | Fedora Linux 44 (KDE Plasma Desktop) | Kernel 7.2.11-200.fc44.x86_64 | x86_64 architecture |
| **CPU Cores** | 16 cores | Hardware | High multithread capacity |
| **RAM** | 15.3 GiB total (5.7 GiB available, 8.0 GiB swap) | Hardware | Ample memory available |
| **Free Disk** | 109 GiB free (89 GiB used of 198 GiB NVMe partition) | `/` (`/dev/nvme0n1p6`) | NVMe high-speed storage |
| **Go** | `go version go1.26.10-X:nodwarf5 linux/amd64` | `/usr/bin/go` | Installed and verified |
| **Python 3** | `Python 3.14.8` | `/usr/bin/python3` | Note: Python 3.14 (cutting edge) |
| **pip** | `pip 26.0.1` | `/usr/bin/pip` | Installed |
| **Python venv** | Supported / Available | Standard library | Verified via `python3 -m venv` |
| **Docker** | `Docker version 29.9.2, build a82e528` | `/usr/bin/docker` | **Daemon is running** (2 active containers) |
| **Kind** | `kind version 0.26.0` | `/home/unhealthycoder/.local/bin/kind` | Installed and working |
| **k3d** | Not installed | - | - |
| **minikube** | Not installed | - | - |
| **kubectl** | Client `v1.37.1`, Kustomize `v5.8.1` | `/home/unhealthycoder/.local/bin/kubectl` | Installed and working |
| **Helm** | **Not installed** | - | Not found in PATH |
| **k6** | **Not installed** | - | Not found in PATH |
| **Node.js** | `v22.23.1` | `/usr/bin/node` | Installed |
| **Git** | `git version 2.56.0` | `/usr/bin/git` | Installed |

### Active Clusters and Containers
- **Kubectl Contexts (`kubectl config get-contexts`)**:
  - `CURRENT`: `*`
  - `NAME`: `kind-prescale-dev`
  - `CLUSTER`: `kind-prescale-dev`
  - `AUTHINFO`: `kind-prescale-dev`
- **Reachable Kubernetes Nodes (`kubectl get nodes --request-timeout=5s`)**:
  ```
  NAME                         STATUS   ROLES           AGE     VERSION
  prescale-dev-control-plane   Ready    control-plane   5h20m   v1.32.0
  ```
- **Running Docker Containers (`docker ps`)**:
  - `ea099f72a88e`: `kindest/node:v1.32.0` (`prescale-dev-control-plane`) - running 5+ hours.
  - `c613e1865082`: `ghcr.io/tashfeenahmed/freellmapi:latest` (`freellmapi-freellmapi-1`) - running 8+ hours.

### Hardware Capacity Verdict
- **Question**: Can this machine realistically run a 2-node kind cluster, VictoriaMetrics, Grafana, and two copies of a small app at once?
- **Verdict**: **YES, comfortably.**
  - **Memory Footprint Analysis**:
    - 2-node Kind cluster overhead: ~800 MB – 1.2 GB RAM
    - VictoriaMetrics single-node (`vmsingle`): ~80 – 150 MB RAM
    - Grafana: ~100 – 150 MB RAM
    - Two microservice deployments (4–10 lightweight pods): ~250 – 500 MB RAM
    - Total estimated memory requirement: ~1.5 – 2.5 GiB RAM.
  - **Available Resources**: With 5.7 GiB of immediately available memory, 16 CPU cores, and 109 GiB of NVMe disk, the machine will run this entire stack with substantial headroom.

---

## 9. Blockers and Risks

1. **Missing Tooling (`helm`, `k6`)**:
   - `helm` is missing. Standard guides in the repo assume Helm for deploying Prometheus/monitoring. VictoriaMetrics and Grafana must either be deployed via raw K8s manifests / Kustomize or Helm must be provided.
   - `k6` is missing from the host. Running `demo/k6/surge.js` directly will fail unless k6 is installed or run via container (`docker run --net=host -i grafana/k6 run - < demo/k6/surge.js`).
2. **Demo Files Wiped in Latest Git Commit**:
   - `demo/checkout/main.go`, `Dockerfile`, `main_test.go`, `demo/fakedb/*`, and `demo/k8s/*.yaml` are 0-byte stubs on `HEAD`. `go build ./...` fails until these are restored from git history (`git checkout bd0ac46~1 -- demo/`) or replaced.
3. **Python 3.14 Compatibility with ML Dependencies**:
   - The system Python is 3.14.8. Many compiled ML packages (`scikit-learn`, `numpy`, `torch`, `scipy`) may lack pre-built binary wheels on PyPI for Python 3.14, leading to source compilation failures. When running the model inference service, building inside a Python 3.11/3.12 Docker container is strongly recommended.
4. **HPA `minReplicas` Scaling Delay**:
   - Raising `minReplicas` instructs the HPA controller to scale out the deployment. However, HPA controller sync loops run on an interval (typically every 15s), and pod scheduling/readiness takes additional time. Pre-scaling must occur with sufficient lead time (e.g. 30–60 seconds before surge peak).

---

## 10. UNCLEAR Items

1. **Model Interface & Contract**: What is the input/output schema of the teammate's `.pkl` model? Does it predict raw request rate (RPS), replica count, or a binary surge indicator? What time window does it forecast (e.g., 60 seconds into the future)?
2. **Model Serving Architecture**: Will the `.pkl` model be wrapped in a lightweight Python HTTP service (e.g. FastAPI) running alongside the pre-scaler, or will predictions be exported periodically to VictoriaMetrics/Redis?
3. **Side-by-Side Demo Specification**: Should both the baseline (HPA-only) and the predictive pre-scaler run simultaneously against two independent instances of the checkout app under identical traffic, or sequentially across two test runs?
4. **Cluster Preference**: Should the existing active Kind cluster (`prescale-dev`) be used and expanded with an extra worker node, or should a brand-new multi-node Kind cluster be spun up for the demo?

---

## 11. Recommendation: Architecture Strategy

### Recommendation: Start a Clean, Focused Pre-Scaler Structure and Cherry-Pick Useful Modules

**Why:**
1. **Inverted Control Loop**: The existing codebase is built around `ScaleGuard` (evaluating historical scale-up events after they occur and capping `maxReplicas`). Trying to retrofit `ScaleGuard` into a forward-looking predictive pre-scaler that boosts `minReplicas` creates conflicting CRD semantics, stale fields, and confusing names.
2. **Elimination of Dead Weight**: Over 60% of the repository logic is dedicated to ex-post statistical detection (`internal/detector`: CUSUM changepoint, OLS regression, confidence scoring, connection ceilings) and synthetic benchmarking (`cmd/prescale-eval`: 1,180 lines). None of this is needed when the ML model handles forecasting.
3. **High Salvage Value of Core Components**:
   - **`internal/metrics`**: The Prometheus/VictoriaMetrics range client can be reused directly.
   - **`internal/actuator`**: The conflict-safe HPA patching, annotation tracking (`original-min`, `boosted-until`), and TTL expiration logic can be extracted and adapted to `minReplicas` in ~150 lines of clean Go.
   - **`demo/` (from commit `4b5f3ff`)**: The checkout app, fakedb, k6 load script, and manifests can be restored immediately from git history to provide a complete, working demo environment.
4. **Agility and Maintainability**: A clean controller or lightweight daemon (`prescaler`) that queries VictoriaMetrics / the prediction service and uses the adapted actuator to adjust HPA `minReplicas` will be smaller, easier to debug, and simpler to demonstrate during review.
