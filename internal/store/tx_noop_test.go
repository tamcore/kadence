package store_test

import (
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const txNoopMissingConversation = "11111111-1111-1111-1111-111111111111"

// Operations that find nothing to do report success without committing. The
// commit is what could still fail on a dead connection, so a caller that
// changed nothing must never be told the call failed.
func TestConversationDeleteMissingRowSucceeds(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := t.Context()
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)

	u, err := users.Create(ctx, model.User{
		Username: "txnoop", Email: "txnoop@example.test", PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := convs.Delete(ctx, txNoopMissingConversation, u.ID); err != nil {
		t.Fatalf("delete missing conversation = %v, want nil", err)
	}
}

func TestPauseByConversationWithoutTaskSucceeds(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := t.Context()
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	tasks := store.NewScheduledTaskRepository(pool, 10)

	u, err := users.Create(ctx, model.User{
		Username: "txnoop", Email: "txnoop@example.test", PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := convs.CreateWithKind(ctx, u.ID, "no task", model.ConversationKindChat)
	if err != nil {
		t.Fatal(err)
	}

	paused, err := tasks.PauseByConversation(ctx, conversation.ID, u.ID)
	if err != nil {
		t.Fatalf("pause conversation without task = %v, want nil", err)
	}
	if paused {
		t.Fatal("paused = true, want false when no task is linked")
	}
}
