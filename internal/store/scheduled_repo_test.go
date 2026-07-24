package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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
	scheduledCompiledQuery  = "query"
	scheduledConfirmedName  = "Confirmed"
	scheduledOldPrompt      = "old prompt"
	scheduledSafeResult     = "Safe result"
	scheduledDailyRRULE     = "FREQ=DAILY"
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
		DeliveryPolicy: "always", InitialRun: "wait", StaticMessage: "Drink water now.",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == "" || task.Version != 1 {
		t.Fatalf("created task = %+v", task)
	}
	if task.DeliveryPolicy != "always" || task.InitialRun != "wait" || task.StaticMessage != "Drink water now." {
		t.Fatalf("proposal fields did not round trip: %+v", task)
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

func TestScheduledTaskRepositoryBoundsListResponses(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 200)
	owner := createScheduledUser(t, ctx, users, "bounded-owner", "bounded@example.com")

	var newest model.ScheduledTask
	var activeID string
	var unreadID string
	for i := range 101 {
		conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
		state := model.ScheduledTaskStateCompleted
		var nextRunAt *time.Time
		if i == 0 {
			state = model.ScheduledTaskStateActive
			nextRunAt = new(time.Now().UTC().Add(time.Hour))
		}
		task, err := repo.Create(ctx, model.ScheduledTask{
			UserID: owner.ID, ConversationID: conversation.ID, Name: "Task " + strconv.Itoa(i),
			Kind: model.ScheduledTaskKindReminder, State: state,
			CompiledPrompt: "bounded", Timezone: scheduledTimezoneUTC, NextRunAt: nextRunAt,
		})
		if err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
		if _, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
			TaskID: task.ID, OccurrenceKey: "run-" + strconv.Itoa(i),
			ScheduledFor: time.Now().UTC(), State: model.ScheduledTaskRunStateCompleted, Unread: i == 1,
		}); err != nil {
			t.Fatalf("create summary run %d: %v", i, err)
		}
		if i == 0 {
			activeID = task.ID
		}
		if i == 1 {
			unreadID = task.ID
		}
		newest = task
	}

	tasks, err := repo.ListByUser(ctx, owner.ID, 0, 100)
	if err != nil || len(tasks) != 100 {
		t.Fatalf("bounded tasks = %d, %v; want 100", len(tasks), err)
	}
	taskIDs := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		taskIDs[task.ID] = true
	}
	if !taskIDs[activeID] || !taskIDs[unreadID] {
		t.Fatalf("priority tasks missing: active=%t unread=%t", taskIDs[activeID], taskIDs[unreadID])
	}
	summaries, err := repo.ListRunSummaries(ctx, owner.ID, 0, 100)
	if err != nil || len(summaries) != 100 {
		t.Fatalf("bounded summaries = %d, %v; want 100", len(summaries), err)
	}
	for i := 101; i < 201; i++ {
		if _, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
			TaskID: newest.ID, OccurrenceKey: "run-" + strconv.Itoa(i),
			ScheduledFor: time.Now().UTC(), State: model.ScheduledTaskRunStateCompleted,
		}); err != nil {
			t.Fatalf("create history run %d: %v", i, err)
		}
	}
	runs, err := repo.ListRuns(ctx, newest.ID, owner.ID)
	if err != nil || len(runs) != 100 {
		t.Fatalf("bounded runs = %d, %v; want 100", len(runs), err)
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
	if claim.Task.ID != task.ID || claim.Run.State != model.ScheduledTaskRunStateRunning || claim.Run.OccurrenceKey == "" || !claim.FirstRun {
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
	if _, err := repo.RunNow(ctx, owner.ID, task.ID, "manual:not-first", now); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	repeated, err := repo.ClaimDue(ctx, now, 1)
	if err != nil || len(repeated) != 1 || repeated[0].FirstRun {
		t.Fatalf("repeat ClaimDue: %v %+v", err, repeated)
	}
	if err := repo.MarkDelivered(ctx, repeated[0].Run.ID, owner.ID, "Again"); err != nil {
		t.Fatalf("repeat MarkDelivered: %v", err)
	}
	if unread, err := repo.UnreadCount(ctx, owner.ID); err != nil || unread != 2 {
		t.Fatalf("UnreadCount = %d, %v; want 2", unread, err)
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

func TestScheduledTaskRepositoryRunSummaries(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "summary-owner", "summary@example.com")
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Summary",
		Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateCompleted,
		CompiledPrompt: "summary", Timezone: scheduledTimezoneUTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	var recent model.ScheduledTaskRun
	for i := range 2 {
		recent, err = repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
			TaskID: task.ID, OccurrenceKey: "summary-" + strconv.Itoa(i),
			ScheduledFor: time.Now().UTC().Add(time.Duration(i) * time.Minute),
			State:        model.ScheduledTaskRunStateDelivered,
			Unread:       true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	summaries, err := repo.ListRunSummaries(ctx, owner.ID, 0, 100)
	if err != nil || len(summaries) != 1 || summaries[0].TaskID != task.ID ||
		summaries[0].UnreadCount != 2 || summaries[0].RecentRun == nil ||
		summaries[0].RecentRun.ID != recent.ID {
		t.Fatalf("run summaries = %+v, err=%v", summaries, err)
	}
}

func TestScheduledTaskRepositoryListsOnlyStaleRunningOccurrences(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "stale-owner", "stale-owner@example.com")
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	now := time.Now().UTC().Truncate(time.Second)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Recover",
		Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateActive,
		CompiledPrompt: "recover", Timezone: scheduledTimezoneUTC,
		DTStart: new(now.Add(-48 * time.Hour)), RRULE: scheduledDailyRRULE,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldStarted := now.Add(-2 * time.Hour)
	oldRun, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "old-running", ScheduledFor: oldStarted,
		State: model.ScheduledTaskRunStateRunning, StartedAt: &oldStarted,
	})
	if err != nil {
		t.Fatal(err)
	}
	freshStarted := now.Add(-time.Minute)
	if _, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "fresh-running", ScheduledFor: freshStarted,
		State: model.ScheduledTaskRunStateRunning, StartedAt: &freshStarted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "pending", ScheduledFor: now,
		State: model.ScheduledTaskRunStatePending,
	}); err != nil {
		t.Fatal(err)
	}

	stale, err := repo.ListStaleRunning(ctx, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListStaleRunning: %v", err)
	}
	if len(stale) != 1 || stale[0].Run.ID != oldRun.ID || stale[0].Task.ID != task.ID ||
		stale[0].Username != owner.Username {
		t.Fatalf("stale claims = %+v, want old owner-scoped run", stale)
	}
	if none, err := repo.ListStaleRunning(ctx, now.Add(-time.Hour), 0); err != nil || none != nil {
		t.Fatalf("zero-limit stale claims = %+v, %v; want nil", none, err)
	}

	if err := repo.SoftDelete(ctx, task.ID, owner.ID); !errors.Is(err, store.ErrScheduledRunInProgress) {
		t.Fatalf("SoftDelete running task err=%v, want ErrScheduledRunInProgress", err)
	}
	stillActive, err := repo.ListStaleRunning(ctx, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListStaleRunning active task: %v", err)
	}
	if len(stillActive) != 1 || stillActive[0].Run.ID != oldRun.ID ||
		stillActive[0].Task.State != model.ScheduledTaskStateActive ||
		stillActive[0].Task.DeletedAt != nil {
		t.Fatalf("stale claims after rejected delete = %+v, want active task run", stillActive)
	}
	if err := repo.FinishFailure(ctx, model.ScheduledExecutionFailure{
		RunID: oldRun.ID, UserID: owner.ID, Code: "execution_interrupted",
		IncrementFailures: true, TaskState: model.ScheduledTaskStateActive,
	}); err != nil {
		t.Fatalf("FinishFailure stale run: %v", err)
	}
	var runState, taskState string
	if err := pool.QueryRow(ctx,
		`SELECT run.state, task.state
		 FROM scheduled_task_runs AS run
		 JOIN scheduled_tasks AS task ON task.id = run.task_id
		 WHERE run.id = $1`, oldRun.ID,
	).Scan(&runState, &taskState); err != nil {
		t.Fatalf("read recovered stale run: %v", err)
	}
	if runState != model.ScheduledTaskRunStateFailed || taskState != model.ScheduledTaskStateActive {
		t.Fatalf("recovered stale state = run %q task %q", runState, taskState)
	}
}

