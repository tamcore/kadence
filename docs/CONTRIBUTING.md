# Contributing

## Prerequisites

- **Go 1.26+**
- **Node 20+** (for the SvelteKit frontend)
- **Docker** — used by the store tests (testcontainers spin up a real Postgres)
- A Postgres (pgvector) database for running the app locally

## Repository layout

See [ARCHITECTURE.md](ARCHITECTURE.md#package-layout) for the package map. In short:
Go backend under `cmd/` + `internal/`, SvelteKit SPA under `web/` (embedded into the
binary under `-tags prodfrontend`), Helm chart under `charts/kadence/`.

## Build & run

```bash
make build        # backend only → bin/kadence (no embedded frontend)
make build-prod   # frontend build + embed + backend
./bin/kadence     # serves on :8080 — GET /api/healthz
```

Frontend dev server (hot reload, proxies `/api` → `:8080`):

```bash
cd web && npm install && npm run dev
```

Because the frontend is embedded only under `-tags prodfrontend`, `go test ./...`
and `make build` work without an npm build.

## Test & lint

```bash
make test    # go test -race with coverage → coverage.out
make coverage
make lint    # go fmt, go vet, goreleaser check, helm lint
```

- **Store/DB tests** use testcontainers and need Docker; they’re the real gate for
  migrations and repositories. If Docker isn’t available locally they’re skipped —
  CI runs them, so verify green there.
- **Frontend tests:** `cd web && npx vitest run`.
- **E2E:** `make e2e-web` (Playwright over the SPA; needs a database) or
  `make e2e-kind` (full KinD cluster + bundled Postgres smoke test).

Pre-commit hooks run `go test -short`, `go vet`, `go fmt`, and `golangci-lint`.

## Conventions

- **Config is env-only** (`KADENCE_*`, via `config.Load()`); no viper, no flags. Add
  new settings there and document them in [CONFIGURATION.md](CONFIGURATION.md).
- **Router:** chi v5. Middleware order:
  `RequestID → RealIP → AccessLog → Recoverer → SecurityHeaders`.
- **Logging:** `log/slog` (JSON in prod, human-readable in dev). Never log secrets.
- **Errors** are wrapped with `%w`; handle them explicitly, don't swallow.
- **TDD:** write the failing test first; keep files small and focused.
- **Immutability:** return new values; avoid mutating shared state.
- **Migrations:** additive and reversible; they auto-run on startup. Check a table's
  original migration before adding an index (don't recreate an existing one).
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org/). Do **not**
  add a `Co-authored-by` trailer.

## Hard constraints

- **No vendor names in the repo.** Internal infrastructure (managed model hubs, OCR
  endpoints, registries, hosts) is referenced only generically via configurable
  OpenAI-compatible `base_url`s and env values — never named in tracked files.
- The app must **never depend on Kubernetes at runtime** — env config only; all
  cluster concerns live in the Helm chart.
- **MCP servers are remote** and must speak a network transport (`streamable-http` /
  `sse`); there is no in-process tool registry.

## CI

Pull requests run: Go tests (`-race`), commit-lint, Helm chart lint, security scans
(gosec / govulncheck / semgrep), and Playwright e2e. Releases are built by goreleaser
on version tags.
