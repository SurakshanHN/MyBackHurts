# prescale — Product & Technical Requirements Document

## 1. Executive Summary & Overview

**prescale** is a Kubernetes operator designed to protect cloud workloads from harmful or ineffective Horizontal Pod Autoscaler (HPA) scale-up events. 

In traditional Kubernetes environments, an HPA reacts to resource thresholds (such as CPU, memory, or custom metrics) and increases replica counts. However, when downstream dependencies (e.g., databases, caches, messaging queues, external APIs) are saturated, or when an application suffers from lock contention, connection exhaustion, or thrashing:
- Additional replicas **worsen latency and error rates** (e.g., connection storms, cascading failures).
- Or additional replicas **consume cluster resources without any measurable throughput or latency improvement** (wasted capacity).

prescale continuously evaluates HPA scaling events against actual service metrics (RPS, P99 latency, error rates) and downstream dependency saturation using Prometheus and statistical causal inference. When harmful scale-ups are detected in `enforce` mode, prescale automatically caps `maxReplicas` on the HPA, protects the cluster from cascading failures, and records full audit trails for site reliability engineers (SREs).

---

## 2. Goals & Key Objectives

### 2.1 Primary Goals
1. **Prevent Cascading Failures**: Automatically intervene when pod scaling degrades application latency or error rates.
2. **Causal Impact Assessment**: Classify every scale-up into clear verdicts:
   - `helped`: Throughput improved or latency/error rates improved/remained stable under load.
   - `useless`: Added replicas yielded no measurable performance gain.
   - `harmful`: Latency or error rates degraded significantly post-scaling.
   - `unknown`: Insufficient data, high metric volatility, or low statistical confidence.
3. **Root-Cause Attribution**: Identify whether a scale-up failure was caused by downstream dependency saturation (e.g., database connection limits).
4. **Safe, Self-Healing Actuation**: Apply temporary, non-destructive replica caps that never violate safety floors and automatically restore after a configured duration.
5. **Auditable Decision Records**: Generate immutable `ScaleDecision` custom resources for every evaluation.

### 2.2 Non-Goals
- Replacing standard HPA algorithms or predictive autoscalers (KEDA, HPA v2).
- Managing pod scale-downs (scale-down remains controlled by HPA downscale stabilization policies).
- Dynamically tuning application code or database capacity.

---

## 3. System Architecture & Component Interaction

```
┌─────────────────────────────────────────────────────────────┐
│                       Kubernetes                            │
│                                                             │
│  ┌──────────────┐          Scale Event       ┌───────────┐  │
│  │     HPA      │───────────────────────────▶│ScaleGuard │  │
│  │ (scales up)  │◀──────────────┐            │Controller │  │
│  └──────────────┘               │            └─────┬─────┘  │
│         ▲                       │                  │        │
│         │ Scales                │ Patch            │        │
│  ┌──────┴──────┐                │ (maxReplicas)    │        │
│  │ Deployment  │                │                  │        │
│  └─────────────┘        ┌───────┴──────┐           │        │
│                         │ HPA Actuator │           │        │
│                         └──────────────┘           │        │
└─────────────────────────────────┬──────────────────┼────────┘
                                  │                  │
                           Queries Metrics           │ Evaluates
                                  │                  ▼
                         ┌────────▼──────┐     ┌───────────┐
                         │  Prometheus   │────▶│ Detector  │
                         │    Server     │     │  Engine   │
                         └───────────────┘     └─────┬─────┘
                                                     │
                                               Creates Record
                                                     ▼
                                              ┌─────────────┐
                                              │ScaleDecision│
                                              │(Audit CRD)  │
                                              └─────────────┘
```

The system consists of 5 core modules:
1. **Controller (`internal/controller/`)**: Reconciles `ScaleGuard` resources, monitors HPA scale-up occurrences, orchestrates data gathering, invokes the detector, and triggers the actuator.
2. **Metrics Collector (`internal/metrics/`)**: Queries Prometheus for range vectors (RPS, P99 latency, error rates, dependency saturation).
3. **Detector Engine (`internal/detector/`)**: Executes Ordinary Least Squares (OLS) counterfactual baseline projections and CUSUM changepoint analysis to classify the impact of scaling.
4. **Actuator (`internal/actuator/`)**: Safely caps or restores HPA `maxReplicas` using Kubernetes annotations to preserve original specifications.
5. **API / CRD Layer (`api/v1alpha1/`)**: Defines `ScaleGuard` and `ScaleDecision` Custom Resource Definitions.

