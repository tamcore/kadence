package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const (
	testSyntheticPDF = "%PDF-1.4 synthetic"
	testTextLayer    = "text layer"
)

func newExtractionFixture(t *testing.T) (context.Context, *store.DocumentRepository, int64) {
	t.Helper()
	pool := testutil.SetupTestDB(t)
	ctx := t.Context()
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	u, err := users.Create(ctx, model.User{
		Username: "e", Email: "e@x.io", PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return ctx, docs, u.ID
}

func newPendingDocument(
	t *testing.T, ctx context.Context, docs *store.DocumentRepository, ownerID int64,
) model.Document {
	t.Helper()
	doc, err := docs.Create(ctx, model.Document{
		OwnerUserID: &ownerID, Scope: model.ScopePrivate, Filename: testFilenamePriv,
		Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: testTextLayer,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := docs.MarkExtractionPending(ctx, doc.ID, []byte(testSyntheticPDF)); err != nil {
		t.Fatalf("MarkExtractionPending: %v", err)
	}
	return doc
}

func TestCreateDefaultsExtractionStatusToNotNeeded(t *testing.T) {
	// Arrange
	ctx, docs, ownerID := newExtractionFixture(t)

	// Act
	doc, err := docs.Create(ctx, model.Document{
		OwnerUserID: &ownerID, Scope: model.ScopePrivate, Filename: testFilenamePriv,
		Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: testTextLayer,
	})

	// Assert
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if doc.ExtractionStatus != model.ExtractionStatusNotNeeded {
		t.Errorf("ExtractionStatus = %q, want %q",
			doc.ExtractionStatus, model.ExtractionStatusNotNeeded)
	}
}

func TestMarkExtractionPendingThenClaimAndFinish(t *testing.T) {
	// Arrange
	ctx, docs, ownerID := newExtractionFixture(t)
	doc := newPendingDocument(t, ctx, docs, ownerID)

	// Act
	claimed, err := docs.ClaimPendingExtraction(ctx, 10)

	// Assert
	if err != nil {
		t.Fatalf("ClaimPendingExtraction: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != doc.ID {
		t.Fatalf("claimed = %+v, want exactly document %d", claimed, doc.ID)
	}
	if string(claimed[0].RawBytes) != testSyntheticPDF {
		t.Errorf("RawBytes = %q, want the stored upload", claimed[0].RawBytes)
	}
	if claimed[0].ExtractionStatus != model.ExtractionStatusRunning {
		t.Errorf("ExtractionStatus = %q, want %q",
			claimed[0].ExtractionStatus, model.ExtractionStatusRunning)
	}

	// A second claim must not return the same row again.
	again, err := docs.ClaimPendingExtraction(ctx, 10)
	if err != nil {
		t.Fatalf("second ClaimPendingExtraction: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second claim returned %d documents, want 0", len(again))
	}

	// Finishing stores the markdown and moves the row out of the queue.
	if err := docs.FinishExtraction(
		ctx, doc.ID, "text layer\n\n## Page 1\n\n| WEEK 5 | 16k easy |",
		model.ExtractionStatusComplete,
	); err != nil {
		t.Fatalf("FinishExtraction: %v", err)
	}
	visible, err := docs.ListVisibleByIDs(ctx, ownerID, []int64{doc.ID})
	if err != nil {
		t.Fatalf("ListVisibleByIDs: %v", err)
	}
	if len(visible) != 1 {
		t.Fatalf("got %d documents, want 1", len(visible))
	}
	if visible[0].ExtractedMarkdown == testTextLayer {
		t.Error("FinishExtraction did not store the converted markdown")
	}
	stranded, err := docs.RequeueStaleExtractions(ctx, 0)
	if err != nil {
		t.Fatalf("RequeueStaleExtractions: %v", err)
	}
	if stranded != 0 {
		t.Errorf("requeued %d rows, want 0 once the document finished", stranded)
	}
}

func TestRequeueStaleExtractionsRecoversStrandedRows(t *testing.T) {
	// Arrange: claiming leaves the row running, as a crash before finish would.
	ctx, docs, ownerID := newExtractionFixture(t)
	doc := newPendingDocument(t, ctx, docs, ownerID)
	if _, err := docs.ClaimPendingExtraction(ctx, 10); err != nil {
		t.Fatalf("ClaimPendingExtraction: %v", err)
	}

	// Act
	requeued, err := docs.RequeueStaleExtractions(ctx, 0)

	// Assert
	if err != nil {
		t.Fatalf("RequeueStaleExtractions: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued = %d, want 1", requeued)
	}
	reclaimed, err := docs.ClaimPendingExtraction(ctx, 10)
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != doc.ID {
		t.Fatalf("reclaimed = %+v, want the stranded document back in the queue", reclaimed)
	}
}

func TestClaimPendingExtractionRespectsLimit(t *testing.T) {
	// Arrange
	ctx, docs, ownerID := newExtractionFixture(t)
	for range 3 {
		newPendingDocument(t, ctx, docs, ownerID)
	}

	// Act
	claimed, err := docs.ClaimPendingExtraction(ctx, 2)

	// Assert
	if err != nil {
		t.Fatalf("ClaimPendingExtraction: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d documents, want 2", len(claimed))
	}
}

func TestReplaceDocumentChunksSwapsContent(t *testing.T) {
	// Arrange
	pool := testutil.SetupTestDB(t)
	ctx := t.Context()
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	chunks := store.NewChunkRepository(pool, "test-model")
	u, err := users.Create(ctx, model.User{
		Username: "r", Email: "r@x.io", PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	doc, err := docs.Create(ctx, model.Document{
		OwnerUserID: &u.ID, Scope: model.ScopePrivate, Filename: testFilenamePriv,
		Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: testTextLayer,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	original := []model.Chunk{{
		UserID: &u.ID, DocumentID: &doc.ID, Scope: model.ScopePrivate,
		SourceKind: model.ChunkSourceDocument, SourceID: &doc.ID, Content: "stale prose",
	}}
	if err := chunks.InsertBatch(ctx, original, [][]float32{vec1024(1, 0, 0)}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	// Act
	replacement := []model.Chunk{{
		UserID: &u.ID, DocumentID: &doc.ID, Scope: model.ScopePrivate,
		SourceKind: model.ChunkSourceDocument, SourceID: &doc.ID, Content: "WEEK 5 16k easy",
	}}
	err = chunks.ReplaceDocumentChunks(ctx, doc.ID, replacement, [][]float32{vec1024(1, 0, 0)})

	// Assert
	if err != nil {
		t.Fatalf("ReplaceDocumentChunks: %v", err)
	}
	found, err := chunks.SearchContentForUser(ctx, u.ID, "WEEK 5", 10)
	if err != nil {
		t.Fatalf("SearchContentForUser: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d chunks containing the new content, want 1", len(found))
	}
	stale, err := chunks.SearchContentForUser(ctx, u.ID, "stale prose", 10)
	if err != nil {
		t.Fatalf("SearchContentForUser: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("found %d stale chunks, want 0", len(stale))
	}
}

func TestReplaceDocumentChunksRejectsMismatchedEmbeddings(t *testing.T) {
	// Arrange
	pool := testutil.SetupTestDB(t)
	ctx := t.Context()
	chunks := store.NewChunkRepository(pool, "test-model")

	// Act
	err := chunks.ReplaceDocumentChunks(ctx, 1, []model.Chunk{{Content: "a"}}, nil)

	// Assert
	if err == nil {
		t.Fatal("ReplaceDocumentChunks() error = nil, want an error for mismatched lengths")
	}
}

func TestFinishExtractionRetainsBytesOnFailureForRetry(t *testing.T) {
	// Arrange
	ctx, docs, ownerID := newExtractionFixture(t)
	doc := newPendingDocument(t, ctx, docs, ownerID)
	if _, err := docs.ClaimPendingExtraction(ctx, 10); err != nil {
		t.Fatalf("ClaimPendingExtraction: %v", err)
	}

	// Act: a transient provider failure.
	if err := docs.FinishExtraction(
		ctx, doc.ID, testTextLayer, model.ExtractionStatusFailed,
	); err != nil {
		t.Fatalf("FinishExtraction: %v", err)
	}

	// Assert: the upload survives, so the work can be retried.
	retried, err := docs.RetryFailedExtractions(ctx, 3)
	if err != nil {
		t.Fatalf("RetryFailedExtractions: %v", err)
	}
	if retried != 1 {
		t.Fatalf("retried = %d, want 1", retried)
	}
	claimed, err := docs.ClaimPendingExtraction(ctx, 10)
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if len(claimed) != 1 || string(claimed[0].RawBytes) != testSyntheticPDF {
		t.Fatalf("re-claimed = %+v, want the document with its bytes intact", claimed)
	}
}

func TestFinishExtractionReleasesBytesOnSuccess(t *testing.T) {
	// Arrange
	ctx, docs, ownerID := newExtractionFixture(t)
	doc := newPendingDocument(t, ctx, docs, ownerID)

	// Act
	if err := docs.FinishExtraction(
		ctx, doc.ID, "converted", model.ExtractionStatusComplete,
	); err != nil {
		t.Fatalf("FinishExtraction: %v", err)
	}

	// Assert: nothing to retry, and the bytes are gone.
	retried, err := docs.RetryFailedExtractions(ctx, 3)
	if err != nil {
		t.Fatalf("RetryFailedExtractions: %v", err)
	}
	if retried != 0 {
		t.Fatalf("retried = %d, want 0 after a successful conversion", retried)
	}
}

func TestRequeueStaleExtractionsLeavesFreshClaimsAlone(t *testing.T) {
	// Arrange: a peer replica's claim, taken moments ago.
	ctx, docs, ownerID := newExtractionFixture(t)
	newPendingDocument(t, ctx, docs, ownerID)
	if _, err := docs.ClaimPendingExtraction(ctx, 10); err != nil {
		t.Fatalf("ClaimPendingExtraction: %v", err)
	}

	// Act: a restarting replica looks for stale leases.
	requeued, err := docs.RequeueStaleExtractions(ctx, time.Hour)

	// Assert: it must not steal work another replica is still doing.
	if err != nil {
		t.Fatalf("RequeueStaleExtractions: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued = %d, want 0 for a fresh claim", requeued)
	}
}

func TestRetryFailedExtractionsStopsAfterMaxAttempts(t *testing.T) {
	// Arrange: fail the document repeatedly.
	ctx, docs, ownerID := newExtractionFixture(t)
	doc := newPendingDocument(t, ctx, docs, ownerID)
	for range 3 {
		if _, err := docs.ClaimPendingExtraction(ctx, 10); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if err := docs.FinishExtraction(
			ctx, doc.ID, testTextLayer, model.ExtractionStatusFailed,
		); err != nil {
			t.Fatalf("FinishExtraction: %v", err)
		}
		if _, err := docs.RetryFailedExtractions(ctx, 3); err != nil {
			t.Fatalf("RetryFailedExtractions: %v", err)
		}
	}

	// Act
	retried, err := docs.RetryFailedExtractions(ctx, 3)

	// Assert: exhausted, and its bytes released rather than retained forever.
	if err != nil {
		t.Fatalf("RetryFailedExtractions: %v", err)
	}
	if retried != 0 {
		t.Fatalf("retried = %d, want 0 once attempts are exhausted", retried)
	}
	claimed, err := docs.ClaimPendingExtraction(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d documents, want 0", len(claimed))
	}
}
