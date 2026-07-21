-- +goose Up
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN unit_system  TEXT NOT NULL DEFAULT 'metric'
    CHECK (unit_system IN ('metric','imperial'));

-- +goose Down
ALTER TABLE users DROP COLUMN unit_system;
ALTER TABLE users DROP COLUMN display_name;
