-- +goose Up
-- Embeddings are now pinned to 1024 dims (KADENCE_EMBED_DIMENSIONS, MRL-truncated
-- client-side when the provider doesn't honor the "dimensions" request field).
-- Drop any pre-existing rows at a different width (e.g. legacy 4096-dim vectors)
-- before narrowing the column type; ALTER COLUMN TYPE would otherwise fail, and
-- these rows are stale anyway once the embedding model/width changes. The
-- online re-index worker (internal/reindex) repopulates them at 1024 dims on
-- next startup.
DELETE FROM chunks WHERE vector_dims(embedding) <> 1024;

ALTER TABLE chunks ALTER COLUMN embedding TYPE vector(1024);

-- pgvector HNSW supports up to 2000 dims for `vector` (4000 for `halfvec`), so
-- pinning to 1024 makes an ANN index possible. Defaults (m=16, ef_construction=64)
-- are fine at current corpus scale; revisit hnsw.iterative_scan if recall dips
-- once the model/user post-filter below removes a large fraction of ANN
-- candidates.
CREATE INDEX idx_chunks_embedding_hnsw ON chunks USING hnsw (embedding vector_cosine_ops);

-- SearchTopK filters WHERE embedding_model = $1 AND (user_id = $2 OR scope =
-- 'public'); pgvector applies this as a post-filter after the ANN scan, so an
-- index on the filter columns keeps that step cheap.
CREATE INDEX idx_chunks_model_user ON chunks(embedding_model, user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_chunks_model_user;
DROP INDEX IF EXISTS idx_chunks_embedding_hnsw;
ALTER TABLE chunks ALTER COLUMN embedding TYPE vector;
