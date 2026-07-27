package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pgvector/pgvector-go"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestDocumentRepositoryListVisibleByIDsPreservesOrderAndIncludesMarkdown(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	documents := store.NewDocumentRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "visible-doc-owner", Email: "visible-doc-owner@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	privateDocument, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "private.md",
		Mime: testMimeMarkdown, SourceType: model.DocSourceText,
		ExtractedMarkdown: "private markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	publicDocument, err := documents.Create(ctx, model.Document{
		Scope: model.ScopePublic, Filename: "public.md",
		Mime: testMimeMarkdown, SourceType: model.DocSourceText,
		ExtractedMarkdown: "public markdown",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := documents.ListVisibleByIDs(
		ctx, owner.ID, []int64{publicDocument.ID, privateDocument.ID},
	)
	if err != nil {
		t.Fatalf("ListVisibleByIDs: %v", err)
	}
	if len(got) != 2 ||
		got[0].ID != publicDocument.ID || got[0].ExtractedMarkdown != "public markdown" ||
		got[1].ID != privateDocument.ID || got[1].ExtractedMarkdown != "private markdown" {
		t.Fatalf("visible documents = %+v", got)
	}
}

func TestDocumentRepositoryListVisibleByIDsFailsClosedForInvisibleMissingAndDeleted(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	documents := store.NewDocumentRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "visible-doc-requester", Email: "visible-doc-requester@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, model.User{
		Username: "visible-doc-other", Email: "visible-doc-other@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	invisible, err := documents.Create(ctx, model.Document{
		OwnerUserID: &other.ID, Scope: model.ScopePrivate, Filename: "other-private.md",
		Mime: testMimeMarkdown, SourceType: model.DocSourceText,
		ExtractedMarkdown: "must not leak",
	})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := documents.Create(ctx, model.Document{
		OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: "deleted.md",
		Mime: testMimeMarkdown, SourceType: model.DocSourceText,
		ExtractedMarkdown: "deleted content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := documents.Delete(ctx, owned.ID, owner.ID); err != nil {
		t.Fatal(err)
	}

	for _, ids := range [][]int64{
		{invisible.ID},
		{99_999_999},
		{owned.ID},
		{invisible.ID, 99_999_999},
	} {
		got, err := documents.ListVisibleByIDs(ctx, owner.ID, ids)
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("ListVisibleByIDs(%v) error = %v, want ErrNotFound", ids, err)
		}
		if got != nil {
			t.Fatalf("ListVisibleByIDs(%v) = %+v, want no partial result", ids, got)
		}
	}
}

func TestChunkRepositorySearchTopKByVisibleDocumentsBatchesAndLimitsPerDocument(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	documents := store.NewDocumentRepository(pool)
	chunks := store.NewChunkRepository(pool, "current-model")
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "explicit-chunk-owner", Email: "explicit-chunk-owner@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, model.User{
		Username: "explicit-chunk-other", Email: "explicit-chunk-other@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	privateDocument := createExplicitChunkDocument(t, ctx, documents, &owner.ID, model.ScopePrivate, "private.md")
	publicDocument := createExplicitChunkDocument(t, ctx, documents, nil, model.ScopePublic, "public.md")
	invisibleDocument := createExplicitChunkDocument(t, ctx, documents, &other.ID, model.ScopePrivate, "other.md")

	insertExplicitDocumentChunk(t, ctx, chunks, &owner.ID, privateDocument.ID, model.ScopePrivate, "private-best", vec1024(1, 0))
	insertExplicitDocumentChunk(t, ctx, chunks, &owner.ID, privateDocument.ID, model.ScopePrivate, "private-second", vec1024(0.8, 0.2))
	insertExplicitDocumentChunk(t, ctx, chunks, nil, publicDocument.ID, model.ScopePublic, "public-best", vec1024(1, 0))
	insertExplicitDocumentChunk(t, ctx, chunks, nil, publicDocument.ID, model.ScopePublic, "public-second", vec1024(0.7, 0.3))
	insertExplicitDocumentChunk(t, ctx, chunks, &other.ID, invisibleDocument.ID, model.ScopePrivate, "must-not-leak", vec1024(1, 0))

	if _, err := pool.Exec(ctx,
		`INSERT INTO chunks (
		     user_id, document_id, scope, source_kind, content, embedding, embedding_model
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		owner.ID, privateDocument.ID, model.ScopePrivate, model.ChunkSourceDocument,
		"wrong-model", pgvector.NewVector(vec1024(1, 0)), "old-model",
	); err != nil {
		t.Fatal(err)
	}

	got, err := chunks.SearchTopKByVisibleDocuments(
		ctx,
		owner.ID,
		[]int64{publicDocument.ID, privateDocument.ID, invisibleDocument.ID},
		vec1024(1, 0),
		1,
	)
	if err != nil {
		t.Fatalf("SearchTopKByVisibleDocuments: %v", err)
	}
	if len(got[publicDocument.ID]) != 1 || got[publicDocument.ID][0].Content != "public-best" {
		t.Fatalf("public results = %+v", got[publicDocument.ID])
	}
	if len(got[privateDocument.ID]) != 1 || got[privateDocument.ID][0].Content != "private-best" {
		t.Fatalf("private results = %+v", got[privateDocument.ID])
	}
	if chunksForInvisible, ok := got[invisibleDocument.ID]; ok && len(chunksForInvisible) != 0 {
		t.Fatalf("invisible private document leaked chunks: %+v", chunksForInvisible)
	}
}

func createExplicitChunkDocument(
	t *testing.T,
	ctx context.Context,
	documents *store.DocumentRepository,
	ownerID *int64,
	scope string,
	filename string,
) model.Document {
	t.Helper()
	document, err := documents.Create(ctx, model.Document{
		OwnerUserID: ownerID, Scope: scope, Filename: filename,
		Mime: testMimeMarkdown, SourceType: model.DocSourceText,
		ExtractedMarkdown: filename + " full content",
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func insertExplicitDocumentChunk(
	t *testing.T,
	ctx context.Context,
	chunks *store.ChunkRepository,
	userID *int64,
	documentID int64,
	scope string,
	content string,
	embedding []float32,
) {
	t.Helper()
	if err := chunks.Insert(ctx, model.Chunk{
		UserID: userID, DocumentID: &documentID, Scope: scope,
		SourceKind: model.ChunkSourceDocument, Content: content,
	}, embedding); err != nil {
		t.Fatal(err)
	}
}
