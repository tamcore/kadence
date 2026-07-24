package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const (
	scheduledTimezoneUTC    = "UTC"
	scheduledTimezoneBerlin = "Europe/Berlin"
)

func TestScheduledUserTimezoneAndConversationKind(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)

	u, err := users.Create(ctx, model.User{Username: testAliceUsername, Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	if u.Timezone != scheduledTimezoneUTC {
		t.Fatalf("default timezone = %q, want UTC", u.Timezone)
	}
	if err := users.UpdateTimezone(ctx, u.ID, scheduledTimezoneBerlin); err != nil {
		t.Fatalf("UpdateTimezone: %v", err)
	}
	updated, err := users.GetByID(ctx, u.ID)
	if err != nil || updated.Timezone != scheduledTimezoneBerlin {
		t.Fatalf("timezone round trip: %v %+v", err, updated)
	}

	conversation, err := conversations.CreateWithKind(ctx, u.ID, "Scheduled", model.ConversationKindScheduled)
	if err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}
	if conversation.Kind != model.ConversationKindScheduled {
		t.Fatalf("conversation kind = %q", conversation.Kind)
	}
	if ordinary, err := conversations.ListByUser(ctx, u.ID); err != nil || len(ordinary) != 0 {
		t.Fatalf("normal list includes scheduled conversation: %v %+v", err, ordinary)
	}
}

func TestScheduledTaskRepositoryOwnerScopeAndActiveLimit(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 1)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	other := createScheduledUser(t, ctx, users, "other", testEmailB)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	otherConversation := createScheduledConversation(t, ctx, conversations, other.ID)

	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Morning reminder",
		Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateActive,
		CompiledPrompt: "Drink water", Timezone: scheduledTimezoneUTC, NextRunAt: new(time.Now().UTC().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == "" || task.Version != 1 {
		t.Fatalf("created task = %+v", task)
	}
	if _, err := repo.GetByID(ctx, task.ID, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner GetByID err = %v, want ErrNotFound", err)
	}
	if _, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: otherConversation.ID, Name: "Cross-owner conversation",
		Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateDraft,
		CompiledPrompt: "should not link", Timezone: scheduledTimezoneUTC,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner conversation Create err = %v, want ErrNotFound", err)
	}
	task.Name = "Updated reminder"
	task.CompiledPrompt = "Drink water after lunch"
	task.Version = 2
	updated, err := repo.Update(ctx, task, owner.ID)
	if err != nil || updated.Name != task.Name || updated.CompiledPrompt != task.CompiledPrompt || updated.Version != 2 {
		t.Fatalf("Update: %v %+v", err, updated)
	}
	if _, err := repo.Update(ctx, task, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner Update err = %v, want ErrNotFound", err)
	}
	if _, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Second", Kind: model.ScheduledTaskKindReminder,
		State: model.ScheduledTaskStateActive, CompiledPrompt: "second", Timezone: scheduledTimezoneUTC,
	}); !errors.Is(err, store.ErrActiveTaskLimit) {
		t.Fatalf("active-limit Create err = %v, want ErrActiveTaskLimit", err)
	}
	ordinary, err := conversations.Create(ctx, owner.ID, "Ordinary chat")
	if err != nil {
		t.Fatalf("create ordinary conversation: %v", err)
	}
	if _, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: ordinary.ID, Name: "Wrong kind", Kind: model.ScheduledTaskKindReminder,
		State: model.ScheduledTaskStateDraft, CompiledPrompt: "should not link", Timezone: scheduledTimezoneUTC,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ordinary conversation Create err = %v, want ErrNotFound", err)
	}
	task.ConversationID = ordinary.ID
	if _, err := repo.Update(ctx, task, owner.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ordinary conversation Update err = %v, want ErrNotFound", err)
	}
	task.ConversationID = conversation.ID
	if err := repo.SoftDelete(ctx, task.ID, owner.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := repo.GetByID(ctx, task.ID, owner.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted GetByID err = %v, want ErrNotFound", err)
	}
}

