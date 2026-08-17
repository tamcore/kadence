# Configuration

Kadence's main server is configured **entirely through environment variables**
(prefix `KADENCE_*`), loaded once at startup by `config.Load()`. There are no config
files or main-server command-line flags. `config.Validate()` fails fast on startup
for invalid combinations (see [Validation](#validation)). The internal
`file-bridge` subcommand also uses environment variables; Helm supplies them
automatically when FIT analysis is enabled.

Values shown are the built-in defaults; `—` means unset/empty.

## Core / server

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_LISTEN_ADDR` | `:8080` | HTTP listener bind address. |
| `KADENCE_HEALTH_ADDR` | `:8081` | Dedicated liveness-only listener bind address, serving only `GET /healthz` (200, no auth). Stays up through the entire graceful-drain window so kubelet's liveness probe never fires during a rolling shutdown; the readiness probe stays on the main listener's `/api/healthz` so the pod is still correctly removed from Service endpoints once draining starts. |
| `KADENCE_ENV` | `dev` | `dev` \| `prod` \| `production`. Prod enables secure cookies + strict CSRF. |
| `KADENCE_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`. |
| `KADENCE_DATABASE_URL` | — (required) | Postgres DSN (pgvector). goose migrations run on startup. |

## Auth & security

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_CSRF_SECRET` | — | `gorilla/csrf` secret. Required in prod (must be at least 32 bytes); random per-restart in dev. Share across replicas. |
| `KADENCE_TRUSTED_ORIGINS` | — | Comma-separated CSRF/WebAuthn trusted origins (e.g. `https://kadence.example.com`). |
| `KADENCE_ENCRYPTION_KEY` | — | Base64-encoded 32-byte key (AES-256-GCM) for secrets at rest (MCP credentials, WebAuthn ceremony data). If set, it is always decoded and length-checked at startup, even when no key-dependent feature (passkeys, user-defined MCP) is enabled — a malformed value fails startup unconditionally, not just when the dependent feature is on. |
| `KADENCE_WEBAUTHN_RP_ID` | — | WebAuthn Relying Party ID = the site's effective domain (e.g. `kadence.example.com`). Empty disables passkeys. Also requires `KADENCE_TRUSTED_ORIGINS` + a valid `KADENCE_ENCRYPTION_KEY`. |
| `KADENCE_ADMIN_USERNAME` | — | First-run admin bootstrap (created only when the users table is empty). |
| `KADENCE_ADMIN_EMAIL` | — | First-run admin email. |
| `KADENCE_ADMIN_PASSWORD` | — | First-run admin password (bcrypt-hashed at insert). Minimum **12 characters** — shorter values fail startup, but only on a first boot that actually creates the admin. On an already-bootstrapped install the value is never read, so a shorter legacy value does not block an upgrade. |

## Rate limiting

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_RATE_LIMIT_GLOBAL` | `300` | Per-IP requests/minute across all `/api` routes (`/api/healthz` and the static frontend are exempt). `0` disables. |
| `KADENCE_RATE_LIMIT_AUTH` | `10` | Per-IP requests/minute on auth-sensitive endpoints: `POST /api/session`, `POST /api/webauthn/login/begin`, `POST /api/webauthn/login/finish`, `POST /api/credentials/{requestId}`. `0` disables. |
| `KADENCE_MAX_BODY_BYTES` | `1048576` (1 MiB) | Max request body size across `/api` routes in general. Document uploads and multipart `POST /api/chat` turns are exempt from this cap and governed by `KADENCE_UPLOAD_MAX_BYTES`; JSON chat remains under the general cap. |

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
| `KADENCE_LLM_MAX_TOKENS` | `8192` | Max completion tokens per request. A longer answer is truncated and reassembled through up to three continuation round-trips, which is slow and reads as a hung chat — lower this only for models or budgets that require it. |
| `KADENCE_LLM_TEMPERATURE` | `0.3` | Sampling temperature. |
| `KADENCE_LLM_TIMEOUT` | `300s` | Per-request timeout (Go duration). |
| `KADENCE_SYSTEM_PROMPT` | — | Overrides the built-in chat system prompt. |
| `KADENCE_LLM_CONTEXT_BUDGET` | `32000` | Estimated request-context budget, separate from `KADENCE_LLM_MAX_TOKENS` (the completion cap). Text uses a `len/4` heuristic and native images reserve `ceil(raw bytes/3)` for encoded transport. The current message and its evidence are mandatory; if they exceed the budget the request is rejected. Optional history is retained newest-first in whole turns and attachment payloads are loaded only for retained user turns. |

## Conversation title provider

Kadence makes one best-effort title request after the first successful assistant
answer in an ordinary new chat. Each empty title setting falls back independently
to its matching main LLM setting.

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_TITLE_MODEL` | `KADENCE_LLM_MODEL` | Title model id. |
| `KADENCE_TITLE_BASE_URL` | `KADENCE_LLM_BASE_URL` | OpenAI-compatible title provider base URL. |
| `KADENCE_TITLE_API_KEY` | `KADENCE_LLM_API_KEY` | Title provider API key. |

The request has a fixed three-second timeout and a 256-token completion cap.
Kadence keeps the deterministic fallback title if title generation fails. It
does not retry title generation later.

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

## MCP intent safeguard

The MCP intent safeguard is independent of the topic classifier. It reuses the
guardrail model, base URL, API key, and history-window settings above, including
their main-provider fallbacks, but does not require
`KADENCE_GUARDRAIL_ENABLED=true`.

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_MCP_INTENT_GUARD_ENABLED` | `false` | Require and classify `_kadence_intent` for each LLM-originated remote MCP call, including a remote download made by native FIT analysis. |

When enabled, each remote call adds one classifier request before dispatch, so it
adds classifier latency to the tool call. The check fails closed: malformed or
missing intent, unavailable trusted context, a denial, a classifier failure, or an
invalid response blocks the call. Credential placeholders are substituted only
after an `ALLOW` decision. When disabled, MCP tool schemas and dispatch keep their
previous behavior. Startup fails when this flag is enabled and neither
`KADENCE_GUARDRAIL_API_KEY` nor `KADENCE_LLM_API_KEY` resolves to a non-empty key.

## Scheduled tasks

Scheduled is opt-in. The main chat model refines a user's request into a
reviewable task definition; nothing runs until the user confirms that proposal.
Static reminders execute without inference. Data and monitoring tasks gather
evidence with an owner-scoped snapshot of their explicitly authorized MCP tools,
then pass the bounded result back to the main model for the user-facing response.

The worker provider overrides are resolved independently. An empty worker model,
base URL, or API key inherits the corresponding `KADENCE_LLM_*` value, so an
operator may move unattended gathering to a cheaper model, a different compatible
endpoint, or both. Synthesis still uses the main model.

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_SCHEDULED_ENABLED` | `false` | Enables the authenticated Scheduled API, navigation, and background worker. |
| `KADENCE_SCHEDULED_WORKER_MODEL` | (main model) | Model used for unattended evidence gathering and tool calls. |
| `KADENCE_SCHEDULED_WORKER_BASE_URL` | (main base URL) | Compatible provider endpoint used by the worker. |
| `KADENCE_SCHEDULED_WORKER_API_KEY` | (main key) | Worker provider API key. Keep it in a Secret. |
| `KADENCE_SCHEDULED_WORKER_MAX_TOKENS` | `2048` | Maximum completion tokens for each worker request. Must be positive when enabled. |
| `KADENCE_SCHEDULED_WORKER_TEMPERATURE` | (main temperature) | Sampling temperature for unattended evidence gathering and tool calls. |
| `KADENCE_SCHEDULED_WORKER_TIMEOUT` | `300s` | Timeout for each worker gather request. Must be a positive Go duration. |
| `KADENCE_SCHEDULED_WORKER_MAX_ITERATIONS` | `16` | Maximum agentic gather/tool-loop iterations per occurrence. Must be positive. |
| `KADENCE_SCHEDULED_WORKER_CONCURRENCY` | `1` | Maximum occurrences executed concurrently by each app replica. PostgreSQL claims prevent duplicate execution across replicas. |
| `KADENCE_SCHEDULED_MAX_ACTIVE_PER_USER` | `10` | Maximum active tasks per owner. Draft, paused, and terminal tasks do not consume the limit. |

Recurring schedules use an IANA timezone plus an RFC 5545 `DTSTART`/`RRULE` and
must be at least one hour apart. A missed recurring occurrence coalesces to one
catch-up run, then the task advances to the next future occurrence. A task may
deliver after every run or only when monitoring data changes. Initial behavior can
wait, deliver a preview, or establish a quiet monitoring baseline.

## Web Push (notifications)

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_PUSH_VAPID_PUBLIC_KEY` | — | VAPID public key (base64url). Exposed to the browser. Enables push when set with the others. |
| `KADENCE_PUSH_VAPID_PRIVATE_KEY` | — | VAPID private key (base64url). Secret — never logged or sent to clients. |
| `KADENCE_PUSH_VAPID_SUBJECT` | — | VAPID subject: a `mailto:` or `https` URL identifying the sender. |

Generate a keypair with any web-push VAPID tool. All three must be set together;
setting only some fails startup.

Task states are `draft`, `active`, `paused`, `completed`, `failed`, and `deleted`.
Runs are immutable records in `pending`, `running`, `no_change`, `delivered`,
`completed`, or `failed`. Once an occurrence starts, Kadence never replays it
automatically after a timeout, provider error, or process loss; the user may
explicitly run the task again. Missing authorized tools pause a task immediately.
Other failures increment the consecutive-failure count: a one-off task becomes
`failed`, while a recurring task pauses after three consecutive failures. A
successful run resets that count.

Stale running occurrences are recovered only after the worker gather timeout plus
the primary `KADENCE_LLM_TIMEOUT` plus 30 seconds (10 minutes 30 seconds with
the defaults), and recovery follows the same failure policy rather than replaying
the occurrence. Quiet `no_change` run rows are deleted after 30 days; delivered,
completed, and failed audit rows are retained while the task exists. Deleting a
linked Scheduled conversation pauses its live task and retains the conversation
and audit history. Deleting a task soft-deletes the task while preserving its
linked conversation and immutable run audit records.

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
| `KADENCE_UPLOAD_MAX_BYTES` | `10485760` (10 MB) | Max size of each knowledge-document upload and aggregate file bytes in one chat turn. Chat accepts at most five files; explicit references do not count toward this byte limit. |
| `KADENCE_INGEST_CHUNK_CHARS` | `1000` | Chunk size (characters) for RAG splitting. |
| `KADENCE_MARKITDOWN_URL` | — | `markitdown-mcp` service URL. Empty falls back to the pure-Go PDF path. |
| `KADENCE_MARKITDOWN_AUTH_USER` | — | markitdown basic-auth username. |
| `KADENCE_MARKITDOWN_AUTH_PASS` | — | markitdown basic-auth password. |
| `KADENCE_MARKITDOWN_TRANSPORT` | `streamable-http` | markitdown MCP transport. |
| `KADENCE_PDF_PAGE_IMAGES_ENABLED` | `true` | Extract page-content images from PDFs so a vision-capable model can read tables that carry no text layer. Needs a vision-capable model; without one the turn degrades to text only. |
| `KADENCE_PDF_PAGE_IMAGE_MIN_COVERAGE` | `0.12` | Minimum share of a nominal 150dpi page render an embedded image must cover to count as page content. At most one image per page is kept: the largest that qualifies. |
| `KADENCE_PDF_PAGE_IMAGE_MAX_PAGES` | `20` | Max page images per chat turn and per ingested document. |

## MCP

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_MCP_MAX_ITERATIONS` | `16` | Max agentic tool-loop iterations per chat turn. |
| `KADENCE_MCP_MAX_TOOLS` | `100` | Cap on tool definitions injected per request. |
| `KADENCE_MCP_AUDIT_TTL` | `48h` | Retention for full remote MCP call audit records. Must be a positive Go duration. Expired records are hidden immediately and deleted on startup, then hourly. |
| `KADENCE_MCP_CA_FILE` | — | PEM CA bundle for verifying MCP/markitdown TLS. Empty = system trust store. |
| `KADENCE_USER_MCP_ALLOWED_HOSTS` | — | Comma-separated host allowlist for user-registered MCP servers. Enables the feature only when set together with `KADENCE_ENCRYPTION_KEY`. |
| `KADENCE_PUBLIC_URL` | — | The bare origin this deployment is reachable at, e.g. `https://kadence.example.com`. Required once any MCP server uses OAuth; no path, query, fragment, or trailing slash, because the callback path is appended to it and the result must equal the registered redirect URI byte for byte. |

### OAuth-authenticated MCP servers

A server whose credential belongs to the user rather than the deployment is
declared with `AUTH_MODE=oauth` instead of `_AUTH_USER`/`_AUTH_PASS`. Each user
links their own account through a browser flow, and Kadence then sends that
user's own bearer token.

| Variable | Required | Meaning |
|---|---|---|
| `MCP_<NAME>_<SCOPE>_AUTH_MODE` | yes | `oauth` selects per-user tokens. Anything else keeps basic auth. |
| `MCP_<NAME>_<SCOPE>_OAUTH_CLIENT_ID` | yes | The client id registered with that server. |
| `MCP_<NAME>_<SCOPE>_OAUTH_CLIENT_SECRET` | no | Only for a confidential client. A public client with PKCE needs none. |
| `MCP_<NAME>_<SCOPE>_OAUTH_RESOURCE` | yes | The RFC 8707 resource indicator. Must equal the server's own `_URL`. |
| `MCP_<NAME>_<SCOPE>_OAUTH_SCOPES` | yes | Comma-separated. `garmin:read`, `garmin:write` and `garmin:destructive` are grantable; any other scope is refused at boot. |

Adding a scope does not widen an authorization a user already gave: a refresh
cannot grant what was never consented to. Every already-linked user must
authorize again, and **Settings → Integrations** shows `reconnect to allow
changes` with a **Reconnect** button until they do. Enabling the write tier in
the Helm chart therefore needs both halves — `garmin.oauth.scopes` gains
`garmin:write` AND `garmin.enableWriteTools: true` — and every existing link
needs one reconnect.

The destructive tier (`garmin.enableDestructiveTools`) asks the user to confirm
each call while it is in flight, on the chat stream that made it. Two
consequences follow. It requires `replicaCount: 1`, because the goroutine
waiting for that answer lives in one pod and the answering request could
otherwise land on another — the chart refuses to render the combination. And a
scheduled or otherwise unattended run refuses every such call outright, since
there is no stream to ask on.

The wait is 25 seconds, bounded by the MCP client transport's own 30-second cap
on an in-flight server request rather than chosen for comfort. An unanswered
prompt is a refusal.

Such a server also requires `KADENCE_PUBLIC_URL` and a 32-byte
`KADENCE_ENCRYPTION_KEY` — the per-user tokens are stored encrypted. Register
`<KADENCE_PUBLIC_URL>/api/mcp/oauth/callback` as the client's redirect URI; the
authorization server matches it exactly.
| `KADENCE_USER_MCP_MAX_SERVERS` | `10` | Max user-defined MCP servers a single owner may register. `POST /api/mcp` returns 400 over the cap. |

LLM-selected remote MCP calls from chats and Scheduled workers are recorded with
their model-visible arguments, secret-redacted result or error, actor, conversation
UUID, source, tool-call id, model, status, and timing. Guarded calls also record
the intent, verdict, and reason; blocked calls have `blocked` status. Audit
persistence fails open: an audit database error never blocks the tool call. Admins
can inspect retained records under **MCP Audit** and filter by intent or verdict.
Full audit records are TTL-only; health checks, tool discovery, ingestion calls,
and purely native tools are outside this audit, while a native tool's nested remote
MCP call is included. Existing per-message `tool_calls` remain part of chat history
and are not governed by this TTL.

### MCP server env contract

Configured MCP servers are declared by a fixed env pattern; the app builds one HTTP
client per server on startup:

```
MCP_<NAME>_<SCOPE>_URL
MCP_<NAME>_<SCOPE>_AUTH_USER
MCP_<NAME>_<SCOPE>_AUTH_PASS
MCP_<NAME>_<SCOPE>_TRANSPORT     # streamable-http | sse
MCP_<NAME>_<SCOPE>_TOOLS         # optional: comma/space-separated globs (unprefixed tool names)
MCP_<NAME>_<SCOPE>_ALIAS         # optional: short name replacing NAME as the tool-name prefix
MCP_<NAME>_<SCOPE>_HINT          # optional: "when to use this" line injected into the chat system prompt
```

`ALIAS`, when set, must match `^[a-z0-9][a-z0-9-]{0,31}$` (the same format as
user-defined server names); an invalid alias is dropped (falls back to `NAME`)
with a warning logged, never a startup failure. If two servers visible to the
same user would resolve to the same prefix, the later one falls back to its
own `NAME` instead of colliding — also logged, never a crash.

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

## Native FIT analysis

One or more numbered route groups enable the native
`kadence__analyze_garmin_fit(activity_id)` tool. `<N>` is a non-negative integer;
gaps are allowed. Each complete group binds a private bridge to one exact
environment-configured MCP server and scope.

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_FIT_ROUTE_<N>_SERVER_NAME` | — | Exact MCP env server name, for example `GARMIN1`. |
| `KADENCE_FIT_ROUTE_<N>_SERVER_SCOPE` | — | Exact MCP scope: `GLOBAL` or `USER_<username>`. |
| `KADENCE_FIT_ROUTE_<N>_DOWNLOAD_TOOL` | — | Unprefixed MCP tool name, for example `download_activity_file`. It must accept `{"activity_id": <positive integer>}`, write a `.fit` file into the bridge's shared directory, and return a path or JSON containing `path`/`file_path`. Kadence resolves the effective alias/name prefix from the current user's MCP snapshot. |
| `KADENCE_FIT_ROUTE_<N>_BRIDGE_URL` | — | Base URL of this route's private file bridge, for example `http://kadence-mcp-garmin1:8081`. |
| `KADENCE_FIT_ROUTE_<N>_BRIDGE_AUTH_USER` | — | HTTP Basic-auth username for this route's bridge. |
| `KADENCE_FIT_ROUTE_<N>_BRIDGE_AUTH_PASS` | — | HTTP Basic-auth password for this route's bridge. |
| `KADENCE_FIT_MAX_BYTES` | `33554432` (32 MiB) | App-side bridge-response cap. Must be positive when FIT analysis is enabled. The decoder independently hard-caps FIT input at 32 MiB, so raising this value does not raise the decoder limit. |

The native tool consumes one slot from `KADENCE_MCP_MAX_TOOLS`. Its output is
bounded to an activity summary and at most 100 lap splits; raw records, GPS
positions, and arbitrary FIT developer data are not returned. Routes are filtered
per chat snapshot, so two users may each have an independent MCP pod and bridge with
the same effective alias. When one user can see multiple FIT routes, the native tool
adds a required `source` enum containing only that user's effective MCP prefixes.

### File-bridge subcommand

The `kadence file-bridge` helper runs beside the selected MCP server. These settings
are normally rendered by `mcp.servers[].fitAnalysis`; they are documented for
non-Helm deployments and troubleshooting.

| Variable | Default | Purpose |
|---|---|---|
| `KADENCE_FILE_BRIDGE_ADDR` | `:8081` | Bridge listener address. `GET /healthz` is unauthenticated; file requests use `GET /files/<name>.fit`. |
| `KADENCE_FILE_BRIDGE_ROOT` | — (required) | Shared directory containing downloaded FIT files. Only direct-child regular `.fit` files are served. |
| `KADENCE_FILE_BRIDGE_AUTH_USER` | — (required) | HTTP Basic-auth username. |
| `KADENCE_FILE_BRIDGE_AUTH_PASS` | — (required) | HTTP Basic-auth password. |
| `KADENCE_FILE_BRIDGE_MAX_BYTES` | `33554432` (32 MiB) | Maximum file size served by the bridge; must be positive. |

The bridge rejects traversal, subdirectories, symlinks, non-regular files, changed
file identities, and oversized files. It removes a file only after transferring the
complete, unchanged file successfully.

## Validation

`config.Validate()` notably rejects startup when:

1. `KADENCE_DATABASE_URL` is empty.
2. In production (`KADENCE_ENV=prod`/`production`), `KADENCE_CSRF_SECRET` is empty
   or shorter than 32 bytes. In dev, an empty secret does *not* fail startup — the
   router falls back to an in-process random secret instead.
3. In production, `KADENCE_USER_MCP_ALLOWED_HOSTS` is set but `KADENCE_ENCRYPTION_KEY`
   is not a valid 32-byte key. In dev this combination does not fail startup; the
   user-defined-MCP feature simply stays disabled (see item 10 below for the
   unconditional case).
4. `KADENCE_RATE_LIMIT_GLOBAL` or `KADENCE_RATE_LIMIT_AUTH` is negative.
5. `KADENCE_LLM_CONTEXT_BUDGET` is not a positive integer.
6. `KADENCE_MCP_INTENT_GUARD_ENABLED=true` and neither `KADENCE_GUARDRAIL_API_KEY`
   nor `KADENCE_LLM_API_KEY` is set.
7. A `KADENCE_FIT_ROUTE_<N>_*` group is partial, has an invalid scope or prefixed
   download-tool name, or duplicates another route's MCP server/scope.
8. At least one FIT route is configured and `KADENCE_FIT_MAX_BYTES` is not positive.
9. Scheduled is enabled without a primary `KADENCE_LLM_API_KEY`, or any Scheduled
   worker budget/concurrency/active-task limit is not positive.
10. Only some of `KADENCE_PUSH_VAPID_PUBLIC_KEY`, `KADENCE_PUSH_VAPID_PRIVATE_KEY`,
   `KADENCE_PUSH_VAPID_SUBJECT` are set (all three or none are required).
11. `KADENCE_ENCRYPTION_KEY` is set but fails to base64-decode, or does not decode to
    exactly 32 bytes — **unconditionally**, regardless of whether passkeys or
    user-defined MCP are otherwise enabled. An unset key is not an error; only a
    malformed one is.
12. Any of these is not a positive integer/duration: `KADENCE_LLM_MAX_TOKENS`,
    `KADENCE_LLM_TIMEOUT`, `KADENCE_RAG_TOP_K`, `KADENCE_MCP_MAX_ITERATIONS`,
    `KADENCE_MCP_MAX_TOOLS`, `KADENCE_MCP_AUDIT_TTL`, `KADENCE_UPLOAD_MAX_BYTES`,
    `KADENCE_INGEST_CHUNK_CHARS`. Additionally, `KADENCE_EMBED_DIMENSIONS`,
    `KADENCE_USER_MCP_MAX_SERVERS`, and `KADENCE_MAX_BODY_BYTES` must each be
    non-negative (zero is allowed for these three; the others must be strictly
    positive).

Passkeys additionally require `KADENCE_WEBAUTHN_RP_ID` **and** `KADENCE_TRUSTED_ORIGINS`
**and** a valid 32-byte `KADENCE_ENCRYPTION_KEY`; if the RP ID is set without the
others, startup fails with a message naming what's missing.

**Defaults are not always "safe" fallbacks.** `envIntOr`, `envFloatOr`,
`envDurationOr`, and `envBoolOr` silently return the built-in default when the env
var is set to a value that fails to parse (e.g. `KADENCE_LLM_MAX_TOKENS=abc`) —
this is *not* a startup error, so a typo'd numeric/duration/boolean value is
indistinguishable from an unset one at runtime. The same applies to `KADENCE_ENV`:
any value other than exactly `prod` or `production` is treated as `dev`
(`IsProd()` does a direct string match), so a typo like `KADENCE_ENV=production `
(trailing space) or `KADENCE_ENV=prd` silently runs in dev mode — with dev's
weaker CSRF/cookie behavior — rather than failing.

## Feature gating summary

| Feature | Enabled when |
|---|---|
| Chat | `KADENCE_LLM_API_KEY` set |
| RAG memory | `KADENCE_EMBED_API_KEY` set |
| Guardrail | `KADENCE_GUARDRAIL_ENABLED=true` |
| MCP intent safeguard | `KADENCE_MCP_INTENT_GUARD_ENABLED=true` + resolved guardrail or main API key |
| Scheduled tasks | `KADENCE_SCHEDULED_ENABLED=true` + `KADENCE_LLM_API_KEY` |
| Web push | `KADENCE_PUSH_VAPID_PUBLIC_KEY` + `KADENCE_PUSH_VAPID_PRIVATE_KEY` + `KADENCE_PUSH_VAPID_SUBJECT` |
| Passkeys | `KADENCE_WEBAUTHN_RP_ID` + `KADENCE_TRUSTED_ORIGINS` + 32-byte `KADENCE_ENCRYPTION_KEY` |
| User-defined MCP | `KADENCE_USER_MCP_ALLOWED_HOSTS` + 32-byte `KADENCE_ENCRYPTION_KEY` |
| Rich ingestion | `KADENCE_MARKITDOWN_URL` set (else PDF text fast-path only) |
| Native FIT analysis | At least one complete `KADENCE_FIT_ROUTE_<N>_*` group |
