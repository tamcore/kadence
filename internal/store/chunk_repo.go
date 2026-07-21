package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/tamcore/kadence/internal/model"
)

// ChunkRepository accesses the chunks (embeddings) table.
type ChunkRepository struct {
	pool           *pgxpool.Pool
	embeddingModel string
}

// NewChunkRepository returns a ChunkRepository that stamps and filters
// chunks by embeddingModel, so a future embedding-model change can re-index
// instead of wiping.
func NewChunkRepository(pool *pgxpool.Pool, embeddingModel string) *ChunkRepository {
	return &ChunkRepository{pool: pool, embeddingModel: embeddingModel}
}

// Insert stores a chunk with its embedding, tagged with the repository's
// embedding model.
func (r *ChunkRepository) Insert(ctx context.Context, c model.Chunk, embedding []float32) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO chunks (user_id, conversation_id, document_id, scope, source_kind, source_id, content, embedding, embedding_model)
		 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9)`,
		c.UserID, c.ConversationID, c.DocumentID, c.Scope, c.SourceKind, c.SourceID, c.Content,
		pgvector.NewVector(embedding), r.embeddingModel)
	if err != nil {
		return fmt.Errorf("insert chunk: %w", err)
	}
	return nil
}

// SearchTopK returns the k chunks nearest to the query embedding (cosine),
// restricted to the user's own chunks plus any public chunks, and further
// restricted to chunks tagged with the repository's embedding model.
func (r *ChunkRepository) SearchTopK(ctx context.Context, userID int64, embedding []float32, k int) ([]model.Chunk, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, conversation_id::text, document_id, scope, source_kind, source_id, content, created_at
		 FROM chunks
		 WHERE (user_id = $1 OR scope = 'public') AND embedding_model = $4
		 ORDER BY embedding <=> $2
		 LIMIT $3`,
		userID, pgvector.NewVector(embedding), k, r.embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	defer rows.Close()
	var out []model.Chunk
	for rows.Next() {
		var c model.Chunk
		if err := rows.Scan(&c.ID, &c.UserID, &c.ConversationID, &c.DocumentID, &c.Scope, &c.SourceKind, &c.SourceID, &c.Content, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChunkRef is a lightweight projection of a chunk used by the context
// explorer, omitting the embedding and other fields not needed there.
type ChunkRef struct {
	Content    string
	SourceKind string
	DocumentID *int64
}

// maxOverviewChunks caps how many chunks ListContentForUser scans, so the
// context overview stays bounded even for large corpora.
const maxOverviewChunks = 5000

// ListContentForUser returns the content of a user's own chunks plus any
// public chunks, newest first, capped at maxOverviewChunks.
func (r *ChunkRepository) ListContentForUser(ctx context.Context, userID int64) ([]ChunkRef, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT content, source_kind, document_id FROM chunks
		 WHERE user_id = $1 OR scope = 'public'
		 ORDER BY created_at DESC LIMIT $2`, userID, maxOverviewChunks)
	if err != nil {
		return nil, fmt.Errorf("list chunks for user: %w", err)
	}
	defer rows.Close()
	return scanChunkRefRows(rows)
}

// SearchContentForUser returns chunks (the user's own plus any public) whose
// content contains term (case-insensitive), newest first, capped at limit.
func (r *ChunkRepository) SearchContentForUser(ctx context.Context, userID int64, term string, limit int) ([]ChunkRef, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT content, source_kind, document_id FROM chunks
		 WHERE (user_id = $1 OR scope = 'public') AND content ILIKE '%' || $2 || '%'
		 ORDER BY created_at DESC LIMIT $3`, userID, term, limit)
	if err != nil {
		return nil, fmt.Errorf("search chunks for user: %w", err)
	}
	defer rows.Close()
	return scanChunkRefRows(rows)
}

// scanChunkRefRows scans rows selected as (content, source_kind, document_id)
// into ChunkRef values.
func scanChunkRefRows(rows pgx.Rows) ([]ChunkRef, error) {
	var out []ChunkRef
	for rows.Next() {
		var ref ChunkRef
		if err := rows.Scan(&ref.Content, &ref.SourceKind, &ref.DocumentID); err != nil {
			return nil, fmt.Errorf("scan chunk ref: %w", err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
