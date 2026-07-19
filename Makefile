VERSION ?= dev
LDFLAGS := -s -w

IMAGE_REGISTRY ?= ghcr.io/tamcore
IMAGE_NAME     ?= kadence
IMAGE_TAG      ?= $(shell openssl rand -hex 8)   # fresh random per invocation (cache-bust)

.PHONY: help build build-prod fmt vet test coverage lint goreleaser-check helm-lint dev-deploy-k8s clean

help: ## Show this help message
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build the kadence binary (without embedded frontend)
	@go build -ldflags "$(LDFLAGS)" -o bin/kadence ./cmd/server

build-prod: ## Build the production binary with the SvelteKit frontend embedded
	@cd web && npm ci --silent && npm run build
	@go build -ldflags "$(LDFLAGS)" -tags prodfrontend -o bin/kadence ./cmd/server

fmt: ## Run go fmt
	go fmt ./...

vet: ## Run go vet
	go vet ./...

test: ## Run all Go tests with race detector and coverage
	@go test -race -coverprofile=coverage.out ./...

coverage: test ## Print coverage by func and total
	@go tool cover -func=coverage.out

goreleaser-check: ## Validate .goreleaser.yaml
	@if [ -f .goreleaser.yaml ]; then goreleaser check; else echo ".goreleaser.yaml not present - skipping"; fi

lint: fmt vet goreleaser-check helm-lint ## Run linters

helm-lint: ## Lint the Helm chart
	helm lint ./charts/kadence -f ./charts/kadence/values.yaml

dev-deploy-k8s: ## Build dev image, push to $(IMAGE_REGISTRY), deploy to K8s (needs charts/kadence/values-dev.yaml)
	@test -f charts/kadence/values-dev.yaml || { echo "charts/kadence/values-dev.yaml missing (gitignored; copy from values-dev.yaml.example)"; exit 1; }
	docker build -t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile.dev .
	docker push $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	kubectl delete deployment,statefulset,job -n kadence -l app.kubernetes.io/instance=kadence --ignore-not-found --wait
	helm upgrade --install kadence ./charts/kadence -n kadence --create-namespace \
		-f ./charts/kadence/values-dev.yaml \
		--set image.repository="$(IMAGE_REGISTRY)/$(IMAGE_NAME)" \
		--set image.tag="$(IMAGE_TAG)"

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out dist/ web/build/
