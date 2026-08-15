package ingest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tamcore/kadence/internal/embed"
	"github.com/tamcore/kadence/internal/model"
)

// DocumentStore persists uploaded documents.
type DocumentStore interface {
	Create(ctx context.Context, d model.Document) (model.Document, error)
	// MarkExtractionPending queues a document for the page-image pass and
	// stores the bytes that pass needs. Called only after the document's
	// initial chunks exist, so the worker cannot race that first indexing.
	MarkExtractionPending(ctx context.Context, id int64, rawBytes []byte) error
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
	// pageImagesEnabled gates queuing PDFs for the page-image pass. With no
	// worker running, queuing would retain every upload's bytes forever.
	pageImagesEnabled bool
}

// NewService builds an ingest Service. pageImagesEnabled must reflect whether
// the pdfvision worker is actually running.
func NewService(
	extractors []Extractor, e embed.Embedder, docs DocumentStore, chunks ChunkStore,
	chunkChars int, pageImagesEnabled bool,
) *Service {
	return &Service{
		extractors:        extractors,
		embedder:          e,
		docs:              docs,
		chunks:            chunks,
		chunkChars:        chunkChars,
		pageImagesEnabled: pageImagesEnabled,
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

	doc, err := s.docs.Create(ctx, model.Document{
		OwnerUserID:       ownerUserID,
		Scope:             scope,
		Filename:          filename,
		Mime:              mime,
		SourceType:        res.SourceType,
		ExtractedMarkdown: res.Markdown,
		ExtractionStatus:  model.ExtractionStatusNotNeeded,
	})
	if err != nil {
		return model.Document{}, fmt.Errorf("create document %s: %w", filename, err)
	}

	pieces := ChunkText(res.Markdown, s.chunkChars)
	if len(pieces) == 0 {
		return s.queuePageImagePass(ctx, doc, mime, data), nil
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

	return s.queuePageImagePass(ctx, doc, mime, data), nil
}

// queuePageImagePass marks a PDF for the page-image pass once its initial
// chunks exist, so the worker cannot replace chunks that ingestion is still
// writing. Queuing is skipped entirely when the feature is off: with no worker
// running, the stored bytes would never be released.
//
// A queuing failure is logged rather than returned: the upload itself
// succeeded, and the document is fully usable through its text layer.
func (s *Service) queuePageImagePass(
	ctx context.Context, doc model.Document, mime string, data []byte,
) model.Document {
	if !s.pageImagesEnabled || mime != pdfMimeType || len(data) == 0 {
		return doc
	}
	if err := s.docs.MarkExtractionPending(ctx, doc.ID, data); err != nil {
		slog.Error("queue pdf page-image pass",
			"document", doc.ID, "filename", doc.Filename, "err", err)
		return doc
	}
	doc.ExtractionStatus = model.ExtractionStatusPending
	return doc
}
