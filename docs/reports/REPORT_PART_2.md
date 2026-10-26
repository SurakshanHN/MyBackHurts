# REPORT_PART_2: Repository Restructure, Decoupling, Workload Restoration & Green Build

## 1. TL;DR
- **Rebuild Branch**: Switched to `predictive-rebuild` with full snapshot tagged `pre-rebuild` (`e9ffd69`).
- **Pruned Dead Code**: Removed nested `prescale/prescale/` (64 files), Kubebuilder CRDs/scaffolding, detector (14 files), and eval harness (11 files).
- **Decoupled Packages**: `internal/metrics` decoupled from `detector` by inlining `Sample` and `DepSample`; all 25 unit tests in `metrics` and `actuator` pass.
- **Restored Workload**: `checkout`, `fakedb`, manifests, and k6 script restored from commit `bd0ac46~1` into `workload/` and `loadtest/`; empty demo stubs purged.
- **New Skeleton**: Created lightweight `cmd/prescaler/main.go` daemon (standard library only) with CLI flags and graceful shutdown.
- **Build Status**: `gofmt -l .` is clean, `go build ./...`, `go vet ./...`, and `go test ./...` all pass 100% (31 total unit tests passing).
- **Dependency Reductions**: Pruned `ginkgo` and `gomega` from direct dependencies; direct dependencies now limited to Kubernetes API and Prometheus client.
- **Ready for Next Part**: Actuator is ready for inversion (raising `minReplicas` ahead of predicted surges).

---

## 2. Git Safety
- **Snapshot Commit Hash**: `e9ffd69` (`wip: snapshot before rebuild`) on branch `main`
- **Git Tag**: `pre-rebuild`
- **Working Branch**: `predictive-rebuild`
- **Commits on Branch (`git log --oneline pre-rebuild..HEAD`)**:
  ```
  ac2e19e style: format workload/fakedb/main_test.go
  d8e9eef chore: new project skeleton
  5e986d3 workload: restore checkout, fakedb and k6 script from history
  528e538 refactor(metrics): remove dependency on detector package
  cda4529 chore: remove nested copy, kubebuilder scaffold and detector
  ```

---

## 3. Protection-Rule Check Results
- **Files Searched**: `AGENTS.md`, `README.md`, `.github/workflows/`, and recursive regex searches for `"protected"` across all `*.md`, `*.yaml`, `*.json`, and `*.toml` files.
- **Findings**:
  - No repository rule files (such as `.cursorrules`, `.agent/`, `CLAUDE.md`, or `CONTRIBUTING.md`) define any files as protected from deletion.
  - The commit message `ff69ab9` (*"revert accidental formatting of protected file prescale/prescale/internal/detector/types.go"*) was authored by Varchas H V and reverted a 1-character comment whitespace change.
  - Commits touching `prescale/prescale/`:
    - `ff69ab9`: Varchas H V (revert comment whitespace)
    - `bf9a644`: Varchas H V (controller decision rules)
    - `be6bf35`: SurakshanHN (README update)
    - `c67df35`: Varchas H V (initial scaffolding)
  - **Verdict**: There are no formal file protection constraints. Deleting the abandoned nested scaffold was safe and completed.

---

## 4. Deleted Components and Approximate Line Counts

| Deleted Component / Group | File Count | Line Count | Notes |
| :--- | :--- | :--- | :--- |
| `prescale/prescale/` (nested scaffold) | 64 files | 4,510 lines | Stale duplicate from initial commit |
| `api/` (CRD schemas and types) | 4 files | 618 lines | ScaleGuard & ScaleDecision CRDs |
| `config/` (Kubebuilder RBAC, manifests) | 33 files | 1,257 lines | Kustomize manifests for old operator |
| `hack/` | 1 file | 15 lines | Boilerplate header |
| `test/` (e2e suite and envtest utils) | 3 files | 684 lines | Old e2e tests |
| `cmd/` (`main.go`, `demo`, `eval`, `live`) | 7 files | 2,337 lines | Old manager and evaluation CLIs |
| `internal/controller/` | 4 files | 1,195 lines | ScaleGuard reconciler & decision loop |
| `internal/detector/` | 14 files | 3,314 lines | CUSUM, OLS baseline, causal inference |
| `internal/report/` | 2 files | 396 lines | Text report generation for detector |
| Scaffolding & misc (`PROJECT`, `Dockerfile`, etc.) | 5 files | 879 lines | Old Kubebuilder metadata & Makefile |
| `bin/` (committed local binaries) | 7 files | 46.1 MB blob | Committed local etcd/apiserver binaries |
| **Total Deleted** | **144 files** | **~15,205 lines** | Cleanly removed from working tree |

