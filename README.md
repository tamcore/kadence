# Kadence

Self-hostable, multi-user AI coach. Domain-configurable chat with MCP tool access,
per-user RAG memory, and an admin-published shared knowledge corpus.

Kubernetes-native (Helm); a single Go workload + Postgres. See `IDEA.md` for the
north-star design and phase roadmap.

## Development

```bash
make build        # build backend (no embedded frontend)
make build-prod   # build frontend + embed + build backend
make test         # go test -race with coverage
./bin/kadence     # runs on :8080 — GET /api/healthz
```
