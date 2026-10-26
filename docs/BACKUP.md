# Backup and Restore Guide

This guide covers how to preserve and restore the demo cluster state.
The kind cluster `prescale-demo` must never be deleted or recreated.

## What to back up

| Item | Location | Method |
|---|---|---|
| Kubernetes manifests | `deploy/` directory | git |
| Grafana dashboard | `deploy/grafana/prescale-demo.json` | git |
| A/B results (raw) | `docs/reports/data/` | git |
| k6 scripts | `loadtest/` | git |
| Predictor source | `predictor/` | git |
| Docker images | local Docker | `docker save` |

## Save Docker images

```bash
# Save prescaler image
docker save prescale/prescaler:dev | gzip > /tmp/prescaler-dev.tar.gz

# Save predictor image
docker save prescale-predictor:0.1.1 | gzip > /tmp/prescale-predictor-0.1.1.tar.gz

# Save k6 image (tag from K6_IMAGE file)
K6_IMG=$(cat loadtest/K6_IMAGE | tr -d ' \n')
docker save "$K6_IMG" | gzip > /tmp/k6-img.tar.gz
```

## Restore Docker images

```bash
docker load < /tmp/prescaler-dev.tar.gz
docker load < /tmp/prescale-predictor-0.1.1.tar.gz
docker load < /tmp/k6-img.tar.gz

# Re-load prescaler into kind
kind load docker-image prescale/prescaler:dev --name prescale-demo
```

## Restore cluster state from scratch (if cluster is lost)

> ⚠️ Only do this if the kind cluster is truly gone. Normally, never delete it.

```bash
# 1. Recreate kind cluster
kind create cluster --name prescale-demo --config deploy/kind/cluster.yaml

# 2. Install KEDA
kubectl --context kind-prescale-demo apply -f deploy/keda/

# 3. Install VictoriaMetrics
kubectl --context kind-prescale-demo apply -k deploy/observability/

# 4. Load images
kind load docker-image prescale/prescaler:dev --name prescale-demo

# 5. Deploy workloads
kubectl --context kind-prescale-demo apply -k deploy/prescaler/
# Repeat for baseline and predictive namespaces

# 6. Import Grafana dashboard
bash scripts/grafana_import.sh
```

## Snapshot current cluster state

```bash
KC="kubectl --context kind-prescale-demo"

# Dump all resources in relevant namespaces
for ns in predictive baseline monitoring keda; do
  $KC get all,scaledobject,configmap,secret -n $ns -o yaml > /tmp/snapshot_$ns.yaml 2>/dev/null || true
done
```

## Grafana dashboard backup

The dashboard is committed to git at `deploy/grafana/prescale-demo.json`.
To re-import after a Grafana restart:
```bash
bash scripts/grafana_import.sh
```
