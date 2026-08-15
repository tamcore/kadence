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

// InsertBatch stores multiple chunks together with their embeddings in a
// single round trip (a pgx pipelined batch), tagged with the repository's
// embedding model. Unlike calling Insert once per chunk, the whole batch runs
// in one implicit transaction: either every chunk is written or none is.
func (r *ChunkRepository) InsertBatch(ctx context.Context, chunks []model.Chunk, embeddings [][]float32) error {
	if len(chunks) != len(embeddings) {
		return fmt.Errorf("insert chunk batch: got %d chunks for %d embeddings", len(chunks), len(embeddings))
	}
	if len(chunks) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for i, c := range chunks {
		batch.Queue(
			`INSERT INTO chunks (user_id, conversation_id, document_id, scope, source_kind, source_id, content, embedding, embedding_model)
			 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9)`,
			c.UserID, c.ConversationID, c.DocumentID, c.Scope, c.SourceKind, c.SourceID, c.Content,
			pgvector.NewVector(embeddings[i]), r.embeddingModel)
	}
	br := r.pool.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	for range chunks {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("insert chunk batch: %w", err)
		}
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

// SearchTopKByVisibleDocuments returns up to kPerDocument nearest current-model
// chunks for each requested document in one query. Visibility is enforced by
// joining documents: public documents and userID's own private documents are
// eligible; invisible or missing document IDs contribute no results.
func (r *ChunkRepository) SearchTopKByVisibleDocuments(
	ctx context.Context,
	userID int64,
	documentIDs []int64,
	embedding []float32,
	kPerDocument int,
) (map[int64][]model.Chunk, error) {
	out := make(map[int64][]model.Chunk)
	if len(documentIDs) == 0 || kPerDocument <= 0 {
		return out, nil
	}
	queryVector := pgvector.NewVector(embedding)
	rows, err := r.pool.Query(ctx,
		`SELECT nearest.id, nearest.user_id, nearest.conversation_id::text,
		        nearest.document_id, nearest.scope, nearest.source_kind,
		        nearest.source_id, nearest.content, nearest.created_at
		   FROM unnest($1::bigint[]) WITH ORDINALITY AS requested(id, ordinal)
		   JOIN documents d
		     ON d.id = requested.id
		    AND (d.scope = $2 OR (d.scope = $3 AND d.owner_user_id = $4))
		   JOIN LATERAL (
		        SELECT c.*, c.embedding <=> $5 AS distance
		          FROM chunks c
		         WHERE c.document_id = d.id
		           AND c.source_kind = $6
		           AND c.embedding_model = $7
		         ORDER BY distance, c.id
		         LIMIT $8
		   ) AS nearest ON true
		  ORDER BY requested.ordinal, nearest.distance, nearest.id`,
		documentIDs, model.ScopePublic, model.ScopePrivate, userID,
		queryVector, model.ChunkSourceDocument, r.embeddingModel, kPerDocument,
	)
	if err != nil {
		return nil, fmt.Errorf("search visible document chunks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var chunk model.Chunk
		if err := rows.Scan(
			&chunk.ID, &chunk.UserID, &chunk.ConversationID, &chunk.DocumentID,
			&chunk.Scope, &chunk.SourceKind, &chunk.SourceID, &chunk.Content,
			&chunk.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan visible document chunk: %w", err)
		}
		if chunk.DocumentID != nil {
			out[*chunk.DocumentID] = append(out[*chunk.DocumentID], chunk)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search visible document chunks: %w", err)
	}
	return out, nil
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

// reembedBatchDefault is the number of stale chunks re-embedded per batch.
const reembedBatchDefault = 64

// AdoptUntagged stamps every chunk with a NULL embedding_model as the current
// model. Used once on feature introduction so pre-existing vectors (produced by
// the then-current model) are treated as current rather than re-embedded.
func (r *ChunkRepository) AdoptUntagged(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE chunks SET embedding_model = $1 WHERE embedding_model IS NULL`, r.embeddingModel)
	if err != nil {
		return 0, fmt.Errorf("adopt untagged chunks: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ReindexStatus reports how many chunks are stale (not on the current embedding
// model) and the total, across all users.
func (r *ChunkRepository) ReindexStatus(ctx context.Context) (stale, total int64, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE embedding_model IS DISTINCT FROM $1), count(*) FROM chunks`,
		r.embeddingModel).Scan(&stale, &total)
	if err != nil {
		return 0, 0, fmt.Errorf("reindex status: %w", err)
	}
	return stale, total, nil
}

// ReembedBatch re-embeds up to batch stale chunks in a single transaction,
// claiming them with FOR UPDATE SKIP LOCKED so concurrent workers (multiple
// replicas) never process the same row. Returns the number re-embedded; 0 means
// no stale chunks remain.
func (r *ChunkRepository) ReembedBatch(ctx context.Context, embed func(context.Context, []string) ([][]float32, error), batch int) (int, error) {
	if batch <= 0 {
		batch = reembedBatchDefault
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("reembed begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, content FROM chunks
		 WHERE embedding_model IS DISTINCT FROM $1
		 ORDER BY id
		 LIMIT $2
		 FOR UPDATE SKIP LOCKED`, r.embeddingModel, batch)
	if err != nil {
		return 0, fmt.Errorf("reembed claim: %w", err)
	}
	var ids []int64
	var contents []string
	for rows.Next() {
		var id int64
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			rows.Close()
			return 0, fmt.Errorf("reembed scan: %w", err)
		}
		ids = append(ids, id)
		contents = append(contents, content)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("reembed rows: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	vecs, err := embed(ctx, contents)
	if err != nil {
		return 0, fmt.Errorf("reembed embed: %w", err)
	}
	if len(vecs) != len(ids) {
		return 0, fmt.Errorf("reembed: got %d vectors for %d chunks", len(vecs), len(ids))
	}
	updateBatch := &pgx.Batch{}
	for i, id := range ids {
		updateBatch.Queue(
			`UPDATE chunks SET embedding = $1, embedding_model = $2 WHERE id = $3`,
			pgvector.NewVector(vecs[i]), r.embeddingModel, id)
	}
	br := tx.SendBatch(ctx, updateBatch)
	for range ids {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return 0, fmt.Errorf("reembed update: %w", err)
		}
	}
	if err := br.Close(); err != nil {
		return 0, fmt.Errorf("reembed update close: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("reembed commit: %w", err)
	}
	return len(ids), nil
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

// DeleteByDocument removes every chunk belonging to one document, so the
// document can be re-chunked after its extracted text changes.
func (r *ChunkRepository) DeleteByDocument(ctx context.Context, documentID int64) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM chunks WHERE document_id = $1`, documentID); err != nil {
		return fmt.Errorf("delete chunks for document %d: %w", documentID, err)
	}
	return nil
}
