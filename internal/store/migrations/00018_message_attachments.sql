-- +goose Up
CREATE TABLE message_attachments (
    id                 BIGSERIAL PRIMARY KEY,
    message_id         BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    filename           TEXT NOT NULL,
    mime_type          TEXT NOT NULL,
    kind               VARCHAR(20) NOT NULL CHECK (kind IN ('image', 'document')),
    size_bytes         BIGINT NOT NULL CHECK (size_bytes >= 0),
    raw_bytes          BYTEA NOT NULL,
    CHECK (size_bytes = octet_length(raw_bytes)),
    extracted_markdown TEXT NOT NULL DEFAULT '',
    image_width        INTEGER CHECK (image_width > 0),
    image_height       INTEGER CHECK (image_height > 0),
    ordinal            INTEGER NOT NULL CHECK (ordinal >= 0),
    UNIQUE (message_id, ordinal)
);

CREATE TABLE message_document_references (
    id                BIGSERIAL PRIMARY KEY,
    message_id        BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    document_id       BIGINT REFERENCES documents(id) ON DELETE SET NULL,
    filename_snapshot TEXT NOT NULL,
    scope_snapshot    VARCHAR(20) NOT NULL CHECK (scope_snapshot IN ('private', 'public')),
    ordinal           INTEGER NOT NULL CHECK (ordinal >= 0),
    UNIQUE (message_id, ordinal)
);

CREATE UNIQUE INDEX idx_message_document_references_live_document
    ON message_document_references(message_id, document_id)
    WHERE document_id IS NOT NULL;
CREATE INDEX idx_message_document_references_document_id
    ON message_document_references(document_id)
    WHERE document_id IS NOT NULL;

-- +goose Down
DROP TABLE message_document_references;
DROP TABLE message_attachments;
