-- +goose Up
CREATE TABLE chat_scheduled_handoffs (
    id                         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_conversation_id     UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    source_user_message_id     BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    source_content_fingerprint BYTEA NOT NULL
                               CHECK (octet_length(source_content_fingerprint) = 32),
    assistant_message_id       BIGINT REFERENCES messages(id) ON DELETE SET NULL,
    scheduled_task_id          UUID UNIQUE REFERENCES scheduled_tasks(id) ON DELETE SET NULL,
    invocation_ordinal         SMALLINT NOT NULL
                               CHECK (invocation_ordinal BETWEEN 1 AND 5),
    artifact_state             TEXT NOT NULL
                               CHECK (artifact_state IN ('creating', 'ready', 'failed', 'dismissed')),
    error_code                 TEXT NOT NULL DEFAULT ''
                               CHECK (char_length(error_code) <= 64
                                  AND error_code ~ '^[a-z0-9_-]*$'),
    retryable                  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_user_message_id, source_content_fingerprint, invocation_ordinal),
    CHECK (
        artifact_state = 'creating'
        OR (artifact_state = 'dismissed' AND scheduled_task_id IS NULL)
        OR (artifact_state IN ('ready', 'failed') AND scheduled_task_id IS NOT NULL)
    )
);

CREATE INDEX idx_chat_scheduled_handoffs_source
    ON chat_scheduled_handoffs (source_conversation_id, source_user_message_id);
CREATE INDEX idx_chat_scheduled_handoffs_assistant
    ON chat_scheduled_handoffs (assistant_message_id)
    WHERE assistant_message_id IS NOT NULL;

-- +goose Down
DROP TABLE chat_scheduled_handoffs;
