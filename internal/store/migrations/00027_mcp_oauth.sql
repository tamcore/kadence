-- +goose Up
-- One row per (user, server) OAuth link. cas_version exists because a refresh
-- is a cross-system rotation: the writer that observed a version must be the
-- one that commits it, or two refreshers each consume a single-use refresh
-- token and the second revokes the whole family.
CREATE TABLE mcp_oauth_tokens (
    id                BIGSERIAL   PRIMARY KEY,
    user_id           BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    server_id         TEXT        NOT NULL,
    access_token      BYTEA       NOT NULL,
    access_expires_at TIMESTAMPTZ NOT NULL,
    refresh_token     BYTEA       NOT NULL,
    scope             TEXT        NOT NULL,
    resource          TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'linked'
        CHECK (status IN ('linked', 'reauth_required', 'disconnect_pending')),
    cas_version       BIGINT      NOT NULL DEFAULT 1,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, server_id)
);

-- A pending authorization. The state and the browser binding are stored as
-- digests rather than values: a leaked row must not let its holder complete
-- someone else's authorization.
CREATE TABLE mcp_oauth_transactions (
    state_hash    BYTEA       PRIMARY KEY,
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    server_id     TEXT        NOT NULL,
    pkce_verifier BYTEA       NOT NULL,
    redirect_uri  TEXT        NOT NULL,
    binding_hash  BYTEA       NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX mcp_oauth_transactions_expires_at_idx
    ON mcp_oauth_transactions (expires_at);

-- +goose Down
DROP TABLE mcp_oauth_transactions;
DROP TABLE mcp_oauth_tokens;
