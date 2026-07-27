-- +goose Up
CREATE TABLE push_subscriptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint        TEXT NOT NULL,
    p256dh          TEXT NOT NULL,
    auth            TEXT NOT NULL,
    user_agent      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_success_at TIMESTAMPTZ,
    failure_count   INT NOT NULL DEFAULT 0,
    UNIQUE (user_id, endpoint)
);
CREATE INDEX idx_push_subscriptions_user ON push_subscriptions (user_id);

ALTER TABLE scheduled_task_runs
    ADD COLUMN push_dispatched_at  TIMESTAMPTZ,
    ADD COLUMN delivery_message_id BIGINT REFERENCES messages(id) ON DELETE SET NULL;

-- Efficient polling for undispatched deliveries.
CREATE INDEX idx_scheduled_runs_undispatched
    ON scheduled_task_runs (id)
    WHERE state = 'delivered' AND push_dispatched_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_scheduled_runs_undispatched;
ALTER TABLE scheduled_task_runs
    DROP COLUMN IF EXISTS delivery_message_id,
    DROP COLUMN IF EXISTS push_dispatched_at;
DROP TABLE IF EXISTS push_subscriptions;