func TestScheduledTaskRepositoryClaimsRunsAndRetention(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	now := time.Now().UTC().Truncate(time.Second)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Due", Kind: model.ScheduledTaskKindReminder,
		State: model.ScheduledTaskStateActive, CompiledPrompt: "due", Timezone: scheduledTimezoneUTC, NextRunAt: new(now.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := repo.ClaimDue(ctx, now, 2)
	if err != nil || len(claims) != 1 {
		t.Fatalf("ClaimDue: %v %+v", err, claims)
	}
	claim := claims[0]
	if claim.Task.ID != task.ID || claim.Run.State != model.ScheduledTaskRunStateRunning || claim.Run.OccurrenceKey == "" {
		t.Fatalf("claim = %+v", claim)
	}
	if again, err := repo.ClaimDue(ctx, now, 2); err != nil || len(again) != 0 {
		t.Fatalf("second ClaimDue = %v %+v, want none", err, again)
	}
	if _, err := repo.CreateRun(ctx, owner.ID, claim.Run); !errors.Is(err, store.ErrOccurrenceTaken) {
		t.Fatalf("duplicate occurrence err = %v, want ErrOccurrenceTaken", err)
	}
	if err := repo.MarkDelivered(ctx, claim.Run.ID, owner.ID, "Here is your reminder"); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if unread, err := repo.UnreadCount(ctx, owner.ID); err != nil || unread != 1 {
		t.Fatalf("UnreadCount = %d, %v; want 1", unread, err)
	}
	if err := repo.MarkRead(ctx, task.ID, owner.ID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if unread, err := repo.UnreadCount(ctx, owner.ID); err != nil || unread != 0 {
		t.Fatalf("UnreadCount after read = %d, %v; want 0", unread, err)
	}

	noChange, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "old-no-change", ScheduledFor: now.AddDate(0, 0, -31),
		State: model.ScheduledTaskRunStateNoChange, FinishedAt: new(now.AddDate(0, 0, -31)),
	})
	if err != nil {
		t.Fatalf("CreateRun no change: %v", err)
	}
	if deleted, err := repo.DeleteExpiredNoChange(ctx, now.AddDate(0, 0, -30)); err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredNoChange = %d, %v; want 1", deleted, err)
	}
	if noChange.ID == 0 {
		t.Fatal("run id was not populated")
	}
}

func TestScheduledTaskRepositoryPausesAfterThreeFailures(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Failures", Kind: model.ScheduledTaskKindData,
		State: model.ScheduledTaskStateActive, CompiledPrompt: "query", Timezone: scheduledTimezoneUTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		run, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
			TaskID: task.ID, OccurrenceKey: "failure-" + string(rune('0'+i)), ScheduledFor: time.Now().UTC(), State: model.ScheduledTaskRunStateRunning,
		})
		if err != nil {
			t.Fatalf("CreateRun %d: %v", i, err)
		}
		if err := repo.RecordFailure(ctx, run.ID, owner.ID, "provider unavailable"); err != nil {
			t.Fatalf("RecordFailure %d: %v", i, err)
		}
	}
	updated, err := repo.GetByID(ctx, task.ID, owner.ID)
	if err != nil || updated.ConsecutiveFailures != 3 || updated.State != model.ScheduledTaskStatePaused {
		t.Fatalf("after failures: %v %+v", err, updated)
	}
}

func TestScheduledTaskRepositoryDeliveryResetsFailures(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Recovery", Kind: model.ScheduledTaskKindData,
		State: model.ScheduledTaskStateActive, CompiledPrompt: "query", Timezone: scheduledTimezoneUTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "failed", ScheduledFor: time.Now().UTC(), State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordFailure(ctx, failed.ID, owner.ID, "temporary provider error"); err != nil {
		t.Fatal(err)
	}
	delivered, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "delivered", ScheduledFor: time.Now().UTC(), State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkDelivered(ctx, delivered.ID, owner.ID, "recovered"); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetByID(ctx, task.ID, owner.ID)
	if err != nil || updated.ConsecutiveFailures != 0 {
		t.Fatalf("successful delivery did not reset failures: %v %+v", err, updated)
	}
}

func TestScheduledTaskRepositoryCreateRunRequiresTaskOwner(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	other := createScheduledUser(t, ctx, users, "other", testEmailB)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Private run", Kind: model.ScheduledTaskKindReminder,
		State: model.ScheduledTaskStateActive, CompiledPrompt: "private", Timezone: scheduledTimezoneUTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := model.ScheduledTaskRun{TaskID: task.ID, OccurrenceKey: "owner-scope", ScheduledFor: time.Now().UTC(), State: model.ScheduledTaskRunStateRunning}
	if _, err := repo.CreateRun(ctx, other.ID, run); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner CreateRun err = %v, want ErrNotFound", err)
	}
	if created, err := repo.CreateRun(ctx, owner.ID, run); err != nil || created.ID == 0 {
		t.Fatalf("owner CreateRun = %+v, %v", created, err)
	}
}

