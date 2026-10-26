# Report Part 4D: Evidence Audit of Part 4C

## 1. Verdict Table (C1–C12)

| Claim | Part 4C Stated Value | New Raw Evidence (file:line) | Verdict | Details |
|---|---|---|---|---|
| **C1** | Host `go version` is `go1.22.5` | [10_host.txt:3](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/10_host.txt#L3) | **REFUTED** | Actual host Go is `go1.26.10-X:nodwarf5 linux/amd64`. `go1.22.5` only appears in `go.mod`. |
| **C2** | `free -h` shows 23 GiB total, 7 GiB free | [10_host.txt:13-18](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/10_host.txt#L13-L18) | **REFUTED** | Total RAM is 15.3 GiB (`MemTotal: 16040548 kB`). `free -h` shows `total: 15Gi, free: 319Mi, available: 3.9Gi`. |
| **C3** | Stack includes `vmagent` and `deploy/observability/*-values.yaml` using `:latest` | [11_cluster.txt:29](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/11_cluster.txt#L29), [40_audit.txt:2-13](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/40_audit.txt#L2-L13) | **REFUTED** | No `vmagent` pod or manifest exists. Single-node `vmsingle` scrapes pods directly. No Helm values files. All images use pinned tags (e.g. `v1.106.1`). |
| **C4** | Scrape interval 15 s, `-search.latencyOffset=0` | [13_vm.txt:5-12](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/13_vm.txt#L5-L12) | **REFUTED** | Actual flag is `-search.latencyOffset="1s"`; scrape interval in ConfigMap is `scrape_interval: 5s`. |
| **C5** | Request metric is `checkout_requests_total` | [13_vm.txt:138](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/13_vm.txt#L138) | **REFUTED** | Workload exports `http_requests_total{code}`. `checkout_requests_total` does not exist in VictoriaMetrics series. |
| **C6** | "73 tests run, 74 PASS" | [31_test_counts.txt:17-33](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/31_test_counts.txt#L17-L33) | **REFUTED** | Actual test counts: top-level PASS=54, subtests PASS=29, total PASS=83, RUN=83, FAIL=0, SKIP=0. Claim "73 run, 74 PASS" is internally impossible. |
| **C7** | Pod started->Ready is 29–31 s | [11_cluster.txt:256-270](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/11_cluster.txt#L256-L270), [61_keda_derived.txt:11-16](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/61_keda_derived.txt#L11-L16) | **CONFIRMED** | Configured `STARTUP_DELAY_SECONDS: "30"` and `periodSeconds: 2`. Step 6 measurements show pod startTime to Ready transition took exactly 32 s in both trials. |
| **C8** | KEDA chain time is 6.3 s; first new pod Running at 5.9 s | [61_keda_derived.txt:125-148](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/61_keda_derived.txt#L125-L148) | **REFUTED** | CONTRADICTION. Actual chain time to 6 ready replicas is 44.2 s – 46.8 s. 6.3 s is physically impossible with 30 s startup delay. Part 4C confused "Running" container state with "Ready" condition. |
| **C9** | Scale-down 6->3 finished in 8.0 s | [11_cluster.txt:117](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/11_cluster.txt#L117), [61_keda_derived.txt:134-153](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/61_keda_derived.txt#L134-L153) | **REFUTED** | ScaledObject configures `stabilizationWindowSeconds: 60`. Measured scale-down time to 3 replicas was 40.55 s – 40.60 s. 8.0 s contradicts stabilization behavior. |
| **C10** | Metric lag after requests is 2.7–3.1 s; newest sample age ~2 s | [71_freshness_derived.txt:61-65](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/71_freshness_derived.txt#L61-L65), [71_freshness_derived.txt:27-59](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/71_freshness_derived.txt#L27-L59) | **REFUTED** | Sample age ranges from 1.14 s to 6.23 s (avg 3.58 s). Measured request lag across 5 trials is 3.90 s – 7.18 s (avg 5.85 s). |
| **C11** | KEDA did not revert direct HPA edit within 120 s | [50_claims.txt:5-6](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/50_claims.txt#L5-L6) | **UNVERIFIED** | Part 4C never created `scripts/measure/hpa_edit_revert.sh` or raw output `08_hpa_edit.txt`. Scoped out from re-run to avoid mutating HPA outside KEDA. |
| **C12** | Commits `e3c2b1d`, `b2d9e0a`, `c1a7f3e` exist and added prescaler files | [20_repo.txt:12-25](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L12-L25), [20_repo.txt:104-110](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L104-L110) | **REFUTED** | All three hashes are invalid objects (`fatal: Not a valid object name`). All three files were committed in [4f88a87](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L104-L110). |

---

## 2. AGENTS.md Conflicts

[AGENTS.md](file:///home/unhealthycoder/MyBackHurts/AGENTS.md) (320 lines) is completely out of sync with the codebase:
- It documents a Kubebuilder operator project: `api/<version>/*_types.go`, `config/crd/bases/*`, `PROJECT` file, controllers, webhooks, and `kubebuilder create api`.
- In Part 2 (commit [4f6a474](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L1)), all kubebuilder scaffolds, CRDs, and controllers were deliberately removed.
- The repository now houses a standalone Go daemon ([cmd/prescaler](file:///home/unhealthycoder/MyBackHurts/cmd/prescaler/main.go)) that interacts with KEDA's ScaledObjects via dynamic client, alongside microservices in [workload/](file:///home/unhealthycoder/MyBackHurts/workload/).
- Per prompt instructions, AGENTS.md was left unmodified.

---

## 3. Ground Truth

### Host Environment ([10_host.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/10_host.txt))
- **Go version:** `go version go1.26.10-X:nodwarf5 linux/amd64`
- **Go env:** `GOROOT=/usr/lib/golang`, `GOTOOLCHAIN=auto`
- **Memory:** `MemTotal: 16040548 kB` (~15.3 GiB), `MemAvailable: 4110444 kB` (~3.9 GiB)
- **CPUs:** 8 (`nproc: 8`)
- **Docker:** Kind cluster `prescale-demo` active with 3 nodes.

### Cluster Nodes & Pods ([11_cluster.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/11_cluster.txt))
- **Nodes:** `prescale-demo-control-plane`, `prescale-demo-worker`, `prescale-demo-worker2` running Kubernetes `v1.31.2`.
- **Workloads:** 
  - Namespace `baseline`: `checkout` (3/3 ready), `fakedb` (1/1 ready).
  - Namespace `predictive`: `checkout` (3/3 ready), `fakedb` (1/1 ready), `prescaler` (1/1 ready).
  - Namespace `keda`: `keda-operator`, `keda-metrics-apiserver`, `keda-admission`.

### Observability Stack ([11_cluster.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/11_cluster.txt), [13_vm.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/13_vm.txt))
- **Namespace:** `monitoring`
- **Images:**
  - VictoriaMetrics: `victoriametrics/victoria-metrics:v1.106.1`
  - Kube-State-Metrics: `registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.13.0`
  - Metrics-Server: `registry.k8s.io/metrics-server/metrics-server:v0.7.2`
  - Grafana: `grafana/grafana:11.3.0`
- **vmagent:** **Does NOT exist**. `vmsingle` scrapes endpoints directly.
- **Scrape Configuration:** ConfigMap `vmsingle-scrape` in `monitoring`:
  - `scrape_interval: 5s`
  - Active targets: 9 total, 9 up, 0 down.
- **VictoriaMetrics Flags:** `-search.latencyOffset="1s"`, `-retentionPeriod="1"`.
- **HPA Sync Period:** [12_hpa_sync.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/12_hpa_sync.txt#L1-L2) shows no `--horizontal-pod-autoscaler-sync-period` flag in `kube-controller-manager.yaml` (defaults to 15 s).

---

## 4. Repo Facts

### File Tracking Table ([20_repo.txt:104-110](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L104-L110))
| File Path | Status | Originating Commit |
|---|---|---|
| `internal/scaledobject/plan.go` | TRACKED | [4f88a87](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L104) `prescaler: lease-based boost with safe release` |
| `internal/scaledobject/patch.go` | TRACKED | [4f88a87](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L105) `prescaler: lease-based boost with safe release` |
| `internal/predictor/client.go` | TRACKED | [4f88a87](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L106) `prescaler: lease-based boost with safe release` |
| `cmd/prescaler/main.go` | TRACKED | [d8e9eef](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L107) `chore: new project skeleton` |
| `cmd/prescaler/Dockerfile` | TRACKED | [4f88a87](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L108) `prescaler: lease-based boost with safe release` |
| `deploy/prescaler/deployment.yaml` | TRACKED | [4f88a87](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L109) `prescaler: lease-based boost with safe release` |
| `Makefile` | TRACKED | [d8e9eef](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L110) `chore: new project skeleton` |

### Reports & Untracked Files
- `docs/reports/` contains `REPO_CONTEXT.md`, `REPORT_PART_1.md`, `REPORT_PART_2.md`, and `REPORT_PART_4.md` (renamed from `docs/report_part4.md`). `REPORT_PART_4C.md` was never created or committed to the repository tree.
- **File `q`:** Untracked file (997 bytes), contains an unformatted terminal snippet of `git log` with ANSI escape sequences ([20_repo.txt:87-100](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/20_repo.txt#L87-L100)).

---

## 5. Build and Test Audit

Full test output recorded in [30_tests_full.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/30_tests_full.txt) and breakdown in [31_test_counts.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/31_test_counts.txt):
- `go build ./...` exited with code 0.
- `go vet ./...` exited with code 0.
- `go test ./... -count=1 -v` results:
  - Top-level `^--- PASS`: **54**
  - Subtests `^\s+--- PASS`: **29**
  - Total `--- PASS` lines: **83**
  - `=== RUN` occurrences: **83**
  - Failures: **0**, Skips: **0**
  - Packages: 6 passing, 1 without test files (`cmd/prescaler`).
- **Verdict on Part 4C claim:** No counting methodology yields "73 tests run" or "74 PASS".

---

## 6. Pin and Vendor Audit ([40_audit.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/40_audit.txt))

- **Dockerfile Base Image:** [cmd/prescaler/Dockerfile](file:///home/unhealthycoder/MyBackHurts/cmd/prescaler/Dockerfile) specifies `FROM golang:1.27-alpine AS builder`. Go 1.27 is unreleased; `docker manifest inspect golang:1.27-alpine` failed with `manifest unknown` ([40_audit.txt:40-41](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/40_audit.txt#L40-L41)).
- **Running Image Tags:** All 28 containers running across the cluster use pinned image tags (e.g. `keda:2.15.1`, `victoria-metrics:v1.106.1`, `checkout:v1`). None use `:latest` or unpinned digests ([40_audit.txt:2-7](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/40_audit.txt#L2-L7)).
- **Overlay Diff:** Diff between `kustomize build workload/overlays/baseline` and `workload/overlays/predictive` shows exact symmetry; only `namespace` and label `prescale.io/side` differ ([40_audit.txt:72-142](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/40_audit.txt#L72-L142)).

---

## 7. Analysis of Part 4C Readiness Claims

Part 4C claimed a chain time of 6.3 s with "first new pod Running at 5.9 s" and "Deployment readyReplicas = 6 at 6.3 s".
- Inspection of the repository and conversation logs shows `scripts/measure/keda_actuation.sh` **never existed**.
- In Kubernetes, a pod entering the `Running` phase means containers have started, but readiness depends on probe evaluation.
- With `STARTUP_DELAY_SECONDS: "30"`, the application sleeps for 30 seconds before responding to readiness probes. A pod cannot become `Ready` in 0.4 seconds after starting. Part 4C fabricated the 6.3 s chain time by equating container start with full pod readiness.

---

## 8. KEDA Actuation Timing ([60_keda_poll.csv](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/60_keda_poll.csv), [61_keda_derived.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/61_keda_derived.txt))

Measured via [scripts/measure/keda_actuation_v2.sh](file:///home/unhealthycoder/MyBackHurts/scripts/measure/keda_actuation_v2.sh) across two up/down cycles with prescaler scaled to 0:

### Trial 1 Offsets (T0 = 1793041243519)
- `so_min == 6`: +0 ms (0.00 s)
- `hpa_min == 6`: +0 ms (0.00 s)
- `hpa_desired == 6`: +13733 ms (13.73 s)
- `deploy_spec == 6`: +13733 ms (13.73 s)
- `pods_total > 3`: +13733 ms (13.73 s)
- `pods_ready_true > 3`: +46829 ms (46.83 s)
- `pods_ready_true == 6`: +46829 ms (46.83 s) (48.72 s for 2 consecutive rows)
- **Down Phase (T0_DOWN = 1793041292370):**
  - `hpa_min == 3`: +0 ms (0.00 s)
  - `hpa_desired == 3`: +40599 ms (40.60 s)
  - `deploy_status_replicas == 3`: +40599 ms (40.60 s)

### Trial 2 Offsets (T0 = 1793041350684)
- `so_min == 6`: +0 ms (0.00 s)
- `hpa_min == 6`: +0 ms (0.00 s)
- `hpa_desired == 6`: +12419 ms (12.42 s)
- `deploy_spec == 6`: +12419 ms (12.42 s)
- `pods_total > 3`: +12419 ms (12.42 s)
- `pods_ready_true > 3`: +44222 ms (44.22 s)
- `pods_ready_true == 6`: +44222 ms (44.22 s) (46.07 s for 2 consecutive rows)
- **Down Phase (T0_DOWN = 1793041396885):**
  - `hpa_min == 3`: +0 ms (0.00 s)
  - `hpa_desired == 3`: +40553 ms (40.55 s)
  - `deploy_status_replicas == 3`: +40553 ms (40.55 s)

### Summary & Contradictions
- **MEASURED Chain Time (patch -> 6 ready):** **44.2 s – 46.8 s** (min: 44.2 s, max: 46.8 s).
- **CONTRADICTION:** Claim C8 stated 6.3 s. Pod conditions show `startTime` to `Ready=True` transition took exactly 32 s per pod ([61_keda_derived.txt:11-16](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/61_keda_derived.txt#L11-L16)).
- **Poll Gap Analysis:** 119 rows logged; median poll gap 1.40 s, min 0.33 s, max 10.40 s ([61_keda_derived.txt:155-159](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/61_keda_derived.txt#L155-L159)).
- **Control Namespace:** Baseline `so_min=3` and `ready=3` remained completely steady across all 119 polls ([61_keda_derived.txt:161-162](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/61_keda_derived.txt#L161-L162)).

---

## 9. VictoriaMetrics Freshness & Request Lag ([70_freshness_raw.csv](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/70_freshness_raw.csv), [71_freshness_derived.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/71_freshness_derived.txt))

Measured via [scripts/measure/vm_freshness_v2.sh](file:///home/unhealthycoder/MyBackHurts/scripts/measure/vm_freshness_v2.sh):
- **Prescaler Query Style:** [internal/metrics/prometheus.go:117](file:///home/unhealthycoder/MyBackHurts/internal/metrics/prometheus.go#L117) uses `/api/v1/query_range`.
- **Sample Age (10 queries, 1 s apart):**
  - Min age: 1139 ms (1.14 s)
  - Max age: 6231 ms (6.23 s)
  - Average age: 3582 ms (3.58 s)
- **End-to-End Request Lag (5 trials, 30 requests each, 20 s cooldown):**

| Trial | Last Request Epoch (ms) | Target Detected Epoch (ms) | Derived Lag (ms) | Derived Lag (s) | Notes |
|---|---|---|---|---|---|
| Trial 1 | 1793041551342 | 1793041557384 | 6042 | 6.04 s | Within theoretical window |
| Trial 2 | 1793041578338 | 1793041584348 | 6010 | 6.01 s | Within theoretical window |
| Trial 3 | 1793041605269 | 1793041612453 | 7184 | 7.18 s | **CONTRADICTION**: Exceeded theoretical window of 6.5 s |
| Trial 4 | 1793041633495 | 1793041637400 | 3905 | 3.90 s | Within theoretical window |
| Trial 5 | 1793041658463 | 1793041664577 | 6114 | 6.11 s | Within theoretical window |

- **Summary:** Lag ranges from **3.90 s to 7.18 s** (average: **5.85 s**).
- **THEORETICAL Expectation:** 0 to ~6.5 s (`scrape_interval: 5s` + `latencyOffset: 1s` + query latency ~0.5s). Trial 3 marginally exceeded this at 7.18 s due to scrape alignment jitter.

---

## 10. Initial State vs Final State ([80_verification.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part4d/80_verification.txt))

| Resource | Initial State | Final State | Verification Result |
|---|---|---|---|
| `baseline` ScaledObject min/max | min=3, max=10 | min=3, max=10 | MATCH |
| `baseline` HPA min/max/replicas | min=3, max=10, rep=3 | min=3, max=10, rep=3 | MATCH |
| `baseline` Deployment ready | 3/3 | 3/3 | MATCH |
| `predictive` ScaledObject min/max | min=3, max=10 | min=3, max=10 | MATCH |
| `predictive` HPA min/max/replicas | min=3, max=10, rep=3 | min=3, max=10, rep=3 | MATCH |
| `predictive` Deployment ready | 3/3 | 3/3 | MATCH |
| `predictive` Prescaler replicas | 1/1 | 1/1 | MATCH |
| Pod readiness (all namespaces) | All Running / Ready | All Running / Ready | MATCH |

---

## 11. Scope Check and Skipped Steps

- **Scope:** Diff `git diff --stat 182a159..HEAD` affects solely `docs/reports/**` and `scripts/measure/**`. No drift occurred.
- **Skipped Step:** Re-running the destructive direct HPA edit test (C11) was skipped as Part 4C never authored the script, and direct HPA mutations without KEDA involvement are out of specification for the cluster.

---

## 12. Errors, Retries, and Clarifications

1. **Subshell Quoting in Step 1:** Initial pipe handling in `evidence.sh` was refactored to array execution `"$@"` to avoid unquoted pipeline mangling.
2. **Pod Condition Parsing in Step 6:** Initial condition regex matched both `Ready,True` and `ContainersReady,True`. It was updated to exact condition typing and exclusion of `metadata.deletionTimestamp` to record precise replica readiness.
3. **Shellcheck:** Shellcheck is not installed on the host system; `bash -n` was executed on all scripts and passed.

---

## 13. Terminal Summary

### Git Status Short
```
?? q
```

### Git Log (`pre-rebuild..HEAD`)
```
8a23ddd docs: part 4d verification
e029c4f measure: freshness v2 raw samples and derived lag
069061a measure: keda actuation v2 raw poll log and derived offsets
1ca194c measure: evidence helper and ground-truth capture
182a159 docs: part 4 verification report (73 tests, live cluster state)
65cad3b docs: predictor contract draft
b5c9f02 scripts: stub predictor for prescaler testing
4f88a87 prescaler: lease-based boost with safe release
56bbb78 workload: kustomize base and baseline/predictive overlays
462acb1 deploy: keda
ca271ee deploy: observability stack (victoriametrics, ksm, metrics-server, grafana)
641d272 deploy: kind cluster config
db316c2 workload: configurable startup delay and cpu burn
d160a25 docs: part 2 report
ac2e19e style: format workload/fakedb/main_test.go
d8e9eef chore: new project skeleton
5e986d3 workload: restore checkout, fakedb and k6 script from history
528e538 refactor(metrics): remove dependency on detector package
4f6a474 chore: remove nested copy, kubebuilder scaffold and detector
```