func TestScheduledTaskRepositoryDraftRevisionProposalCAS(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Version: 1, Name: scheduledConfirmedName,
		Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateActive,
		CompiledPrompt: scheduledOldPrompt, Timezone: scheduledTimezoneUTC, NextRunAt: new(time.Now().UTC().Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := repo.BeginDraftRevision(ctx, task.ID, owner.ID, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	if draft.State != model.ScheduledTaskStateDraft || draft.Version != 2 || draft.CompiledPrompt != "" || draft.NextRunAt != nil {
		t.Fatalf("draft revision = %+v", draft)
	}
	stale := draft
	stale.Version = 1
	stale.CompiledPrompt = "stale prompt"
	if _, err := repo.SaveProposal(ctx, stale, owner.ID, 1); !errors.Is(err, store.ErrStaleScheduledProposal) {
		t.Fatalf("stale SaveProposal err=%v", err)
	}
	draft.CompiledPrompt = "new prompt"
	draft.Name = "Edited"
	saved, err := repo.SaveProposal(ctx, draft, owner.ID, 2)
	if err != nil || saved.CompiledPrompt != "new prompt" {
		t.Fatalf("SaveProposal = %+v, %v", saved, err)
	}
	if _, err := repo.ConfirmProposal(ctx, task.ID, owner.ID, 1, time.Now().UTC()); !errors.Is(err, store.ErrStaleScheduledProposal) {
		t.Fatalf("stale ConfirmProposal err=%v", err)
	}
	confirmed, err := repo.ConfirmProposal(ctx, task.ID, owner.ID, 2, time.Now().UTC())
	if err != nil || confirmed.State != model.ScheduledTaskStateActive {
		t.Fatalf("ConfirmProposal = %+v, %v", confirmed, err)
	}

	raceConversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	raceTask, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: raceConversation.ID, Version: 1, Name: "Concurrent edit",
		Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateActive,
		CompiledPrompt: "race prompt", Timezone: scheduledTimezoneUTC, NextRunAt: new(time.Now().UTC().Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	type revisionResult struct {
		task model.ScheduledTask
		err  error
	}
	start := make(chan struct{})
	results := make(chan revisionResult, 2)
	for range 2 {
		go func() {
			<-start
			revised, revisionErr := repo.BeginDraftRevision(ctx, raceTask.ID, owner.ID, raceTask.Version)
			results <- revisionResult{task: revised, err: revisionErr}
		}()
	}
	close(start)
	var succeeded, staleCount int
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			succeeded++
			if result.task.Version != raceTask.Version+1 {
				t.Fatalf("winning revision = %+v", result.task)
			}
		case errors.Is(result.err, store.ErrStaleScheduledProposal):
			staleCount++
		default:
			t.Fatalf("concurrent revision err=%v", result.err)
		}
	}
	if succeeded != 1 || staleCount != 1 {
		t.Fatalf("concurrent revisions: succeeded=%d stale=%d", succeeded, staleCount)
	}
	persisted, err := repo.GetByID(ctx, raceTask.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Version != raceTask.Version+1 || persisted.State != model.ScheduledTaskStateDraft {
		t.Fatalf("persisted concurrent revision = %+v", persisted)
	}
}

func TestScheduledTaskRepositoryLifecycleCASDoesNotOverwriteDraftRevision(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)

	for _, tc := range []struct {
		name       string
		state      string
		transition func(context.Context, string, int64, int) (model.ScheduledTask, error)
	}{
		{
			name:  "pause loses to edit",
			state: model.ScheduledTaskStateActive,
			transition: func(ctx context.Context, id string, ownerID int64, version int) (model.ScheduledTask, error) {
				return repo.Pause(ctx, id, ownerID, version)
			},
		},
		{
			name:  "resume loses to edit",
			state: model.ScheduledTaskStatePaused,
			transition: func(ctx context.Context, id string, ownerID int64, version int) (model.ScheduledTask, error) {
				return repo.Resume(ctx, id, ownerID, version, time.Now().UTC().Add(time.Hour))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
			task, err := repo.Create(ctx, model.ScheduledTask{
				UserID: owner.ID, ConversationID: conversation.ID, Version: 1, Name: scheduledConfirmedName,
				Kind: model.ScheduledTaskKindReminder, State: tc.state, CompiledPrompt: scheduledOldPrompt,
				Timezone: scheduledTimezoneUTC, RRULE: scheduledDailyRRULE, DTStart: new(time.Now().UTC()),
				AuthorizedTools: []string{"search"}, StaticMessage: "old static message",
				NextRunAt: new(time.Now().UTC().Add(time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}

			staleVersion := task.Version
			draft, err := repo.BeginDraftRevision(ctx, task.ID, owner.ID, task.Version)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tc.transition(ctx, task.ID, owner.ID, staleVersion); !errors.Is(err, store.ErrInvalidScheduledTaskState) {
				t.Fatalf("stale lifecycle transition err=%v", err)
			}
			persisted, err := repo.GetByID(ctx, task.ID, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.State != model.ScheduledTaskStateDraft || persisted.Version != draft.Version ||
				persisted.CompiledPrompt != "" || persisted.NextRunAt != nil ||
				len(persisted.AuthorizedTools) != 0 || persisted.StaticMessage != "" {
				t.Fatalf("draft revision overwritten: %+v", persisted)
			}

			raceConversation := createScheduledConversation(t, ctx, conversations, owner.ID)
			raceTask, err := repo.Create(ctx, model.ScheduledTask{
				UserID: owner.ID, ConversationID: raceConversation.ID, Version: 1, Name: "Racing",
				Kind: model.ScheduledTaskKindReminder, State: tc.state, CompiledPrompt: "race prompt",
				Timezone: scheduledTimezoneUTC, RRULE: scheduledDailyRRULE, DTStart: new(time.Now().UTC()),
				AuthorizedTools: []string{"search"}, StaticMessage: "race static message",
				NextRunAt: new(time.Now().UTC().Add(time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			editResult := make(chan error, 1)
			transitionResult := make(chan error, 1)
			go func() {
				<-start
				_, err := repo.BeginDraftRevision(ctx, raceTask.ID, owner.ID, raceTask.Version)
				editResult <- err
			}()
			go func() {
				<-start
				_, err := tc.transition(ctx, raceTask.ID, owner.ID, raceTask.Version)
				transitionResult <- err
			}()
			close(start)
			if err := <-editResult; err != nil {
				t.Fatalf("concurrent edit err=%v", err)
			}
			if err := <-transitionResult; err != nil && !errors.Is(err, store.ErrInvalidScheduledTaskState) {
				t.Fatalf("concurrent lifecycle err=%v", err)
			}
			persisted, err = repo.GetByID(ctx, raceTask.ID, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.State != model.ScheduledTaskStateDraft || persisted.Version != raceTask.Version+1 ||
				persisted.CompiledPrompt != "" || persisted.NextRunAt != nil {
				t.Fatalf("concurrent draft revision overwritten: %+v", persisted)
			}
		})
	}
}

func TestScheduledTaskRepositoryRejectsDraftRevisionWhileRunInProgress(t *testing.T) {
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
		UserID: owner.ID, ConversationID: conversation.ID, Version: 1, Name: scheduledConfirmedName,
		Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateActive,
		CompiledPrompt: scheduledOldPrompt, Timezone: scheduledTimezoneUTC, NextRunAt: new(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := repo.RunNow(ctx, owner.ID, task.ID, "manual:edit-conflict", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BeginDraftRevision(ctx, task.ID, owner.ID, task.Version); !errors.Is(err, store.ErrScheduledRunInProgress) {
		t.Fatalf("pending run edit err=%v", err)
	}

	claims, err := repo.ClaimDue(ctx, now, 1)
	if err != nil || len(claims) != 1 || claims[0].Run.ID != pending.ID {
		t.Fatalf("ClaimDue: claims=%+v err=%v", claims, err)
	}
	if _, err := repo.BeginDraftRevision(ctx, task.ID, owner.ID, task.Version); !errors.Is(err, store.ErrScheduledRunInProgress) {
		t.Fatalf("running run edit err=%v", err)
	}
	if err := repo.MarkDelivered(ctx, pending.ID, owner.ID, "done"); err != nil {
		t.Fatal(err)
	}
	draft, err := repo.BeginDraftRevision(ctx, task.ID, owner.ID, task.Version)
	if err != nil {
		t.Fatalf("terminal run edit err=%v", err)
	}
	if draft.State != model.ScheduledTaskStateDraft || draft.Version != task.Version+1 {
		t.Fatalf("draft after terminal run=%+v", draft)
	}
}

func TestScheduledTaskRepositoryRejectsLifecycleChangesWhileRunInProgress(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	type lifecycleChange func(*store.ScheduledTaskRepository, model.ScheduledTask, int64, time.Time) error
	changes := []struct {
		name string
		run  lifecycleChange
	}{
		{
			name: "run now",
			run: func(repo *store.ScheduledTaskRepository, task model.ScheduledTask, userID int64, now time.Time) error {
				_, err := repo.RunNow(ctx, userID, task.ID, "manual:overlap", now)
				return err
			},
		},
		{
			name: "pause",
			run: func(repo *store.ScheduledTaskRepository, task model.ScheduledTask, userID int64, _ time.Time) error {
				_, err := repo.Pause(ctx, task.ID, userID, task.Version)
				return err
			},
		},
		{
			name: "soft delete",
			run: func(repo *store.ScheduledTaskRepository, task model.ScheduledTask, userID int64, _ time.Time) error {
				return repo.SoftDelete(ctx, task.ID, userID)
			},
		},
		{
			name: "pause by conversation",
			run: func(repo *store.ScheduledTaskRepository, task model.ScheduledTask, userID int64, _ time.Time) error {
				_, err := repo.PauseByConversation(ctx, task.ConversationID, userID)
				return err
			},
		},
	}

	for _, runState := range []string{model.ScheduledTaskRunStatePending, model.ScheduledTaskRunStateRunning} {
		for _, change := range changes {
			t.Run(runState+"/"+change.name, func(t *testing.T) {
				testutil.CleanTables(t, pool)
				users := store.NewUserRepository(pool)
				conversations := store.NewConversationRepository(pool)
				repo := store.NewScheduledTaskRepository(pool, 10)
				owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
				conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
				now := time.Now().UTC().Truncate(time.Second)
				task, err := repo.Create(ctx, model.ScheduledTask{
					UserID: owner.ID, ConversationID: conversation.ID, Version: 1, Name: scheduledConfirmedName,
					Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateActive,
					CompiledPrompt: scheduledOldPrompt, Timezone: scheduledTimezoneUTC,
					NextRunAt: new(now.Add(time.Hour)),
				})
				if err != nil {
					t.Fatal(err)
				}
				run, err := repo.RunNow(ctx, owner.ID, task.ID, "manual:in-progress", now)
				if err != nil {
					t.Fatal(err)
				}
				if runState == model.ScheduledTaskRunStateRunning {
					claims, claimErr := repo.ClaimDue(ctx, now, 1)
					if claimErr != nil || len(claims) != 1 || claims[0].Run.ID != run.ID {
						t.Fatalf("ClaimDue: claims=%+v err=%v", claims, claimErr)
					}
				}
				before, err := repo.GetByID(ctx, task.ID, owner.ID)
				if err != nil {
					t.Fatal(err)
				}

				if err := change.run(repo, before, owner.ID, now); !errors.Is(err, store.ErrScheduledRunInProgress) {
					t.Fatalf("lifecycle change err=%v, want ErrScheduledRunInProgress", err)
				}
				after, err := repo.GetByID(ctx, task.ID, owner.ID)
				if err != nil {
					t.Fatalf("task changed visibility: %v", err)
				}
				sameNextRun := after.NextRunAt == nil && before.NextRunAt == nil ||
					after.NextRunAt != nil && before.NextRunAt != nil && after.NextRunAt.Equal(*before.NextRunAt)
				if after.State != before.State || after.Version != before.Version || !sameNextRun {
					t.Fatalf("task changed while run in progress: before=%+v after=%+v", before, after)
				}
				runs, err := repo.ListRuns(ctx, task.ID, owner.ID)
				if err != nil || len(runs) != 1 || runs[0].ID != run.ID || runs[0].State != runState {
					t.Fatalf("runs after rejected lifecycle change=%+v err=%v", runs, err)
				}
			})
		}
	}
}

func TestScheduledTaskRepositoryRunNowIsAtomicAndClaimsPendingRunOnce(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 1)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	activeConversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	pausedConversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	if _, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: activeConversation.ID, Kind: model.ScheduledTaskKindReminder,
		State: model.ScheduledTaskStateActive, CompiledPrompt: "active", Timezone: scheduledTimezoneUTC,
	}); err != nil {
		t.Fatal(err)
	}
	paused, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: pausedConversation.ID, Kind: model.ScheduledTaskKindReminder,
		State: model.ScheduledTaskStatePaused, CompiledPrompt: "paused", Timezone: scheduledTimezoneUTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := repo.RunNow(ctx, owner.ID, paused.ID, "manual:rollback", now); !errors.Is(err, store.ErrActiveTaskLimit) {
		t.Fatalf("RunNow active-limit err=%v", err)
	}
	unchanged, err := repo.GetByID(ctx, paused.ID, owner.ID)
	if err != nil || unchanged.State != model.ScheduledTaskStatePaused || unchanged.NextRunAt != nil {
		t.Fatalf("rollback task=%+v err=%v", unchanged, err)
	}
	if runs, err := repo.ListRuns(ctx, paused.ID, owner.ID); err != nil || len(runs) != 0 {
		t.Fatalf("rollback runs=%+v err=%v", runs, err)
	}

	repo = store.NewScheduledTaskRepository(pool, 2)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION scheduled_test_fail_manual_due() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.next_run_at IS NOT NULL AND OLD.next_run_at IS DISTINCT FROM NEW.next_run_at THEN
		RAISE EXCEPTION 'forced due update failure';
	END IF;
	RETURN NEW;
END;
$$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER scheduled_test_fail_manual_due BEFORE UPDATE ON scheduled_tasks FOR EACH ROW EXECUTE FUNCTION scheduled_test_fail_manual_due()`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RunNow(ctx, owner.ID, paused.ID, "manual:update-rollback", now); err == nil {
		t.Fatal("RunNow update failure = nil")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER scheduled_test_fail_manual_due ON scheduled_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION scheduled_test_fail_manual_due()`); err != nil {
		t.Fatal(err)
	}
	if runs, err := repo.ListRuns(ctx, paused.ID, owner.ID); err != nil || len(runs) != 0 {
		t.Fatalf("update rollback runs=%+v err=%v", runs, err)
	}
	pending, err := repo.RunNow(ctx, owner.ID, paused.ID, "manual:claim-once", now)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		claims []model.ClaimedScheduledTask
		err    error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			claims, err := repo.ClaimDue(ctx, now, 1)
			results <- result{claims: claims, err: err}
		})
	}
	wg.Wait()
	close(results)
	var claims []model.ClaimedScheduledTask
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		claims = append(claims, result.claims...)
	}
	if len(claims) != 1 || claims[0].Run.ID != pending.ID || claims[0].Run.OccurrenceKey != pending.OccurrenceKey || claims[0].Run.State != model.ScheduledTaskRunStateRunning {
		t.Fatalf("claims=%+v pending=%+v", claims, pending)
	}
}

func TestScheduledTaskRepositorySanitizesPublicFailureCode(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Kind: model.ScheduledTaskKindData,
		State: model.ScheduledTaskStateActive, CompiledPrompt: scheduledCompiledQuery, Timezone: scheduledTimezoneUTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "failure-code", ScheduledFor: time.Now().UTC(), State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Error != "" {
		t.Fatalf("new running run error=%q, want empty", run.Error)
	}
	if err := repo.RecordFailure(ctx, run.ID, owner.ID, "provider failed with secret=abc"); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRuns(ctx, task.ID, owner.ID)
	if err != nil || len(runs) != 1 || runs[0].Error != "execution_failed" || len(runs[0].Error) > 64 {
		t.Fatalf("runs=%+v err=%v", runs, err)
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
		State: model.ScheduledTaskStateActive, CompiledPrompt: scheduledCompiledQuery, Timezone: scheduledTimezoneUTC,
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
		State: model.ScheduledTaskStateActive, CompiledPrompt: scheduledCompiledQuery, Timezone: scheduledTimezoneUTC,
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

func TestScheduledTaskRepositoryFinishSuccessIsAtomicAndCASProtected(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	other := createScheduledUser(t, ctx, users, "other", testEmailB)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	now := time.Now().UTC().Truncate(time.Second)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Atomic", Kind: model.ScheduledTaskKindData,
		State: model.ScheduledTaskStateActive, CompiledPrompt: scheduledCompiledQuery, Timezone: scheduledTimezoneUTC,
		ConsecutiveFailures: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "atomic", ScheduledFor: now, State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	next := now.Add(time.Hour)
	success := model.ScheduledExecutionSuccess{
		RunID: run.ID, UserID: owner.ID, ConversationID: conversation.ID,
		RunState: model.ScheduledTaskRunStateDelivered, TaskState: model.ScheduledTaskStateActive,
		Content: scheduledSafeResult, Unread: true, MonitoringState: json.RawMessage(`{"cursor":2}`), NextRunAt: &next,
	}
	if err := repo.FinishSuccess(ctx, success); err != nil {
		t.Fatal(err)
	}
	history, err := messages.ListByConversation(ctx, conversation.ID)
	if err != nil || len(history) != 1 || history[0].Role != model.MsgRoleAssistant || history[0].Content != scheduledSafeResult {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	runs, err := repo.ListRuns(ctx, task.ID, owner.ID)
	if err != nil || len(runs) != 1 || runs[0].State != model.ScheduledTaskRunStateDelivered || !runs[0].Unread || runs[0].Result != scheduledSafeResult {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	updated, err := repo.GetByID(ctx, task.ID, owner.ID)
	if err != nil || updated.ConsecutiveFailures != 0 || string(updated.MonitoringState) != `{"cursor": 2}` ||
		updated.NextRunAt == nil || !updated.NextRunAt.Equal(next) {
		t.Fatalf("task=%+v err=%v", updated, err)
	}
	if err := repo.FinishSuccess(ctx, success); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second finish err=%v", err)
	}
	freshRun, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "other-owner", ScheduledFor: now, State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	success.RunID, success.UserID = freshRun.ID, other.ID
	if err := repo.FinishSuccess(ctx, success); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner finish err=%v", err)
	}
	history, _ = messages.ListByConversation(ctx, conversation.ID)
	if len(history) != 1 {
		t.Fatalf("partial message inserted: %+v", history)
	}
}

func TestScheduledTaskRepositoryFinishNoChangeAndFailureTransitions(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	repo := store.NewScheduledTaskRepository(pool, 10)
	owner := createScheduledUser(t, ctx, users, "owner", testEmailO)
	conversation := createScheduledConversation(t, ctx, conversations, owner.ID)
	now := time.Now().UTC().Truncate(time.Second)
	task, err := repo.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: conversation.ID, Name: "Monitor", Kind: model.ScheduledTaskKindMonitoring,
		State: model.ScheduledTaskStateActive, CompiledPrompt: scheduledCompiledQuery, Timezone: scheduledTimezoneUTC,
		ConsecutiveFailures: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	noChange, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "no-change", ScheduledFor: now, State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	next := now.Add(time.Hour)
	if err := repo.FinishSuccess(ctx, model.ScheduledExecutionSuccess{
		RunID: noChange.ID, UserID: owner.ID, ConversationID: conversation.ID,
		RunState: model.ScheduledTaskRunStateNoChange, TaskState: model.ScheduledTaskStateActive,
		MonitoringState: json.RawMessage(`{"baseline":true}`), NextRunAt: &next,
	}); err != nil {
		t.Fatal(err)
	}
	if history, err := messages.ListByConversation(ctx, conversation.ID); err != nil || len(history) != 0 {
		t.Fatalf("no-change history=%+v err=%v", history, err)
	}
	failed, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "failed-cas", ScheduledFor: now, State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishFailure(ctx, model.ScheduledExecutionFailure{
		RunID: failed.ID, UserID: owner.ID, Code: "raw provider error!", TaskState: model.ScheduledTaskStatePaused, Pause: true, IncrementFailures: true,
	}); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRuns(ctx, task.ID, owner.ID)
	if err != nil || runs[0].Error != "execution_failed" || runs[0].State != model.ScheduledTaskRunStateFailed {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	updated, err := repo.GetByID(ctx, task.ID, owner.ID)
	if err != nil || updated.State != model.ScheduledTaskStatePaused || updated.ConsecutiveFailures != 1 {
		t.Fatalf("task=%+v err=%v", updated, err)
	}
	if err := repo.FinishFailure(ctx, model.ScheduledExecutionFailure{RunID: failed.ID, UserID: owner.ID, Code: "again"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second failure err=%v", err)
	}
}

func TestScheduledTaskRepositoryFinishPreservesPauseAndMissingToolFailureCount(t *testing.T) {
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
		UserID: owner.ID, ConversationID: conversation.ID, Version: 1, Name: "Paused",
		Kind: model.ScheduledTaskKindData, State: model.ScheduledTaskStateActive,
		CompiledPrompt: scheduledCompiledQuery, Timezone: scheduledTimezoneUTC, ConsecutiveFailures: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "paused-success", ScheduledFor: now, State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Pause(ctx, task.ID, owner.ID, task.Version); !errors.Is(err, store.ErrScheduledRunInProgress) {
		t.Fatalf("pause running task err=%v, want ErrScheduledRunInProgress", err)
	}
	next := now.Add(time.Hour)
	if err := repo.FinishSuccess(ctx, model.ScheduledExecutionSuccess{
		RunID: run.ID, UserID: owner.ID, ConversationID: conversation.ID,
		RunState: model.ScheduledTaskRunStateDelivered, TaskState: model.ScheduledTaskStateActive,
		Content: "Finished current occurrence", Unread: true, MonitoringState: json.RawMessage(`{}`), NextRunAt: &next,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Pause(ctx, task.ID, owner.ID, task.Version); err != nil {
		t.Fatalf("pause after terminal run: %v", err)
	}
	paused, err := repo.GetByID(ctx, task.ID, owner.ID)
	if err != nil || paused.State != model.ScheduledTaskStatePaused || paused.NextRunAt != nil {
		t.Fatalf("paused task=%+v err=%v", paused, err)
	}
	paused.ConsecutiveFailures = 2
	paused, err = repo.Update(ctx, paused, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	missingRun, err := repo.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "missing-tool", ScheduledFor: now, State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishFailure(ctx, model.ScheduledExecutionFailure{
		RunID: missingRun.ID, UserID: owner.ID, Code: "missing_tool",
		TaskState: model.ScheduledTaskStatePaused, Pause: true, IncrementFailures: false,
	}); err != nil {
		t.Fatal(err)
	}
	paused, err = repo.GetByID(ctx, task.ID, owner.ID)
	if err != nil || paused.State != model.ScheduledTaskStatePaused || paused.ConsecutiveFailures != 2 {
		t.Fatalf("missing-tool task=%+v err=%v", paused, err)
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
