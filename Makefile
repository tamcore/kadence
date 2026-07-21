VERSION ?= dev
LDFLAGS := -s -w

IMAGE_REGISTRY ?= ghcr.io/tamcore
IMAGE_NAME     ?= kadence
IMAGE_TAG      := $(if $(IMAGE_TAG),$(IMAGE_TAG),$(shell openssl rand -hex 8))   # single expansion (a recursive ?= re-runs openssl per reference → build/push/deploy tag mismatch); honors an env/CLI override
KUBE_CONTEXT   ?=

_HELM_CTX   = $(if $(KUBE_CONTEXT),--kube-context $(KUBE_CONTEXT),)
_KUBECTL_CTX = $(if $(KUBE_CONTEXT),--context $(KUBE_CONTEXT),)

.PHONY: help build build-prod fmt vet test coverage lint goreleaser-check helm-lint dev-deploy-k8s e2e-web e2e-kind clean

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

e2e-web: ## Build + run Playwright browser e2e (needs KADENCE_DATABASE_URL)
	@cd web && npm ci --silent && npm run build
	@go build -ldflags "$(LDFLAGS)" -tags prodfrontend -o bin/kadence ./cmd/server
	@go build -ldflags "$(LDFLAGS)" -o bin/e2e-stub ./e2e/stub
	@bash scripts/e2e-web.sh

e2e-kind: ## Build image, KinD, helm install, smoke
	cd web && npm ci --silent && npm run build
	docker build -f Dockerfile.dev -t kadence:ci .
	kind create cluster --name kadence-ci 2>/dev/null || true
	kind load docker-image kadence:ci --name kadence-ci
	helm install kadence charts/kadence -n kadence --create-namespace \
		-f charts/kadence/values-ci.yaml \
		--set image.repository=kadence \
		--set image.tag=ci \
		--wait --timeout 5m
	kubectl -n kadence rollout status deploy/kadence --timeout=180s
	kubectl -n kadence port-forward svc/kadence 8080:8080 & \
	sleep 3; \
	curl -fsS localhost:8080/api/healthz | grep -q ok
	@echo "Smoke passed. Run 'kind delete cluster --name kadence-ci' to clean up."

helm-lint: ## Lint the Helm chart
	helm lint ./charts/kadence -f ./charts/kadence/values.yaml

dev-deploy-k8s: ## Build dev image, push to $(IMAGE_REGISTRY), deploy to K8s (needs charts/kadence/values-dev.yaml; set KUBE_CONTEXT=foo to target specific cluster)
	@test -f charts/kadence/values-dev.yaml || { echo "charts/kadence/values-dev.yaml missing (gitignored; copy from values-dev.yaml.example)"; exit 1; }
	docker build -t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile.dev .
	docker push $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	# Render + kubectl apply (not helm install/upgrade): avoids helm's release
	# state and server-side-apply field-ownership conflicts (e.g. an out-of-band
	# `kubectl` edit of a Deployment field would make `helm upgrade` fail the
	# whole release). kubectl apply reconciles each manifest in place.
	kubectl $(_KUBECTL_CTX) create namespace kadence --dry-run=client -o yaml | kubectl $(_KUBECTL_CTX) apply -f -
	helm template kadence ./charts/kadence $(_HELM_CTX) -n kadence \
		-f ./charts/kadence/values-dev.yaml \
		--set image.repository="$(IMAGE_REGISTRY)/$(IMAGE_NAME)" \
		--set image.tag="$(IMAGE_TAG)" \
		| kubectl $(_KUBECTL_CTX) apply -f -

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out dist/ web/build/
