# Architecture

Kadence is a single Go workload plus a PostgreSQL (pgvector) database, with an
embedded SvelteKit single-page app. All Kubernetes complexity lives in the Helm
chart — **the binary never depends on Kubernetes at runtime**; it reads everything
from environment variables and talks to external services (LLM, embeddings, MCP
servers) over HTTP.

## Package layout

```
cmd/server/          entrypoint (main.go) + serve.Run() orchestrator
internal/
  api/               chi router, handlers, middleware (session, CSRF, security headers)
  auth/              bcrypt passwords, session-id generation, request-context helpers
  config/            env loading (KADENCE_*), defaults, fail-fast Validate()
  crypto/            AES-256-GCM cipher for secrets at rest
  provider/          LLM client abstraction (OpenAI-compatible; streaming + tool calls)
  embed/             embedding backend abstraction (OpenAI-compatible)
  mcp/               remote MCP server registry: env contract, scoping, tool filtering
  secret/            credential broker — one-time placeholder tokens, never logs secrets
  chat/              per-turn orchestration: guardrail → RAG → provider stream → tool loop
  rag/               pgvector retrieval (per-user private ∪ admin public corpus)
  ingest/            document extraction pipeline (PDF fallback + markitdown-mcp)
  reindex/           background re-embed worker when the embedding model changes
  knowledge/         dependency-free text analytics (keywords/entities for the context view)
  webauthn/          passkey ceremonies (registration/assertion) + encrypted ceremony cookie
  model/             domain types
  store/             pgx pool, goose migrations, repositories
web/                 SvelteKit SPA, embedded via //go:embed under -tags prodfrontend
charts/kadence/      Helm chart
```

Handlers receive their dependencies explicitly (constructor injection of repos and
services), keeping wiring in `serve.Run()` and units independently testable.

## Request flow

`chi` middleware order: `RequestID → RealIP → AccessLog → Recoverer → SecurityHeaders`.
`LoadUser` resolves the `session_id` cookie to a user on every request; `RequireAuth`
gates authenticated routes. Unsafe methods are CSRF-protected (`gorilla/csrf`),
except the login and passkey-login endpoints, which have no prior session/token — for
passkey login the origin-bound WebAuthn assertion is itself the CSRF defense.

The SPA is served from the same binary (embedded) with an `index.html` fallback for
client-side routing. In tests and dev the frontend is not embedded, so `go test ./...`
runs without an npm build.

## Chat pipeline (`chat/`)

Each turn runs:

1. **Guardrail** (optional) — a configurable topic classifier. It replies
   `ON_TOPIC` / `OFF_TOPIC` using the last N text-bearing turns; off-topic returns a
   configurable refusal. It **fails open** (proceeds on classifier error) and can use
   a separate model/endpoint from the main provider.
2. **RAG retrieve** — embed the user's message and pull the top-K chunks from the
   user's private memory plus the admin public corpus.
3. **Assemble + stream** — build the context (system prompt stamped with the current
   date and the user's unit preference) and stream from the provider, running an
   **MCP tool loop**: the model requests a tool → the app dispatches it to the right
   MCP client → the result is fed back → repeat, up to a configured iteration cap.
4. **Persist + embed** — the turn is stored and embedded back into RAG.

Responses stream to the browser as Server-Sent Events (`ChatEvent` JSON).

## MCP orchestration (`mcp/`)

MCP servers are **remote and network-transport only** (`streamable-http` / `sse`) —
there is no in-process tool registry. On startup the registry scans the environment
for a fixed contract and builds one client per server:

```
MCP_<NAME>_<SCOPE>_URL          # http(s) endpoint
MCP_<NAME>_<SCOPE>_AUTH_USER    # basic-auth username
MCP_<NAME>_<SCOPE>_AUTH_PASS    # basic-auth password
MCP_<NAME>_<SCOPE>_TRANSPORT    # streamable-http | sse
MCP_<NAME>_<SCOPE>_TOOLS        # optional glob allowlist (unprefixed tool names)
```

`<SCOPE>` is `GLOBAL` (available to everyone) or `USER_<username>` (that user only).
At chat time a user's tool set is **global servers ∪ their own servers**. Users may
also register their own MCP servers at runtime (URL + basic auth), gated by a host
allowlist; those credentials are encrypted at rest.

App-side tool filtering (globs against the unprefixed tool name) keeps tool lists
short and independent of each server's own filtering. TLS to MCP servers is optional
(`KADENCE_MCP_CA_FILE` for a custom CA); the deployed sidecars add basic auth and
network isolation on top.

## RAG & ingestion

- **Retrieval** filters on `user_id = current ∪ scope = public`, so each user sees
  their own memory plus the admin corpus, never other users' data.
- **Ingestion** normalizes each input to markdown, then chunks → embeds → stores.
  Text-layer PDFs use a pure-Go fast path; richer extraction (scanned PDFs, images,
  screenshots) goes through a `markitdown-mcp` service when configured.
- Chunks are tagged with the embedding model. Changing the embedding model triggers a
  background **re-index** so vectors migrate without wiping stored knowledge.

## Providers & embeddings

The `Provider` interface (streaming + tool-calling) is implemented against an
OpenAI-compatible client, so any compatible endpoint works by pointing
`KADENCE_LLM_BASE_URL` at it. The `Embedder` interface is likewise OpenAI-compatible
with a configurable base URL; the embedding dimension must match the `chunks` vector
column. Model providers and endpoints are referenced generically via `base_url` — no
vendor is named in the repo.

## Data model (Postgres + pgvector)

All timestamps are UTC. Migrations are embedded SQL run by `goose` on startup
(additive and reversible).

- **users** — credentials (bcrypt), role (`admin`/`user`), display name, unit system,
  and a random `webauthn_user_handle`.
- **sessions** — opaque cookie id + a non-secret `public_id`, plus device/IP/last-seen
  metadata for the active-sessions view.
- **conversations** / **messages** — chat history; messages carry `content` and
  `tool_calls`. Conversations also have an immutable UUID.
- **documents** / **chunks** — ingested material and their embeddings; `scope`
  distinguishes private from the admin public corpus. Chunks store the embedding model.
- **user_mcp_servers** — user-registered MCP servers (auth password encrypted).
- **webauthn_credentials** — registered passkeys (credential id, public key, sign
  count, backup flags, transports, last used).

## Security model

- **Sessions** — server-side (Postgres), opaque `session_id` cookie (HttpOnly,
  `SameSite=Lax`, `Secure` in prod); the raw id is never returned by any API.
- **CSRF** — `gorilla/csrf` on unsafe methods, with a trusted-origins allowlist.
- **Passwords** — bcrypt.
- **Passkeys** — WebAuthn; the ceremony `SessionData` is carried in a short-lived,
  AES-256-GCM-encrypted, HttpOnly cookie (stateless, multi-replica safe).
- **Secrets at rest** — MCP credentials and WebAuthn ceremony data are encrypted with
  AES-256-GCM (`KADENCE_ENCRYPTION_KEY`).
- **Credential broker** — when a tool needs a secret (e.g. a service login), the LLM
  only ever sees an opaque one-time placeholder token; the real value is substituted
  at dispatch time and redacted from logs and transcripts.
- **MCP isolation** — each MCP server is deployed behind a basic-auth nginx sidecar,
  reachable only from the main app by NetworkPolicy, optionally over TLS.

See [CONFIGURATION.md](CONFIGURATION.md) for the environment variables referenced
here, and [DEPLOYMENT.md](DEPLOYMENT.md) for how the chart renders all of this.
