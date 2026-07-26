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
  fit/               bounded FIT activity decoding (summary + splits, no GPS records)
  pace/              strict metric/imperial/mps running-pace conversion
  secret/            credential broker — one-time placeholder tokens, never logs secrets
  chat/              per-turn orchestration: guardrail → RAG → provider stream → tool loop
  scheduled/         conversational task compiler, recurrence engine, worker + executor
  rag/               pgvector retrieval (per-user private ∪ admin public corpus)
  ingest/            document extraction pipeline (PDF fallback + markitdown-mcp)
  reindex/           background re-embed worker when the embedding model changes
  knowledge/         dependency-free text analytics (keywords/entities for the context view)
  webauthn/          passkey ceremonies (registration/assertion) + encrypted ceremony cookie
  model/             domain types
  mcpaudit/          fail-open remote-call audit lifecycle + secret-safe persistence
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
2. **Current-turn evidence** — validate up to five ordered files, extract supported
   documents after the guardrail, and resolve up to ten explicit references against
   the user's private documents plus the public corpus. Image bytes become native
   provider image parts; document text is framed as untrusted context.
3. **RAG retrieve** — embed the user's message and pull top-ranked chunks from the
   user's private memory plus the admin public corpus. Current attachments and
   explicit references receive context-budget priority over broad retrieval.
