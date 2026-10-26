.DEFAULT_GOAL := help

.PHONY: help build test vet fmt tidy clean

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
