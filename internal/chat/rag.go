package chat

import (
	"context"
	"fmt"

	"github.com/tamcore/kadence/internal/embed"
	"github.com/tamcore/kadence/internal/model"
)

// ChunkStore is the chunk persistence the RAG component needs.
type ChunkStore interface {
	Insert(ctx context.Context, c model.Chunk, embedding []float32) error
	SearchTopK(ctx context.Context, userID int64, embedding []float32, k int) ([]model.Chunk, error)
}

// RAG embeds and retrieves conversation memory.
type RAG struct {
	embedder embed.Embedder
	chunks   ChunkStore
	topK     int
}

// NewRAG constructs a RAG component.
func NewRAG(e embed.Embedder, chunks ChunkStore, topK int) *RAG {
	if topK <= 0 {
		topK = 5
	}
	return &RAG{embedder: e, chunks: chunks, topK: topK}
}

// Embed returns the embedding for a single text.
func (r *RAG) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := r.embedder.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embed: empty result")
	}
	return vecs[0], nil
}

// Retrieve embeds the query and returns the top-k chunk contents plus the query
// embedding (so the caller can reuse it to store the query as a chunk).
func (r *RAG) Retrieve(ctx context.Context, userID int64, query string) ([]string, []float32, error) {
	emb, err := r.Embed(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	found, err := r.chunks.SearchTopK(ctx, userID, emb, r.topK)
	if err != nil {
		return nil, emb, err
	}
	contents := make([]string, 0, len(found))
	for _, c := range found {
		contents = append(contents, c.Content)
	}
	return contents, emb, nil
}

// Store inserts a private message chunk with a precomputed embedding.
func (r *RAG) Store(ctx context.Context, userID, conversationID, sourceID int64, content string, embedding []float32) error {
	return r.chunks.Insert(ctx, model.Chunk{
		UserID:         &userID,
		ConversationID: &conversationID,
		Scope:          model.ScopePrivate,
		SourceKind:     model.ChunkSourceMessage,
		SourceID:       &sourceID,
		Content:        content,
	}, embedding)
}