---

## 4. Custom Resource Definitions (CRDs) Specification

### 4.1 `ScaleGuard` (Scope: Namespaced)
`ScaleGuard` defines the monitoring policy and protection guardrails for a specific HPA.

#### Spec Fields (`ScaleGuardSpec`)
| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `hpaName` | `string` | Yes | - | Name of the target HorizontalPodAutoscaler in the same namespace. |
| `mode` | `string` (`observe` \| `enforce`) | Yes | - | Operating mode: `observe` records decisions only; `enforce` caps HPA on harmful events. |
| `prometheusURL` | `string` | Yes | - | Base URL of the Prometheus server (e.g., `http://prometheus:9090`). |
| `queries.rps` | `string` | Yes | - | PromQL query returning request rate (requests/sec) for the workload. |
| `queries.p99Millis` | `string` | Yes | - | PromQL query returning 99th percentile latency in milliseconds. |
| `queries.errorRate` | `string` | Yes | - | PromQL query returning error rate as a float between 0.0 and 1.0. |
| `dependencies` | `[]Dependency` | No | `[]` | List of downstream services to monitor for saturation. |
| `dependencies[].name` | `string` | Yes | - | Unique identifier for the dependency (e.g., `postgres`, `redis`). |
| `dependencies[].saturationQuery` | `string` | Yes | - | PromQL returning saturation percentage (0–100%). |
| `dependencies[].thresholdPercent` | `int` | Yes | `90` | Saturation percentage threshold above which dependency is considered saturated. |
| `warmupWindow` | `metav1.Duration` | Yes | `2m` | Duration to wait after a scale-up before sampling post-scale metrics. |
| `maxFreezeDuration` | `metav1.Duration` | Yes | `30m` | Time duration to keep an active cap before automatically unfreezing / restoring original `maxReplicas`. |
| `minReplicasFloor` | `int32` | Yes | - | Absolute minimum floor for `maxReplicas`; capping will never reduce `maxReplicas` below this value. |

#### Status Fields (`ScaleGuardStatus`)
| Field | Type | Description |
|---|---|---|
| `phase` | `string` | Current operational phase: `Idle`, `Evaluating`, `Capped`, `Restored`. |
| `lastVerdict` | `string` | Most recent verdict: `helped`, `useless`, `harmful`, or `unknown`. |
| `lastConfidence` | `string` | Confidence level of the last verdict (formatted float 0.00 to 1.00). |
| `lastMessage` | `string` | Human-readable explanation of the last decision or action taken. |
| `cap` | `*CapStatus` | Details of the active cap if currently capped: `originalMaxReplicas`, `cappedAt`, `since`, and `reason`. |
| `trustScore` | `int` | Running health/trust score (range: 0–100) reflecting workload autoscaling health over time. |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes status conditions (`Ready`, `PrometheusReachable`, `HPAExists`). |

---

### 4.2 `ScaleDecision` (Scope: Namespaced)
`ScaleDecision` represents an immutable audit log entry created each time a scale-up is evaluated.

#### Spec Fields (`ScaleDecisionSpec`)
| Field | Type | Description |
|---|---|---|
| `guardName` | `string` | Name of the parent `ScaleGuard` resource. |
| `time` | `metav1.Time` | Timestamp of the scaling evaluation. |
| `verdict` | `string` | Evaluation verdict (`helped`, `useless`, `harmful`, `unknown`). |
| `confidence` | `string` | Statistical confidence of the verdict (0.0 to 1.0). |
| `fromReplicas` | `int32` | Replica count before scale-up. |
| `toReplicas` | `int32` | Replica count after scale-up. |
| `culprit` | `string` | Identified saturated dependency name, if any (empty string if none). |
| `reasons` | `[]string` | Plain-English factual explanations (e.g., `"P99 rose 82% vs counterfactual"`). |
| `actionTaken` | `string` | Action performed by the operator: `observed`, `capped`, or `restored`. |

#### Status Fields (`ScaleDecisionStatus`)
| Field | Type | Description |
|---|---|---|
| `outcome` | `string` | Post-evaluation observation (e.g., `"Cap prevented 4 further harmful scale-ups"`). |
| `estimatedMinutesSaved` | `int` | Estimated operational or degraded minutes mitigated. |

---

## 5. Functional Requirements

### 5.1 Event Detection & Lifecycle
1. **Target Resource Binding**:
   - Each `ScaleGuard` is associated with exactly one HPA in the same namespace via `spec.hpaName`.
   - The controller must verify the existence of the target HPA and update condition `HPAExists`.
