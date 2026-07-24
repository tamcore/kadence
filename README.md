# Kadence

Self-hostable, multi-user **AI coach**. Users chat with an LLM that has live tool
access via remote **MCP servers**, backed by per-user **RAG memory** and an
admin-published shared knowledge corpus. Kubernetes-native (Helm); a single Go
workload + Postgres. The coaching domain, system prompt, and allowed topics are all
configurable — the default is endurance running.

## Features

- **Streaming chat** with markdown rendering and an agentic **MCP tool loop** (the
  model calls tools, results feed back, it continues).
- **MCP tools** — external integrations (e.g. Garmin) wired **globally** or
  **per-user**; users can register their own MCP servers by URL + basic auth.
- **Per-user RAG memory** — past conversations and uploaded documents are embedded
  and retrieved in future chats (private to each user).
- **Shared corpus** — admins publish material (plans, coaching docs) visible to all.
- **Document ingestion** — PDFs and images/screenshots → markdown → RAG.
- **Topic guardrail** — a configurable classifier keeps chat on-domain.
- **Scheduled coaching** — refine reminders, data checks, and monitors in a
  chat-like flow, confirm the final instruction, then run them safely in the
  background with optional lower-cost worker inference.
- **Auth** — username/password with server sessions, plus **passkeys (WebAuthn)**;
  active-session management and per-user display/unit preferences.
- **Security-first** — CSRF protection, secrets encrypted at rest (AES-256-GCM),
  a credential broker so the LLM never sees raw secrets, and network-isolated
  MCP sidecars.

## Quick start

**Deploy (Helm, Kubernetes):**

```bash
helm upgrade --install kadence ./charts/kadence -n kadence --create-namespace \
  -f my-values.yaml
```

Bring your own Postgres (pgvector) or enable the bundled one, set your provider
keys, and configure MCP servers in values. See **[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)**.

**Run locally (dev):**

```bash
make build && ./bin/kadence      # backend on :8080 — GET /api/healthz
cd web && npm run dev            # frontend dev server, proxies /api → :8080
```

See **[docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)** for the full dev setup.

## Documentation

- **[Architecture](docs/ARCHITECTURE.md)** — how it fits together: chat pipeline,
  MCP orchestration, RAG, providers, data model, security model.
- **[Configuration](docs/CONFIGURATION.md)** — every `KADENCE_*` environment
  variable and the MCP env contract.
- **[Deployment](docs/DEPLOYMENT.md)** — the Helm chart: Postgres, MCP sidecars,
  secrets, ingress, network policies.
- **[Contributing](docs/CONTRIBUTING.md)** — dev environment, build/test/lint,
  conventions, and workflow.
