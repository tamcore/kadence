# Deployment

Kadence ships as a Helm chart (`charts/kadence/`). The supported target is
Kubernetes; the app itself is k8s-agnostic (env config only), so docker-compose or a
bare binary also work but are user-maintained.

## Install

```bash
helm upgrade --install kadence ./charts/kadence \
  -n kadence --create-namespace \
  -f my-values.yaml
```

Provide, at minimum: a database, provider keys, admin bootstrap, and (in prod) a CSRF
secret. All non-secret settings go under `config:` (rendered into a ConfigMap); all
secrets go under `secrets:` (rendered into a Secret, or supply an `existingSecret`).
The keys map 1:1 to the environment variables in [CONFIGURATION.md](CONFIGURATION.md).

## Database

Two options:

- **Bundled Postgres** — set `postgres.enabled=true` to render a `pgvector/pgvector`
  StatefulSet + Service with a persistent volume. Good for getting started and used by
  the KinD e2e path.
- **External Postgres** — leave `postgres.enabled=false` and point Kadence at your own
  pgvector-enabled database via `externalDatabase.url` or `externalDatabase.existingSecret`.

Migrations (`goose`, embedded SQL) run automatically on startup — no separate job.

## Upgrade notes

**This release changes the app Deployment's selector** (it now includes
`app.kubernetes.io/component: app`, scoping it to the app pod instead of every
workload in the release). Kubernetes Deployment selectors are immutable, so
`helm upgrade` against an existing install will fail with an "field is
immutable" error. Before upgrading, delete the existing Deployment once:

```bash
kubectl delete deployment <release-name> -n kadence
```

then re-run `helm upgrade`/`helm apply`. This causes a brief outage (pods are
recreated) but does not touch the Service or PodDisruptionBudget, and no data
is lost — Postgres and any persistent volumes are unaffected.

## MCP servers

Each entry under `mcp.servers[]` renders a full, isolated unit:

- a **Deployment** (the MCP image + its env / mounted secret),
- an **nginx basic-auth sidecar** (credentials auto-generated, or `existingSecret`),
- a **Service**,
- a **NetworkPolicy** allowing ingress **only from the main app**,
- optional **TLS** (cert-manager) when `mcp.tls.enabled=true`,
- injection of the matching `MCP_<NAME>_<SCOPE>_*` env vars into the main Deployment.

```yaml
mcp:
  basicAuth:
    # provide a stable password (recommended) or let the chart generate one
    password: <shared-across-renders>
  tls:
    enabled: false
  servers:
    - name: garmin
      scope: { user: alice }        # or: scope: global
      image: <registry>/garmin-mcp:<tag>
      pullPolicy: Always
      transport: streamable-http
      port: 8000
      path: /mcp
      env:
        - { name: GARMIN_MCP_TRANSPORT, value: streamable-http }
      tools: ["get_activit*", "*_workout", "download_activity_file"] # app-side glob allowlist
      alias: garmin                          # optional: tool-name prefix override
      hint: "use for activity history and training plans"  # optional: chat system-prompt guidance
      existingSecret: garmin-creds           # server-specific secret (optional)
      persistence:                           # optional per-server volume
        enabled: true
        size: 1Gi
        mountPath: /data/.garminconnect
      fitAnalysis:                           # optional native FIT analysis
        enabled: true
        downloadDir: /data/fit
        downloadTool: download_activity_file # unprefixed MCP tool name
```

Notes:
- Prefer a **tag + `pullPolicy: Always`** over a digest pin when you want the sidecar
  to track a rolling image; a digest pin always re-pulls that exact build.
- The sidecar proxies to the MCP on `127.0.0.1:<port>`. The proxied `Host` header
  includes the port so it satisfies MCP SDKs that enforce DNS-rebinding/Host checks.

### Native FIT activity analysis

Enable `fitAnalysis` independently on any number of `mcp.servers[]` entries.
`downloadTool` is the unprefixed MCP tool name. The chart emits a distinct numbered
`KADENCE_FIT_ROUTE_<N>_*` group containing the server's exact name, scope, bridge,
and credentials; at chat time Kadence combines that route with the effective
alias/name prefix from the current user's MCP snapshot.

For example, two entries with unique names (`garmin1` and `garmin2`), scopes
`{user: alice}` and `{user: bob}`, and the same `alias: garmin` remain isolated:
Alice's snapshot resolves only the first bridge and Bob's only the second. If one
user can see multiple FIT-enabled servers, the model-facing
`kadence__analyze_garmin_fit` tool adds a required `source` enum containing only
that user's effective prefixes.

The selected MCP server must:

- accept a positive `activity_id`,
- write the downloaded file beneath `downloadDir`, and
- return the resulting path directly or as JSON containing `path` or `file_path`.

The chart mounts a per-pod `emptyDir` at `downloadDir` in both the MCP container and
a Kadence `file-bridge` sidecar. It also:

- exposes the bridge on the MCP Service's port 8081,
- injects the download tool, bridge URL, and bridge credentials into the app,
- reuses the MCP basic-auth Secret for the private bridge,
- adds app egress and MCP-pod ingress rules for port 8081 when NetworkPolicy is
  enabled, and
- configures hardened bridge security context, health probes, and resources.

The app sends only the returned `.fit` basename to the bridge. The bridge confines
access to a direct regular file beneath `downloadDir`, caps files at 32 MiB, and
deletes the unchanged file after a complete successful transfer. Kadence decodes
the response in memory and exposes only a metric summary and at most 100 lap splits;
it does not retain raw records or GPS positions.