2. **Scale-Up Detection**:
   - The controller must observe changes in `hpa.status.currentReplicas` or `hpa.status.desiredReplicas`.
   - When replicas increase (`toReplicas > fromReplicas`), a scale-up event is registered with timestamp $T_{scale}$.
3. **Warmup Window**:
   - After $T_{scale}$, the controller must wait for `spec.warmupWindow` (e.g., 2 minutes) to allow new pods to start, pass readiness probes, and stabilize traffic routing before sampling metrics.

### 5.2 Metrics Ingestion & Prometheus Integration
1. **Prometheus Queries**:
   - The controller queries Prometheus using HTTP range queries covering $[T_{scale} - \text{preWindow}, T_{scale} + \text{postWindow}]$.
   - Queries must resolve:
     - Workload RPS (`queries.rps`)
     - P99 latency in milliseconds (`queries.p99Millis`)
     - Error rate as ratio 0.0–1.0 (`queries.errorRate`)
     - Saturated dependency metrics (`dependencies[i].saturationQuery`)
2. **Robustness & Error Handling**:
   - Returns `ErrNoData` if Prometheus returns empty result matrices.
   - Returns `ErrStale` if returned metric samples do not cover the requested time window.
   - If Prometheus is unreachable or returns HTTP errors, update condition `PrometheusReachable=False` and set verdict to `unknown`.

### 5.3 Detection & Causal Inference Engine
1. **Counterfactual Baseline Projection (`internal/detector/baseline.go`)**:
   - Utilizes pre-scale-up metrics ($T < T_{scale}$) to compute an Ordinary Least Squares (OLS) linear trend ($y = m \cdot x + b$).
   - Calculates residual standard deviation ($s_{resid}$) and standard error of prediction across degrees of freedom ($n - 2$).
   - Projects expected metric intervals (mean, lower, and upper bounds at 95% confidence with $z = 1.96$).
2. **CUSUM Changepoint Detection (`internal/detector/changepoint.go`)**:
   - Implements two-sided Cumulative Sum (CUSUM) to detect abrupt shifts in latency or error rates post scale-up.
   - Configurable parameters: allowance $k = 0.5$, threshold $h = 8.0$ (standard deviation units).
   - Variance floor ensures near-constant baselines do not trigger false positives on minor background noise (floor scaled to 5% of baseline mean).
3. **Dependency Saturation Analysis**:
   - Computes maximum and average saturation values for each configured dependency during the post-scale-up period.
   - If dependency saturation exceeds `thresholdPercent`, mark that dependency as the `culprit`.
4. **Verdict Determination**:
   - **`helped`**: P99 latency and error rates did not breach the counterfactual prediction upper bound, and throughput (RPS) scaled proportionally or remained stable.
   - **`harmful`**: P99 latency or error rate increased significantly beyond the counterfactual upper bound (by more than `HarmMargin`, default 15%), or CUSUM detected an upward shift.
   - **`useless`**: Replicas increased, but throughput did not increase, and latency/error rates showed no statistically significant change.
   - **`unknown`**: Sample size was too small ($n < \text{MinSamples}$), variance was undefined, or statistical confidence was below `MinConfidence`.

### 5.4 Actuation & Safety Guardrails
1. **Mode Enforcement**:
   - **`observe`**: The controller creates a `ScaleDecision` and logs findings. The target HPA is never modified.
   - **`enforce`**: On a `harmful` verdict, the controller proceeds to cap the HPA.
2. **Annotation Preservation (`internal/actuator/hpa.go`)**:
   - Before modifying HPA `maxReplicas`, the original value is stored in annotation:
     `prescale.io/original-max-replicas: "<original_value>"`
   - The capping operator identity is stored in:
     `prescale.io/capped-by: "scaleguard/<name>"`
3. **Safety Bound Enforcement**:
   - Capped `maxReplicas` is computed as $\max(\text{currentReplicas}, \text{minReplicasFloor})$.
   - The cap will **never** reduce `maxReplicas` below `spec.minReplicasFloor`.
4. **Auto-Restoration**:
   - If an HPA has been capped for longer than `spec.maxFreezeDuration` (default 30 minutes), the actuator automatically restores `maxReplicas` to the original value and removes the cap annotations.
   - ScaleGuard status transitions to `phase: Restored`.

