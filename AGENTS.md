# Kadence — Agent Guide

Self-hostable, multi-user AI coach. Go backend + embedded SvelteKit SPA, deployed via
Helm on Kubernetes. This file is the quick orientation for agents/contributors; the
full docs live under [`docs/`](docs/).

- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — package layout, chat pipeline,
  MCP orchestration, RAG, data model, security model.
- **[docs/CONFIGURATION.md](docs/CONFIGURATION.md)** — every `KADENCE_*` env var.
- **[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)** — the Helm chart.
- **[docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)** — dev setup, test/lint, conventions.

## Layout

```
cmd/server/            entrypoint (main.go) + serve.Run() orchestrator
internal/api/          chi router, handlers, middleware
internal/config/       env-based config (KADENCE_* prefix)
internal/{chat,mcp,rag,ingest,provider,embed}/   chat pipeline + integrations
internal/{auth,crypto,secret,webauthn}/          auth + secrets + passkeys
internal/store/        pgx + goose migrations + repositories
web/                   SvelteKit SPA, embedded via //go:embed (-tags prodfrontend)
charts/kadence/        Helm chart
```

## Conventions

- **Config:** env-only, `KADENCE_*` prefix, via `config.Load()` + fail-fast `Validate()`. No viper/flags.
- **Router:** chi v5. Middleware order: RequestID → RealIP → AccessLog → Recoverer → SecurityHeaders.
- **Logging:** `log/slog`. Never log secrets.
- **Frontend:** embedded only under `-tags prodfrontend`; `go test ./...` works without an npm build.
- **DB tests:** testcontainers (need Docker) — the real gate for migrations/repos; run in CI.
- **Commits:** Conventional Commits. **No `Co-authored-by` trailer.**
- **TDD:** write the failing test first; keep files small and focused; wrap errors with `%w`.

## Common commands

```bash
make build        # backend only
make build-prod   # frontend build + embed + backend
make test         # go test -race + coverage
make lint         # fmt, vet, goreleaser check, helm lint
cd web && npm run dev            # frontend dev server (proxies /api → :8080)
make dev-deploy-k8s KUBE_CONTEXT=<ctx> IMAGE_REGISTRY=<registry>   # dev cluster deploy
```

## Hard constraints

- **No vendor names in the repo.** Internal infra is referenced only generically via
  configurable OpenAI-compatible `base_url`s / env values.
- The app must never depend on Kubernetes at runtime — env config only.
- MCP servers are remote and must speak network transport (streamable-http/sse).
