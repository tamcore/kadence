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
		Username: testAliceUsername, Email: "alice@example.com",
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

func TestUserRepository_UpdateProfileAndPassword(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	repo := store.NewUserRepository(pool)
	ctx := context.Background()

	a, err := repo.Create(ctx, model.User{Username: testAliceUsername, Email: "alice@x.io", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b, _ := repo.Create(ctx, model.User{Username: testBobUsername, Email: testEmailBob, PasswordHash: "h", Role: model.RoleUser})

	if a.UnitSystem != model.UnitMetric || a.DisplayName != "" || a.Location != "" || a.AboutMe != "" {
		t.Fatalf("defaults: display=%q unit=%q location=%q aboutMe=%q", a.DisplayName, a.UnitSystem, a.Location, a.AboutMe)
	}
	if err := repo.UpdateProfile(ctx, a.ID, "Alice A", "newalice@x.io", model.UnitImperial, "Berlin, Germany", "Marathon runner training for a sub-3."); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	got, _ := repo.GetByID(ctx, a.ID)
	if got.DisplayName != "Alice A" || got.Email != "newalice@x.io" || got.UnitSystem != model.UnitImperial ||
		got.Location != "Berlin, Germany" || got.AboutMe != "Marathon runner training for a sub-3." {
		t.Fatalf("after update: %#v", got)
	}

	// Clearing location/aboutMe (empty strings) must round-trip to empty, not
	// leave the previous value behind.
	if err := repo.UpdateProfile(ctx, a.ID, "Alice A", "newalice@x.io", model.UnitImperial, "", ""); err != nil {
		t.Fatalf("UpdateProfile clear: %v", err)
	}
	got, _ = repo.GetByID(ctx, a.ID)
	if got.Location != "" || got.AboutMe != "" {
		t.Fatalf("after clearing: location=%q aboutMe=%q", got.Location, got.AboutMe)
	}

	if err := repo.UpdateProfile(ctx, a.ID, "Alice A", b.Email, model.UnitImperial, "", ""); !errors.Is(err, store.ErrEmailTaken) {
		t.Fatalf("email collision err=%v want ErrEmailTaken", err)
	}
	if err := repo.UpdatePassword(ctx, a.ID, "newhash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	got, _ = repo.GetByID(ctx, a.ID)
	if got.PasswordHash != "newhash" {
		t.Fatalf("password not updated: %q", got.PasswordHash)
	}
}

func TestUserRepository_UpdateUserAndCountAdmins(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	repo := store.NewUserRepository(pool)
	ctx := context.Background()

	a, err := repo.Create(ctx, model.User{Username: testAliceUsername, Email: "alice@x.io", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	b, err := repo.Create(ctx, model.User{Username: testBobUsername, Email: testEmailBob, PasswordHash: "h", Role: model.RoleAdmin})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	if n, _ := repo.CountAdmins(ctx); n != 1 {
		t.Fatalf("CountAdmins = %d, want 1", n)
	}

	updated, err := repo.UpdateUser(ctx, a.ID, "alice2", "alice2@x.io", model.RoleAdmin)
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.Username != "alice2" || updated.Email != "alice2@x.io" || !updated.IsAdmin() {
		t.Fatalf("after update: %#v", updated)
	}
	if n, _ := repo.CountAdmins(ctx); n != 2 {
		t.Fatalf("CountAdmins after promote = %d, want 2", n)
	}

	if _, err := repo.UpdateUser(ctx, a.ID, b.Username, "alice2@x.io", model.RoleUser); !errors.Is(err, store.ErrUsernameTaken) {
		t.Fatalf("username collision err=%v want ErrUsernameTaken", err)
	}
	if _, err := repo.UpdateUser(ctx, a.ID, "alice2", b.Email, model.RoleUser); !errors.Is(err, store.ErrEmailTaken) {
		t.Fatalf("email collision err=%v want ErrEmailTaken", err)
	}
	if _, err := repo.UpdateUser(ctx, 999999, "ghost", "ghost@x.io", model.RoleUser); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing id err=%v want ErrNotFound", err)
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