### 5.5 Trust Score Management
- Each `ScaleGuard` maintains a `trustScore` integer (range 0 to 100, initialized to 100):
  - Increments on consecutive `helped` scale-ups (+5 up to 100).
  - Decrements on `useless` scale-ups (-5).
  - Decrements sharply on `harmful` scale-ups (-20 down to 0).
- Provides SREs with an immediate high-level health index for each autoscaled service.

---

## 6. Non-Functional & Operational Requirements

### 6.1 Reliability & Concurrency
- **Idempotency**: Reconciliation must be idempotent. Re-running reconciliation without state change must yield no side effects.
- **Conflict Handling**: Before patching or updating HPA or CRD objects, the controller must re-fetch the latest object from the API server to prevent optimistic concurrency conflicts.
- **Garbage Collection & Ownership**: `ScaleDecision` records must declare `ScaleGuard` as their controller owner (`SetControllerReference`) so they are cleaned up if the guard is deleted.

### 6.2 Security & Compliance
- **Pod Security Standards**: Operator manifests must satisfy the Kubernetes `restricted` pod security standard:
  - Run as non-root user (`runAsNonRoot: true`, `runAsUser: 65532`).
  - Read-only root filesystem (`readOnlyRootFilesystem: true`).
  - Drop all capabilities (`capabilities.drop: ["ALL"]`).
  - `allowPrivilegeEscalation: false`.
- **HTTP/2 Hardening**: HTTP/2 disabled by default on metrics and webhook endpoints to mitigate HTTP/2 Stream Cancellation and Rapid Reset vulnerabilities (GHSA-qppj-fm5r-hxr3, GHSA-4374-p667-p6c8).
- **Secure Metrics Serving**: Support authentication and authorization filtering (`FilterProvider: filters.WithAuthenticationAndAuthorization`) with optional cert-manager TLS certificates.

### 6.3 Performance
- Reconciliation execution should finish within 500ms (excluding Prometheus query network latency).
- Memory footprint of the controller manager under standard load should remain below 128MiB.
- Low overhead Prometheus queries: step duration and lookback ranges are strictly bounded to the evaluation window.

### 6.4 Logging Conventions
- Follow Kubernetes logging message style guidelines:
  - Capitalized starting letters.
  - No trailing periods.
  - Active voice and past tense for completed actions (`"Created ScaleDecision"`, `"Could not patch HPA"`).
  - Structured key-value pairs (e.g., `log.Info("Capped HPA maxReplicas", "hpa", hpa.Name, "cappedAt", newMax)`).

---

## 7. Role-Based Access Control (RBAC) Requirements

The controller manager requires the following cluster permissions:

| API Group | Resources | Verbs | Purpose |
|---|---|---|---|
| `autoscaling.prescale.io` | `scaleguards`, `scaleguards/status`, `scaleguards/finalizers` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Manage ScaleGuard resources |
| `autoscaling.prescale.io` | `scaledecisions`, `scaledecisions/status`, `scaledecisions/finalizers` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Record ScaleDecision audit entries |
| `autoscaling` | `horizontalpodautoscalers`, `horizontalpodautoscalers/status` | `get`, `list`, `watch`, `update`, `patch` | Monitor scaling events and patch `maxReplicas` |
| `apps` | `deployments`, `deployments/status` | `get`, `list`, `watch` | Monitor pod readiness and replica states |
| `coordination.k8s.io` | `leases` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | Leader election |
| `` (core) | `events` | `create`, `patch` | Publish operational events |

---

## 8. Verification & Testing Requirements

1. **Unit Testing (`internal/detector/`)**:
   - Regression fit verification: Flat slopes, positive slopes, negative slopes, and noisy series.
   - Boundary tests: Fewer than 3 points, zero variance, NaN values, infinite values.
   - CUSUM changepoint detection tests: Validating detection of shifts $\ge 8\sigma$, rejection of clean white noise, and behavior with small baseline samples.
2. **Controller Unit / Integration Tests (`internal/controller/`)**:
   - Run via `envtest` with a real API server and etcd.
   - Verify reconciler creation of `ScaleDecision` upon synthetic HPA scale-up.
   - Verify HPA annotation and `maxReplicas` capping in `enforce` mode.
   - Verify no HPA modification occurs in `observe` mode.
   - Verify unfreezing occurs after `maxFreezeDuration`.
3. **End-to-End (E2E) Tests (`test/e2e/`)**:
   - Run in an isolated Kind cluster (`prescale-test-e2e`).
   - Validates namespace isolation, restricted pod security enforcement, controller-manager deployment readiness, and secure metrics endpoint availability.

