package ingest

import (
	"context"
	"fmt"

	"github.com/tamcore/kadence/internal/embed"
	"github.com/tamcore/kadence/internal/model"
)

// DocumentStore persists uploaded documents.
type DocumentStore interface {
	Create(ctx context.Context, d model.Document) (model.Document, error)
}

// ChunkStore persists a chunk together with its embedding. Satisfied by
// *store.ChunkRepository.
type ChunkStore interface {
	Insert(ctx context.Context, c model.Chunk, embedding []float32) error
}

// Service orchestrates the document ingestion pipeline: extract → chunk →
// embed → persist.
type Service struct {
	extractors []Extractor
	embedder   embed.Embedder
	docs       DocumentStore
	chunks     ChunkStore
	chunkChars int
}

// NewService builds an ingest Service.
func NewService(extractors []Extractor, e embed.Embedder, docs DocumentStore, chunks ChunkStore, chunkChars int) *Service {
	return &Service{
		extractors: extractors,
		embedder:   e,
		docs:       docs,
		chunks:     chunks,
		chunkChars: chunkChars,
	}
}

// Ingest extracts text from data, persists the document, then chunks,
// embeds, and persists each chunk. Errors are returned (not swallowed) so
// the uploading user can be told ingestion failed.
func (s *Service) Ingest(ctx context.Context, ownerUserID *int64, scope, filename, mime string, data []byte) (model.Document, error) {
	extractor, err := Select(s.extractors, mime)
	if err != nil {
		return model.Document{}, err
	}

	res, err := extractor.Extract(ctx, data)
	if err != nil {
		return model.Document{}, fmt.Errorf("extract %s: %w", filename, err)
	}

	doc, err := s.docs.Create(ctx, model.Document{
		OwnerUserID:       ownerUserID,
		Scope:             scope,
		Filename:          filename,
		Mime:              mime,
		SourceType:        res.SourceType,
		ExtractedMarkdown: res.Markdown,
	})
	if err != nil {
		return model.Document{}, fmt.Errorf("create document %s: %w", filename, err)
	}

	pieces := ChunkText(res.Markdown, s.chunkChars)
	if len(pieces) == 0 {
		return doc, nil
	}

	vecs, err := s.embedder.Embed(ctx, pieces)
	if err != nil {
		return model.Document{}, fmt.Errorf("embed document %d: %w", doc.ID, err)
	}
	if len(vecs) != len(pieces) {
		return model.Document{}, fmt.Errorf("embed document %d: got %d vectors for %d chunks", doc.ID, len(vecs), len(pieces))
	}

	for i, piece := range pieces {
		chunk := model.Chunk{
			UserID:     ownerUserID,
			DocumentID: &doc.ID,
			Scope:      scope,
			SourceKind: model.ChunkSourceDocument,
			SourceID:   &doc.ID,
			Content:    piece,
		}
		if err := s.chunks.Insert(ctx, chunk, vecs[i]); err != nil {
			return model.Document{}, fmt.Errorf("insert chunk %d for document %d: %w", i, doc.ID, err)
		}
	}

	return doc, nil
}
