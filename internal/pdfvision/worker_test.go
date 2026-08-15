package pdfvision

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../ingest/testdata/imagepage.pdf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

const textLayer = "prose only"

func testOptions() ingest.PageImageOptions {
	return ingest.PageImageOptions{MinCoverage: 0.12, MaxPages: 20}
}

type fakeStore struct {
	mu       sync.Mutex
	batches  [][]model.Document
	markdown map[int64]string
	statuses map[int64]string
	claims   int
	requeues int
	retries  int
	drained  context.CancelFunc
}

func newFakeStore(batches ...[]model.Document) *fakeStore {
	return &fakeStore{
		batches:  batches,
		markdown: map[int64]string{},
		statuses: map[int64]string{},
	}
}

func (f *fakeStore) ClaimPendingExtraction(_ context.Context, _ int) ([]model.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if len(f.batches) == 0 {
		// Run polls forever by design; end the loop once the queue drains.
		if f.drained != nil {
			f.drained()
		}
		return nil, nil
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	return batch, nil
}

func (f *fakeStore) RequeueRunningExtractions(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requeues++
	return 0, nil
}

func (f *fakeStore) RetryFailedExtractions(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retries++
	return 0, nil
}

func (f *fakeStore) FinishExtraction(_ context.Context, id int64, markdown, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markdown[id] = markdown
	f.statuses[id] = status
	return nil
}

// runDrained runs the worker until its queue empties, then lets the poll loop
// exit via context cancellation.
func runDrained(t *testing.T, store *fakeStore, describe DescribeFunc, reindex ReindexFunc, opts ingest.PageImageOptions) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.drained = cancel
	Run(ctx, store, describe, reindex, opts, slog.Default())
}

func describeConstant(text string) DescribeFunc {
	return func(context.Context, []byte, string) (string, error) { return text, nil }
}

func TestRunAppendsDescribedTablesAndMarksComplete(t *testing.T) {
	// Arrange
	store := newFakeStore([]model.Document{
		{ID: 7, ExtractedMarkdown: textLayer, RawBytes: fixtureBytes(t)},
	})

	// Act
	runDrained(t, store, describeConstant("| WEEK 5 | 16k easy |"), nil, testOptions())

	// Assert
	if store.statuses[7] != model.ExtractionStatusComplete {
		t.Errorf("status = %q, want %q", store.statuses[7], model.ExtractionStatusComplete)
	}
	if !strings.Contains(store.markdown[7], "WEEK 5") {
		t.Errorf("markdown = %q, want it to contain the described table", store.markdown[7])
	}
	if !strings.Contains(store.markdown[7], textLayer) {
		t.Error("worker dropped the original text layer")
	}
}

func TestRunMarksFailedAndPreservesTextWhenDescribeFails(t *testing.T) {
	// Arrange
	store := newFakeStore([]model.Document{
		{ID: 8, ExtractedMarkdown: textLayer, RawBytes: fixtureBytes(t)},
	})
	describe := func(context.Context, []byte, string) (string, error) {
		return "", errors.New("vision unavailable")
	}

	// Act
	runDrained(t, store, describe, nil, testOptions())

	// Assert
	if store.statuses[8] != model.ExtractionStatusFailed {
		t.Errorf("status = %q, want %q", store.statuses[8], model.ExtractionStatusFailed)
	}
	if store.markdown[8] != textLayer {
		t.Errorf("markdown = %q, want the original text layer preserved", store.markdown[8])
	}
}

func TestRunMarksNotNeededWhenNoPageImagesQualify(t *testing.T) {
	// Arrange
	store := newFakeStore([]model.Document{
		{ID: 9, ExtractedMarkdown: textLayer, RawBytes: fixtureBytes(t)},
	})
	describe := func(context.Context, []byte, string) (string, error) {
		t.Error("describe must not be called when no images qualify")
		return "", nil
	}

	// Act: a zero MaxPages makes extraction a no-op.
	runDrained(t, store, describe, nil, ingest.PageImageOptions{})

	// Assert
	if store.statuses[9] != model.ExtractionStatusNotNeeded {
		t.Errorf("status = %q, want %q", store.statuses[9], model.ExtractionStatusNotNeeded)
	}
}

func TestRunMarksFailedOnMalformedPDF(t *testing.T) {
	// Arrange
	store := newFakeStore([]model.Document{
		{ID: 10, ExtractedMarkdown: textLayer, RawBytes: []byte("not a pdf")},
	})

	// Act
	runDrained(t, store, describeConstant("table"), nil, testOptions())

	// Assert
	if store.statuses[10] != model.ExtractionStatusFailed {
		t.Errorf("status = %q, want %q", store.statuses[10], model.ExtractionStatusFailed)
	}
}

func TestRunDrainsUntilNoDocumentsRemain(t *testing.T) {
	// Arrange: two batches, then an empty claim ends the loop.
	store := newFakeStore(
		[]model.Document{{ID: 1, RawBytes: fixtureBytes(t)}},
		[]model.Document{{ID: 2, RawBytes: fixtureBytes(t)}},
	)

	// Act
	runDrained(t, store, describeConstant("table"), nil, testOptions())

	// Assert
	if len(store.statuses) != 2 {
		t.Fatalf("finished %d documents, want 2", len(store.statuses))
	}
	if store.claims != 3 {
		t.Errorf("claims = %d, want 3 (two batches plus the empty claim that stops the loop)", store.claims)
	}
}

func TestRunStopsOnCancelledContext(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := newFakeStore([]model.Document{{ID: 11, RawBytes: fixtureBytes(t)}})

	// Act
	Run(ctx, store, describeConstant("table"), nil, testOptions(), slog.Default())

	// Assert
	if len(store.statuses) != 0 {
		t.Fatalf("finished %d documents on a cancelled context, want 0", len(store.statuses))
	}
}

func TestRunReindexesBeforeReportingComplete(t *testing.T) {
	// Arrange
	store := newFakeStore([]model.Document{
		{ID: 12, ExtractedMarkdown: textLayer, RawBytes: fixtureBytes(t)},
	})
	var gotMarkdown string
	reindex := func(_ context.Context, _ model.Document, markdown string) error {
		gotMarkdown = markdown
		return nil
	}

	// Act
	runDrained(t, store, describeConstant("| WEEK 5 |"), reindex, testOptions())

	// Assert
	if store.statuses[12] != model.ExtractionStatusComplete {
		t.Errorf("status = %q, want %q", store.statuses[12], model.ExtractionStatusComplete)
	}
	if !strings.Contains(gotMarkdown, "WEEK 5") {
		t.Errorf("reindex received %q, want the converted markdown", gotMarkdown)
	}
}

func TestRunMarksFailedWhenReindexFails(t *testing.T) {
	// Arrange: chunks would be stale, so completion must not be reported.
	store := newFakeStore([]model.Document{
		{ID: 13, ExtractedMarkdown: textLayer, RawBytes: fixtureBytes(t)},
	})
	reindex := func(context.Context, model.Document, string) error {
		return errors.New("embed unavailable")
	}

	// Act
	runDrained(t, store, describeConstant("| WEEK 5 |"), reindex, testOptions())

	// Assert
	if store.statuses[13] != model.ExtractionStatusFailed {
		t.Errorf("status = %q, want %q", store.statuses[13], model.ExtractionStatusFailed)
	}
}
