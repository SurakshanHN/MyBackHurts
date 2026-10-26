.DEFAULT_GOAL := help

CLUSTER_NAME := prescale-demo
KUBECTL := kubectl --context kind-$(CLUSTER_NAME)

.PHONY: help build test vet fmt tidy clean cluster-up cluster-down images obs-up keda-up workload-up prescaler-image prescaler-up up down status smoke

help: ## Display this help message
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build prescaler daemon binary into bin/
	@mkdir -p bin
	go build -o bin/prescaler ./cmd/prescaler

test: ## Run all unit tests
	go test -v ./...

vet: ## Run go vet on all packages
	go vet ./...

fmt: ## Format all Go code
	gofmt -s -w .

tidy: ## Clean up and verify go.mod dependencies
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin/ cover.out dist/

cluster-up: ## Create kind cluster if not already present
	@if ! kind get clusters | grep -q "^$(CLUSTER_NAME)$$"; then \
		echo "Creating kind cluster $(CLUSTER_NAME)..."; \
		docker run --rm --privileged alpine:3.21 sysctl -w fs.inotify.max_user_watches=524288 fs.inotify.max_user_instances=8192 >/dev/null 2>&1 || true; \
		kind create cluster --name $(CLUSTER_NAME) --config deploy/kind/kind-config.yaml; \
	else \
		echo "Cluster $(CLUSTER_NAME) already exists"; \
	fi
	@$(KUBECTL) wait --for=condition=Ready nodes --all --timeout=120s

cluster-down: ## Delete kind cluster (refuses if not prescale-demo)
	@if [ "$(CLUSTER_NAME)" != "prescale-demo" ]; then \
		echo "Refusing to delete cluster $(CLUSTER_NAME)"; \
		exit 1; \
	fi
	@if kind get clusters | grep -q "^$(CLUSTER_NAME)$$"; then \
		kind delete cluster --name $(CLUSTER_NAME); \
	else \
		echo "Cluster $(CLUSTER_NAME) does not exist"; \
	fi

images: ## Build workload images and load into kind
	docker build -t prescale/checkout:dev -f workload/checkout/Dockerfile .
	docker build -t prescale/fakedb:dev -f workload/fakedb/Dockerfile .
	kind load docker-image --name $(CLUSTER_NAME) prescale/checkout:dev prescale/fakedb:dev
	@for img in registry.k8s.io/metrics-server/metrics-server:v0.9.0 \
		quay.io/victoriametrics/victoria-metrics:v1.115.0 \
		registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.15.0 \
		grafana/grafana:11.4.0 \
		ghcr.io/kedacore/keda:2.19.0 \
		ghcr.io/kedacore/keda-metrics-apiserver:2.19.0 \
		ghcr.io/kedacore/keda-admission-webhooks:2.19.0; do \
		docker save "$$img" | docker exec -i $(CLUSTER_NAME)-worker ctr --namespace=k8s.io images import - >/dev/null 2>&1 || true; \
		docker save "$$img" | docker exec -i $(CLUSTER_NAME)-worker2 ctr --namespace=k8s.io images import - >/dev/null 2>&1 || true; \
		docker save "$$img" | docker exec -i $(CLUSTER_NAME)-control-plane ctr --namespace=k8s.io images import - >/dev/null 2>&1 || true; \
	done

prescaler-image: ## Build prescaler image and load into kind
	docker build -t prescale/prescaler:dev -f cmd/prescaler/Dockerfile .
	kind load docker-image --name $(CLUSTER_NAME) prescale/prescaler:dev

prescaler-up: ## Deploy prescaler into predictive namespace
	$(KUBECTL) apply -k deploy/prescaler/
	$(KUBECTL) rollout status deployment -n predictive prescaler --timeout=90s

obs-up: ## Deploy observability stack
	$(KUBECTL) apply -k deploy/observability/
	$(KUBECTL) rollout status deployment -n monitoring metrics-server --timeout=90s
	$(KUBECTL) rollout status deployment -n monitoring vmsingle --timeout=60s
	$(KUBECTL) rollout status deployment -n monitoring kube-state-metrics --timeout=60s
	$(KUBECTL) rollout status deployment -n monitoring grafana --timeout=60s

keda-up: ## Deploy KEDA autoscaling operator
	$(KUBECTL) apply --server-side -k deploy/keda/
	$(KUBECTL) rollout status deployment -n keda keda-admission --timeout=60s
	$(KUBECTL) rollout status deployment -n keda keda-metrics-apiserver --timeout=60s
	$(KUBECTL) rollout status deployment -n keda keda-operator --timeout=60s
	$(KUBECTL) wait --for=condition=Available apiservice/v1beta1.external.metrics.k8s.io --timeout=60s

workload-up: ## Deploy baseline and predictive workloads
	$(KUBECTL) apply -k workload/k8s/overlays/baseline
	$(KUBECTL) apply -k workload/k8s/overlays/predictive
	$(KUBECTL) rollout status deployment -n baseline fakedb --timeout=60s
	$(KUBECTL) rollout status deployment -n baseline checkout --timeout=90s
	$(KUBECTL) rollout status deployment -n predictive fakedb --timeout=60s
	$(KUBECTL) rollout status deployment -n predictive checkout --timeout=90s
	$(KUBECTL) wait --for=condition=Ready scaledobject/checkout -n baseline --timeout=60s
	$(KUBECTL) wait --for=condition=Ready scaledobject/checkout -n predictive --timeout=60s

up: cluster-up images obs-up keda-up workload-up prescaler-image prescaler-up ## Spin up entire platform (cluster, images, obs, keda, workload, prescaler)

down: cluster-down ## Tear down entire platform

status: ## Display cluster status across all namespaces
	@echo "=== Nodes ==="
	@$(KUBECTL) get nodes
	@echo "\n=== Pods ==="
	@$(KUBECTL) get pods -A
	@echo "\n=== ScaledObjects ==="
	@$(KUBECTL) get scaledobject -A
	@echo "\n=== HPAs ==="
	@$(KUBECTL) get hpa -A

smoke: ## Run smoke traffic test against baseline and predictive
	python3 scripts/smoke_traffic.py --rps 20 --duration 60 --url-baseline http://localhost:8081/ --url-predictive http://localhost:8082/
