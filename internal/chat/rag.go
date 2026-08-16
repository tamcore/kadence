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
	SearchTopKByVisibleDocuments(
		ctx context.Context,
		userID int64,
		documentIDs []int64,
		embedding []float32,
		kPerDocument int,
	) (map[int64][]model.Chunk, error)
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

// TurnRetrieval is one query embedding reused for broad memory and selected
// document sections.
type TurnRetrieval struct {
	Broad      []string
	ByDocument map[int64][]string
	Embedding  []float32
}

// RetrieveTurn embeds query once, retrieves broad context plus per-document
// sections, and removes selected-document chunks from the broad results.
func (r *RAG) RetrieveTurn(
	ctx context.Context, userID int64, query string, documentIDs []int64,
) (TurnRetrieval, error) {
	embedding, err := r.Embed(ctx, query)
	if err != nil {
		return TurnRetrieval{}, err
	}
	result := TurnRetrieval{
		Embedding: embedding, ByDocument: make(map[int64][]string),
	}
	found, err := r.chunks.SearchTopK(ctx, userID, embedding, r.topK)
	if err != nil {
		return result, err
	}
	selected := make(map[int64]struct{}, len(documentIDs))
	for _, id := range documentIDs {
		selected[id] = struct{}{}
	}
	for _, chunk := range found {
		if chunk.DocumentID != nil {
			if _, excluded := selected[*chunk.DocumentID]; excluded {
				continue
			}
		}
		result.Broad = append(result.Broad, chunk.Content)
	}
	if len(documentIDs) == 0 {
		return result, nil
	}
	byDocument, err := r.chunks.SearchTopKByVisibleDocuments(
		ctx, userID, documentIDs, embedding, r.topK,
	)
	if err != nil {
		return result, err
	}
	for documentID, chunks := range byDocument {
		for _, chunk := range chunks {
			result.ByDocument[documentID] = append(
				result.ByDocument[documentID], chunk.Content,
			)
		}
	}
	return result, nil
}

// Store inserts a private message chunk with a precomputed embedding.
func (r *RAG) Store(ctx context.Context, userID int64, conversationID string, sourceID int64, content string, embedding []float32) error {
	return r.chunks.Insert(ctx, model.Chunk{
		UserID:         &userID,
		ConversationID: &conversationID,
		Scope:          model.ScopePrivate,
		SourceKind:     model.ChunkSourceMessage,
		SourceID:       &sourceID,
		Content:        content,
	}, embedding)
}
