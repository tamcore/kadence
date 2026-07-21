-- +goose Up
CREATE TABLE user_mcp_servers (
    id             BIGSERIAL PRIMARY KEY,
    owner_user_id  BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name           VARCHAR(64) NOT NULL,
    url            TEXT        NOT NULL,
    transport      VARCHAR(20) NOT NULL DEFAULT 'streamable-http'
                   CHECK (transport IN ('streamable-http','sse')),
    auth_user      TEXT        NOT NULL DEFAULT '',
    auth_pass_enc  BYTEA       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_user_id, name)
);
CREATE INDEX idx_user_mcp_servers_owner ON user_mcp_servers(owner_user_id);

-- +goose Down
DROP TABLE user_mcp_servers;
