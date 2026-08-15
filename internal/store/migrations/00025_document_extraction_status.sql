-- +goose Up
-- Tables rendered as raster images carry no text layer, so a vision-capable
-- model has to read them from the page image. raw_bytes holds the original
-- upload only while that conversion is pending or running; FinishExtraction
-- clears it, so a PDF is never stored twice for long.
ALTER TABLE documents
    ADD COLUMN extraction_status TEXT NOT NULL DEFAULT 'not_needed'
        CHECK (extraction_status IN ('not_needed', 'pending', 'running', 'complete', 'failed')),
    ADD COLUMN raw_bytes BYTEA;

-- Partial: the worker only ever scans for in-flight rows, and this keeps the
-- index tiny once a corpus is mostly converted.
CREATE INDEX idx_documents_extraction_status
    ON documents(extraction_status)
    WHERE extraction_status IN ('pending', 'running');

-- +goose Down
DROP INDEX IF EXISTS idx_documents_extraction_status;
ALTER TABLE documents
    DROP COLUMN raw_bytes,
    DROP COLUMN extraction_status;
