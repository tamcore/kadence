package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
	if err := repo.UpdateProfile(ctx, a.ID, "Alice A", "newalice@x.io", model.UnitImperial, "Berlin, Germany", "Marathon runner training for a sub-3.", "UTC"); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	got, _ := repo.GetByID(ctx, a.ID)
	if got.DisplayName != "Alice A" || got.Email != "newalice@x.io" || got.UnitSystem != model.UnitImperial ||
		got.Location != "Berlin, Germany" || got.AboutMe != "Marathon runner training for a sub-3." {
		t.Fatalf("after update: %#v", got)
	}

	// Clearing location/aboutMe (empty strings) must round-trip to empty, not
	// leave the previous value behind.
	if err := repo.UpdateProfile(ctx, a.ID, "Alice A", "newalice@x.io", model.UnitImperial, "", "", "UTC"); err != nil {
		t.Fatalf("UpdateProfile clear: %v", err)
	}
	got, _ = repo.GetByID(ctx, a.ID)
	if got.Location != "" || got.AboutMe != "" {
		t.Fatalf("after clearing: location=%q aboutMe=%q", got.Location, got.AboutMe)
	}

	if err := repo.UpdateProfile(ctx, a.ID, "Alice A", b.Email, model.UnitImperial, "", "", "UTC"); !errors.Is(err, store.ErrEmailTaken) {
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

func TestUserRepositoryDeleteCascadesScheduledAudit(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)

	user, err := users.Create(ctx, model.User{
		Username: "scheduled-owner", Email: "scheduled-owner@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	conversation, err := conversations.CreateWithKind(ctx, user.ID, "Scheduled", model.ConversationKindScheduled)
	if err != nil {
		t.Fatalf("create scheduled conversation: %v", err)
	}
	taskID := createScheduledTaskWithDeliveredRun(t, ctx, pool, user.ID, conversation.ID)

	if err := users.Delete(ctx, user.ID); err != nil {
		t.Fatalf("Delete user with scheduled audit: %v", err)
	}
	for table, query := range map[string]string{
		conversationsTable:    `SELECT COUNT(*) FROM conversations WHERE id = $1::uuid`,
		"scheduled_tasks":     `SELECT COUNT(*) FROM scheduled_tasks WHERE id = $1::uuid`,
		"scheduled_task_runs": `SELECT COUNT(*) FROM scheduled_task_runs WHERE task_id = $1::uuid`,
	} {
		id := taskID
		if table == conversationsTable {
			id = conversation.ID
		}
		var count int
		if err := pool.QueryRow(ctx, query, id).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count after user delete = %d, want 0", table, count)
		}
	}
}

func TestScheduledTaskSoftDeleteRetainsAudit(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)

	user, err := users.Create(ctx, model.User{
		Username: "scheduled-soft-delete", Email: "scheduled-soft-delete@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	conversation, err := conversations.CreateWithKind(ctx, user.ID, "Scheduled", model.ConversationKindScheduled)
	if err != nil {
		t.Fatalf("create scheduled conversation: %v", err)
	}
	taskID := createScheduledTaskWithDeliveredRun(t, ctx, pool, user.ID, conversation.ID)

	if err := store.NewScheduledTaskRepository(pool, 10).SoftDelete(ctx, taskID, user.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	var state string
	var deleted bool
	if err := pool.QueryRow(ctx,
		`SELECT state, deleted_at IS NOT NULL FROM scheduled_tasks WHERE id = $1::uuid`, taskID).
		Scan(&state, &deleted); err != nil {
		t.Fatalf("read soft-deleted task: %v", err)
	}
	if state != model.ScheduledTaskStateDeleted || !deleted {
		t.Fatalf("soft-deleted task state=%q deleted_at_set=%t", state, deleted)
	}
	var runCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM scheduled_task_runs
		  WHERE task_id = $1::uuid AND state = 'delivered' AND result = 'audit result'`, taskID).
		Scan(&runCount); err != nil {
		t.Fatalf("count retained task runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("retained delivered run count = %d, want 1", runCount)
	}
}

func createScheduledTaskWithDeliveredRun(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	userID int64,
	conversationID string,
) string {
	t.Helper()
	var taskID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO scheduled_tasks (
		     user_id, conversation_id, kind, state, compiled_prompt, timezone
		 ) VALUES ($1, $2::uuid, 'reminder', 'completed', 'reflect', 'UTC')
		 RETURNING id::text`,
		userID, conversationID).Scan(&taskID); err != nil {
		t.Fatalf("insert scheduled task: %v", err)
	}
	deliveredAt := time.Date(2026, 7, 24, 20, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`INSERT INTO scheduled_task_runs (
		     task_id, occurrence_key, scheduled_for, state, finished_at, result
		 ) VALUES ($1::uuid, 'scheduled:2026-07-24T20:00:00Z', $2, 'delivered', $2, 'audit result')`,
		taskID, deliveredAt); err != nil {
		t.Fatalf("insert delivered task run: %v", err)
	}
	return taskID
}
