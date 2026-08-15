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

// ChunkStore persists chunks together with their embeddings in one batch.
// Satisfied by *store.ChunkRepository.
type ChunkStore interface {
	InsertBatch(ctx context.Context, chunks []model.Chunk, embeddings [][]float32) error
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

	res, err := extractor.Extract(ctx, data, mime)
	if err != nil {
		return model.Document{}, fmt.Errorf("extract %s: %w", filename, err)
	}

	// A PDF may hide its tables in page rasters, so it is queued for the
	// page-image pass and keeps its bytes until that finishes. Everything else
	// is already fully extracted.
	status := model.ExtractionStatusNotNeeded
	var rawBytes []byte
	if mime == pdfMimeType {
		status = model.ExtractionStatusPending
		rawBytes = data
	}
	doc, err := s.docs.Create(ctx, model.Document{
		OwnerUserID:       ownerUserID,
		Scope:             scope,
		Filename:          filename,
		Mime:              mime,
		SourceType:        res.SourceType,
		ExtractedMarkdown: res.Markdown,
		ExtractionStatus:  status,
		RawBytes:          rawBytes,
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

	chunks := make([]model.Chunk, len(pieces))
	for i, piece := range pieces {
		chunks[i] = model.Chunk{
			UserID:     ownerUserID,
			DocumentID: &doc.ID,
			Scope:      scope,
			SourceKind: model.ChunkSourceDocument,
			SourceID:   &doc.ID,
			Content:    piece,
		}
	}
	if err := s.chunks.InsertBatch(ctx, chunks, vecs); err != nil {
		return model.Document{}, fmt.Errorf("insert chunks for document %d: %w", doc.ID, err)
	}

	return doc, nil
}
