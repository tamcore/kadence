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
      tools: ["get_activit*", "*_workout"]   # app-side glob allowlist
      existingSecret: garmin-creds           # server-specific secret (optional)
      persistence:                           # optional per-server volume
        enabled: true
        size: 1Gi
        mountPath: /root/.garminconnect
```

Notes:
- Prefer a **tag + `pullPolicy: Always`** over a digest pin when you want the sidecar
  to track a rolling image; a digest pin always re-pulls that exact build.
- The sidecar proxies to the MCP on `127.0.0.1:<port>`. The proxied `Host` header
  includes the port so it satisfies MCP SDKs that enforce DNS-rebinding/Host checks.

## Optional document ingestion (markitdown)

Set the `markitdown` block to deploy a `markitdown-mcp` sidecar (its own nginx
basic-auth + NetworkPolicy + optional TLS) for rich PDF/image extraction. Without it,
Kadence falls back to the pure-Go PDF text path.

Like `mcp.basicAuth`, set `markitdown.basicAuth.password` to a stable value under
`helm template | kubectl apply` — otherwise the password auto-generates on every
deploy and the app and markitdown sidecar disagree on credentials until pods restart.

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
- Each MCP/markitdown workload is basic-auth protected in front of nginx, and its
  `NetworkPolicy` restricts **ingress** to the main app pod only.
- `KADENCE_CSRF_SECRET` must be shared across replicas (set it explicitly in prod).

### Known gaps in MCP/markitdown sidecar hardening (current state)

The MCP server / markitdown **workload container** (as opposed to its nginx sidecar,
which is fully hardened per above) does not yet have:

- `runAsNonRoot` / a pinned `runAsUser` — it drops Linux capabilities and disables
  privilege escalation, but runs as whatever user the upstream image defaults to.
- Liveness/readiness probes on either the workload or nginx containers.
- Default CPU/memory **limits** — `mcp.servers[].resources` and
  `markitdown.resources` accept `requests`/`limits`, but nothing is set unless you
  provide them; an unbounded workload can consume node resources.
- **Egress** restriction — the rendered `NetworkPolicy` only sets
  `policyTypes: [Ingress]`, so outbound traffic from MCP/markitdown pods is not
  restricted by the chart; a compromised MCP image could still reach arbitrary
  network destinations.

These are tracked as follow-up (wave-2) hardening work; this section will be
updated once they land.

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
