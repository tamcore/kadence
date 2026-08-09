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
  chat/skill/        model-facing skill definitions injected into the chat tool catalog
  scheduled/         conversational task compiler, recurrence engine, worker + executor
  bg/                panic-containment policy for long-lived background goroutines
  push/              web push dispatch (VAPID) with per-send timeout and failure pruning
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
   provider image parts; document text is framed as untrusted context. Native image
   transport cost is reserved before fitting document text, and a mandatory current
   turn that cannot fit is rejected without dropping evidence.
3. **RAG retrieve** — embed the user's message and pull top-ranked chunks from the
   user's private memory plus the admin public corpus. Current attachments and
   explicit references receive context-budget priority over broad retrieval.
4. **Assemble + stream** — build the context (system prompt stamped with the current
   date and the user's unit preference) and stream from the provider, running a
   **tool loop**: the model requests a remote MCP or narrow Kadence-native tool → the
   app dispatches it → the result is fed back → repeat, up to a configured iteration
   cap. Remote tool results are size-capped and fed back inside the same
   `<untrusted_context>` JSON fence as document text, so a result cannot escape its
   own fence or pose as an instruction.
5. **Persist + embed** — the turn, safe file metadata, raw attachment payloads, and
   reference snapshots are stored transactionally; text is embedded back into RAG.
6. **Title new chats** — after a first assistant response persists for an ordinary
   new chat, Kadence sends only the first bounded visible user text and the final
   visible, redacted assistant text to a tool-free title provider. It excludes
   system prompts, history, attachments and file names, tool arguments and
   results, audit data, credentials, and hidden metadata. The owner-scoped
   current-title compare-and-set preserves a manual or concurrent rename. On a
   successful swap, a canonical, sidebar-safe `title` SSE event arrives before
   `done`. The frontend immediately upserts it into its sorted conversation
   store, then reconciles again at the terminal event. A generation failure
   retains the deterministic fallback title. A persistence error leaves the
   current canonical title unchanged. A compare-and-set miss preserves the
   current canonical title, including a manual or concurrent rename. A title-event
   send or flush failure happens after the generated title persists; the chat
   still succeeds and terminal reconciliation can recover that canonical title.
   Scheduled handoffs, existing chats, edits, regenerations, guardrail refusals,
   and failed assistant responses skip title generation.

Text-only turns retain the JSON request contract. Rich turns use bounded multipart
requests and are fully parsed before SSE begins, so a rejected upload cannot create a
partial turn. Responses stream to the browser as Server-Sent Events (`ChatEvent`
JSON); attachment payloads never appear in message JSON.

### Inline Scheduled handoff

Chat can offer future follow-ups, but it may create a Scheduled draft only when
the **current user turn explicitly requests future unattended work**. A
suggestion, a prior turn, or model inference is not authorization. One
`kadence__draft_future_unattended_task` call represents one independently
confirmable task. Direct calendar or domain work belongs to its direct MCP tool,
including an operation that creates data for a future date. A direct-operation
failure does not create a draft: the model must separately request a future
retry or follow-up through the Scheduled tool.

Its instruction is a bounded, safe transfer of the source request and a small
recent-text context; it is not a copied tool transcript, credential, or
unbounded chat history. The persisted handoff envelope also contains
server-owned compiler context. It is private compiler input, not a display
format: an owner-scoped Scheduled detail view projects only the delegated
instruction into its first user bubble. Later refinements and direct Scheduled
messages remain unchanged. The API and UI never receive the envelope, prior-chat
records, timestamps, trust markers, or tool catalog records.

The handoff creates a relational draft and a source-message artifact, then hands
compiler authority to `scheduled/`. The chat model cannot choose the final
schedule, task kind, or integrations: the Scheduled compiler validates the
proposal against the owner-scoped MCP snapshot and only exact authorized tools.
Each card is reviewed, adjusted, dismissed, or confirmed independently. The UI
hydrates artifacts for a batch of source message IDs in one request, so loading a
conversation does not issue one request per card.

Handoffs are keyed by `(source conversation, source user message, ordinal)`.
Regenerating or rewinding a source assistant response therefore reuses its same
draft slots rather than multiplying tasks. Deleting/rewinding source messages
uses tombstones for obsolete, unconfirmed artifacts; confirmed tasks and their
links survive the rewind as auditable Scheduled work. There is no new environment
variable for this handoff; it uses the existing Scheduled feature and provider/MCP
configuration.

Existing false drafts are manual-cleanup scope. Kadence adds no migration,
automatic deletion, or automatic dismissal for them.

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

If handoff preparation fails, the artifact persists only one stable safe code:
`provider_timeout`, `provider_unavailable`, `invalid_definition`, or
`internal_error`. The category is safe for API and UI display; wrapped provider
causes and compiler input remain server-side. Existing lifecycle rules decide
whether that artifact remains retryable.

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

Inline drafts retain their source chat link while their executions deliver into
their separate Scheduled conversations/inboxes. Thus a source card continues to
show its task state and details link after confirmation, while run results, unread
state, and retryable execution history remain in the Scheduled surface.

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

When the MCP intent safeguard is enabled, Kadence adds the required reserved
`_kadence_intent` string argument to each accepted remote-tool schema and the
native FIT analysis schema. Interactive calls use the current user request and
bounded chat history as trusted context. Scheduled calls use the confirmed task
instruction and its task state. Tool descriptions and arguments are data, not
authority.

The guarded catalog omits a remote tool when its schema is malformed or not an
object, already defines `_kadence_intent` as a property, or cannot be safely
augmented.

The guard fails closed before dispatch, in this order:

1. Parse one JSON object, require a non-empty `_kadence_intent`, then remove it
   from the arguments.
2. Check the clean arguments, tool definition, intent, and trusted context with
   the classifier.
3. Only after `ALLOW`, substitute one-time credential placeholders and invoke the
   remote tool.

The classifier output is capped at 512 tokens. It must be exactly one JSON object
with uppercase `ALLOW` or `DENY` and a trimmed, non-empty UTF-8 reason of at most
512 bytes. Extra or invalid output fails closed.

Malformed input, a missing trusted context, a denied decision, a classifier error,
or an invalid classifier response blocks the call before credential substitution or
remote dispatch. With the safeguard disabled, schemas and calls retain their prior
behavior. Native FIT analysis follows the same boundary: its approved intent is
inherited by the nested download, which is authorized again against that download
tool and its clean arguments. An inherited intent is not trusted context.

Each LLM-driven remote invocation is independently recorded in `mcp_call_audit`.
Records attribute the actor, conversation, chat or Scheduled source, model,
provider tool-call id, remote tool, timing, and terminal status. Guarded records
also store the intent, guard verdict, and guard reason; a refused call has
`blocked` status. Arguments are the model-visible form before credential
substitution; results and errors pass through the same broker-secret redaction used
by chat persistence. Recording fails open so an audit outage cannot change tool
availability. Admin list/detail endpoints and the UI can filter by intent and guard
verdict. Full audit records are retained only for the configured TTL: expired rows
are hidden at query time and a startup/hourly reaper deletes them. Discovery,
health, ingestion, and native-only calls are not included, while remote calls
nested inside native tools are. List API responses and UI summaries omit arguments,
guard reason, result, and error. Detail responses return the retained safe/redacted
fields and the UI detail shows the guard reason.

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

There is no dedicated `rag/` package: pgvector storage and similarity search live in
`internal/store` (`chunk_repo.go`), and the retrieval step of the chat pipeline
(embed query → fetch ranked chunks) lives in `internal/chat` (`rag.go`), called
from `chat/service.go`'s per-turn orchestration.

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
- **scheduled_tasks** / **scheduled_runs** / **scheduled_definition_messages** —
  owner-scoped task definitions, immutable occurrence results, and the bounded
  compiler thread behind the Scheduled inbox.
- **scheduled_handoffs** — source-message artifacts and their per-source ordinal
  slots, including draft/confirmed/tombstoned linkage used to make regeneration
  and rewinds idempotent without deleting confirmed work.
- **documents** / **chunks** — ingested material and their embeddings; `scope`
  distinguishes private from the admin public corpus. Chunks store the embedding model.
- **user_mcp_servers** — user-registered MCP servers (auth password encrypted).
- **mcp_call_audit** — TTL-bounded full payload/result audit of LLM-driven remote
  MCP calls, with actor, conversation, source, model, intent, guard decision,
  status, and timing snapshots.
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
- **MCP audit** — stores only model-visible placeholder arguments, intent and guard
  decision fields, and broker-redacted outputs/errors; raw substituted credential
  values are never persisted.
- **MCP isolation** — each MCP server is deployed behind a basic-auth nginx sidecar,
  reachable only from the main app by NetworkPolicy, optionally over TLS.
- **FIT-file isolation** — transient FIT files live in a per-pod `emptyDir`; the
  authenticated bridge accepts only direct `.fit` filenames and deletes a file
  after a complete successful transfer.

See [CONFIGURATION.md](CONFIGURATION.md) for the environment variables referenced
here, and [DEPLOYMENT.md](DEPLOYMENT.md) for how the chart renders all of this.
