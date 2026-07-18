-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE chunks (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id BIGINT      REFERENCES conversations(id) ON DELETE CASCADE,
    scope           VARCHAR(20) NOT NULL DEFAULT 'private' CHECK (scope IN ('private', 'public')),
    source_kind     VARCHAR(20) NOT NULL CHECK (source_kind IN ('message', 'document')),
    source_id       BIGINT,
    content         TEXT        NOT NULL,
    embedding       vector      NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_chunks_user_id ON chunks(user_id);
CREATE INDEX idx_chunks_conversation_id ON chunks(conversation_id);

-- +goose Down
DROP TABLE chunks;
