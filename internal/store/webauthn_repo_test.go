package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestWebAuthnRepo_CreateListGet(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	repo := store.NewWebAuthnCredentialRepository(pool)

	u := newUser(t, users, "wa-user")

	cred := model.WebAuthnCredential{
		UserID: u.ID, CredentialID: []byte("cred-abc"), PublicKey: []byte("pub"),
		AAGUID: []byte("aa"), SignCount: 1, Transports: []string{"internal"}, Name: "MacBook",
	}
	if err := repo.Create(ctx, cred); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := repo.ListByUser(ctx, u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list err=%v len=%d", err, len(list))
	}
	got := list[0]
	if got.PublicID == "" || got.Name != "MacBook" || got.SignCount != 1 {
		t.Fatalf("bad row: %+v", got)
	}

	byCred, err := repo.GetByCredentialID(ctx, []byte("cred-abc"))
	if err != nil || byCred.UserID != u.ID {
		t.Fatalf("getByCredentialID err=%v row=%+v", err, byCred)
	}
	if _, err := repo.GetByCredentialID(ctx, []byte("missing")); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestWebAuthnRepo_RenameDelete_OwnerScoped(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	repo := store.NewWebAuthnCredentialRepository(pool)
	owner := newUser(t, users, "wa-owner")
	other := newUser(t, users, "wa-other")

	_ = repo.Create(ctx, model.WebAuthnCredential{UserID: owner.ID, CredentialID: []byte("c1"), PublicKey: []byte("p")})
	pub := mustList(t, repo, owner.ID)[0].PublicID

	if err := repo.Rename(ctx, pub, other.ID, "hijack"); err != store.ErrNotFound {
		t.Fatalf("cross-user rename must ErrNotFound, got %v", err)
	}
	if err := repo.Rename(ctx, pub, owner.ID, "renamed"); err != nil {
		t.Fatalf("owner rename: %v", err)
	}
	if mustList(t, repo, owner.ID)[0].Name != "renamed" {
		t.Fatal("rename did not persist")
	}
	if err := repo.DeleteByPublicIDForUser(ctx, pub, other.ID); err != store.ErrNotFound {
		t.Fatalf("cross-user delete must ErrNotFound, got %v", err)
	}
	if err := repo.DeleteByPublicIDForUser(ctx, pub, owner.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if len(mustList(t, repo, owner.ID)) != 0 {
		t.Fatal("row not deleted")
	}
}

func TestWebAuthnRepo_UpdateSignCount(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	repo := store.NewWebAuthnCredentialRepository(pool)
	u := newUser(t, users, "wa-sc")
	_ = repo.Create(ctx, model.WebAuthnCredential{UserID: u.ID, CredentialID: []byte("c"), PublicKey: []byte("p")})

	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdateSignCount(ctx, []byte("c"), 42, now); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := mustList(t, repo, u.ID)[0]
	if got.SignCount != 42 || got.LastUsedAt == nil {
		t.Fatalf("sign count/last used not updated: %+v", got)
	}
}

func TestUserRepo_GetByWebAuthnHandle(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	u := newUser(t, users, "wa-handle")
	reloaded, err := users.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.WebAuthnHandle == "" {
		t.Fatal("handle empty after insert")
	}
	byHandle, err := users.GetByWebAuthnHandle(ctx, reloaded.WebAuthnHandle)
	if err != nil || byHandle.ID != u.ID {
		t.Fatalf("getByHandle err=%v id=%d", err, byHandle.ID)
	}
	if _, err := users.GetByWebAuthnHandle(ctx, "00000000-0000-0000-0000-000000000000"); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func mustList(t *testing.T, r *store.WebAuthnCredentialRepository, uid int64) []model.WebAuthnCredential {
	t.Helper()
	l, err := r.ListByUser(context.Background(), uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return l
}
