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

// Shared test-fixture emails, reused across store_test files to avoid
// goconst duplicate-literal warnings.
const (
	testEmailA   = "a@x.io"
	testEmailB   = "b@x.io"
	testEmailO   = "o@x.io"
	testEmailBob = "bob@x.io"
)

func TestConversationAndMessageFlow(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	msgs := store.NewMessageRepository(pool)
	ctx := context.Background()

	u, err := users.Create(ctx, model.User{Username: testAliceUsername, Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	c, err := convs.Create(ctx, u.ID, "First chat")
	if err != nil || c.ID == "" {
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

func TestMessageToolCallsPersisted(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	msgs := store.NewMessageRepository(pool)
	ctx := context.Background()

	u, err := users.Create(ctx, model.User{Username: testAliceUsername, Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	c, err := convs.Create(ctx, u.ID, "chat")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	calls := []model.MessageToolCall{
		{Name: "garmin__get_activities_by_date", Arguments: `{"start_date":"2026-07-19"}`},
		{Name: "garmin__get_activity_weather", Arguments: `{"activity_id":123}`},
	}
	if _, err := msgs.AddWithToolCalls(ctx, c.ID, model.MsgRoleAssistant, "answer", calls); err != nil {
		t.Fatalf("add with tool calls: %v", err)
	}
	// A plain Add stores no tool calls.
	if _, err := msgs.Add(ctx, c.ID, model.MsgRoleUser, "thanks"); err != nil {
		t.Fatalf("add user msg: %v", err)
	}

	list, err := msgs.ListByConversation(ctx, c.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if len(list[0].ToolCalls) != 2 || list[0].ToolCalls[0].Name != "garmin__get_activities_by_date" ||
		list[0].ToolCalls[0].Arguments != `{"start_date":"2026-07-19"}` {
		t.Fatalf("assistant tool calls not round-tripped: %+v", list[0].ToolCalls)
	}
	if list[1].ToolCalls != nil {
		t.Fatalf("plain Add should store no tool calls, got: %+v", list[1].ToolCalls)
	}
}

func TestConversationScopedToOwner(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	ctx := context.Background()

	owner, _ := users.Create(ctx, model.User{Username: "owner", Email: testEmailO, PasswordHash: "h", Role: model.RoleUser})
	other, _ := users.Create(ctx, model.User{Username: "other", Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})
	c, _ := convs.Create(ctx, owner.ID, "secret")

	if _, err := convs.GetByID(ctx, c.ID, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user GetByID err = %v, want ErrNotFound", err)
	}
}

func TestConversationUpdateTitle(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	ctx := context.Background()

	owner, _ := users.Create(ctx, model.User{Username: "owner", Email: testEmailO, PasswordHash: "h", Role: model.RoleUser})
	other, _ := users.Create(ctx, model.User{Username: "other", Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})
	c, _ := convs.Create(ctx, owner.ID, "old title")

	updated, err := convs.UpdateTitle(ctx, c.ID, owner.ID, "new title")
	if err != nil || updated.Title != "new title" {
		t.Fatalf("update title: %v %+v", err, updated)
	}

	got, err := convs.GetByID(ctx, c.ID, owner.ID)
	if err != nil || got.Title != "new title" {
		t.Fatalf("get after update: %v %+v", err, got)
	}

	if _, err := convs.UpdateTitle(ctx, c.ID, other.ID, "hijacked"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user UpdateTitle err = %v, want ErrNotFound", err)
	}

	if _, err := convs.UpdateTitle(ctx, "00000000-0000-0000-0000-000000000000", owner.ID, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing id UpdateTitle err = %v, want ErrNotFound", err)
	}
}
