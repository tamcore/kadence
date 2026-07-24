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
	if _, err := repo.CreateRun(ctx, claim.Run); !errors.Is(err, store.ErrOccurrenceTaken) {
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

	noChange, err := repo.CreateRun(ctx, model.ScheduledTaskRun{
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
		run, err := repo.CreateRun(ctx, model.ScheduledTaskRun{
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
	failed, err := repo.CreateRun(ctx, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "failed", ScheduledFor: time.Now().UTC(), State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordFailure(ctx, failed.ID, owner.ID, "temporary provider error"); err != nil {
		t.Fatal(err)
	}
	delivered, err := repo.CreateRun(ctx, model.ScheduledTaskRun{
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
