VERSION ?= dev
LDFLAGS := -s -w

.PHONY: help build build-prod fmt vet test coverage lint goreleaser-check clean

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

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

lint: fmt vet goreleaser-check ## Run linters

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out dist/ web/build/
