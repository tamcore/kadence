-- +goose Up
ALTER TABLE chunks ADD COLUMN embedding_model TEXT;

-- +goose Down
ALTER TABLE chunks DROP COLUMN embedding_model;