---

## 5. Decoupling Details

### What Moved
- Removed `github.com/prescale/prescale/internal/detector` import across `internal/metrics`.
- Moved `Sample` and `DepSample` struct definitions directly into `internal/metrics/types.go` with identical fields:
  ```go
  type Sample struct {
      T                   time.Time
      Replicas, Ready     int
      RPS, P99Ms, ErrRate float64
  }

  type DepSample struct {
      T          time.Time
      Saturation float64
      LatencyMs  float64
  }
  ```
- Updated `internal/metrics/prometheus.go`, `fake.go`, and `metrics_test.go` to use local `Sample` and `DepSample`.
- `internal/actuator` had zero dependencies on deleted packages and required no source modifications.

### Unit Tests Before and After
- `internal/metrics`: 13 tests passed before, **13 tests passed after** (0 dropped).
- `internal/actuator`: 12 tests passed before, **12 tests passed after** (0 dropped).

### Kubernetes Client Library & Test Helpers Used by Actuator
- **Client Library**: `sigs.k8s.io/controller-runtime/pkg/client` (`client.Client`).
- **Test Helpers**: `sigs.k8s.io/controller-runtime/pkg/client/fake` (`fake.NewClientBuilder()`) and `sigs.k8s.io/controller-runtime/pkg/client/interceptor`.

### Direct `go.mod` Dependencies Before vs After
- **Before**:
  - `github.com/onsi/ginkgo/v2 v2.27.4`
  - `github.com/onsi/gomega v1.39.0`
  - `k8s.io/apimachinery v0.37.0`
  - `k8s.io/client-go v0.37.0`
  - `sigs.k8s.io/controller-runtime v0.25.0`
- **After**:
  - `github.com/prometheus/client_golang v1.24.0` (required by restored workload)
  - `k8s.io/api v0.37.0`
  - `k8s.io/apimachinery v0.37.0`
  - `sigs.k8s.io/controller-runtime v0.25.0`
- Pruned Ginkgo, Gomega, and raw client-go from direct dependencies.

---

## 6. Restored Workload

### Source Commit
- Restored from `bd0ac46~1` (commit `3879adb`, the last commit before `bd0ac46` truncated files to 0 bytes).

### Restored Files and Sizes

| Restored File | Size | Destination |
| :--- | :--- | :--- |
| `demo/checkout/Dockerfile` | 471 B | `workload/checkout/Dockerfile` |
| `demo/checkout/main.go` | 4,693 B | `workload/checkout/main.go` |
| `demo/checkout/main_test.go` | 2,216 B | `workload/checkout/main_test.go` |
| `demo/fakedb/Dockerfile` | 457 B | `workload/fakedb/Dockerfile` |
| `demo/fakedb/main.go` | 5,082 B | `workload/fakedb/main.go` |
| `demo/fakedb/main_test.go` | 4,177 B | `workload/fakedb/main_test.go` |
| `demo/k8s/checkout.yaml` | 1,734 B | `workload/k8s/checkout.yaml` |
| `demo/k8s/fakedb.yaml` | 1,343 B | `workload/k8s/fakedb.yaml` |
| `demo/k8s/namespace.yaml` | 54 B | `workload/k8s/namespace.yaml` |
| `demo/k6/surge.js` | 2,199 B | `loadtest/surge.js` |
| `demo/README.md` | 4,878 B | `docs/old-demo-README.md` |