4. **Assemble + stream** — build the context (system prompt stamped with the current
   date and the user's unit preference) and stream from the provider, running a
   **tool loop**: the model requests a remote MCP or narrow Kadence-native tool → the
   app dispatches it → the result is fed back → repeat, up to a configured iteration
   cap.
5. **Persist + embed** — the turn, safe file metadata, raw attachment payloads, and
   reference snapshots are stored transactionally; text is embedded back into RAG.

Text-only turns retain the JSON request contract. Rich turns use bounded multipart
requests and are fully parsed before SSE begins, so a rejected upload cannot create a
partial turn. Responses stream to the browser as Server-Sent Events (`ChatEvent`
JSON); attachment payloads never appear in message JSON.

## Scheduled pipeline (`scheduled/`)

Scheduled uses a separate owner-scoped conversation kind and a confirm-before-run
state machine:

1. The main model receives the complete bounded definition thread and either asks
   one structured clarification question or returns a complete proposal.
2. Each new answer atomically invalidates the prior proposal. Confirmation uses
   the proposal version as a compare-and-swap, so stale tabs cannot activate an
   older definition.
3. Confirmed one-off or RFC 5545 recurring tasks become due in PostgreSQL.
   Every app replica polls, but row-locked occurrence claims and unique occurrence
   keys provide cross-replica at-most-once execution.
4. Static reminders persist their fixed message without provider inference.
   Data/monitoring tasks create a fresh immutable MCP snapshot for the task owner,
   intersect it with the exact confirmed tool names, and gather bounded evidence
   with the worker model. The main model synthesizes that data into the delivered
   result.
5. The run transition, result message, unread marker, monitoring state, and next
   occurrence are committed atomically.

Task definitions, runs, unread state, and MCP visibility are scoped by `user_id`.
Task states are `draft`, `active`, `paused`, `completed`, `failed`, and `deleted`;
the immutable run states are `pending`, `running`, `no_change`, `delivered`,
`completed`, and `failed`. Recurring schedules coalesce missed occurrences into
one catch-up run before advancing to the next future occurrence. Claiming clears
the next due time and records a unique running occurrence, so a timeout, provider
error, or process loss never automatically replays an occurrence. A user can
explicitly run the task again instead.

A missing confirmed tool pauses its task immediately. Other execution failures
increment a consecutive-failure count; one-off tasks become `failed`, while
recurring tasks pause after three consecutive failures. Successful runs reset that
count. Deleting a linked Scheduled conversation pauses its live task and retains
the conversation and immutable audit history. Deleting a task is a soft delete:
its linked conversation and run records remain intact.

There is no global in-memory task registry. The list API is bounded and
priority-paginated (active, unread, paused/draft, then terminal), and definition
messages carry a separate persistence purpose so frequent deliveries cannot evict
compiler context. The UI supports draft replay, pause/resume, run-now, deletion,
and readback of immutable run history.

The compiler and result synthesis use the main provider. Evidence gathering can
use independently configured worker model/base URL/key overrides, including a
cheaper model or another compatible endpoint. Provider and MCP boundaries remain
ordinary HTTP; the runtime has no Kubernetes dependency.

## MCP orchestration (`mcp/`)

MCP servers are **remote and network-transport only** (`streamable-http` / `sse`) —
there is no in-process MCP server. Kadence may expose narrow native orchestration
tools, but those still use the user's remote MCP snapshot for external operations.
On startup the registry scans the environment for a fixed contract and builds one
client per server:

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

Each LLM-driven remote invocation is independently recorded in `mcp_call_audit`.
Records attribute the actor, conversation, chat or Scheduled source, model,
provider tool-call id, remote tool, timing, and terminal status. Arguments are the
model-visible form before credential substitution; results and errors pass through
the same broker-secret redaction used by chat persistence. Recording fails open so
an audit outage cannot change tool availability. Admin list/detail endpoints expose
retained records; a global TTL hides expired rows at query time and a startup/hourly
reaper deletes them. Discovery, health, ingestion, and native-only calls are not
included, while remote calls nested inside native tools are.

## Native pace conversion (`pace/`)

Kadence always exposes `kadence__convert_pace` in interactive and unattended
tool catalogs. The local, read-only tool converts strict `M:SS` running paces
between min/km, min/mi, and meters per second without model arithmetic. It uses
the exact 1609.344-meter international mile and rounds human pace output to the
nearest second; meters-per-second output is not deliberately rounded.

Remote MCP definitions cannot shadow this built-in. The dedicated
`pace-conversion` skill gates its first call in a turn, and
`workout-programming` requires one conversion call per custom pace-range bound.

## Native FIT analysis (`fit/`)

When FIT analysis is configured, Kadence adds
`kadence__analyze_garmin_fit(activity_id)` to the model's tool set. It deliberately
bridges two separate pod filesystems instead of assuming an MCP download path is
local to the app:

1. Kadence matches configured FIT routes against the exact MCP server name and
   scope visible in the current user's snapshot. It combines that server's effective
   alias/name prefix with the route's unprefixed download tool.
2. The MCP server writes the `.fit` file into an ephemeral `emptyDir` shared only
   with a Kadence `file-bridge` sidecar and returns its path.
3. The app reduces that result to a direct-child `.fit` basename and fetches it from
   the private bridge over HTTP Basic authentication. With chart NetworkPolicy
   enabled, port 8081 is permitted only between the app and the selected MCP pod.
4. The bridge serves only regular, unchanged files confined beneath its configured
   root. After a complete successful transfer it deletes the same file.
5. `internal/fit` decodes the file in memory into metric-labelled activity and lap
   summaries. Reads are capped at 32 MiB, output at 100 splits, and record samples,
   GPS positions, and arbitrary developer data are discarded.

The raw FIT file is never stored in Postgres or RAG. Failures exposed to the model
are generic; operational logs contain only a bounded failure stage, never the raw
path or file contents. Any number of user-scoped MCP servers may have independent
FIT routes. A user sees only routes belonging to MCP servers in their snapshot; if
more than one is visible, the native tool requires a `source` chosen from those
servers' effective prefixes.

## RAG & ingestion

- **Retrieval** filters on `user_id = current ∪ scope = public`, so each user sees
  their own memory plus the admin corpus, never other users' data.
- **Ingestion** normalizes each input to markdown, then chunks → embeds → stores.
  Text-layer PDFs use a pure-Go fast path; richer extraction (scanned PDFs, images,
  screenshots) goes through a `markitdown-mcp` service when configured.
- **Explicit chat references** bypass broad-retrieval uncertainty: a referenced
  document is included whole when it fits, otherwise its ranked sections are
  included with a truncation marker. Visibility is checked again for every turn,
  edit, and regeneration.
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
- **mcp_call_audit** — bounded full payload/result audit of LLM-driven remote MCP
  calls, with actor, conversation, source, model, status, and timing snapshots.
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
- **MCP audit** — stores only model-visible placeholder arguments and broker-redacted
  outputs/errors; raw substituted credential values are never persisted.
- **MCP isolation** — each MCP server is deployed behind a basic-auth nginx sidecar,
  reachable only from the main app by NetworkPolicy, optionally over TLS.
- **FIT-file isolation** — transient FIT files live in a per-pod `emptyDir`; the
  authenticated bridge accepts only direct `.fit` filenames and deletes a file
  after a complete successful transfer.

See [CONFIGURATION.md](CONFIGURATION.md) for the environment variables referenced
here, and [DEPLOYMENT.md](DEPLOYMENT.md) for how the chart renders all of this.
