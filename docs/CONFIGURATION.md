# Configuration

Kadence is configured **entirely through environment variables** (prefix
`KADENCE_*`), loaded once at startup by `config.Load()`. There are no config files
or command-line flags. `config.Validate()` fails fast on startup for invalid
combinations (see [Validation](#validation)).

Values shown are the built-in defaults; `—` means unset/empty.

## Core / server

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_LISTEN_ADDR` | `:8080` | HTTP listener bind address. |
| `KADENCE_ENV` | `dev` | `dev` \| `prod` \| `production`. Prod enables secure cookies + strict CSRF. |
| `KADENCE_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`. |
| `KADENCE_DATABASE_URL` | — (required) | Postgres DSN (pgvector). goose migrations run on startup. |

## Auth & security

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_CSRF_SECRET` | — | `gorilla/csrf` secret. Required in prod; random per-restart in dev. Share across replicas. |
| `KADENCE_TRUSTED_ORIGINS` | — | Comma-separated CSRF/WebAuthn trusted origins (e.g. `https://kadence.example.com`). |
| `KADENCE_ENCRYPTION_KEY` | — | Base64-encoded 32-byte key (AES-256-GCM) for secrets at rest (MCP credentials, WebAuthn ceremony data). |
| `KADENCE_WEBAUTHN_RP_ID` | — | WebAuthn Relying Party ID = the site's effective domain (e.g. `kadence.example.com`). Empty disables passkeys. Also requires `KADENCE_TRUSTED_ORIGINS` + a valid `KADENCE_ENCRYPTION_KEY`. |
| `KADENCE_ADMIN_USERNAME` | — | First-run admin bootstrap (created only when the users table is empty). |
| `KADENCE_ADMIN_EMAIL` | — | First-run admin email. |
| `KADENCE_ADMIN_PASSWORD` | — | First-run admin password (bcrypt-hashed at insert). |

## Rate limiting

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_RATE_LIMIT_GLOBAL` | `300` | Per-IP requests/minute across all `/api` routes (`/api/healthz` and the static frontend are exempt). `0` disables. |
| `KADENCE_RATE_LIMIT_AUTH` | `10` | Per-IP requests/minute on auth-sensitive endpoints: `POST /api/session`, `POST /api/webauthn/login/begin`, `POST /api/webauthn/login/finish`, `POST /api/credentials/{requestId}`. `0` disables. |
| `KADENCE_MAX_BODY_BYTES` | `1048576` (1 MiB) | Max request body size across `/api` routes in general. Document uploads (`POST /api/documents`, `POST /api/admin/documents`) are exempt from this cap and governed solely by `KADENCE_UPLOAD_MAX_BYTES` instead. Oversized bodies fail with `400`. |

Both limiters key on the request's resolved client IP (in-memory sliding window,
via `go-chi/httprate`), which chi's `RealIP` middleware derives from
`X-Forwarded-For`/`X-Real-IP`. **This assumes a trusted reverse-proxy chain**
(e.g. ingress-nginx) that sets those headers from the real client address and
strips any client-supplied values before they reach Kadence. If Kadence is
ever exposed directly to untrusted clients, it must not forward
client-supplied `X-Forwarded-For`/`X-Real-IP` — otherwise a client can spoof
its rate-limit bucket. Full trusted-proxy allowlisting (verifying the
immediate peer is actually the proxy) is not yet implemented.

## LLM provider

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_LLM_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible provider base URL. |
| `KADENCE_LLM_API_KEY` | — | Chat API key. Chat is disabled if unset. |
| `KADENCE_LLM_MODEL` | `gpt-4o-mini` | Model id. |
| `KADENCE_LLM_MAX_TOKENS` | `2048` | Max completion tokens per request. |
| `KADENCE_LLM_TEMPERATURE` | `0.3` | Sampling temperature. |
| `KADENCE_LLM_TIMEOUT` | `300s` | Per-request timeout (Go duration). |
| `KADENCE_SYSTEM_PROMPT` | — | Overrides the built-in chat system prompt. |
| `KADENCE_LLM_CONTEXT_BUDGET` | `32000` | Token budget (estimated via a `len/4` heuristic, not a real tokenizer) for the prior-conversation history sent with each request, separate from `KADENCE_LLM_MAX_TOKENS` (the completion cap). When history would exceed the budget, whole oldest-middle turns (a user message plus its assistant reply) are dropped — never split mid-turn — always keeping the conversation's first user message and as many of the newest turns as fit. |

## Guardrail (topic classifier)

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_GUARDRAIL_ENABLED` | `false` | Enable the on-topic classifier. |
| `KADENCE_GUARDRAIL_MODEL` | (main model) | Classifier model override. |
| `KADENCE_GUARDRAIL_BASE_URL` | (main base URL) | Classifier provider override. |
| `KADENCE_GUARDRAIL_API_KEY` | (main key) | Classifier API key override. |
| `KADENCE_GUARDRAIL_HISTORY_WINDOW` | `6` | Number of recent text turns used for classification. |
| `KADENCE_DOMAIN_NAME` | endurance-coaching default | Domain description injected into the classifier prompt. |
| `KADENCE_ALLOWED_TOPICS` | endurance defaults | Approved topics. |
| `KADENCE_REFUSAL_MESSAGE` | coaching-only default | Reply sent when a message is off-topic. |

## Embeddings & RAG

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_EMBED_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible embeddings base URL. |
| `KADENCE_EMBED_API_KEY` | — | Embeddings API key. RAG is disabled if unset. |
| `KADENCE_EMBED_MODEL` | `text-embedding-3-small` | Embeddings model id. Changing it triggers a background re-index. |
| `KADENCE_RAG_TOP_K` | `5` | Number of chunks retrieved per query. |
| `KADENCE_EMBED_DIMENSIONS` | `1024` | Pins the embedding vector length so it fits a fixed-width `vector(1024)` column with an HNSW index. Sent as the OpenAI-compat `dimensions` request field; if the provider ignores it, the client truncates to N dims and L2-renormalizes (valid for Matryoshka/MRL-trained models). `0` only stops the client from sending the `dimensions` field and disables client-side truncation; after migration 00011 the DB column stays `vector(1024)`, so 0 must not be used unless the provider natively returns 1024-dim vectors — otherwise inserts/searches fail with a Postgres "different vector dimensions" error. Only changing KADENCE_EMBED_MODEL (not dimensions) triggers a background re-index. |

> **Operator warning (migration 00011):** upgrading to this release runs a one-time migration
> that pins `chunks.embedding` to `vector(1024)`. Any pre-existing row wider than 1024 dims is
> converted in place (truncated to the first 1024 dims and L2-renormalized — the same MRL
> truncation the client applies to its own output); this is a lossy but content-preserving
> conversion. Any pre-existing row narrower than 1024 dims is **deleted** — it cannot be widened
> without re-embedding, and that content's searchability is lost permanently (re-ingest the
> source document/message to restore it). This only affects rows already narrower than 1024 dims
> before the upgrade, which requires `KADENCE_EMBED_DIMENSIONS` to have previously been set below
> 1024.

## Ingestion

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_UPLOAD_MAX_BYTES` | `10485760` (10 MB) | Max upload size. |
| `KADENCE_INGEST_CHUNK_CHARS` | `1000` | Chunk size (characters) for RAG splitting. |
| `KADENCE_MARKITDOWN_URL` | — | `markitdown-mcp` service URL. Empty falls back to the pure-Go PDF path. |
| `KADENCE_MARKITDOWN_AUTH_USER` | — | markitdown basic-auth username. |
| `KADENCE_MARKITDOWN_AUTH_PASS` | — | markitdown basic-auth password. |
| `KADENCE_MARKITDOWN_TRANSPORT` | `streamable-http` | markitdown MCP transport. |

## MCP

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_MCP_MAX_ITERATIONS` | `16` | Max agentic tool-loop iterations per chat turn. |
| `KADENCE_MCP_MAX_TOOLS` | `100` | Cap on tool definitions injected per request. |
| `KADENCE_MCP_CA_FILE` | — | PEM CA bundle for verifying MCP/markitdown TLS. Empty = system trust store. |
| `KADENCE_USER_MCP_ALLOWED_HOSTS` | — | Comma-separated host allowlist for user-registered MCP servers. Enables the feature only when set together with `KADENCE_ENCRYPTION_KEY`. |
| `KADENCE_USER_MCP_MAX_SERVERS` | `10` | Max user-defined MCP servers a single owner may register. `POST /api/mcp` returns 400 over the cap. |

### MCP server env contract

Configured MCP servers are declared by a fixed env pattern; the app builds one HTTP
client per server on startup:

```
MCP_<NAME>_<SCOPE>_URL
MCP_<NAME>_<SCOPE>_AUTH_USER
MCP_<NAME>_<SCOPE>_AUTH_PASS
MCP_<NAME>_<SCOPE>_TRANSPORT     # streamable-http | sse
MCP_<NAME>_<SCOPE>_TOOLS         # optional: comma/space-separated globs (unprefixed tool names)
```

- `<NAME>` — e.g. `GARMIN`. `<SCOPE>` — `GLOBAL` (all users) or `USER_<username>`.
- A user's tools at chat time = global servers ∪ their own servers.

Example:

```
MCP_GARMIN_GLOBAL_URL=http://kadence-mcp-garmin:8080
MCP_GARMIN_GLOBAL_AUTH_USER=kadence
MCP_GARMIN_GLOBAL_AUTH_PASS=<generated>
MCP_GARMIN_GLOBAL_TRANSPORT=streamable-http
MCP_GARMIN_GLOBAL_TOOLS=get_activit*,*_workout
```

In a Helm deployment these are rendered for you from `mcp.servers[]` — see
[DEPLOYMENT.md](DEPLOYMENT.md).

## Validation

`config.Validate()` rejects startup when:

1. `KADENCE_DATABASE_URL` is empty.
2. `KADENCE_ENV` is prod/production but `KADENCE_CSRF_SECRET` is empty.
3. `KADENCE_USER_MCP_ALLOWED_HOSTS` is set but `KADENCE_ENCRYPTION_KEY` is not a valid
   32-byte key.
4. `KADENCE_RATE_LIMIT_GLOBAL` or `KADENCE_RATE_LIMIT_AUTH` is negative.
5. `KADENCE_LLM_CONTEXT_BUDGET` is not a positive integer.

Passkeys additionally require `KADENCE_WEBAUTHN_RP_ID` **and** `KADENCE_TRUSTED_ORIGINS`
**and** a valid 32-byte `KADENCE_ENCRYPTION_KEY`; if the RP ID is set without the
others, startup fails with a message naming what's missing.

## Feature gating summary

| Feature | Enabled when |
|---|---|
| Chat | `KADENCE_LLM_API_KEY` set |
| RAG memory | `KADENCE_EMBED_API_KEY` set |
| Guardrail | `KADENCE_GUARDRAIL_ENABLED=true` |
| Passkeys | `KADENCE_WEBAUTHN_RP_ID` + `KADENCE_TRUSTED_ORIGINS` + 32-byte `KADENCE_ENCRYPTION_KEY` |
| User-defined MCP | `KADENCE_USER_MCP_ALLOWED_HOSTS` + 32-byte `KADENCE_ENCRYPTION_KEY` |
| Rich ingestion | `KADENCE_MARKITDOWN_URL` set (else PDF text fast-path only) |
