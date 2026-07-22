-- +goose Up
-- Embeddings are now pinned to 1024 dims (KADENCE_EMBED_DIMENSIONS, MRL-truncated
-- client-side when the provider doesn't honor the "dimensions" request field).
-- ALTER COLUMN TYPE below requires every row to already be exactly 1024 dims,
-- so pre-existing rows at a different width are handled here first:
--
--   - wider than 1024 (e.g. legacy 4096-dim vectors): converted in place by
--     taking the first 1024 dims and L2-renormalizing, i.e. the same MRL
--     truncate+renormalize the client applies to its own output (see
--     internal/embed.truncateAndRenormalize). This preserves the row instead
--     of discarding it.
--   - narrower than 1024: cannot be widened without re-embedding from the
--     source content, so these are deleted. This is expected to be rare
--     (only reachable if KADENCE_EMBED_DIMENSIONS was previously configured
--     below 1024) and is a real, permanent loss of that row's searchability:
--     the online re-index worker (internal/reindex) only re-embeds
--     surviving rows on an embedding-model mismatch, it does not repopulate
--     rows deleted here.
UPDATE chunks
SET embedding = l2_normalize(subvector(embedding, 1, 1024))::vector(1024)
WHERE vector_dims(embedding) > 1024;

DELETE FROM chunks WHERE vector_dims(embedding) < 1024;

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
