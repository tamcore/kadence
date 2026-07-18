-- +goose Up
CREATE TABLE documents (
    id                 BIGSERIAL PRIMARY KEY,
    owner_user_id      BIGINT      REFERENCES users(id) ON DELETE CASCADE,
    scope              VARCHAR(20) NOT NULL DEFAULT 'private' CHECK (scope IN ('private', 'public')),
    filename           TEXT        NOT NULL,
    mime               TEXT        NOT NULL,
    source_type        VARCHAR(20) NOT NULL CHECK (source_type IN ('pdf', 'image', 'text')),
    extracted_markdown TEXT        NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_documents_owner_user_id ON documents(owner_user_id);
CREATE INDEX idx_documents_scope ON documents(scope);

ALTER TABLE chunks ADD COLUMN document_id BIGINT REFERENCES documents(id) ON DELETE CASCADE;
ALTER TABLE chunks ALTER COLUMN user_id DROP NOT NULL;
CREATE INDEX idx_chunks_document_id ON chunks(document_id);

-- +goose Down
-- NOTE: SET NOT NULL fails if any ownerless public-document chunks (user_id IS
-- NULL) exist. Rolling back a populated corpus requires deleting those rows first
-- (they are dropped anyway when the documents table below is removed via cascade,
-- so run this Down only after clearing public document chunks, or on a clean schema).
ALTER TABLE chunks DROP COLUMN document_id;
ALTER TABLE chunks ALTER COLUMN user_id SET NOT NULL;
DROP TABLE documents;
