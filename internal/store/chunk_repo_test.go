package store_test

import (
	"context"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestChunkSearchTopKOrdersByCosine(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	chunks := store.NewChunkRepository(pool)
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
	chunks := store.NewChunkRepository(pool)
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
	chunks := store.NewChunkRepository(pool)
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	c, _ := convs.Create(ctx, u.ID, "chat")

	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, ConversationID: new(c.ID), Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "remember this"}, []float32{1, 0, 0})

	if err := convs.Delete(ctx, c.ID, u.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	got, _ := chunks.SearchTopK(ctx, u.ID, []float32{1, 0, 0}, 10)
	if len(got) != 0 {
		t.Fatalf("chunks should be gone after conversation delete: %+v", got)
	}
}
