// Package pdfvision converts page-content images in ingested PDFs to markdown
// using a vision-capable model, so tables that carry no text layer become
// searchable RAG content instead of being silently dropped.
package pdfvision

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

// claimBatch is how many documents one pass claims at a time. Small, because
// each document costs several vision calls.
const claimBatch = 5

// Store is the document persistence the worker needs.
type Store interface {
	ClaimPendingExtraction(ctx context.Context, limit int) ([]model.Document, error)
	FinishExtraction(ctx context.Context, id int64, markdown, status string) error
}

// DescribeFunc converts one page image to markdown via a vision-capable model.
type DescribeFunc func(ctx context.Context, image []byte, mime string) (string, error)

// Run claims pending documents and converts their page images to markdown
// until none remain or ctx is cancelled.
//
// A document whose conversion fails keeps its original text layer and is marked
// failed rather than left running, so a stuck row never blocks the queue and
// never loses the text that was already extracted.
//
// Panic containment is the caller's responsibility: bg wraps this at the call
// site, matching the reindex worker.
func Run(
	ctx context.Context, s Store, describe DescribeFunc,
	opts ingest.PageImageOptions, log *slog.Logger,
) {
	for {
		if ctx.Err() != nil {
			return
		}
		docs, err := s.ClaimPendingExtraction(ctx, claimBatch)
		if err != nil {
			log.Error("pdfvision: claim failed", "err", err)
			return
		}
		if len(docs) == 0 {
			return
		}
		for _, doc := range docs {
			if ctx.Err() != nil {
				return
			}
			markdown, status := describeDocument(ctx, doc, describe, opts, log)
			if err := s.FinishExtraction(ctx, doc.ID, markdown, status); err != nil {
				log.Error("pdfvision: finish failed", "document", doc.ID, "err", err)
			}
		}
	}
}

// describeDocument converts one document's page images, returning the markdown
// to store and the status to record.
func describeDocument(
	ctx context.Context, doc model.Document, describe DescribeFunc,
	opts ingest.PageImageOptions, log *slog.Logger,
) (string, string) {
	images, err := ingest.ExtractPageImages(doc.RawBytes, opts)
	if err != nil {
		log.Error("pdfvision: extract failed", "document", doc.ID, "err", err)
		return doc.ExtractedMarkdown, model.ExtractionStatusFailed
	}
	if len(images) == 0 {
		return doc.ExtractedMarkdown, model.ExtractionStatusNotNeeded
	}

	sections := make([]string, 0, len(images)+1)
	if doc.ExtractedMarkdown != "" {
		sections = append(sections, doc.ExtractedMarkdown)
	}
	for _, image := range images {
		markdown, describeErr := describe(ctx, image.Data, image.MIME)
		if describeErr != nil {
			log.Error("pdfvision: describe failed",
				"document", doc.ID, "page", image.Page, "err", describeErr)
			return doc.ExtractedMarkdown, model.ExtractionStatusFailed
		}
		if strings.TrimSpace(markdown) == "" {
			continue
		}
		sections = append(sections,
			fmt.Sprintf("## Page %d (extracted from image)\n\n%s", image.Page, markdown))
	}
	return strings.Join(sections, "\n\n"), model.ExtractionStatusComplete
}