func TestScheduledTaskRepositoryActiveLimitSerializesConcurrentCreates(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 1)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	first := createScheduledConversation(t, ctx, conversations, owner.ID)
	second := createScheduledConversation(t, ctx, conversations, owner.ID)

	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire advisory lock connection: %v", err)
	}
	defer lockConn.Release()
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, owner.ID); err != nil {
		t.Fatalf("lock owner: %v", err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = lockConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, owner.ID)
		}
	}()

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, conversation := range []model.Conversation{first, second} {
		wg.Add(1)
		go func(conversationID string) {
			defer wg.Done()
			<-start
			_, err := repo.Create(ctx, model.ScheduledTask{
				UserID: owner.ID, ConversationID: conversationID, Name: "Concurrent", Kind: model.ScheduledTaskKindReminder,
				State: model.ScheduledTaskStateActive, CompiledPrompt: "concurrent", Timezone: scheduledTimezoneUTC,
			})
			results <- err
		}(conversation.ID)
	}
	close(start)
	waitForAdvisoryWaiters(t, ctx, pool, 2)
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, owner.ID); err != nil {
		t.Fatalf("unlock owner: %v", err)
	}
	locked = false
	wg.Wait()
	close(results)

	var successes, limited int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, store.ErrActiveTaskLimit):
			limited++
		default:
			t.Fatalf("concurrent Create error = %v", err)
		}
	}
	if successes != 1 || limited != 1 {
		t.Fatalf("concurrent Create outcomes: successes=%d limited=%d, want one each", successes, limited)
	}
}

func TestScheduledTaskRepositoryClaimDueSkipsLockedTask(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Claim once", Kind: model.ScheduledTaskKindReminder,
		State: model.ScheduledTaskStateActive, CompiledPrompt: "claim", Timezone: scheduledTimezoneUTC, NextRunAt: new(now.Add(-time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}

	const claimGate int64 = 7362519
	if _, err := pool.Exec(ctx, `CREATE FUNCTION scheduled_test_block_run_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
		PERFORM pg_advisory_xact_lock(7362519);
		RETURN NEW;
END;
$$`); err != nil {
		t.Fatalf("create claim gate function: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER scheduled_test_block_run_insert BEFORE INSERT ON scheduled_task_runs FOR EACH ROW EXECUTE FUNCTION scheduled_test_block_run_insert()`); err != nil {
		t.Fatalf("create claim gate trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS scheduled_test_block_run_insert ON scheduled_task_runs`)
		_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS scheduled_test_block_run_insert()`)
	})
	gateConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire claim gate connection: %v", err)
	}
	defer gateConn.Release()
	if _, err := gateConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, claimGate); err != nil {
		t.Fatalf("lock claim gate: %v", err)
	}
	gateLocked := true
	defer func() {
		if gateLocked {
			_, _ = gateConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, claimGate)
		}
	}()

	first := make(chan struct {
		claims []model.ClaimedScheduledTask
		err    error
	}, 1)
	go func() {
		claims, err := repo.ClaimDue(ctx, now, 1)
		first <- struct {
			claims []model.ClaimedScheduledTask
			err    error
		}{claims, err}
	}()
	waitForAdvisoryWaiters(t, ctx, pool, 1)
	second, err := repo.ClaimDue(ctx, now, 1)
	if err != nil {
		t.Fatalf("second ClaimDue: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second ClaimDue claimed %d tasks, want 0", len(second))
	}
	if _, err := gateConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, claimGate); err != nil {
		t.Fatalf("unlock claim gate: %v", err)
	}
	gateLocked = false
	firstResult := <-first
	if firstResult.err != nil || len(firstResult.claims) != 1 {
		t.Fatalf("first ClaimDue = %+v, %v; want one claim", firstResult.claims, firstResult.err)
	}
}

func waitForAdvisoryWaiters(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatalf("count advisory waiters: %v", err)
		}
		if waiting >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d advisory lock waiters", want)
}

func createScheduledUser(t *testing.T, ctx context.Context, users *store.UserRepository, username, email string) model.User {
	t.Helper()
	u, err := users.Create(ctx, model.User{Username: username, Email: email, PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func createScheduledConversation(t *testing.T, ctx context.Context, conversations *store.ConversationRepository, userID int64) model.Conversation {
	t.Helper()
	c, err := conversations.CreateWithKind(ctx, userID, "Scheduled", model.ConversationKindScheduled)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return c
}
