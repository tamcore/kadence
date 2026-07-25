package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const testHandoffTitle = "Task"

func TestChatScheduledHandoffCreateOrGetDraftIsIdempotent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	repo := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "handoff-create", "handoff-create@example.com")
	source, err := conversations.Create(ctx, owner.ID, "Source")
	if err != nil {
		t.Fatal(err)
	}
	message, err := messages.AddChatUser(ctx, source.ID, "Schedule this")
	if err != nil {
		t.Fatal(err)
	}
	in := store.CreateChatHandoffInput{
		UserID: owner.ID, SourceConversationID: source.ID, SourceUserMessageID: message.ID,
		SourceContentFingerprint: handoffFingerprint(1), InvocationOrdinal: 1, Title: "Schedule this", Timezone: scheduledTimezoneUTC,
	}
	created, fresh, err := repo.CreateOrGetDraft(ctx, in)
	if err != nil || !fresh {
		t.Fatalf("first CreateOrGetDraft fresh=%t err=%v", fresh, err)
	}
	if created.Handoff.ArtifactState != model.ScheduledHandoffStateCreating || created.Task == nil ||
		created.Task.State != model.ScheduledTaskStateDraft || created.Task.ConversationID == "" {
		t.Fatalf("created handoff = %+v", created)
	}
	reused, fresh, err := repo.CreateOrGetDraft(ctx, in)
	if err != nil || fresh || reused.Handoff.ID != created.Handoff.ID || reused.Task == nil || reused.Task.ID != created.Task.ID {
		t.Fatalf("reused handoff = %+v fresh=%t err=%v", reused, fresh, err)
	}
	in.InvocationOrdinal = 2
	second, fresh, err := repo.CreateOrGetDraft(ctx, in)
	if err != nil || !fresh || second.Handoff.ID == created.Handoff.ID || second.Task == nil || second.Task.ID == created.Task.ID {
		t.Fatalf("second ordinal = %+v fresh=%t err=%v", second, fresh, err)
	}
	var taskCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE user_id = $1`, owner.ID).Scan(&taskCount); err != nil || taskCount != 2 {
		t.Fatalf("draft task count=%d err=%v, want 2", taskCount, err)
	}
}

func TestChatScheduledHandoffRejectsNonUserChatSourceMessages(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	repo := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "handoff-source-role", "handoff-source-role@example.com")
	source, err := conversations.Create(ctx, owner.ID, "Source")
	if err != nil {
		t.Fatal(err)
	}
	user, err := messages.AddChatUser(ctx, source.ID, "Schedule this")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := messages.AddChatAssistantIfLatestUser(ctx, source.ID, user, "I can help", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	nonChatUser, err := messages.AddDefinition(ctx, source.ID, model.MsgRoleUser, "Not an ordinary chat turn")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		messageID   int64
		fingerprint byte
	}{
		{name: "assistant", messageID: assistant.ID, fingerprint: 70},
		{name: "non-chat-purpose", messageID: nonChatUser.ID, fingerprint: 71},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := repo.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{
				UserID: owner.ID, SourceConversationID: source.ID, SourceUserMessageID: tc.messageID,
				SourceContentFingerprint: handoffFingerprint(tc.fingerprint), InvocationOrdinal: 1,
				Title: testHandoffTitle, Timezone: scheduledTimezoneUTC,
			})
			if !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("CreateOrGetDraft err=%v, want ErrNotFound", err)
			}
		})
	}
	var handoffs, tasks int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE user_id = $1`, owner.ID).Scan(&handoffs); err != nil || handoffs != 0 {
		t.Fatalf("handoff count=%d err=%v", handoffs, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE user_id = $1`, owner.ID).Scan(&tasks); err != nil || tasks != 0 {
		t.Fatalf("task count=%d err=%v", tasks, err)
	}
}

func TestChatScheduledHandoffConcurrentSlotCreationCreatesOneDraft(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	repo := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "handoff-race", "handoff-race@example.com")
	source, _ := conversations.Create(ctx, owner.ID, "Source")
	message, _ := messages.AddChatUser(ctx, source.ID, "Schedule this")
	in := store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID, SourceUserMessageID: message.ID,
		SourceContentFingerprint: handoffFingerprint(2), InvocationOrdinal: 1, Title: "Race", Timezone: scheduledTimezoneUTC}
	type result struct {
		row   store.HydratedChatHandoff
		fresh bool
		err   error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			row, fresh, err := repo.CreateOrGetDraft(ctx, in)
			results <- result{row, fresh, err}
		})
	}
	wg.Wait()
	close(results)
	var rows []result
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		rows = append(rows, result)
	}
	if len(rows) != 2 || rows[0].row.Handoff.ID != rows[1].row.Handoff.ID || rows[0].fresh == rows[1].fresh {
		t.Fatalf("concurrent results = %+v", rows)
	}
	var taskCount, handoffCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE user_id = $1`, owner.ID).Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatalf("task count=%d err=%v, want 1", taskCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE user_id = $1`, owner.ID).Scan(&handoffCount); err != nil || handoffCount != 1 {
		t.Fatalf("handoff count=%d err=%v, want 1", handoffCount, err)
	}
}

func TestChatScheduledHandoffLifecycleHydrationAndOwnerScope(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	repo := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "handoff-owner", "handoff-owner@example.com")
	other := createScheduledUser(t, ctx, users, "handoff-other", "handoff-other@example.com")
	source, _ := conversations.Create(ctx, owner.ID, "Source")
	userMessage, _ := messages.AddChatUser(ctx, source.ID, "Schedule this")
	first, _, err := repo.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID, SourceUserMessageID: userMessage.ID,
		SourceContentFingerprint: handoffFingerprint(3), InvocationOrdinal: 1, Title: "First", Timezone: scheduledTimezoneUTC})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := repo.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID, SourceUserMessageID: userMessage.ID,
		SourceContentFingerprint: handoffFingerprint(3), InvocationOrdinal: 2, Title: "Second", Timezone: scheduledTimezoneUTC})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkTaskReady(ctx, other.ID, first.Task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner MarkTaskReady err=%v, want ErrNotFound", err)
	}
	if err := repo.MarkTaskReady(ctx, owner.ID, first.Task.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkTaskFailed(ctx, owner.ID, second.Task.ID, "compiler_failed", true); err != nil {
		t.Fatal(err)
	}
	assistant, err := messages.AddChatAssistantIfLatestUser(ctx, source.ID, userMessage, "Drafted", nil, []string{first.Handoff.ID, second.Handoff.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AddDefinition(ctx, first.Task.ConversationID, model.MsgRoleAssistant, "Ready to schedule"); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AddDefinition(ctx, second.Task.ConversationID, model.MsgRoleAssistant, "Need a date"); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListByAssistantMessages(ctx, owner.ID, source.ID, []int64{assistant.ID})
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListByAssistantMessages rows=%+v err=%v", rows, err)
	}
	states := map[string]store.HydratedChatHandoff{}
	for _, row := range rows {
		states[row.Handoff.ArtifactState] = row
	}
	if states[model.ScheduledHandoffStateReady].LatestDefinitionAssistant != "Ready to schedule" ||
		states[model.ScheduledHandoffStateFailed].Handoff.ErrorCode != "compiler_failed" ||
		!states[model.ScheduledHandoffStateFailed].Handoff.Retryable {
		t.Fatalf("hydrated states = %+v", rows)
	}
	if rows, err := repo.ListByAssistantMessages(ctx, other.ID, source.ID, []int64{assistant.ID}); err != nil || len(rows) != 0 {
		t.Fatalf("cross-owner hydration rows=%+v err=%v", rows, err)
	}
}

func TestChatScheduledHandoffDiscardAndCleanupOnlyDrafts(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	repo := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "handoff-clean", "handoff-clean@example.com")
	other := createScheduledUser(t, ctx, users, "handoff-clean-other", "handoff-clean-other@example.com")
	source, _ := conversations.Create(ctx, owner.ID, "Source")
	message, _ := messages.AddChatUser(ctx, source.ID, "Schedule this")
	newDraft := func(ordinal int) store.HydratedChatHandoff {
		t.Helper()
		row, _, err := repo.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID, SourceUserMessageID: message.ID,
			SourceContentFingerprint: handoffFingerprint(byte(ordinal + 4)), InvocationOrdinal: ordinal, Title: "Draft", Timezone: scheduledTimezoneUTC})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	dismissed := newDraft(1)
	if err := repo.DiscardDraft(ctx, owner.ID, dismissed.Task.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DiscardDraft(ctx, other.ID, dismissed.Task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner discard err=%v, want ErrNotFound", err)
	}
	var state string
	var taskID *string
	if err := pool.QueryRow(ctx, `SELECT artifact_state, scheduled_task_id::text FROM chat_scheduled_handoffs WHERE id = $1::uuid`, dismissed.Handoff.ID).Scan(&state, &taskID); err != nil || state != model.ScheduledHandoffStateDismissed || taskID != nil {
		t.Fatalf("dismissed tombstone state=%q task=%v err=%v", state, taskID, err)
	}
	var dismissedConversation int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM conversations WHERE id = $1::uuid`, dismissed.Task.ConversationID).Scan(&dismissedConversation); err != nil || dismissedConversation != 0 {
		t.Fatalf("dismissed conversation count=%d err=%v", dismissedConversation, err)
	}
	draft := newDraft(2)
	otherSource, _ := conversations.Create(ctx, other.ID, "Other source")
	otherMessage, _ := messages.AddChatUser(ctx, otherSource.ID, "Schedule this")
	otherDraft, _, err := repo.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: other.ID, SourceConversationID: otherSource.ID,
		SourceUserMessageID: otherMessage.ID, SourceContentFingerprint: handoffFingerprint(20), InvocationOrdinal: 1, Title: "Other draft", Timezone: scheduledTimezoneUTC})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CleanupDrafts(ctx, owner.ID, []string{otherDraft.Handoff.ID}); err != nil {
		t.Fatal(err)
	}
	var otherCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, otherDraft.Handoff.ID).Scan(&otherCount); err != nil || otherCount != 1 {
		t.Fatalf("cross-owner cleanup count=%d err=%v", otherCount, err)
	}
	confirmed := newDraft(3)
	if err := repo.MarkTaskReady(ctx, owner.ID, confirmed.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE scheduled_tasks SET state = 'active' WHERE id = $1::uuid`, confirmed.Task.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DiscardDraft(ctx, owner.ID, confirmed.Task.ID); !errors.Is(err, store.ErrInvalidScheduledTaskState) {
		t.Fatalf("confirmed discard err=%v, want ErrInvalidScheduledTaskState", err)
	}
	if err := repo.CleanupDrafts(ctx, owner.ID, []string{draft.Handoff.ID, confirmed.Handoff.ID, dismissed.Handoff.ID}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{draft.Handoff.ID} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("draft handoff retained count=%d err=%v", count, err)
		}
	}
	var confirmedCount, dismissedCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, confirmed.Handoff.ID).Scan(&confirmedCount); err != nil || confirmedCount != 1 {
		t.Fatalf("confirmed handoff count=%d err=%v", confirmedCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, dismissed.Handoff.ID).Scan(&dismissedCount); err != nil || dismissedCount != 1 {
		t.Fatalf("dismissed tombstone count=%d err=%v", dismissedCount, err)
	}
}

func TestChatScheduledHandoffStateUpdatesRecoverAndIgnoreOrdinaryTasks(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	repo := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "handoff-recover", "handoff-recover@example.com")
	source, err := conversations.Create(ctx, owner.ID, "Source")
	if err != nil {
		t.Fatal(err)
	}
	message, err := messages.AddChatUser(ctx, source.ID, "Schedule this")
	if err != nil {
		t.Fatal(err)
	}
	row, _, err := repo.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{
		UserID: owner.ID, SourceConversationID: source.ID, SourceUserMessageID: message.ID,
		SourceContentFingerprint: handoffFingerprint(42), InvocationOrdinal: 1, Title: "Recover", Timezone: scheduledTimezoneUTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkTaskReady(ctx, owner.ID, row.Task.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkTaskFailed(ctx, owner.ID, row.Task.ID, "compiler_failed", true); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkTaskReady(ctx, owner.ID, row.Task.ID); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT artifact_state FROM chat_scheduled_handoffs WHERE id = $1::uuid`, row.Handoff.ID).Scan(&state); err != nil || state != model.ScheduledHandoffStateReady {
		t.Fatalf("recovered state=%q err=%v", state, err)
	}

	ordinaryConversation, err := conversations.CreateWithKind(ctx, owner.ID, "Ordinary", model.ConversationKindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	var ordinaryTaskID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO scheduled_tasks (user_id, conversation_id, name, kind, state, timezone)
		 VALUES ($1, $2::uuid, 'Ordinary', 'reminder', 'draft', $3) RETURNING id::text`,
		owner.ID, ordinaryConversation.ID, scheduledTimezoneUTC,
	).Scan(&ordinaryTaskID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkTaskReady(ctx, owner.ID, ordinaryTaskID); err != nil {
		t.Fatalf("ordinary MarkTaskReady err=%v", err)
	}
	if err := repo.MarkTaskFailed(ctx, owner.ID, ordinaryTaskID, "compiler_failed", true); err != nil {
		t.Fatalf("ordinary MarkTaskFailed err=%v", err)
	}
}

func handoffFingerprint(last byte) []byte {
	fingerprint := make([]byte, 32)
	fingerprint[len(fingerprint)-1] = last
	return fingerprint
}
