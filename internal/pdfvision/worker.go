// Package pdfvision converts page-content images in ingested PDFs to markdown
// using a vision-capable model, so tables that carry no text layer become
// searchable RAG content instead of being silently dropped.
package pdfvision

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

// claimBatch is how many documents one pass claims at a time. Small, because
// each document costs several vision calls.
const claimBatch = 5

// pollInterval is how long the worker sleeps after draining the queue before
// looking again. Uploads arrive at human pace, so this need not be tight.
var pollInterval = 30 * time.Second

// staleClaimAfter is how long a claim may sit in running before another replica
// may take it back. Comfortably longer than a slow document so a healthy worker
// is never preempted mid-conversion.
const staleClaimAfter = 30 * time.Minute

// maxAttempts bounds retries of a failing document.
const maxAttempts = 3

// documentTimeout bounds one document's conversion. Without it a stalled
// provider would hold the single worker, and every queued document behind it,
// for the life of the process.
const documentTimeout = 15 * time.Minute

// Store is the document persistence the worker needs.
type Store interface {
	ClaimPendingExtraction(ctx context.Context, limit int) ([]model.Document, error)
	FinishExtraction(ctx context.Context, id int64, markdown, status string) error
	// RequeueStaleExtractions returns rows whose claim lease expired back to
	// pending, so work stranded by a crash is retried without stealing a
	// peer replica's in-flight claim.
	RequeueStaleExtractions(ctx context.Context, olderThan time.Duration) (int64, error)
	// RetryFailedExtractions returns failed rows that still hold their upload
	// bytes to pending, bounded by maxAttempts so a permanently unconvertible
	// document stops costing vision calls on every restart.
	RetryFailedExtractions(ctx context.Context, maxAttempts int) (int64, error)
}

// DescribeFunc converts one page image to markdown via a vision-capable model.
type DescribeFunc func(ctx context.Context, image []byte, mime string) (string, error)

// ReindexFunc re-chunks and re-embeds a document after its extracted text
// changed. Without it the new table content would be readable only through an
// explicit document reference, and invisible to RAG search.
type ReindexFunc func(ctx context.Context, doc model.Document, markdown string) error

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
	ctx context.Context, s Store, describe DescribeFunc, reindex ReindexFunc,
	opts ingest.PageImageOptions, log *slog.Logger,
) {
	// A crash or shutdown can leave rows in running, which the claim query
	// never selects. Recover them once at start so they are not stuck forever.
	if requeued, err := s.RequeueStaleExtractions(ctx, staleClaimAfter); err != nil {
		log.Error("pdfvision: requeue running failed", "err", err)
	} else if requeued > 0 {
		log.Info("pdfvision: requeued interrupted documents", "count", requeued)
	}
	// Earlier failures are usually transient (a provider outage or a rejected
	// request). Retry the ones that still hold their bytes.
	if retried, err := s.RetryFailedExtractions(ctx, maxAttempts); err != nil {
		log.Error("pdfvision: retry failed extractions failed", "err", err)
	} else if retried > 0 {
		log.Info("pdfvision: retrying previously failed documents", "count", retried)
	}

	for {
		if ctx.Err() != nil {
			return
		}
		docs, err := s.ClaimPendingExtraction(ctx, claimBatch)
		if err != nil {
			// Returning here would end the worker for the life of the
			// process: RunForever only restarts panics. A database blip must
			// not permanently disable conversion.
			log.Error("pdfvision: claim failed, retrying", "err", err)
			if !sleepCtx(ctx, pollInterval) {
				return
			}
			continue
		}
		if len(docs) == 0 {
			// Idle, not finished: RunForever treats a normal return as done
			// and never restarts, so exiting here would ignore every later
			// upload for the life of the process.
			if !sleepCtx(ctx, pollInterval) {
				return
			}
			continue
		}
		for _, doc := range docs {
			if ctx.Err() != nil {
				return
			}
			docCtx, cancel := context.WithTimeout(ctx, documentTimeout)
			markdown, status := describeDocument(docCtx, doc, describe, opts, log)
			// Re-chunk before recording success: a document reported complete
			// while its chunks still hold the old text would be silently
			// missing from RAG search, which is the failure this whole feature
			// exists to remove.
			if status == model.ExtractionStatusComplete && reindex != nil {
				if err := reindex(ctx, doc, markdown); err != nil {
					log.Error("pdfvision: reindex failed", "document", doc.ID, "err", err)
					status = model.ExtractionStatusFailed
				}
			}
			if err := s.FinishExtraction(ctx, doc.ID, markdown, status); err != nil {
				log.Error("pdfvision: finish failed", "document", doc.ID, "err", err)
			}
			cancel()
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

// sleepCtx waits d or until ctx is done, reporting whether the full wait
// elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
