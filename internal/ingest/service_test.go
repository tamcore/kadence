package ingest_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

type fakeExtractor struct{}

func (fakeExtractor) CanHandle(mime string) bool { return mime == "application/pdf" }
func (fakeExtractor) Extract(_ context.Context, _ []byte, _ string) (ingest.Result, error) {
	return ingest.Result{Markdown: "para one here.\n\npara two here.", SourceType: model.DocSourcePDF}, nil
}

type fakeEmbedder struct{ n int }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.n = len(texts)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

type fakeDocs struct {
	created      model.Document
	queuedID     int64
	queuedBytes  []byte
	queuedCalled bool
}

func (f *fakeDocs) MarkExtractionPending(_ context.Context, id int64, rawBytes []byte) error {
	f.queuedCalled = true
	f.queuedID = id
	f.queuedBytes = rawBytes
	return nil
}

func (f *fakeDocs) Create(_ context.Context, d model.Document) (model.Document, error) {
	d.ID = 42
	f.created = d
	return d, nil
}

type fakeChunks struct{ inserted []model.Chunk }

func (f *fakeChunks) InsertBatch(_ context.Context, chunks []model.Chunk, embeddings [][]float32) error {
	if len(chunks) != len(embeddings) {
		return fmt.Errorf("fakeChunks.InsertBatch: got %d chunks for %d embeddings", len(chunks), len(embeddings))
	}
	f.inserted = append(f.inserted, chunks...)
	return nil
}

func TestIngestPrivatePDF(t *testing.T) {
	emb := &fakeEmbedder{}
	fc := &fakeChunks{}
	uid := int64(7)
	svc := ingest.NewService([]ingest.Extractor{fakeExtractor{}}, emb, &fakeDocs{}, fc, 20, false)

	doc, err := svc.Ingest(context.Background(), &uid, model.ScopePrivate, "p.pdf", "application/pdf", []byte("%PDF..."))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if doc.ID != 42 || doc.Scope != model.ScopePrivate || doc.OwnerUserID == nil || *doc.OwnerUserID != 7 {
		t.Fatalf("doc wrong: %+v", doc)
	}
	if len(fc.inserted) == 0 || emb.n != len(fc.inserted) {
		t.Fatalf("expected one chunk per embedding: chunks=%d embeds=%d", len(fc.inserted), emb.n)
	}
	for _, c := range fc.inserted {
		if c.SourceKind != model.ChunkSourceDocument || c.DocumentID == nil || *c.DocumentID != 42 {
			t.Fatalf("chunk not linked to document: %+v", c)
		}
		if c.UserID == nil || *c.UserID != 7 || c.Scope != model.ScopePrivate {
			t.Fatalf("private chunk owner/scope wrong: %+v", c)
		}
	}
}

func TestIngestPublicPDFOwnerless(t *testing.T) {
	fc := &fakeChunks{}
	svc := ingest.NewService([]ingest.Extractor{fakeExtractor{}}, &fakeEmbedder{}, &fakeDocs{}, fc, 20, false)
	doc, err := svc.Ingest(context.Background(), nil, model.ScopePublic, "pub.pdf", "application/pdf", []byte("%PDF..."))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if doc.OwnerUserID != nil || doc.Scope != model.ScopePublic {
		t.Fatalf("public doc must be ownerless: %+v", doc)
	}
	for _, c := range fc.inserted {
		if c.UserID != nil || c.Scope != model.ScopePublic {
			t.Fatalf("public chunk must be ownerless: %+v", c)
		}
	}
}

func TestIngestUnsupportedType(t *testing.T) {
	svc := ingest.NewService([]ingest.Extractor{fakeExtractor{}}, &fakeEmbedder{}, &fakeDocs{}, &fakeChunks{}, 20, false)
	if _, err := svc.Ingest(context.Background(), nil, model.ScopePublic, "x.png", "image/png", []byte("x")); err == nil {
		t.Fatalf("expected unsupported-type error")
	}
}

func TestIngestQueuesPDFOnlyAfterChunksLand(t *testing.T) {
	// Arrange
	docs := &fakeDocs{}
	chunks := &fakeChunks{}
	uid := int64(7)
	svc := ingest.NewService([]ingest.Extractor{fakeExtractor{}}, &fakeEmbedder{}, docs, chunks, 20, true)

	// Act
	doc, err := svc.Ingest(
		context.Background(), &uid, model.ScopePrivate, "p.pdf", "application/pdf", []byte("%PDF..."),
	)

	// Assert
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !docs.queuedCalled {
		t.Fatal("PDF was not queued for the page-image pass")
	}
	if docs.queuedID != doc.ID {
		t.Errorf("queued document %d, want %d", docs.queuedID, doc.ID)
	}
	if len(chunks.inserted) == 0 {
		t.Error("chunks must be inserted before the document is queued")
	}
	if doc.ExtractionStatus != model.ExtractionStatusPending {
		t.Errorf("ExtractionStatus = %q, want %q", doc.ExtractionStatus, model.ExtractionStatusPending)
	}
}

func TestIngestDoesNotQueueWhenPageImagesDisabled(t *testing.T) {
	// Arrange: with no worker running, queuing would retain bytes forever.
	docs := &fakeDocs{}
	uid := int64(7)
	svc := ingest.NewService([]ingest.Extractor{fakeExtractor{}}, &fakeEmbedder{}, docs, &fakeChunks{}, 20, false)

	// Act
	if _, err := svc.Ingest(
		context.Background(), &uid, model.ScopePrivate, "p.pdf", "application/pdf", []byte("%PDF..."),
	); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Assert
	if docs.queuedCalled {
		t.Fatal("PDF was queued even though page images are disabled")
	}
}

type textExtractor struct{}

func (textExtractor) CanHandle(mime string) bool { return mime == "text/plain" }
func (textExtractor) Extract(_ context.Context, _ []byte, _ string) (ingest.Result, error) {
	return ingest.Result{Markdown: "plain text here.", SourceType: model.DocSourceText}, nil
}

func TestIngestDoesNotQueueNonPDF(t *testing.T) {
	// Arrange
	docs := &fakeDocs{}
	uid := int64(7)
	svc := ingest.NewService([]ingest.Extractor{textExtractor{}}, &fakeEmbedder{}, docs, &fakeChunks{}, 20, true)

	// Act
	if _, err := svc.Ingest(
		context.Background(), &uid, model.ScopePrivate, "n.txt", "text/plain", []byte("hello"),
	); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Assert
	if docs.queuedCalled {
		t.Fatal("non-PDF was queued for the page-image pass")
	}
}
