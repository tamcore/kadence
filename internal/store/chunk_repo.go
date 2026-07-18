package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/tamcore/kadence/internal/model"
)

// ChunkRepository accesses the chunks (embeddings) table.
type ChunkRepository struct{ pool *pgxpool.Pool }

// NewChunkRepository returns a ChunkRepository.
func NewChunkRepository(pool *pgxpool.Pool) *ChunkRepository {
	return &ChunkRepository{pool: pool}
}

// Insert stores a chunk with its embedding.
func (r *ChunkRepository) Insert(ctx context.Context, c model.Chunk, embedding []float32) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO chunks (user_id, conversation_id, document_id, scope, source_kind, source_id, content, embedding)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		c.UserID, c.ConversationID, c.DocumentID, c.Scope, c.SourceKind, c.SourceID, c.Content, pgvector.NewVector(embedding))
	if err != nil {
		return fmt.Errorf("insert chunk: %w", err)
	}
	return nil
}

// SearchTopK returns the k chunks nearest to the query embedding (cosine),
// restricted to the user's own chunks plus any public chunks.
func (r *ChunkRepository) SearchTopK(ctx context.Context, userID int64, embedding []float32, k int) ([]model.Chunk, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, conversation_id, document_id, scope, source_kind, source_id, content, created_at
		 FROM chunks
		 WHERE user_id = $1 OR scope = 'public'
		 ORDER BY embedding <=> $2
		 LIMIT $3`,
		userID, pgvector.NewVector(embedding), k)
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
