package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

func TestSessionDeleteExpired(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	ctx := context.Background()
	u := newUser(t, users, "heidi")

	_ = sessions.Create(ctx, model.Session{ID: "live", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	_ = sessions.Create(ctx, model.Session{ID: "expired1", UserID: u.ID, ExpiresAt: time.Now().Add(-time.Minute)})
	_ = sessions.Create(ctx, model.Session{ID: "expired2", UserID: u.ID, ExpiresAt: time.Now().Add(-time.Hour)})

	n, err := sessions.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteExpired count = %d, want 2", n)
	}

	// Live session survives; a second call is a no-op.
	if _, err := sessions.GetByID(ctx, "live"); err != nil {
		t.Fatalf("live session should still exist: %v", err)
	}
	n2, err := sessions.DeleteExpired(ctx)
	if err != nil || n2 != 0 {
		t.Fatalf("second DeleteExpired: n=%d err=%v, want 0,nil", n2, err)
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
	// ListByUser cannot recover the raw id (only its hash is stored), so
	// compare against the hash of the raw id used at Create time.
	if list[0].ID != store.HashSessionID("s2") {
		t.Fatalf("order: want s2 (hashed) first, got %s", list[0].ID)
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

// TestSessionCreateGetRoundTripsThroughHash creates a session with a raw id,
// fetches it back by that same raw id, and verifies the row actually stored
// in the sessions table is the sha256 hash of the raw id rather than the raw
// value itself.
func TestSessionCreateGetRoundTripsThroughHash(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	ctx := context.Background()
	u := newUser(t, users, "ivan")

	const raw = "raw-session-token-abc123"
	if err := sessions.Create(ctx, model.Session{ID: raw, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := sessions.GetByID(ctx, raw)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.UserID != u.ID {
		t.Fatalf("GetByID user mismatch: %+v", got)
	}
	if got.ID != raw {
		t.Fatalf("GetByID should hand back the raw id it was given: got %q, want %q", got.ID, raw)
	}

	assertRawSessionIDAbsentButHashPresent(t, pool, raw)
}

// TestSessionRawTokenNeverStored is a second, more direct assertion that
// looking up sessions.id by the raw token never matches: only the hash does.
func TestSessionRawTokenNeverStored(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	ctx := context.Background()
	u := newUser(t, users, "judy")

	const raw = "another-raw-session-token-xyz789"
	if err := sessions.Create(ctx, model.Session{ID: raw, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	assertRawSessionIDAbsentButHashPresent(t, pool, raw)
}

// assertRawSessionIDAbsentButHashPresent queries the sessions table directly
// (bypassing SessionRepository's hashing) and asserts the raw token is not a
// stored id, while its sha256 hash is.
func assertRawSessionIDAbsentButHashPresent(t *testing.T, pool *pgxpool.Pool, raw string) {
	t.Helper()
	ctx := context.Background()

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, raw).Scan(&count); err != nil {
		t.Fatalf("query raw id: %v", err)
	}
	if count != 0 {
		t.Fatalf("raw session token found stored in sessions.id: must only store the hash")
	}

	hash := store.HashSessionID(raw)
	var stillCount int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, hash).Scan(&stillCount)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("query hashed id: %v", err)
	}
	if stillCount != 1 {
		t.Fatalf("expected exactly one row stored under the hashed id, got %d", stillCount)
	}
}
