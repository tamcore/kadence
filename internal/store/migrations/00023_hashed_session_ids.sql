-- +goose Up
-- Session ids were stored and looked up in plaintext: the raw cookie value
-- was the primary key. Any read access to this table (leaked backup,
-- misconfigured replica, SQLi elsewhere) yielded directly usable session
-- tokens for every live session. From here on, "id" stores sha256(raw
-- session id) hex-encoded (64 chars, fits the existing VARCHAR(64) column);
-- the raw token is never persisted, only held in the client's cookie.
--
-- Hashing is one-way, so existing rows cannot be migrated forward: their
-- plaintext ids cannot be turned into the hash of a value we no longer have.
-- This statement deletes every existing session, which logs out every user
-- on deploy. That is expected and accepted for this security fix.
DELETE FROM sessions;

-- +goose Down
-- No schema change was made (the id column was already VARCHAR(64), wide
-- enough for a hex sha256 digest), so there is nothing to structurally
-- revert. The deleted sessions from the Up migration cannot be restored.
