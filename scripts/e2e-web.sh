#!/usr/bin/env bash
# Boots the e2e LLM/embed stub and the built Kadence binary, waits for the
# app to become healthy, runs the Playwright browser e2e suite against it,
# and tears both processes down on exit (success or failure).
#
# Requires: bin/kadence and bin/e2e-stub already built (by the caller/CI,
# e.g. `go build -tags prodfrontend -o bin/kadence ./cmd/server` and
# `go build -o bin/e2e-stub ./e2e/stub`), and a reachable Postgres given via
# KADENCE_DATABASE_URL.
set -euo pipefail

if [[ -z "${KADENCE_DATABASE_URL:-}" ]]; then
	echo "error: KADENCE_DATABASE_URL is required" >&2
	exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

stub_bin="bin/e2e-stub"
app_bin="bin/kadence"

for bin in "$stub_bin" "$app_bin"; do
	if [[ ! -x "$bin" ]]; then
		echo "error: $bin not found or not executable (build it before running this script)" >&2
		exit 1
	fi
done

app_addr="127.0.0.1:8080"
# KADENCE_ENV=dev is required: prod mode enforces Secure cookies + strict CSRF
# origin checks that a plain-http localhost browser session cannot satisfy.
admin_password="${E2E_ADMIN_PASSWORD:?E2E_ADMIN_PASSWORD is required (use a clearly-test value, e.g. e2e-admin-pw)}"

# Test-only encryption key + host allowlist so e2e/mcp.spec.ts can exercise
# the user-defined MCP server CRUD flow (KADENCE_USER_MCP_ALLOWED_HOSTS gates
# it off by default). The allowlisted host never needs to actually
# resolve/respond — creation only validates the URL, and the health poller
# is allowed to report the server as unhealthy/checking.
user_mcp_key="$(openssl rand -base64 32)"

# The e2e suite logs in ~14 times across parallel Playwright workers that all
# share the same client address (127.0.0.1), and the default auth rate limit
# (10/min) is scoped per-client-address — not per-user — so a real limit would
# make the suite flaky under parallel workers. Disable both limits for this
# harness only; production defaults are untouched.

STUB_ADDR=":9099" "$stub_bin" &
stub_pid=$!

KADENCE_ENV=dev \
	KADENCE_DATABASE_URL="$KADENCE_DATABASE_URL" \
	KADENCE_LLM_BASE_URL="http://localhost:9099/v1" \
	KADENCE_LLM_API_KEY="stub" \
	KADENCE_LLM_MODEL="stub" \
	KADENCE_EMBED_BASE_URL="http://localhost:9099/v1" \
	KADENCE_EMBED_API_KEY="stub" \
	KADENCE_EMBED_MODEL="stub" \
	KADENCE_ADMIN_USERNAME="admin" \
	KADENCE_ADMIN_EMAIL="admin@example.com" \
	KADENCE_ADMIN_PASSWORD="$admin_password" \
	KADENCE_ENCRYPTION_KEY="$user_mcp_key" \
	KADENCE_USER_MCP_ALLOWED_HOSTS="*.e2e.test" \
	KADENCE_RATE_LIMIT_AUTH=0 \
	KADENCE_RATE_LIMIT_GLOBAL=0 \
	"$app_bin" &
app_pid=$!

cleanup() {
	local status=$?
	echo "tearing down e2e-web: stub(${stub_pid}) app(${app_pid})" >&2
	kill "$stub_pid" "$app_pid" >/dev/null 2>&1 || true
	wait "$stub_pid" "$app_pid" >/dev/null 2>&1 || true
	exit "$status"
}
trap cleanup EXIT

echo "waiting for app to become healthy at http://${app_addr}/api/healthz ..." >&2
for _ in $(seq 1 60); do
	if curl -fsS "http://${app_addr}/api/healthz" >/dev/null 2>&1; then
		echo "app is healthy" >&2
		break
	fi
	sleep 1
done

if ! curl -fsS "http://${app_addr}/api/healthz" >/dev/null 2>&1; then
	echo "error: app did not become healthy in time" >&2
	exit 1
fi

E2E_ADMIN_USERNAME="admin" E2E_ADMIN_PASSWORD="$admin_password" npm --prefix web run e2e
