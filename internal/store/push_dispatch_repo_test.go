package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

// deliveredRunEnv captures the seeded values a claim is expected to surface.
type deliveredRunEnv struct {
	taskTitle string
	userID    int64
}

// setupDeliveredScheduledRun seeds a user, conversation, scheduled task (with a
// name + delivery_conversation_id), a delivered assistant message, and a
// scheduled_task_run in state 'delivered' with delivery_message_id set and
// push_dispatched_at NULL — i.e. exactly one row eligible to be claimed.
func setupDeliveredScheduledRun(t *testing.T, pool *pgxpool.Pool) deliveredRunEnv {
	t.Helper()
	ctx := t.Context()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	u := createScheduledUser(t, ctx, users, "push-dispatch", "push-dispatch@example.com")
	conv := createScheduledConversation(t, ctx, conversations, u.ID)

	const taskTitle = "Morning digest"
	var taskID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO scheduled_tasks (user_id, conversation_id, delivery_conversation_id, name, kind, state)
		 VALUES ($1, $2::uuid, $2::uuid, $3, 'data', 'active')
		 RETURNING id::text`,
		u.ID, conv.ID, taskTitle).Scan(&taskID); err != nil {
		t.Fatalf("insert scheduled task: %v", err)
	}

	var messageID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose)
		 VALUES ($1::uuid, $2, $3, 'scheduled_delivery')
		 RETURNING id`,
		conv.ID, model.MsgRoleAssistant, "digest body").Scan(&messageID); err != nil {
		t.Fatalf("insert delivery message: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO scheduled_task_runs
		     (task_id, occurrence_key, scheduled_for, state, result, delivery_message_id)
		 VALUES ($1::uuid, 'once', NOW(), 'delivered', 'digest body', $2)`,
		taskID, messageID); err != nil {
		t.Fatalf("insert delivered run: %v", err)
	}

	return deliveredRunEnv{taskTitle: taskTitle, userID: u.ID}
}

func TestClaimUndispatchedDeliveriesIsExactlyOnce(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := t.Context()
	setupDeliveredScheduledRun(t, pool)
	repo := store.NewPushSubscriptionRepository(pool)

	// Two concurrent claimers; the run must be handed out exactly once.
	type res struct {
		items []model.PendingPushDelivery
		err   error
	}
	ch := make(chan res, 2)
	for range 2 {
		go func() {
			items, err := repo.ClaimUndispatchedDeliveries(ctx, 10)
			ch <- res{items, err}
		}()
	}
	total := 0
	for range 2 {
		r := <-ch
		if r.err != nil {
			t.Fatalf("claim: %v", r.err)
		}
		total += len(r.items)
	}
	if total != 1 {
		t.Fatalf("expected the run claimed exactly once, got %d", total)
	}

	// A second claim returns nothing (already stamped).
	again, err := repo.ClaimUndispatchedDeliveries(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("expected no rows after dispatch, got %d", len(again))
	}
}

func TestClaimReturnsTaskTitleAndConversation(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := t.Context()
	env := setupDeliveredScheduledRun(t, pool)
	repo := store.NewPushSubscriptionRepository(pool)

	items, err := repo.ClaimUndispatchedDeliveries(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1, got %d", len(items))
	}
	if items[0].TaskTitle != env.taskTitle {
		t.Fatalf("title = %q, want %q", items[0].TaskTitle, env.taskTitle)
	}
	if items[0].UserID != env.userID {
		t.Fatalf("user id = %d, want %d", items[0].UserID, env.userID)
	}
	if items[0].ConversationID == "" {
		t.Fatal("expected delivery conversation id")
	}
	if items[0].MessageID == nil {
		t.Fatal("expected delivery message id")
	}
	if items[0].Result == "" {
		t.Fatal("expected result content")
	}
}
