-- +goose Up
ALTER TABLE users ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';

ALTER TABLE conversations ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat'
    CHECK (kind IN ('chat', 'scheduled'));

CREATE TABLE scheduled_tasks (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id      UUID NOT NULL REFERENCES conversations(id) ON DELETE RESTRICT,
    version              INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    name                 TEXT NOT NULL DEFAULT '',
    kind                 TEXT NOT NULL CHECK (kind IN ('reminder', 'data', 'monitoring')),
    state                TEXT NOT NULL CHECK (state IN ('draft', 'active', 'paused', 'completed', 'failed', 'deleted')),
    compiled_prompt      TEXT NOT NULL DEFAULT '',
    one_off_at           TIMESTAMPTZ,
    dtstart              TIMESTAMPTZ,
    rrule                TEXT,
    timezone             TEXT NOT NULL DEFAULT 'UTC',
    execution_mode       TEXT NOT NULL DEFAULT '',
    authorized_tools     JSONB NOT NULL DEFAULT '[]'::jsonb,
    monitoring_state     JSONB NOT NULL DEFAULT '{}'::jsonb,
    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    next_run_at          TIMESTAMPTZ,
    last_run_at          TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ
);

CREATE TABLE scheduled_task_runs (
    id             BIGSERIAL PRIMARY KEY,
    task_id        UUID NOT NULL REFERENCES scheduled_tasks(id) ON DELETE RESTRICT,
    occurrence_key TEXT NOT NULL,
    scheduled_for  TIMESTAMPTZ NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('pending', 'running', 'no_change', 'delivered', 'completed', 'failed')),
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    result         TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    unread         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (task_id, occurrence_key)
);

CREATE INDEX idx_scheduled_tasks_owner_state ON scheduled_tasks(user_id, state)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_scheduled_tasks_due ON scheduled_tasks(next_run_at)
    WHERE state = 'active' AND deleted_at IS NULL;
CREATE INDEX idx_scheduled_task_runs_task_created ON scheduled_task_runs(task_id, created_at DESC);
CREATE INDEX idx_scheduled_task_runs_unread ON scheduled_task_runs(task_id)
    WHERE unread;

-- +goose Down
DROP TABLE scheduled_task_runs;
DROP TABLE scheduled_tasks;
ALTER TABLE conversations DROP COLUMN kind;
ALTER TABLE users DROP COLUMN timezone;
