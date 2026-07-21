package store_test

import (
	"context"
	"testing"

	"github.com/pgvector/pgvector-go"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestChunkSearchTopKOrdersByCosine(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})

	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "apples"}, []float32{1, 0, 0})
	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "bananas"}, []float32{0, 1, 0})
	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "cherries"}, []float32{0, 0, 1})

	got, err := chunks.SearchTopK(ctx, u.ID, []float32{0.9, 0.1, 0}, 2)
	if err != nil {
		t.Fatalf("SearchTopK: %v", err)
	}
	if len(got) != 2 || got[0].Content != "apples" {
		t.Fatalf("top result should be apples: %+v", got)
	}
}

func TestChunkScopedToUserPlusPublic(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := context.Background()
	owner, _ := users.Create(ctx, model.User{Username: "o", Email: testEmailO, PasswordHash: "h", Role: model.RoleUser})
	other, _ := users.Create(ctx, model.User{Username: "b", Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})

	_ = chunks.Insert(ctx, model.Chunk{UserID: &owner.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "owner-private"}, []float32{1, 0, 0})
	_ = chunks.Insert(ctx, model.Chunk{UserID: &owner.ID, Scope: model.ScopePublic, SourceKind: model.ChunkSourceDocument, Content: "shared-public"}, []float32{1, 0, 0})

	got, _ := chunks.SearchTopK(ctx, other.ID, []float32{1, 0, 0}, 10)
	if len(got) != 1 || got[0].Content != "shared-public" {
		t.Fatalf("other user should only see public: %+v", got)
	}
}

func TestChunkCascadeOnConversationDelete(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	c, _ := convs.Create(ctx, u.ID, "chat")

	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, ConversationID: &c.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "remember this"}, []float32{1, 0, 0})

	if err := convs.Delete(ctx, c.ID, u.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	got, _ := chunks.SearchTopK(ctx, u.ID, []float32{1, 0, 0}, 10)
	if len(got) != 0 {
		t.Fatalf("chunks should be gone after conversation delete: %+v", got)
	}
}

func TestListContentForUserReturnsOwnAndPublicNotOthersPrivate(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := context.Background()
	userA, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	userB, _ := users.Create(ctx, model.User{Username: "b", Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})

	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "a-doc-chunk"}, []float32{1, 0, 0})
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "a-message-chunk"}, []float32{1, 0, 0})
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePublic, SourceKind: model.ChunkSourceDocument, Content: "public-chunk"}, []float32{1, 0, 0})
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userB.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "b-private-chunk"}, []float32{1, 0, 0})

	got, err := chunks.ListContentForUser(ctx, userA.ID)
	if err != nil {
		t.Fatalf("ListContentForUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 chunks (2 own + 1 public), got %d: %+v", len(got), got)
	}
	for _, ref := range got {
		if ref.Content == "b-private-chunk" {
			t.Fatalf("user B's private chunk must not leak to user A: %+v", got)
		}
	}
}

func TestSearchContentForUserFiltersByContent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := context.Background()
	userA, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	userB, _ := users.Create(ctx, model.User{Username: "b", Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})

	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "contains the search term here"}, []float32{1, 0, 0})
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "unrelated content"}, []float32{1, 0, 0})
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userB.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "also has the search term"}, []float32{1, 0, 0})

	got, err := chunks.SearchContentForUser(ctx, userA.ID, "search term", 20)
	if err != nil {
		t.Fatalf("SearchContentForUser: %v", err)
	}
	if len(got) != 1 || got[0].Content != "contains the search term here" {
		t.Fatalf("expected only user A's matching chunk, got %+v", got)
	}
}

func TestChunkRepository_SearchTopK_FiltersByEmbeddingModel(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})

	repo := store.NewChunkRepository(pool, "m1")
	if err := repo.Insert(ctx, model.Chunk{
		UserID: &u.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "hello current",
	}, []float32{1, 0, 0}); err != nil {
		t.Fatalf("insert current: %v", err)
	}
	// A row from a different model with a different dimension.
	if _, err := pool.Exec(ctx,
		`INSERT INTO chunks (user_id, scope, source_kind, content, embedding, embedding_model)
		 VALUES ($1,'private','message','old', $2, 'm0')`,
		u.ID, pgvector.NewVector([]float32{1, 0, 0, 0})); err != nil {
		t.Fatalf("insert foreign: %v", err)
	}

	got, err := repo.SearchTopK(ctx, u.ID, []float32{1, 0, 0}, 10)
	if err != nil {
		t.Fatalf("SearchTopK: %v", err)
	}
	if len(got) != 1 || got[0].Content != "hello current" {
		t.Fatalf("got %#v, want only the m1 chunk", got)
	}
}