- **Dropped**: `demo/k8s/monitoring.yaml` (Prometheus Operator `PodMonitor`) was deliberately dropped; scrape configuration will be handled via `vmagent`.
- **Demo Directory**: Old `demo/` directory completely removed.
- **Build & Test Result**: `go build ./...` compiles cleanly; `go test ./workload/...` passes 100% (6 unit tests).

---

## 7. Workload Facts for Part 3

### `checkout` Service
- **Default Port**: `8080` (configured via `PORT` env var).
- **HTTP Endpoints**:
  - `POST /`: Main business transaction handler.
  - `GET /healthz`: Health/readiness probe (`200 OK`).
  - `GET /metrics`: Prometheus scrape endpoint.
- **Work Execution & CPU Burn**:
  1. Acquires a slot from a concurrency channel (`CONNS_PER_POD`, default `20`).
  2. Issues a `POST /query` HTTP call to downstream `fakedb` (`DB_URL`, default `http://fakedb:8080`).
  3. Propagates non-200 responses if downstream fails.
  4. Runs `BurnCPU(5 * time.Millisecond)`: a tight spin-loop computing `math.Sqrt(12345.6789)` to burn approximately 5ms of 100% CPU core time per request.
  5. Returns HTTP 200 with JSON `{"status": "checkout complete"}`.
- **Environment Variables**:
  - `PORT`: Server listen port (default `8080`).
  - `DB_URL`: Downstream fakedb endpoint (default `http://localhost:8080`).
  - `CONNS_PER_POD`: Max concurrent requests per pod (default `20`).
- **Startup Delays & Probes**:
  - No artificial startup delay in Go code.
  - Kubernetes probes configured with `initialDelaySeconds: 2`, `periodSeconds: 5`.
- **Exposed Prometheus Metrics**:
  - `http_requests_total{code="<http_status>"}` (CounterVec)
  - `http_request_duration_seconds` (Histogram, 12 buckets: `0.005` to `10.0` seconds).
- **HPA & Resource Settings (`workload/k8s/checkout.yaml`)**:
  - `minReplicas`: `3`, `maxReplicas`: `10`
  - Autoscaling Metric: `cpu` average utilization target of `50%`
  - Requests: `cpu: 100m`, `memory: 64Mi`
  - Limits: `cpu: 500m`, `memory: 256Mi`

### `fakedb` Service
- **Default Port**: `8080` (configured via `PORT` env var).
- **HTTP Endpoints**:
  - `POST /query`: Simulates database query execution.
  - `GET /mode` & `POST /mode?mode=<normal|contention>`: Query or dynamically switch operational mode.
  - `GET /healthz`: Health/readiness probe (`200 OK`).
  - `GET /metrics`: Prometheus scrape endpoint.
- **Operating Modes & Concurrency**:
  - Max connections: `MAX_CONNS` env var (default `100`).
  - If active queries >= `MAX_CONNS`, immediately rejects requests with `503 Service Unavailable`.
  - In `normal` mode: query latency is a flat `~10ms`.
  - In `contention` mode: latency grows quadratically with concurrency:
    $$\text{Latency} = 10\text{ms} \times \left(1 + \frac{\text{active\_queries}}{20}\right)^2$$
- **Exposed Prometheus Metrics**:
  - `db_active_connections` (Gauge)
  - `db_max_connections` (Gauge)
  - `db_query_duration_seconds` (Histogram, 11 buckets: `0.005` to `5.0` seconds).

### `surge.js` Load Test Script
- **Target URL**: `__ENV.TARGET_URL` (default `http://localhost:8080`).
- **FakeDB URL**: `__ENV.FAKEDB_URL` (default `http://localhost:8081`).
- **Load Mode**: `__ENV.MODE` (default `contention`; options `contention` or `healthy`).
- **Setup Routine**: Automatically issues `POST ${FAKEDB_URL}/mode?mode=${dbMode}` before starting traffic ramp.
- **Scenario Stages (`ramping-arrival-rate`)**:
  1. `20 RPS` for 1m (baseline healthy traffic; CPU stays under 50% on 3 pods).
  2. Ramp `20 -> 120 RPS` over 1m (drives CPU > 50% target, triggering reactive HPA scale-out).
  3. Sustained `120 RPS` for 2m (evaluates whether pods become ready before latency degrades).
  4. Cool down `120 -> 20 RPS` over 1m.
