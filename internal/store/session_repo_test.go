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
