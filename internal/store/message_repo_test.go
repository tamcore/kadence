package store_test

import (
	"context"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestListChatHistoryIncludesScheduledDeliveryForChatConversation(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "chat-hist-delivery", Email: "chat-hist-delivery@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.CreateWithKind(
		ctx, owner.ID, "chat with delivery", model.ConversationKindChat,
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose)
		 VALUES ($1::uuid, 'assistant', 'delivered', 'scheduled_delivery')`, conversation.ID); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	msgs, err := messages.ListChatHistory(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("ListChatHistory: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "delivered" {
		t.Fatalf("history = %+v, want the scheduled delivery", msgs)
	}
}

func TestListChatHistoryExcludesScheduledMessagesForScheduledConversation(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "sched-hist-guard", Email: "sched-hist-guard@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.CreateWithKind(
		ctx, owner.ID, "scheduled definition thread", model.ConversationKindScheduled,
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose)
		 VALUES
		     ($1::uuid, 'assistant', 'delivered', 'scheduled_delivery'),
		     ($1::uuid, 'user', 'define this', 'scheduled_definition')`, conversation.ID); err != nil {
		t.Fatalf("seed scheduled messages: %v", err)
	}

	msgs, err := messages.ListChatHistory(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("ListChatHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("history = %+v, want empty for scheduled conversation", msgs)
	}
}
