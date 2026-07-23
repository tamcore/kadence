-- +goose Up
ALTER TABLE users ADD COLUMN location VARCHAR(120);
ALTER TABLE users ADD COLUMN about_me VARCHAR(1000);

-- +goose Down
ALTER TABLE users DROP COLUMN about_me;
ALTER TABLE users DROP COLUMN location;
