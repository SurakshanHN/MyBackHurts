# Predictive Autoscaler (PreScale)

A predictive autoscaling system for Kubernetes that forecasts incoming traffic surges and proactively scales up HPA `minReplicas` *before* traffic arrives, eliminating the 30+ second pod-readiness latency of reactive HPA.

> **Note on Project Transition**: The previous reactive `ScaleGuard` operator (which judged HPA scale-ups after the fact and capped `maxReplicas`) has been removed and preserved at git tag `pre-rebuild`.

---

## Architecture Overview

```
                      [ Load Generator: k6 ]
                                 │
                   ┌─────────────┴─────────────┐
                   ▼                           ▼
        [ Baseline App: HPA-only ]   [ Prescaled App: HPA + PreScale ]
                   │                           │
                   └─────────────┬─────────────┘
                                 ▼
                     [ VictoriaMetrics / vmagent ]
                                 │
                        (queries recent RPS)
                                 │
                                 ▼
                    ┌─────────────────────────┐
                    │     prescaler daemon    │
                    │   (cmd/prescaler/main)  │
                    └────────────┬────────────┘
                                 │
              ┌──────────────────┴──────────────────┐
              ▼                                     ▼
     (POST /predict RPS)                  (PATCH minReplicas)
              ▼                                     ▼
   [ Predictor Service ]                    [ Target HPA ]
 (Python 3.12 + ML .pkl)
```

---

## Directory Layout

```
.
├── cmd/
│   └── prescaler/          # Pre-scaler daemon entrypoint
├── deploy/                 # Kubernetes deployment manifests & Helm configs (planned)
├── docs/                   # Documentation & historical references
│   ├── old-demo-README.md  # Original demo guide
│   └── reports/            # Analysis and audit reports (Part 1 & 2)
├── experiments/            # Benchmark definitions & load test results
├── internal/
│   ├── actuator/           # Kubernetes HPA actuation & safety planner
│   └── metrics/            # VictoriaMetrics / Prometheus range query client
├── loadtest/
│   └── surge.js            # k6 traffic surge load test script
├── predictor/              # ML-based traffic forecast service (planned)
├── workload/               # Demo applications & manifests
│   ├── checkout/           # HTTP microservice with CPU load & DB dependency
│   ├── fakedb/             # Database simulation with normal/contention modes
│   └── k8s/                # Workload deployments, services, and HPA
├── Makefile                # Standard developer commands
├── go.mod / go.sum         # Go module dependencies
└── README.md               # Project documentation
```

---

## Current Status

- **What Exists**:
  - `internal/metrics`: Functional VictoriaMetrics/Prometheus time-series client.
  - `internal/actuator`: Conflict-safe Kubernetes HPA patch engine (currently configured for capping `maxReplicas`; inversion to `minReplicas` scheduled for next part).
  - `workload/checkout` & `workload/fakedb`: Fully implemented Go demo microservices with unit tests.
  - `workload/k8s`: Kubernetes manifests for namespace, checkout, and fakedb deployments with HPA.
  - `loadtest/surge.js`: Ramping arrival rate k6 scenario (20 -> 120 -> 20 RPS).
  - `cmd/prescaler`: Skeleton Go daemon with standard-library CLI flags and signal handling.
- **What is Planned**:
  - Inverting actuator logic to raise `minReplicas` ahead of predicted surges.
  - Integration between `prescaler`, VictoriaMetrics, and the predictor service.
  - Packaging the Python 3.12 ML model inference service.
  - Side-by-side benchmark harness in Kubernetes.

---

## Building and Testing

### Prerequisites
- Go 1.24+ (developed with Go 1.26)

### Common Commands

```bash
# Display help and available Makefile targets
make help

# Build the prescaler daemon binary into bin/
make build

# Run all unit tests
make test

# Run code vet
make vet

# Format all Go source files
make fmt

# Clean built binaries
make clean
```