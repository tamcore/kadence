package store_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const (
	testAliceUsername = "alice"
	testNewPrompt     = "new prompt"
)

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
	if _, err := msgs.Add(ctx, c.ID, model.MsgRoleUser, "latest"); err != nil {
		t.Fatalf("add latest msg: %v", err)
	}

	list, err := msgs.ListByConversation(ctx, c.ID)
	if err != nil || len(list) != 3 || list[0].Content != "hello" || list[1].Role != model.MsgRoleAssistant {
		t.Fatalf("list messages: %v %+v", err, list)
	}
	recent, err := msgs.ListRecentByConversation(ctx, c.ID, 2)
	if err != nil || len(recent) != 2 || recent[0].Content != "hi there" || recent[1].Content != "latest" {
		t.Fatalf("list recent messages: %v %+v", err, recent)
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

func TestScheduledDefinitionHistoryExcludes198Deliveries(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	user, err := users.Create(ctx, model.User{Username: "scheduled-history", Email: "scheduled-history@example.com", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.CreateWithKind(ctx, user.ID, "Scheduled", model.ConversationKindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AddDefinition(ctx, conversation.ID, model.MsgRoleUser, "Define my task"); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AddDefinition(ctx, conversation.ID, model.MsgRoleAssistant, "Which days?"); err != nil {
		t.Fatal(err)
	}
	for i := range 198 {
		if _, err := pool.Exec(ctx,
			`INSERT INTO messages (conversation_id, role, content, purpose)
			 VALUES ($1::uuid, $2, $3, 'scheduled_delivery')`,
			conversation.ID, model.MsgRoleAssistant, "delivery-"+strconv.Itoa(i)); err != nil {
			t.Fatalf("insert delivery %d: %v", i, err)
		}
	}

	definitions, err := messages.ListRecentDefinitionByConversation(ctx, conversation.ID, 201)
	if err != nil || len(definitions) != 2 ||
		definitions[0].Content != "Define my task" || definitions[1].Content != "Which days?" {
		t.Fatalf("definition history = %+v, %v", definitions, err)
	}
	all, err := messages.ListByConversation(ctx, conversation.ID)
	if err != nil || len(all) != 200 {
		t.Fatalf("complete history len=%d err=%v", len(all), err)
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

func TestMessageRepositoryEditAndRewind(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	msgs := store.NewMessageRepository(pool)
	chunks := store.NewChunkRepository(pool, "test-model")
	audits := store.NewMCPAuditRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "rewind-edit", Email: "rewind-edit@example.com", PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := convs.Create(ctx, owner.ID, "edit")
	if err != nil {
		t.Fatal(err)
	}
	userMessage, err := msgs.Add(ctx, conversation.ID, model.MsgRoleUser, "old prompt")
	if err != nil {
		t.Fatal(err)
	}
	assistantMessage, err := msgs.Add(ctx, conversation.ID, model.MsgRoleAssistant, "old answer")
	if err != nil {
		t.Fatal(err)
	}
	laterUser, err := msgs.Add(ctx, conversation.ID, model.MsgRoleUser, "later prompt")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []model.Message{userMessage, assistantMessage, laterUser} {
		sourceID := message.ID
		if err := chunks.Insert(ctx, model.Chunk{
			UserID: &owner.ID, ConversationID: &conversation.ID, Scope: model.ScopePrivate,
			SourceKind: model.ChunkSourceMessage, SourceID: &sourceID, Content: message.Content,
		}, make([]float32, 1024)); err != nil {
			t.Fatal(err)
		}
	}
	auditID, err := audits.Start(ctx, model.MCPAuditCall{
		ActorUserID: owner.ID, ActorUsername: owner.Username, ConversationID: conversation.ID,
		Source: model.MCPAuditSourceChat, Model: "test-model", ToolCallID: "call-edit",
		ToolName: "test__tool", Arguments: `{}`, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	edited, err := msgs.EditAndRewind(ctx, conversation.ID, userMessage.ID, owner.ID, testNewPrompt)
	if err != nil {
		t.Fatalf("edit and rewind: %v", err)
	}
	if edited.ID != userMessage.ID || edited.Content != testNewPrompt {
		t.Fatalf("edited message = %+v", edited)
	}
	remaining, err := msgs.ListByConversation(ctx, conversation.ID)
	if err != nil || len(remaining) != 1 || remaining[0].Content != testNewPrompt {
		t.Fatalf("remaining messages = %+v, err=%v", remaining, err)
	}
	var chunkCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM chunks WHERE conversation_id = $1::uuid`, conversation.ID,
	).Scan(&chunkCount); err != nil || chunkCount != 0 {
		t.Fatalf("remaining chunks = %d, err=%v", chunkCount, err)
	}
	if _, err := audits.Get(ctx, auditID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("audit must survive rewind: %v", err)
	}
}

func TestMessageRepositoryRegenerateAndRewind(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	msgs := store.NewMessageRepository(pool)
	chunks := store.NewChunkRepository(pool, "test-model")
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "rewind-regenerate", Email: "rewind-regenerate@example.com", PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := convs.Create(ctx, owner.ID, "regenerate")
	if err != nil {
		t.Fatal(err)
	}
	userMessage, err := msgs.Add(ctx, conversation.ID, model.MsgRoleUser, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	assistantMessage, err := msgs.Add(ctx, conversation.ID, model.MsgRoleAssistant, "first answer")
	if err != nil {
		t.Fatal(err)
	}
	laterUser, err := msgs.Add(ctx, conversation.ID, model.MsgRoleUser, "later")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []model.Message{userMessage, assistantMessage, laterUser} {
		sourceID := message.ID
		if err := chunks.Insert(ctx, model.Chunk{
			UserID: &owner.ID, ConversationID: &conversation.ID, Scope: model.ScopePrivate,
			SourceKind: model.ChunkSourceMessage, SourceID: &sourceID, Content: message.Content,
		}, make([]float32, 1024)); err != nil {
			t.Fatal(err)
		}
	}

	prompt, err := msgs.RegenerateAndRewind(ctx, conversation.ID, assistantMessage.ID, owner.ID)
	if err != nil {
		t.Fatalf("regenerate and rewind: %v", err)
	}
	if prompt.ID != userMessage.ID || prompt.Content != "prompt" {
		t.Fatalf("prompt = %+v", prompt)
	}
	remaining, err := msgs.ListByConversation(ctx, conversation.ID)
	if err != nil || len(remaining) != 1 || remaining[0].ID != userMessage.ID {
		t.Fatalf("remaining messages = %+v, err=%v", remaining, err)
	}
	var sourceIDs []int64
	rows, err := pool.Query(ctx,
		`SELECT source_id FROM chunks WHERE conversation_id = $1::uuid ORDER BY source_id`, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var sourceID int64
		if err := rows.Scan(&sourceID); err != nil {
			t.Fatal(err)
		}
		sourceIDs = append(sourceIDs, sourceID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(sourceIDs) != 1 || sourceIDs[0] != userMessage.ID {
		t.Fatalf("remaining chunk source ids = %v", sourceIDs)
	}
}

func TestMessageRepositoryRewindValidatesOwnerKindAndRole(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	msgs := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, _ := users.Create(ctx, model.User{
		Username: "rewind-owner", Email: "rewind-owner@example.com", PasswordHash: "h", Role: model.RoleUser,
	})
	other, _ := users.Create(ctx, model.User{
		Username: "rewind-other", Email: "rewind-other@example.com", PasswordHash: "h", Role: model.RoleUser,
	})
	conversation, _ := convs.Create(ctx, owner.ID, "chat")
	userMessage, _ := msgs.Add(ctx, conversation.ID, model.MsgRoleUser, "prompt")
	assistantMessage, _ := msgs.Add(ctx, conversation.ID, model.MsgRoleAssistant, "answer")
	scheduled, _ := convs.CreateWithKind(ctx, owner.ID, "scheduled", model.ConversationKindScheduled)
	scheduledUser, _ := msgs.AddDefinition(ctx, scheduled.ID, model.MsgRoleUser, "definition")

	if _, err := msgs.EditAndRewind(ctx, conversation.ID, userMessage.ID, other.ID, "hijack"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner edit err = %v, want ErrNotFound", err)
	}
	if _, err := msgs.EditAndRewind(ctx, scheduled.ID, scheduledUser.ID, owner.ID, "changed"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("scheduled edit err = %v, want ErrNotFound", err)
	}
	if _, err := msgs.EditAndRewind(ctx, conversation.ID, assistantMessage.ID, owner.ID, "wrong role"); !errors.Is(err, store.ErrWrongMessageRole) {
		t.Fatalf("assistant edit err = %v, want ErrWrongMessageRole", err)
	}
	if _, err := msgs.RegenerateAndRewind(ctx, conversation.ID, userMessage.ID, owner.ID); !errors.Is(err, store.ErrWrongMessageRole) {
		t.Fatalf("user regenerate err = %v, want ErrWrongMessageRole", err)
	}
}

func TestMessageRepositoryAddChatAssistantIfLatestUserRejectsEditedTurn(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	msgs := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "stale-chat", Email: "stale-chat@example.com", PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := convs.Create(ctx, owner.ID, "stale")
	if err != nil {
		t.Fatal(err)
	}
	staleUser, err := msgs.AddChatUser(ctx, conversation.ID, "original prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := msgs.EditAndRewind(ctx, conversation.ID, staleUser.ID, owner.ID, "edited prompt"); err != nil {
		t.Fatal(err)
	}

	if _, err := msgs.AddChatAssistantIfLatestUser(ctx, conversation.ID, staleUser, "stale response", nil); !errors.Is(err, store.ErrStaleChatTurn) {
		t.Fatalf("stale assistant err = %v, want ErrStaleChatTurn", err)
	}
	remaining, err := msgs.ListByConversation(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Content != "edited prompt" {
		t.Fatalf("messages after stale assistant = %+v", remaining)
	}
}
