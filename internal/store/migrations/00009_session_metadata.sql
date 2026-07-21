-- +goose Up
ALTER TABLE sessions ADD COLUMN public_id    UUID        NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE sessions ADD COLUMN user_agent   TEXT        NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN ip           TEXT        NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE UNIQUE INDEX idx_sessions_public_id ON sessions(public_id);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);

-- +goose Down
DROP INDEX idx_sessions_user_id;
DROP INDEX idx_sessions_public_id;
ALTER TABLE sessions DROP COLUMN last_seen_at;
ALTER TABLE sessions DROP COLUMN ip;
ALTER TABLE sessions DROP COLUMN user_agent;
ALTER TABLE sessions DROP COLUMN public_id;
