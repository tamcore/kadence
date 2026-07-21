-- +goose Up
ALTER TABLE users ADD COLUMN webauthn_user_handle UUID NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX idx_users_webauthn_handle ON users(webauthn_user_handle);

CREATE TABLE webauthn_credentials (
    id            BIGSERIAL   PRIMARY KEY,
    public_id     UUID        NOT NULL DEFAULT gen_random_uuid(),
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BYTEA       NOT NULL,
    public_key    BYTEA       NOT NULL,
    aaguid        BYTEA       NOT NULL DEFAULT '',
    sign_count    BIGINT      NOT NULL DEFAULT 0,
    transports    TEXT[]      NOT NULL DEFAULT '{}',
    name          TEXT        NOT NULL DEFAULT '',
    backup_eligible BOOLEAN   NOT NULL DEFAULT false,
    backup_state    BOOLEAN   NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_webauthn_credentials_public_id     ON webauthn_credentials(public_id);
CREATE UNIQUE INDEX idx_webauthn_credentials_credential_id ON webauthn_credentials(credential_id);
CREATE INDEX        idx_webauthn_credentials_user_id       ON webauthn_credentials(user_id);

-- +goose Down
DROP TABLE webauthn_credentials;
DROP INDEX idx_users_webauthn_handle;
ALTER TABLE users DROP COLUMN webauthn_user_handle;
