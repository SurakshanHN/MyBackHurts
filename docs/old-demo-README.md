# PreScale Demo: Checkout App & Contention Simulation

This demo demonstrates how PreScale detects downstream contention before threshold-based alerts trigger, distinguishing harmful scale-ups from healthy traffic surges.

---

## 1. Quick Start Guide

### Step 1: Create Kind Cluster

```bash
kind create cluster --name prescale-demo
```

### Step 2: Build & Load Container Images

Build the container images for `fakedb` and `checkout` from the repository root:

```bash
# Build images
docker build -t prescale-demo/fakedb:latest -f demo/fakedb/Dockerfile .
docker build -t prescale-demo/checkout:latest -f demo/checkout/Dockerfile .

# Load images into kind cluster
kind load docker-image prescale-demo/fakedb:latest --name prescale-demo
kind load docker-image prescale-demo/checkout:latest --name prescale-demo
```

### Step 3: Install kube-prometheus-stack via Helm

```bash
# Add prometheus-community chart repository
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# Install kube-prometheus-stack
helm install prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring --create-namespace \
  --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.scrapeInterval=5s \
  --set prometheus.prometheusSpec.evaluationInterval=5s
```

### Step 4: Apply Demo Kubernetes Manifests

Apply the demo namespace, deployments, services, HPA, and PodMonitor:

```bash
kubectl apply -f demo/k8s/
```

Verify that pods and HPA are ready:

```bash
kubectl get pods,hpa -n demo
```

You should see 1 replica of `fakedb` and 3 replicas of `checkout`.

---

## 2. Running Load Tests with k6

Port-forward the `checkout` service (and optionally `fakedb`):

```bash
kubectl port-forward -n demo svc/checkout 8080:8080 &
kubectl port-forward -n demo svc/fakedb 8081:8080 &
```

### Run Scenario A: Contention (Harmful Scale-Up)

In contention mode, downstream database query latency grows quadratically with concurrency:
`latency = 10ms * (1 + active_queries/20)^2`
As the checkout deployment scales from 3 to 10 replicas, connection concurrency increases latency dramatically while connection saturation stays well below 90%:

```bash
MODE=contention k6 run demo/k6/surge.js
```

### Run Scenario B: Healthy Surge (Normal Mode)

In healthy mode, database query latency remains flat at ~10ms. Scaling checkout from 3 to 10 replicas successfully absorbs the surge:

```bash
MODE=healthy k6 run demo/k6/surge.js
```

---

## 3. PromQL Queries for PreScale

The table below provides the exact PromQL expressions used to monitor the checkout service and downstream database:

| Metric | PromQL Query | Description |
| :--- | :--- | :--- |
| **p99 (Latency)** | `histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{namespace="demo", app="checkout"}[1m])) by (le))` | 99th percentile response latency (seconds) of the checkout service |
| **RPS (Throughput)** | `sum(rate(http_requests_total{namespace="demo", app="checkout"}[1m]))` | Incoming requests per second for checkout |
| **Error Rate** | `sum(rate(http_requests_total{namespace="demo", app="checkout", code=~"5.."}[1m])) / clamp_min(sum(rate(http_requests_total{namespace="demo", app="checkout"}[1m])), 0.001)` | Proportion of 5xx responses (0.0 to 1.0) |
| **Replicas** | `kube_deployment_status_replicas{namespace="demo", deployment="checkout"}` | Desired/actual replicas configured by HPA |
| **Ready** | `kube_deployment_status_replicas_ready{namespace="demo", deployment="checkout"}` | Ready and available replicas serving traffic |
| **Dependency Saturation** | `sum(db_active_connections{namespace="demo", app="fakedb"}) / sum(db_max_connections{namespace="demo", app="fakedb"})` | Downstream connection utilization ratio (0.0 to 1.0) |
| **Dependency Latency (ms)** | `histogram_quantile(0.99, sum(rate(db_query_duration_seconds_bucket{namespace="demo", app="fakedb"}[1m])) by (le)) * 1000` | 99th percentile downstream query latency in milliseconds |

---

## 4. Architectural Summary

- **`demo/fakedb`**: Simulates a backend data store with connection limit `MAX_CONNS` (100) and switchable modes (`normal` vs `contention`). Exposes `/metrics` with `db_active_connections`, `db_max_connections`, and `db_query_duration_seconds`.
- **`demo/checkout`**: An HTTP checkout microservice calling `fakedb /query` and burning ~5ms of CPU per request. When traffic increases, CPU utilization exceeds 50%, triggering the HPA to scale up from 3 to 10 pods.
- **`demo/k8s/`**: Full Kubernetes manifests defining namespace `demo`, Deployments, Services, `autoscaling/v2` HPA, and `PodMonitor` with scrape annotations.
- **`demo/k6/surge.js`**: Load test script that ramps traffic to induce the scale-up event.