For non-Helm deployments, reproduce the same private shared-storage and authenticated
HTTP arrangement using the variables in
[CONFIGURATION.md](CONFIGURATION.md#native-fit-analysis). Never expose the bridge
outside the trusted app-to-MCP network path.

## Optional document ingestion (markitdown)

Set the `markitdown` block to deploy a `markitdown-mcp` sidecar (its own nginx
basic-auth + NetworkPolicy + optional TLS) for rich PDF/image extraction. Without it,
Kadence falls back to the pure-Go PDF text path.

Like `mcp.basicAuth`, set `markitdown.basicAuth.password` to a stable value under
`helm template | kubectl apply` — otherwise the password auto-generates on every
deploy and the app and markitdown sidecar disagree on credentials until pods restart.

## Scheduled tasks

Scheduled is disabled by default. Enable it through the chart's existing
environment maps:

```yaml
config:
  KADENCE_SCHEDULED_ENABLED: true
  KADENCE_SCHEDULED_WORKER_MODEL: economical-worker
  KADENCE_SCHEDULED_WORKER_BASE_URL: https://compatible.example.com/v1
  KADENCE_SCHEDULED_WORKER_MAX_TOKENS: 2048
  KADENCE_SCHEDULED_WORKER_TIMEOUT: 300s
  KADENCE_SCHEDULED_WORKER_MAX_ITERATIONS: 16
  KADENCE_SCHEDULED_WORKER_CONCURRENCY: 1
  KADENCE_SCHEDULED_MAX_ACTIVE_PER_USER: 10

secrets:
  KADENCE_SCHEDULED_WORKER_API_KEY: replace-me
```

The worker model/base URL/key are optional and independently inherit the main
`KADENCE_LLM_*` values when omitted. Keep the worker key under `secrets`; the
chart renders it only in the app Secret, never the ConfigMap.

Every app replica runs a worker. `KADENCE_SCHEDULED_WORKER_CONCURRENCY` is a
per-replica bound, while PostgreSQL row claims prevent two replicas from executing
the same occurrence. Size provider quotas for `replicaCount × concurrency`.
The default `terminationGracePeriodSeconds: 635` covers the default 300-second
gather timeout, 300-second primary synthesis timeout, 30-second finalization
margin, and five seconds of shutdown headroom. If either timeout is overridden,
set `terminationGracePeriodSeconds` to at least
`KADENCE_SCHEDULED_WORKER_TIMEOUT + KADENCE_LLM_TIMEOUT + 35s`. A replacement
replica recovers a stale run only after the gather timeout plus the primary timeout
plus 30 seconds; it records the interruption under the normal failure policy and
does not replay the started occurrence. Migration 00015 classifies definition and
delivery messages separately, including data written before that migration.

Users can activate at most `KADENCE_SCHEDULED_MAX_ACTIVE_PER_USER` tasks. Missing
confirmed MCP tools or repeated execution failures pause a task for review.
`no_change` monitoring audit rows expire after 30 days; visible results and other
run audit records remain in PostgreSQL.

## Ingress & TLS

The chart renders an nginx `Ingress` with cert-manager annotations (e.g.
`letsencrypt-prod`), proxy timeouts tuned for SSE streaming, and a body-size limit
above the upload cap. Set your host and issuer in `ingress:`.

## Hardening defaults

- Main Deployment: distroless/nonroot, `runAsNonRoot: true`, read-only root filesystem,
  `seccompProfile: RuntimeDefault`.
- `PodDisruptionBudget` with `minAvailable: 1`.
- Every MCP/markitdown sidecar's **nginx** basic-auth container runs
  `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, drops all
  capabilities, and uses `seccompProfile: RuntimeDefault`.
- MCP and markitdown pods default to uid/gid 65532 with `runAsNonRoot: true`; an MCP
  server may override its pod security context when its image requires another uid.
- MCP and markitdown workload containers drop all capabilities, disable privilege
  escalation, and receive default CPU/memory requests. Per-workload resources may be
  overridden.
- Each MCP/markitdown workload is basic-auth protected in front of nginx, and its
  `NetworkPolicy` restricts **ingress** to the main app pod only.
- MCP NetworkPolicies also restrict egress to DNS plus public TCP 80/443 by default,
  excluding private and link-local ranges. `mcp.servers[].egress` replaces that
  default for a server that needs another destination.
- The optional FIT bridge uses a read-only root filesystem, drops all capabilities,
  has liveness/readiness probes, and is reachable only from the app on port 8081.
- `KADENCE_CSRF_SECRET` must be shared across replicas (set it explicitly in prod).

### Known gaps in MCP/markitdown sidecar hardening (current state)

Remaining gaps are narrower:

- MCP/markitdown upstream processes bind to loopback and cannot be probed directly
  with the current images. The nginx liveness/readiness probe proves the proxy is
  running, not that the upstream MCP process can complete a request.
- Upstream MCP/markitdown root filesystems remain writable for image compatibility.
- Workloads have default resource requests but no default CPU/memory limits; set
  `mcp.servers[].resources` or `markitdown.resources` for production limits.
- The markitdown NetworkPolicy currently restricts ingress only; unlike MCP servers,
  markitdown egress is not restricted by the chart.

## Local / cluster dev deploy

`make dev-deploy-k8s` builds a dev image, pushes it, then `helm template | kubectl apply`
(no Helm release state — avoids server-side-apply field-ownership conflicts). It
requires a local `charts/kadence/values-dev.yaml` (gitignored). Target a specific
cluster with `KUBE_CONTEXT=<ctx>` and registry with `IMAGE_REGISTRY=<registry>`:

```bash
make dev-deploy-k8s KUBE_CONTEXT=mycluster IMAGE_REGISTRY=registry.example.com
```

A KinD-based end-to-end path is available via `make e2e-kind` (bundled Postgres +
smoke test).
