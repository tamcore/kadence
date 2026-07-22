package store_test

import (
	"context"
	"testing"

	"github.com/pgvector/pgvector-go"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

// chunkEmbeddingDims is the fixed width of the chunks.embedding column
// (migration 00011_chunk_hnsw.sql pins it to vector(1024) for the HNSW
// index). Every insert into that column, regardless of embedding_model, must
// be exactly this many dimensions.
const chunkEmbeddingDims = 1024

// vec1024 pads prefix out to chunkEmbeddingDims with trailing zeros, so
// tests can express short, readable direction vectors (e.g. {1, 0, 0}) while
// satisfying the fixed-width column. Zero padding doesn't change cosine
// similarity/ordering between vectors built this way, since it contributes 0
// to both the dot product and the norm.
func vec1024(prefix ...float32) []float32 {
	v := make([]float32, chunkEmbeddingDims)
	copy(v, prefix)
	return v
}

func TestChunkSearchTopKOrdersByCosine(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})

	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "apples"}, vec1024(1, 0, 0))
	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "bananas"}, vec1024(0, 1, 0))
	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "cherries"}, vec1024(0, 0, 1))

	got, err := chunks.SearchTopK(ctx, u.ID, vec1024(0.9, 0.1, 0), 2)
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

	_ = chunks.Insert(ctx, model.Chunk{UserID: &owner.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "owner-private"}, vec1024(1, 0, 0))
	_ = chunks.Insert(ctx, model.Chunk{UserID: &owner.ID, Scope: model.ScopePublic, SourceKind: model.ChunkSourceDocument, Content: "shared-public"}, vec1024(1, 0, 0))

	got, _ := chunks.SearchTopK(ctx, other.ID, vec1024(1, 0, 0), 10)
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

	_ = chunks.Insert(ctx, model.Chunk{UserID: &u.ID, ConversationID: &c.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "remember this"}, vec1024(1, 0, 0))

	if err := convs.Delete(ctx, c.ID, u.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	got, _ := chunks.SearchTopK(ctx, u.ID, vec1024(1, 0, 0), 10)
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

	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "a-doc-chunk"}, vec1024(1, 0, 0))
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "a-message-chunk"}, vec1024(1, 0, 0))
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePublic, SourceKind: model.ChunkSourceDocument, Content: "public-chunk"}, vec1024(1, 0, 0))
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userB.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "b-private-chunk"}, vec1024(1, 0, 0))

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

	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "contains the search term here"}, vec1024(1, 0, 0))
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userA.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "unrelated content"}, vec1024(1, 0, 0))
	_ = chunks.Insert(ctx, model.Chunk{UserID: &userB.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceDocument, Content: "also has the search term"}, vec1024(1, 0, 0))

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
	}, vec1024(1, 0, 0)); err != nil {
		t.Fatalf("insert current: %v", err)
	}
	// A row from a different model (same fixed-width embedding column).
	if _, err := pool.Exec(ctx,
		`INSERT INTO chunks (user_id, scope, source_kind, content, embedding, embedding_model)
		 VALUES ($1,'private','message','old', $2, 'm0')`,
		u.ID, pgvector.NewVector(vec1024(1, 0, 0, 0))); err != nil {
		t.Fatalf("insert foreign: %v", err)
	}

	got, err := repo.SearchTopK(ctx, u.ID, vec1024(1, 0, 0), 10)
	if err != nil {
		t.Fatalf("SearchTopK: %v", err)
	}
	if len(got) != 1 || got[0].Content != "hello current" {
		t.Fatalf("got %#v, want only the m1 chunk", got)
	}
}

func TestChunkRepository_AdoptAndStatusAndReembed(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	uid := u.ID

	// Two untagged (NULL) rows with an "old" vector, one already-current row.
	for range 2 {
		if _, err := pool.Exec(ctx,
			`INSERT INTO chunks (user_id, scope, source_kind, content, embedding)
			 VALUES ($1,'private','message',$2,$3)`,
			uid, "old text", pgvector.NewVector(vec1024(1, 0, 0, 0))); err != nil {
			t.Fatalf("seed null: %v", err)
		}
	}
	cur := store.NewChunkRepository(pool, "m1")
	if err := cur.Insert(ctx, model.Chunk{
		UserID: &uid, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "current",
	}, vec1024(1, 0, 0)); err != nil {
		t.Fatalf("insert current: %v", err)
	}

	// Adopt: the 2 NULL rows become "m1"; the already-"m1" row is untouched.
	n, err := cur.AdoptUntagged(ctx)
	if err != nil || n != 2 {
		t.Fatalf("AdoptUntagged n=%d err=%v, want 2,nil", n, err)
	}

	// Simulate a model change to "m2": under "m2" all 3 rows are stale.
	repo := store.NewChunkRepository(pool, "m2")
	stale, total, err := repo.ReindexStatus(ctx)
	if err != nil || stale != 3 || total != 3 {
		t.Fatalf("ReindexStatus stale=%d total=%d err=%v, want 3,3,nil", stale, total, err)
	}

	// Re-embed everything to "m2" using a stub that returns fixed-width vectors.
	embed := func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i := range texts {
			out[i] = vec1024(0, 0, 0, 0, 1)
		}
		return out, nil
	}
	processed := 0
	for {
		done, err := repo.ReembedBatch(ctx, embed, 2)
		if err != nil {
			t.Fatalf("ReembedBatch: %v", err)
		}
		if done == 0 {
			break
		}
		processed += done
	}
	if processed != 3 {
		t.Fatalf("re-embedded %d, want 3", processed)
	}
	stale, _, _ = repo.ReindexStatus(ctx)
	if stale != 0 {
		t.Fatalf("stale after reembed = %d, want 0", stale)
	}
}

// TestChunkEmbeddingColumnPinnedTo1024Dims exercises the migration
// (00011_chunk_hnsw.sql) contract directly: the chunks.embedding column is a
// fixed-width vector(1024), which both makes the HNSW index possible and
// rejects any insert at another width, protecting the index from
// corruption if a caller (or a misconfigured KADENCE_EMBED_DIMENSIONS)
// bypasses the embed client's own dimension guard.
func TestChunkEmbeddingColumnPinnedTo1024Dims(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	chunks := store.NewChunkRepository(pool, "m1")
	ctx := context.Background()
	u, _ := users.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})

	// A correctly-sized (1024-dim) insert succeeds and is searchable.
	if err := chunks.Insert(ctx, model.Chunk{
		UserID: &u.ID, Scope: model.ScopePrivate, SourceKind: model.ChunkSourceMessage, Content: "on-model",
	}, vec1024(1, 0, 0)); err != nil {
		t.Fatalf("1024-dim insert: %v", err)
	}
	got, err := chunks.SearchTopK(ctx, u.ID, vec1024(1, 0, 0), 10)
	if err != nil {
		t.Fatalf("SearchTopK: %v", err)
	}
	if len(got) != 1 || got[0].Content != "on-model" {
		t.Fatalf("expected the 1024-dim chunk to be found: %+v", got)
	}

	// A wrong-width (3-dim) insert must be rejected by the column type, not
	// silently accepted.
	_, err = pool.Exec(ctx,
		`INSERT INTO chunks (user_id, scope, source_kind, content, embedding, embedding_model)
		 VALUES ($1,'private','message','wrong-width',$2,'m1')`,
		u.ID, pgvector.NewVector([]float32{1, 0, 0}))
	if err == nil {
		t.Fatal("expected wrong-dimension insert to be rejected by the vector(1024) column type")
	}
}
