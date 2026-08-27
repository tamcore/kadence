package store_test

import (
	"errors"
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
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	ctx := t.Context()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: "a@x.io", PasswordHash: "h", Role: model.RoleUser})

	priv, err := docs.Create(ctx, model.Document{OwnerUserID: &u.ID, Scope: model.ScopePrivate, Filename: testFilenamePriv, Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "private text"})
	if err != nil {
		t.Fatalf("create private: %v", err)
	}
	if _, err := docs.Create(ctx, model.Document{OwnerUserID: nil, Scope: model.ScopePublic, Filename: testFilenamePublic, Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "public text"}); err != nil {
		t.Fatalf("create public: %v", err)
	}
	if _, err := docs.Create(ctx, model.Document{
		OwnerUserID: &u.ID, Scope: model.ScopePublic, Filename: "owned-public.pdf",
		Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "public text with owner",
	}); err != nil {
		t.Fatalf("create owned public: %v", err)
	}

	owned, _ := docs.ListByOwner(ctx, u.ID)
	if len(owned) != 1 || owned[0].Filename != testFilenamePriv {
		t.Fatalf("ListByOwner wrong: %+v", owned)
	}
	if owned[0].ExtractedMarkdown != "" {
		t.Fatalf("list must not return extracted_markdown")
	}
	pub, _ := docs.ListPublic(ctx)
	if len(pub) != 2 {
		t.Fatalf("ListPublic wrong: %+v", pub)
	}
	publicFilenames := map[string]bool{}
	for _, document := range pub {
		publicFilenames[document.Filename] = true
	}
	if !publicFilenames[testFilenamePublic] || !publicFilenames["owned-public.pdf"] {
		t.Fatalf("ListPublic wrong: %+v", pub)
	}

	if err := docs.Delete(ctx, priv.ID, u.ID); err != nil {
		t.Fatalf("delete own doc: %v", err)
	}
	if got, _ := docs.ListByOwner(ctx, u.ID); len(got) != 0 {
		t.Fatalf("doc should be gone: %+v", got)
	}
}

func TestDocumentDeleteNotFound(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	ctx := t.Context()
	owner, _ := users.Create(ctx, model.User{Username: "docowner", Email: "docowner@x.io", PasswordHash: "h", Role: model.RoleUser})
	other, _ := users.Create(ctx, model.User{Username: "docother", Email: "docother@x.io", PasswordHash: "h", Role: model.RoleUser})

	priv, err := docs.Create(ctx, model.Document{OwnerUserID: &owner.ID, Scope: model.ScopePrivate, Filename: testFilenamePriv, Mime: testMimePDF, SourceType: model.DocSourcePDF})
	if err != nil {
		t.Fatalf("create private: %v", err)
	}
	pub, err := docs.Create(ctx, model.Document{OwnerUserID: nil, Scope: model.ScopePublic, Filename: testFilenamePublic, Mime: testMimePDF, SourceType: model.DocSourcePDF})
	if err != nil {
		t.Fatalf("create public: %v", err)
	}

	if err := docs.Delete(ctx, 999999, owner.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete nonexistent id err = %v, want ErrNotFound", err)
	}
	if err := docs.Delete(ctx, priv.ID, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete another user's doc err = %v, want ErrNotFound", err)
	}
	if err := docs.DeletePublic(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete public nonexistent id err = %v, want ErrNotFound", err)
	}
	if err := docs.DeletePublic(ctx, priv.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete-public on a private doc err = %v, want ErrNotFound", err)
	}
	if owned, _ := docs.ListByOwner(ctx, owner.ID); len(owned) != 1 {
		t.Fatalf("private doc should be untouched: %+v", owned)
	}

	if err := docs.Delete(ctx, pub.ID, owner.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("owner-scoped delete of a public doc err = %v, want ErrNotFound", err)
	}
	if err := docs.DeletePublic(ctx, pub.ID); err != nil {
		t.Fatalf("delete public: %v", err)
	}
}

func TestDocumentDeleteCascadesChunks(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := t.Context()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: "a@x.io", PasswordHash: "h", Role: model.RoleUser})
	d, _ := docs.Create(ctx, model.Document{OwnerUserID: &u.ID, Scope: model.ScopePrivate, Filename: testFilenamePriv, Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "x"})

	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, DocumentID: &d.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "doc chunk"}, vec1024(1, 0, 0))

	if err := docs.Delete(ctx, d.ID, u.ID); err != nil {
		t.Fatalf("delete doc: %v", err)
	}
	got, _ := chunks.SearchTopK(ctx, u.ID, vec1024(1, 0, 0), 10)
	if len(got) != 0 {
		t.Fatalf("chunks should be gone after document delete: %+v", got)
	}
}

func TestPublicDocumentChunkOwnerlessRetrievable(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	users := store.NewUserRepository(pool)
	docs := store.NewDocumentRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := t.Context()
	reader, _ := users.Create(ctx, model.User{Username: "r", Email: "r@x.io", PasswordHash: "h", Role: model.RoleUser})
	d, _ := docs.Create(ctx, model.Document{OwnerUserID: nil, Scope: model.ScopePublic, Filename: testFilenamePublic, Mime: testMimePDF, SourceType: model.DocSourcePDF, ExtractedMarkdown: "x"})

	if err := chunks.Insert(ctx, model.Chunk{UserID: nil, DocumentID: &d.ID, Scope: model.ScopePublic, SourceKind: model.ChunkSourceDocument, Content: "shared knowledge"}, vec1024(1, 0, 0)); err != nil {
		t.Fatalf("insert ownerless public chunk: %v", err)
	}
	got, _ := chunks.SearchTopK(ctx, reader.ID, vec1024(1, 0, 0), 10)
	if len(got) != 1 || got[0].Content != "shared knowledge" {
		t.Fatalf("public chunk should be retrievable by any user: %+v", got)
	}
}
