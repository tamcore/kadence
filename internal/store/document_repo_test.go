package store_test

import (
	"context"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const (
	testMimePDF        = "application/pdf"
	testFilenamePriv   = "p.pdf"
	testFilenamePublic = "pub.pdf"
)

func TestDocumentCreateListDeleteScoped(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: "a@x.io", PasswordHash: "h", Role: model.RoleUser})

	priv, err := docs.Create(ctx, model.Document{OwnerUserID: &u.ID, Scope: model.ScopePrivate, Filename: testFilenamePriv, Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "private text"})
	if err != nil {
		t.Fatalf("create private: %v", err)
	}
	if _, err := docs.Create(ctx, model.Document{OwnerUserID: nil, Scope: model.ScopePublic, Filename: testFilenamePublic, Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "public text"}); err != nil {
		t.Fatalf("create public: %v", err)
	}

	owned, _ := docs.ListByOwner(ctx, u.ID)
	if len(owned) != 1 || owned[0].Filename != testFilenamePriv {
		t.Fatalf("ListByOwner wrong: %+v", owned)
	}
	if owned[0].ExtractedMarkdown != "" {
		t.Fatalf("list must not return extracted_markdown")
	}
	pub, _ := docs.ListPublic(ctx)
	if len(pub) != 1 || pub[0].Filename != testFilenamePublic {
		t.Fatalf("ListPublic wrong: %+v", pub)
	}

	if err := docs.Delete(ctx, priv.ID, u.ID); err != nil {
		t.Fatalf("delete own doc: %v", err)
	}
	if got, _ := docs.ListByOwner(ctx, u.ID); len(got) != 0 {
		t.Fatalf("doc should be gone: %+v", got)
	}
}

func TestDocumentDeleteCascadesChunks(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	chunks := store.NewChunkRepository(pool)
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: "a@x.io", PasswordHash: "h", Role: model.RoleUser})
	d, _ := docs.Create(ctx, model.Document{OwnerUserID: &u.ID, Scope: model.ScopePrivate, Filename: testFilenamePriv, Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "x"})

	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, DocumentID: &d.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "doc chunk"}, []float32{1, 0, 0})

	if err := docs.Delete(ctx, d.ID, u.ID); err != nil {
		t.Fatalf("delete doc: %v", err)
	}
	got, _ := chunks.SearchTopK(ctx, u.ID, []float32{1, 0, 0}, 10)
	if len(got) != 0 {
		t.Fatalf("chunks should be gone after document delete: %+v", got)
	}
}

func TestPublicDocumentChunkOwnerlessRetrievable(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	chunks := store.NewChunkRepository(pool)
	ctx := context.Background()
	reader, _ := users.Create(ctx, model.User{Username: "r", Email: "r@x.io", PasswordHash: "h", Role: model.RoleUser})
	d, _ := docs.Create(ctx, model.Document{OwnerUserID: nil, Scope: model.ScopePublic, Filename: testFilenamePublic, Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "x"})

	if err := chunks.Insert(ctx, model.Chunk{UserID: nil, DocumentID: &d.ID, Scope: model.ScopePublic, SourceKind: model.ChunkSourceDocument, Content: "shared knowledge"}, []float32{1, 0, 0}); err != nil {
		t.Fatalf("insert ownerless public chunk: %v", err)
	}
	got, _ := chunks.SearchTopK(ctx, reader.ID, []float32{1, 0, 0}, 10)
	if len(got) != 1 || got[0].Content != "shared knowledge" {
		t.Fatalf("public chunk should be retrievable by any user: %+v", got)
	}
}
