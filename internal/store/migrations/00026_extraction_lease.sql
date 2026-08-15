-- +goose Up
-- A claim is a lease, not a permanent transition. Without the claim time a
-- restarting replica cannot tell its own interrupted work from a peer's
-- in-flight work, and would steal an active claim during a rolling update.
-- Attempts bound retries so a permanently malformed PDF stops costing vision
-- calls on every restart.
ALTER TABLE documents
    ADD COLUMN extraction_attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN extraction_claimed_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE documents
    DROP COLUMN extraction_claimed_at,
    DROP COLUMN extraction_attempts;
