package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestUserRepositoryCreateAndGet(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	repo := store.NewUserRepository(pool)
	ctx := context.Background()

	created, err := repo.Create(ctx, model.User{
		Username: "alice", Email: "alice@example.com",
		PasswordHash: "hash", Role: model.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 || created.CreatedAt.IsZero() {
		t.Fatalf("Create did not populate ID/CreatedAt: %+v", created)
	}

	got, err := repo.GetByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if got.Email != "alice@example.com" || !got.IsAdmin() {
		t.Fatalf("unexpected user: %+v", got)
	}

	byID, err := repo.GetByID(ctx, created.ID)
	if err != nil || byID.Username != "alice" {
		t.Fatalf("GetByID: %v, %+v", err, byID)
	}
}

func TestUserRepositoryGetByUsernameNotFound(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	repo := store.NewUserRepository(pool)

	_, err := repo.GetByUsername(context.Background(), "ghost")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUserRepositoryListDeleteCount(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	repo := store.NewUserRepository(pool)
	ctx := context.Background()

	a, _ := repo.Create(ctx, model.User{Username: "a", Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	_, _ = repo.Create(ctx, model.User{Username: "b", Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})

	all, err := repo.ListAll(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListAll: %v len=%d", err, len(all))
	}
	n, _ := repo.Count(ctx)
	if n != 2 {
		t.Fatalf("Count = %d, want 2", n)
	}
	if err := repo.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	n, _ = repo.Count(ctx)
	if n != 1 {
		t.Fatalf("Count after delete = %d, want 1", n)
	}
}
