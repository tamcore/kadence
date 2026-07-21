package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func newUser(t *testing.T, repo *store.UserRepository, name string) model.User {
	t.Helper()
	u, err := repo.Create(context.Background(), model.User{
		Username: name, Email: name + "@x.io", PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestSessionCreateGetDelete(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	ctx := context.Background()
	u := newUser(t, users, "alice")

	s := model.Session{ID: "sess-abc", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := sessions.Create(ctx, s); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := sessions.GetByID(ctx, "sess-abc")
	if err != nil || got.UserID != u.ID {
		t.Fatalf("GetByID: %v %+v", err, got)
	}
	if err := sessions.Delete(ctx, "sess-abc"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := sessions.GetByID(ctx, "sess-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestSessionExpiredIsNotFound(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	ctx := context.Background()
	u := newUser(t, users, "bob")

	_ = sessions.Create(ctx, model.Session{ID: "old", UserID: u.ID, ExpiresAt: time.Now().Add(-time.Minute)})
	if _, err := sessions.GetByID(ctx, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session err = %v, want ErrNotFound", err)
	}
}

func TestSessionDeleteAllByUser(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	ctx := context.Background()
	u := newUser(t, users, "carol")

	_ = sessions.Create(ctx, model.Session{ID: "s1", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	_ = sessions.Create(ctx, model.Session{ID: "s2", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	if err := sessions.DeleteAllByUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteAllByUser: %v", err)
	}
	if _, err := sessions.GetByID(ctx, "s1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("s1 err = %v, want ErrNotFound", err)
	}
}

func TestSessionDeleteOthersByUser(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	ctx := context.Background()
	u := newUser(t, users, "dave")
	other := newUser(t, users, "erin")

	_ = sessions.Create(ctx, model.Session{ID: "keep", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	_ = sessions.Create(ctx, model.Session{ID: "drop1", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	_ = sessions.Create(ctx, model.Session{ID: "drop2", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	_ = sessions.Create(ctx, model.Session{ID: "other-user-sess", UserID: other.ID, ExpiresAt: time.Now().Add(time.Hour)})

	if err := sessions.DeleteOthersByUser(ctx, u.ID, "keep"); err != nil {
		t.Fatalf("DeleteOthersByUser: %v", err)
	}

	if _, err := sessions.GetByID(ctx, "keep"); err != nil {
		t.Fatalf("keep session should still exist: %v", err)
	}
	if _, err := sessions.GetByID(ctx, "drop1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("drop1 err = %v, want ErrNotFound", err)
	}
	if _, err := sessions.GetByID(ctx, "drop2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("drop2 err = %v, want ErrNotFound", err)
	}
	if _, err := sessions.GetByID(ctx, "other-user-sess"); err != nil {
		t.Fatalf("other user's session should be untouched: %v", err)
	}
}

func TestSessionRepository_MetadataAndListRevokeTouch(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	repo := store.NewSessionRepository(pool)
	ctx := context.Background()
	uid := newUser(t, users, "frank").ID
	other := newUser(t, users, "grace").ID

	mk := func(id string) model.Session {
		return model.Session{ID: id, UserID: uid, RememberMe: false, ExpiresAt: time.Now().Add(time.Hour), UserAgent: "UA-" + id, IP: "1.2.3.4"}
	}
	if err := repo.Create(ctx, mk("s1")); err != nil {
		t.Fatalf("create s1: %v", err)
	}
	if err := repo.Create(ctx, mk("s2")); err != nil {
		t.Fatalf("create s2: %v", err)
	}
	// touch s2 into the future so it sorts first + updates ip
	if err := repo.Touch(ctx, "s2", "9.9.9.9", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("touch: %v", err)
	}

	list, err := repo.ListByUser(ctx, uid)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListByUser len=%d err=%v", len(list), err)
	}
	if list[0].ID != "s2" {
		t.Fatalf("order: want s2 first, got %s", list[0].ID)
	}
	if list[0].IP != "9.9.9.9" {
		t.Fatalf("touch ip not applied: %q", list[0].IP)
	}
	if list[1].UserAgent != "UA-s1" || list[1].PublicID == "" {
		t.Fatalf("fields: %#v", list[1])
	}

	// revoke by public_id, owner-scoped
	if err := repo.DeleteByPublicIDForUser(ctx, list[1].PublicID, other); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner revoke err=%v want ErrNotFound", err)
	}
	if err := repo.DeleteByPublicIDForUser(ctx, list[1].PublicID, uid); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if l2, _ := repo.ListByUser(ctx, uid); len(l2) != 1 {
		t.Fatalf("after revoke len=%d want 1", len(l2))
	}
}