- **Thresholds**: `checks: ['rate>0']` (ensures test completes even when 503s occur during contention).

### Manifest Completeness & Prometheus Dependencies
- `workload/k8s/checkout.yaml`, `fakedb.yaml`, and `namespace.yaml` are complete, standard Kubernetes manifests.
- Neither file references Prometheus Operator CRDs (`PodMonitor` or `ServiceMonitor`).
- Both pods include standard annotations:
  - `prometheus.io/scrape: "true"`
  - `prometheus.io/port: "8080"`
  - `prometheus.io/path: "/metrics"`

---

## 8. New Directory Tree

```
.
├── AGENTS.md
├── cmd
│   └── prescaler
│       └── main.go
├── deploy
│   └── README.md
├── docs
│   ├── old-demo-README.md
│   └── reports
│       ├── REPORT_PART_1.md
│       ├── REPORT_PART_2.md
│       └── REPO_CONTEXT.md
├── experiments
│   └── README.md
├── go.mod
├── go.sum
├── internal
│   ├── actuator
│   │   ├── actuator.go
│   │   ├── actuator_test.go
│   │   ├── plan.go
│   │   └── plan_test.go
│   └── metrics
│       ├── fake.go
│       ├── metrics_test.go
│       ├── prometheus.go
│       └── types.go
├── loadtest
│   └── surge.js
├── Makefile
├── predictor
│   └── README.md
├── README.md
└── workload
    ├── checkout
    │   ├── Dockerfile
    │   ├── main.go
    │   └── main_test.go
    ├── fakedb
    │   ├── Dockerfile
    │   ├── main.go
    │   └── main_test.go
    └── k8s
        ├── checkout.yaml
        ├── fakedb.yaml
        └── namespace.yaml
```

---

## 9. Verification Results

| Check / Command | Result | Details |
| :--- | :--- | :--- |
| `gofmt -l .` | **PASS** | Output is completely empty. |
| `go build ./...` | **PASS** | All binaries and packages compile without warnings. |
| `go vet ./...` | **PASS** | Zero issues detected across all packages. |
| `go test ./...` | **PASS** | 31 total tests passed: `actuator` (12), `metrics` (13), `checkout` (3), `fakedb` (3). |
| `make build` | **PASS** | Builds `./bin/prescaler` binary cleanly. |
| `./bin/prescaler --help` | **PASS** | Displays flags: `--victoria-url`, `--predictor-url`, `--namespace`, `--hpa`, `--interval`, `--dry-run`. |
| 3-Second Run (`--dry-run`) | **PASS** | Ticked 3 times at 1s intervals, captured SIGINT, and logged clean shutdown. |

---

## 10. Deviations and Problems Encountered
1. **Formatting in Restored Test File**:
   - `workload/fakedb/main_test.go` had a minor tab indentation variance in its test table from history. Ran `gofmt -s -w` and committed the fix (`ac2e19e`) so `gofmt -l .` reports zero files.
2. **Setup-envtest Binary Permissions**:
   - The committed binaries in `bin/` had read-only (chmod 0555) permissions on the etcd/apiserver files, causing `rm` to fail on first attempt. Resolved with `chmod -R u+w bin` before removing.

---

## 11. UNCLEAR Items and Questions
1. **Actuator Inversion Interface (Part 3)**:
   - Should `internal/actuator` completely replace the `maxReplicas` capping logic with `minReplicas` boosting, or should it retain capping as an optional downward guard in case of unexpected downstream failure?
2. **Workload Namespaces for Side-by-Side Comparison**:
   - In Part 3/4, will the baseline (HPA-only) and prescaled workloads run in separate namespaces (e.g., `demo-baseline` vs `demo-prescaled`), or in the same namespace with prefixed names (`checkout-baseline` vs `checkout-prescaled`)?
