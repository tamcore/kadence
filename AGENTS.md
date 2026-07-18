# Kadence — Agent Guide

Self-hostable, multi-user AI coach. Go backend + embedded SvelteKit SPA, deployed
via Helm on Kubernetes. See `IDEA.md` for the north-star design and phase roadmap.

## Layout

```
cmd/server/            entrypoint (main.go) + serve.Run() orchestrator
internal/api/          chi router, handlers, middleware
internal/config/       env-based config (KADENCE_* prefix)
web/                   SvelteKit SPA, embedded via //go:embed (-tags prodfrontend)
```

## Conventions

- **Config:** env-only, `KADENCE_*` prefix, via `config.Load()`. No viper/flags.
- **Router:** chi v5. Middleware order: RequestID → RealIP → AccessLog → Recoverer → SecurityHeaders.
- **Logging:** `log/slog`.
- **Frontend:** embedded only under `-tags prodfrontend`; `go test ./...` works without an npm build.
- **Commits:** Conventional Commits.
- **TDD:** write the failing test first; keep files small and focused.

## Common commands

```bash
make build        # backend only
make build-prod   # frontend build + embed + backend
make test         # go test -race + coverage
make lint         # fmt, vet, goreleaser check
cd web && npm run dev   # frontend dev server (proxies /api → :8080)
```

## Hard constraints

- **No vendor names in the repo.** Internal infra is referenced only generically
  via configurable OpenAI-compatible `base_url`.
- The app must never depend on Kubernetes at runtime — env config only.
- MCP servers are remote and must speak network transport (streamable-http/sse).
