-- +goose Up
ALTER TABLE user_mcp_servers
    ADD COLUMN alias VARCHAR(32),
    ADD COLUMN hint  VARCHAR(300);

-- +goose Down
ALTER TABLE user_mcp_servers
    DROP COLUMN alias,
    DROP COLUMN hint;
