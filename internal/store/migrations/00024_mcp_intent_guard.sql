-- +goose Up
ALTER TABLE mcp_call_audit
    ADD COLUMN intent TEXT NOT NULL DEFAULT '',
    ADD COLUMN guard_verdict TEXT NOT NULL DEFAULT 'not_evaluated'
        CHECK (guard_verdict IN ('not_evaluated', 'allowed', 'denied', 'error')),
    ADD COLUMN guard_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_call_audit DROP CONSTRAINT mcp_call_audit_status_check;
ALTER TABLE mcp_call_audit ADD CONSTRAINT mcp_call_audit_status_check
    CHECK (status IN ('running', 'succeeded', 'failed', 'blocked'));
CREATE INDEX idx_mcp_call_audit_guard_verdict_started
    ON mcp_call_audit (guard_verdict, started_at DESC, id DESC);

-- +goose Down
DROP INDEX idx_mcp_call_audit_guard_verdict_started;
ALTER TABLE mcp_call_audit DROP CONSTRAINT mcp_call_audit_status_check;
UPDATE mcp_call_audit SET status = 'failed' WHERE status = 'blocked';
ALTER TABLE mcp_call_audit ADD CONSTRAINT mcp_call_audit_status_check
    CHECK (status IN ('running', 'succeeded', 'failed'));
ALTER TABLE mcp_call_audit DROP COLUMN guard_reason, DROP COLUMN guard_verdict, DROP COLUMN intent;
