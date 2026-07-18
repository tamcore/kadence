package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const testAliceUsername = "alice"

func TestConversationAndMessageFlow(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	msgs := store.NewMessageRepository(pool)
	ctx := context.Background()

	u, err := users.Create(ctx, model.User{Username: testAliceUsername, Email: "a@x.io", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	c, err := convs.Create(ctx, u.ID, "First chat")
	if err != nil || c.ID == 0 {
		t.Fatalf("create conversation: %v %+v", err, c)
	}

	if _, err := msgs.Add(ctx, c.ID, model.MsgRoleUser, "hello"); err != nil {
		t.Fatalf("add user msg: %v", err)
	}
	if _, err := msgs.Add(ctx, c.ID, model.MsgRoleAssistant, "hi there"); err != nil {
		t.Fatalf("add assistant msg: %v", err)
	}

	list, err := msgs.ListByConversation(ctx, c.ID)
	if err != nil || len(list) != 2 || list[0].Content != "hello" || list[1].Role != model.MsgRoleAssistant {
		t.Fatalf("list messages: %v %+v", err, list)
	}

	got, err := convs.GetByID(ctx, c.ID, u.ID)
	if err != nil || got.Title != "First chat" {
		t.Fatalf("get conversation: %v %+v", err, got)
	}

	all, err := convs.ListByUser(ctx, u.ID)
	if err != nil || len(all) != 1 {
		t.Fatalf("list conversations: %v len=%d", err, len(all))
	}
}

func TestConversationScopedToOwner(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	ctx := context.Background()

	owner, _ := users.Create(ctx, model.User{Username: "owner", Email: "o@x.io", PasswordHash: "h", Role: model.RoleUser})
	other, _ := users.Create(ctx, model.User{Username: "other", Email: "b@x.io", PasswordHash: "h", Role: model.RoleUser})
	c, _ := convs.Create(ctx, owner.ID, "secret")

	if _, err := convs.GetByID(ctx, c.ID, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user GetByID err = %v, want ErrNotFound", err)
	}
}
