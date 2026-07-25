-- +goose Up
CREATE TABLE mcp_call_audit (
    id                BIGSERIAL PRIMARY KEY,
    actor_user_id     BIGINT NOT NULL,
    actor_username    TEXT NOT NULL,
    conversation_id   UUID NOT NULL,
    source            TEXT NOT NULL CHECK (source IN ('chat', 'scheduled')),
    scheduled_task_id UUID,
    scheduled_run_id  BIGINT,
    model             TEXT NOT NULL,
    tool_call_id      TEXT NOT NULL,
    tool_name         TEXT NOT NULL,
    arguments         TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'running'
                      CHECK (status IN ('running', 'succeeded', 'failed')),
    result            TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    started_at        TIMESTAMPTZ NOT NULL,
    finished_at       TIMESTAMPTZ
);

CREATE INDEX idx_mcp_call_audit_started
    ON mcp_call_audit (started_at DESC, id DESC);
CREATE INDEX idx_mcp_call_audit_conversation_started
    ON mcp_call_audit (conversation_id, started_at DESC);
CREATE INDEX idx_mcp_call_audit_actor_started
    ON mcp_call_audit (actor_user_id, started_at DESC);

-- +goose Down
DROP TABLE mcp_call_audit;
