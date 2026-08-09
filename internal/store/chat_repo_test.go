package store_test

import (
	"context"
	"encoding/json"
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
	testOwner    = "owner"
)

// Shared test-fixture values reused across store_test files to avoid
// goconst duplicate-literal warnings.
const (
	testOtherUsername      = "other"
	testMimeMarkdown       = "text/markdown"
	testMimePNG            = "image/png"
	testChartPNGFilename   = "chart.png"
	testProofPNGFilename   = "proof.png"
	testExtractedMarkdownH = "# First"
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

	owner, _ := users.Create(ctx, model.User{Username: testOwner, Email: testEmailO, PasswordHash: "h", Role: model.RoleUser})
	other, _ := users.Create(ctx, model.User{Username: testOtherUsername, Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})
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

	owner, _ := users.Create(ctx, model.User{Username: testOwner, Email: testEmailO, PasswordHash: "h", Role: model.RoleUser})
	other, _ := users.Create(ctx, model.User{Username: testOtherUsername, Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})
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

func TestConversationUpdateTitleIfCurrent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	convs := store.NewConversationRepository(pool)
	ctx := context.Background()

	owner, _ := users.Create(ctx, model.User{Username: testOwner, Email: testEmailO, PasswordHash: "h", Role: model.RoleUser})
	other, _ := users.Create(ctx, model.User{Username: testOtherUsername, Email: testEmailB, PasswordHash: "h", Role: model.RoleUser})
	c, _ := convs.Create(ctx, owner.ID, "fallback title")

	updated, swapped, err := convs.UpdateTitleIfCurrent(ctx, c.ID, owner.ID, "fallback title", "Generated title")
	if err != nil || !swapped || updated.Title != "Generated title" || updated.ID != c.ID {
		t.Fatalf("first swap = %+v swapped=%v err=%v", updated, swapped, err)
	}

	_, swapped, err = convs.UpdateTitleIfCurrent(ctx, c.ID, owner.ID, "fallback title", "Stale overwrite")
	if err != nil || swapped {
		t.Fatalf("stale swap = %v err=%v, want false/nil", swapped, err)
	}

	_, swapped, err = convs.UpdateTitleIfCurrent(ctx, c.ID, other.ID, "Generated title", "Cross-owner overwrite")
	if err != nil || swapped {
		t.Fatalf("cross-owner swap = %v err=%v, want false/nil", swapped, err)
	}

	_, swapped, err = convs.UpdateTitleIfCurrent(ctx, "00000000-0000-0000-0000-000000000000", owner.ID, "Generated title", "Missing overwrite")
	if err != nil || swapped {
		t.Fatalf("missing swap = %v err=%v, want false/nil", swapped, err)
	}

	got, err := convs.GetByID(ctx, c.ID, owner.ID)
	if err != nil || got.Title != "Generated title" {
		t.Fatalf("get after conditional update: %v %+v", err, got)
	}
}

// mustCreateConversation creates a conversation and fails the test on error,
// reducing repetitive error-handling noise in ordering/pinning fixtures.
func mustCreateConversation(
	t *testing.T, conversations *store.ConversationRepository, ctx context.Context,
	ownerID int64, title string,
) model.Conversation {
	t.Helper()
	c, err := conversations.Create(ctx, ownerID, title)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// requireConversationOrder fails the test unless list matches want by ID,
// exactly and in order — used to assert ConversationRepository.ListByUser
// ordering under pinning/recency rules.
func requireConversationOrder(t *testing.T, list []model.Conversation, err error, want []string) {
	t.Helper()
	if err != nil || len(list) != len(want) {
		t.Fatalf("conversation order = %+v, err=%v, want %d entries", list, err, len(want))
	}
	for i, wantID := range want {
		if list[i].ID != wantID {
			t.Fatalf("ordering[%d]=%s, want=%s full=%+v", i, list[i].ID, wantID, list)
		}
	}
}

func TestConversationNavigationOrderingPinningAndOwnerIsolation(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{Username: "navigation-owner", Email: "navigation-owner@example.com", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.Create(ctx, model.User{Username: "navigation-other", Email: "navigation-other@example.com", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	first, err := conversations.Create(ctx, owner.ID, "first")
	if err != nil || first.LastActivityAt.IsZero() {
		t.Fatalf("create first conversation: %+v, %v", first, err)
	}
	second := mustCreateConversation(t, conversations, ctx, owner.ID, "second")
	recentNew := mustCreateConversation(t, conversations, ctx, owner.ID, "recent new")
	recentOld := mustCreateConversation(t, conversations, ctx, owner.ID, "recent old")
	tieFirst := mustCreateConversation(t, conversations, ctx, owner.ID, "tie first")
	tieSecond := mustCreateConversation(t, conversations, ctx, owner.ID, "tie second")
	if _, err := conversations.CreateWithKind(ctx, owner.ID, "scheduled", model.ConversationKindScheduled); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.July, 1, 10, 0, 0, 0, time.UTC)
	setNavigation := func(id string, pinnedAt *time.Time, lastActivityAt, createdAt time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`UPDATE conversations SET pinned_at = $2, last_activity_at = $3, created_at = $4 WHERE id = $1::uuid`,
			id, pinnedAt, lastActivityAt, createdAt); err != nil {
			t.Fatal(err)
		}
	}

	pinned, err := conversations.UpdatePinned(ctx, first.ID, owner.ID, true)
	if err != nil || pinned.PinnedAt == nil {
		t.Fatalf("pin conversation: %+v, %v", pinned, err)
	}
	firstPin := *pinned.PinnedAt
	repinned, err := conversations.UpdatePinned(ctx, first.ID, owner.ID, true)
	if err != nil || repinned.PinnedAt == nil || !repinned.PinnedAt.Equal(firstPin) {
		t.Fatalf("re-pin must preserve timestamp: %+v, %v", repinned, err)
	}
	if _, err := conversations.UpdatePinned(ctx, second.ID, owner.ID, true); err != nil {
		t.Fatal(err)
	}
	pinnedOldAt := base.Add(time.Hour)
	pinnedNewAt := pinnedOldAt.Add(time.Hour)
	setNavigation(first.ID, &pinnedOldAt, base.Add(time.Minute), base)
	setNavigation(second.ID, &pinnedNewAt, base.Add(2*time.Minute), base)
	setNavigation(recentNew.ID, nil, base.Add(6*time.Hour), base)
	setNavigation(recentOld.ID, nil, base.Add(5*time.Hour), base)
	tieActivityAt := base.Add(4 * time.Hour)
	tieCreatedAt := base.Add(30 * time.Minute)
	setNavigation(tieFirst.ID, nil, tieActivityAt, tieCreatedAt)
	setNavigation(tieSecond.ID, nil, tieActivityAt, tieCreatedAt)
	list, err := conversations.ListByUser(ctx, owner.ID)
	tieEarlier, tieLater := tieFirst.ID, tieSecond.ID
	if tieEarlier < tieLater {
		tieEarlier, tieLater = tieLater, tieEarlier
	}
	wantPinnedOrder := []string{second.ID, first.ID, recentNew.ID, recentOld.ID, tieEarlier, tieLater}
	requireConversationOrder(t, list, err, wantPinnedOrder)
	if _, err := conversations.UpdatePinned(ctx, first.ID, other.ID, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner unpin err=%v, want ErrNotFound", err)
	}
	for range 2 {
		unpinned, err := conversations.UpdatePinned(ctx, first.ID, owner.ID, false)
		if err != nil || unpinned.PinnedAt != nil {
			t.Fatalf("idempotent unpin: %+v, %v", unpinned, err)
		}
	}
	if _, err := conversations.UpdatePinned(ctx, second.ID, owner.ID, false); err != nil {
		t.Fatal(err)
	}
	list, err = conversations.ListByUser(ctx, owner.ID)
	wantRecentOrder := []string{recentNew.ID, recentOld.ID, tieEarlier, tieLater, second.ID, first.ID}
	requireConversationOrder(t, list, err, wantRecentOrder)
}

func TestMessageRepositoryChatWritesAndRewindsTouchActivity(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{Username: "navigation-activity", Email: "navigation-activity@example.com", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "activity")
	if err != nil {
		t.Fatal(err)
	}
	baseline := time.Date(2026, time.July, 1, 10, 0, 0, 0, time.UTC)
	setActivity := func(id string, at time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE conversations SET last_activity_at = $2 WHERE id = $1::uuid`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	activity := func(id string) time.Time {
		t.Helper()
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT last_activity_at FROM conversations WHERE id = $1::uuid`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	setActivity(conversation.ID, baseline)
	first, err := messages.Add(ctx, conversation.ID, model.MsgRoleUser, "pool write")
	if err != nil || !activity(conversation.ID).Equal(first.CreatedAt) {
		t.Fatalf("pool write activity=%s message=%+v err=%v", activity(conversation.ID), first, err)
	}
	setActivity(conversation.ID, baseline)
	second, err := messages.AddChatUser(ctx, conversation.ID, "transaction write")
	if err != nil || !activity(conversation.ID).Equal(second.CreatedAt) {
		t.Fatalf("transaction write activity=%s message=%+v err=%v", activity(conversation.ID), second, err)
	}
	assistant, err := messages.AddChatAssistantIfLatestUser(ctx, conversation.ID, second, "reply", nil, nil)
	if err != nil || !activity(conversation.ID).Equal(assistant.CreatedAt) {
		t.Fatalf("assistant activity=%s message=%+v err=%v", activity(conversation.ID), assistant, err)
	}
	setActivity(conversation.ID, baseline)
	if _, err := messages.EditAndRewind(ctx, conversation.ID, second.ID, owner.ID, "rewritten"); err != nil || !activity(conversation.ID).After(baseline) {
		t.Fatalf("edit activity=%s err=%v", activity(conversation.ID), err)
	}
	regenerateUser, err := messages.AddChatUser(ctx, conversation.ID, "regenerate prompt")
	if err != nil {
		t.Fatal(err)
	}
	regenerateAssistant, err := messages.AddChatAssistantIfLatestUser(ctx, conversation.ID, regenerateUser, "regenerate answer", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	setActivity(conversation.ID, baseline)
	if _, err := messages.RegenerateAndRewind(ctx, conversation.ID, regenerateAssistant.ID, owner.ID); err != nil || !activity(conversation.ID).After(baseline) {
		t.Fatalf("regenerate activity=%s err=%v", activity(conversation.ID), err)
	}

	scheduled, err := conversations.CreateWithKind(ctx, owner.ID, "scheduled", model.ConversationKindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	setActivity(scheduled.ID, baseline)
	if _, err := messages.AddDefinition(ctx, scheduled.ID, model.MsgRoleUser, "definition"); err != nil || !activity(scheduled.ID).Equal(baseline) {
		t.Fatalf("scheduled definition activity=%s err=%v", activity(scheduled.ID), err)
	}
}

func TestMessageRepositoryActivityNeverRegresses(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{
		Username: "navigation-monotonic", Email: "navigation-monotonic@example.com",
		PasswordHash: "h", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "monotonic activity")
	if err != nil {
		t.Fatal(err)
	}
	future := time.Date(2126, time.July, 1, 10, 0, 0, 123456000, time.UTC)
	setFutureActivity := func() {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`UPDATE conversations SET last_activity_at = $2 WHERE id = $1::uuid`,
			conversation.ID, future); err != nil {
			t.Fatal(err)
		}
	}
	assertFutureActivity := func(operation string) {
		t.Helper()
		var got time.Time
		if err := pool.QueryRow(ctx,
			`SELECT last_activity_at FROM conversations WHERE id = $1::uuid`,
			conversation.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !got.Equal(future) {
			t.Fatalf("%s regressed activity=%s, want=%s", operation, got, future)
		}
	}

	setFutureActivity()
	userMessage, err := messages.Add(ctx, conversation.ID, model.MsgRoleUser, "older insert")
	if err != nil {
		t.Fatal(err)
	}
	assertFutureActivity("message insert")

	setFutureActivity()
	if _, err := messages.EditAndRewind(
		ctx, conversation.ID, userMessage.ID, owner.ID, "older rewind",
	); err != nil {
		t.Fatal(err)
	}
	assertFutureActivity("rewind touch")
}

func TestConversationNavigationNonActivityWritesPreserveLastActivity(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	scheduledTasks := store.NewScheduledTaskRepository(pool, 10)
	ctx := context.Background()

	owner, err := users.Create(ctx, model.User{Username: "navigation-no-activity", Email: "navigation-no-activity@example.com", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := conversations.Create(ctx, owner.ID, "no activity")
	if err != nil {
		t.Fatal(err)
	}
	baseline := time.Date(2026, time.July, 1, 10, 0, 0, 0, time.UTC)
	setActivity := func(id string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE conversations SET last_activity_at = $2 WHERE id = $1::uuid`, id, baseline); err != nil {
			t.Fatal(err)
		}
	}
	assertActivity := func(id, operation string) {
		t.Helper()
		var got time.Time
		if err := pool.QueryRow(ctx, `SELECT last_activity_at FROM conversations WHERE id = $1::uuid`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !got.Equal(baseline) {
			t.Fatalf("%s changed activity=%s, want=%s", operation, got, baseline)
		}
	}

	setActivity(conversation.ID)
	if _, err := conversations.UpdateTitle(ctx, conversation.ID, owner.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	assertActivity(conversation.ID, "rename")
	if _, err := conversations.UpdatePinned(ctx, conversation.ID, owner.ID, true); err != nil {
		t.Fatal(err)
	}
	assertActivity(conversation.ID, "pin")
	if _, err := conversations.UpdatePinned(ctx, conversation.ID, owner.ID, false); err != nil {
		t.Fatal(err)
	}
	assertActivity(conversation.ID, "unpin")

	userMessage, err := messages.AddChatUserInput(ctx, conversation.ID, owner.ID, model.ChatUserInput{
		Content: "deferred extraction",
		Attachments: []model.MessageAttachment{{
			Filename: "deferred.md", MIME: testMimeMarkdown, Kind: model.AttachmentKindDocument,
			RawBytes: []byte("source"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	extracted := append([]model.MessageAttachment(nil), userMessage.Attachments...)
	extracted[0].ExtractedMarkdown = "# Extracted"
	setActivity(conversation.ID)
	if _, err := messages.UpdateChatAttachmentExtractions(ctx, conversation.ID, userMessage.ID, owner.ID, extracted); err != nil {
		t.Fatal(err)
	}
	assertActivity(conversation.ID, "attachment extraction")

	scheduledConversation, err := conversations.CreateWithKind(ctx, owner.ID, "scheduled", model.ConversationKindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	setActivity(scheduledConversation.ID)
	if _, err := messages.AddDefinition(ctx, scheduledConversation.ID, model.MsgRoleUser, "definition"); err != nil {
		t.Fatal(err)
	}
	assertActivity(scheduledConversation.ID, "scheduled definition")
	task, err := scheduledTasks.Create(ctx, model.ScheduledTask{
		UserID: owner.ID, ConversationID: scheduledConversation.ID, Name: "delivery",
		Kind: model.ScheduledTaskKindReminder, State: model.ScheduledTaskStateActive,
		CompiledPrompt: "deliver", Timezone: scheduledTimezoneUTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := scheduledTasks.CreateRun(ctx, owner.ID, model.ScheduledTaskRun{
		TaskID: task.ID, OccurrenceKey: "navigation-delivery", ScheduledFor: baseline,
		State: model.ScheduledTaskRunStateRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduledTasks.FinishSuccess(ctx, model.ScheduledExecutionSuccess{
		RunID: run.ID, UserID: owner.ID, ConversationID: scheduledConversation.ID,
		RunState: model.ScheduledTaskRunStateDelivered, TaskState: model.ScheduledTaskStateActive,
		Content: "scheduled delivery", Unread: true, MonitoringState: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	assertActivity(scheduledConversation.ID, "scheduled delivery")
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

	if _, err := msgs.AddChatAssistantIfLatestUser(ctx, conversation.ID, staleUser, "stale response", nil, nil); !errors.Is(err, store.ErrStaleChatTurn) {
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

func TestChatRepositoryAssistantHandoffBindingIsAtomic(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	handoffs := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "bind-owner", "bind-owner@example.com")
	other := createScheduledUser(t, ctx, users, "bind-other", "bind-other@example.com")
	source, _ := conversations.Create(ctx, owner.ID, "Source")
	otherSource, _ := conversations.Create(ctx, other.ID, "Other source")
	prompt, _ := messages.AddChatUser(ctx, source.ID, "Schedule this")
	otherPrompt, _ := messages.AddChatUser(ctx, otherSource.ID, "Schedule this")
	handoff, _, err := handoffs.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID,
		SourceUserMessageID: prompt.ID, SourceContentFingerprint: handoffFingerprint(40), InvocationOrdinal: 1, Title: testHandoffTitle, Timezone: scheduledTimezoneUTC})
	if err != nil {
		t.Fatal(err)
	}
	otherHandoff, _, err := handoffs.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: other.ID, SourceConversationID: otherSource.ID,
		SourceUserMessageID: otherPrompt.ID, SourceContentFingerprint: handoffFingerprint(41), InvocationOrdinal: 1, Title: testHandoffTitle, Timezone: scheduledTimezoneUTC})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AddChatAssistantIfLatestUser(ctx, source.ID, prompt, "Drafted", nil, []string{handoff.Handoff.ID, otherHandoff.Handoff.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("mixed-source binding err=%v, want ErrNotFound", err)
	}
	remaining, err := messages.ListByConversation(ctx, source.ID)
	if err != nil || len(remaining) != 1 {
		t.Fatalf("assistant insertion was not rolled back: messages=%+v err=%v", remaining, err)
	}
	assistant, err := messages.AddChatAssistantIfLatestUser(ctx, source.ID, prompt, "Drafted", nil, []string{handoff.Handoff.ID, handoff.Handoff.ID})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := handoffs.ListByAssistantMessages(ctx, owner.ID, source.ID, []int64{assistant.ID})
	if err != nil || len(rows) != 1 || rows[0].Handoff.ID != handoff.Handoff.ID {
		t.Fatalf("bound handoffs=%+v err=%v", rows, err)
	}
}

func TestChatRepositoryRegenerateRewindsDraftHandoffsButKeepsConfirmed(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	handoffs := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "rewind-handoff", "rewind-handoff@example.com")
	source, _ := conversations.Create(ctx, owner.ID, "Source")
	prompt, _ := messages.AddChatUser(ctx, source.ID, "Schedule this")
	newDraft := func(ordinal int) store.HydratedChatHandoff {
		t.Helper()
		row, _, err := handoffs.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID,
			SourceUserMessageID: prompt.ID, SourceContentFingerprint: handoffFingerprint(byte(50 + ordinal)), InvocationOrdinal: ordinal, Title: testHandoffTitle, Timezone: scheduledTimezoneUTC})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	draft := newDraft(1)
	confirmed := newDraft(2)
	if err := handoffs.MarkTaskReady(ctx, owner.ID, confirmed.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE scheduled_tasks SET state = 'active' WHERE id = $1::uuid`, confirmed.Task.ID); err != nil {
		t.Fatal(err)
	}
	assistant, err := messages.AddChatAssistantIfLatestUser(ctx, source.ID, prompt, "Drafted", nil, []string{draft.Handoff.ID, confirmed.Handoff.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.RegenerateAndRewind(ctx, source.ID, assistant.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	var draftHandoffs, draftTasks, draftConversations int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, draft.Handoff.ID).Scan(&draftHandoffs); err != nil || draftHandoffs != 0 {
		t.Fatalf("draft handoffs=%d err=%v", draftHandoffs, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE id = $1::uuid`, draft.Task.ID).Scan(&draftTasks); err != nil || draftTasks != 0 {
		t.Fatalf("draft tasks=%d err=%v", draftTasks, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM conversations WHERE id = $1::uuid`, draft.Task.ConversationID).Scan(&draftConversations); err != nil || draftConversations != 0 {
		t.Fatalf("draft conversations=%d err=%v", draftConversations, err)
	}
	var confirmedHandoffs, confirmedTasks int
	var placement *int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*), max(assistant_message_id) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, confirmed.Handoff.ID).Scan(&confirmedHandoffs, &placement); err != nil || confirmedHandoffs != 1 || placement != nil {
		t.Fatalf("confirmed handoff count=%d placement=%v err=%v", confirmedHandoffs, placement, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE id = $1::uuid`, confirmed.Task.ID).Scan(&confirmedTasks); err != nil || confirmedTasks != 1 {
		t.Fatalf("confirmed tasks=%d err=%v", confirmedTasks, err)
	}
}

func TestChatRepositoryEditRewindCleansDraftHandoffAndDeleteBlockedByActiveDelivery(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	handoffs := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "delete-handoff", "delete-handoff@example.com")
	newSourceDraft := func(fingerprint byte) (model.Conversation, model.Message, store.HydratedChatHandoff) {
		t.Helper()
		source, _ := conversations.Create(ctx, owner.ID, "Source")
		prompt, _ := messages.AddChatUser(ctx, source.ID, "Schedule this")
		row, _, err := handoffs.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID,
			SourceUserMessageID: prompt.ID, SourceContentFingerprint: handoffFingerprint(fingerprint), InvocationOrdinal: 1, Title: testHandoffTitle, Timezone: scheduledTimezoneUTC})
		if err != nil {
			t.Fatal(err)
		}
		return source, prompt, row
	}
	editSource, editPrompt, editDraft := newSourceDraft(60)
	if _, err := messages.EditAndRewind(ctx, editSource.ID, editPrompt.ID, owner.ID, "Changed request"); err != nil {
		t.Fatal(err)
	}
	var editCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, editDraft.Handoff.ID).Scan(&editCount); err != nil || editCount != 0 {
		t.Fatalf("edited handoff count=%d err=%v", editCount, err)
	}
	deleteSource, _, draft := newSourceDraft(61)
	confirmedSourcePrompt, _ := messages.AddChatUser(ctx, deleteSource.ID, "Second scheduling request")
	confirmed, _, err := handoffs.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: deleteSource.ID,
		SourceUserMessageID: confirmedSourcePrompt.ID, SourceContentFingerprint: handoffFingerprint(62), InvocationOrdinal: 1, Title: "Confirmed", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handoffs.MarkTaskReady(ctx, owner.ID, confirmed.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE scheduled_tasks SET state = 'active' WHERE id = $1::uuid`, confirmed.Task.ID); err != nil {
		t.Fatal(err)
	}
	// CreateOrGetDraft now pre-sets delivery_conversation_id =
	// source_conversation_id, so the confirmed, active task still delivers
	// into deleteSource. Deleting a source chat an active task still
	// delivers into is blocked by the delivery_conversation_id FK (ON
	// DELETE RESTRICT); the whole delete transaction rolls back, so nothing
	// (including the still-draft handoff's own cleanup) is persisted.
	if err := conversations.Delete(ctx, deleteSource.ID, owner.ID); err == nil {
		t.Fatal("Delete err = nil, want FK-violation error for a source chat an active task still delivers into")
	}
	var draftCount, confirmedCount, confirmedTaskCount, confirmedConversationCount, sourceCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, draft.Handoff.ID).Scan(&draftCount); err != nil || draftCount != 1 {
		t.Fatalf("blocked delete draft handoff count=%d err=%v", draftCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, confirmed.Handoff.ID).Scan(&confirmedCount); err != nil || confirmedCount != 1 {
		t.Fatalf("blocked delete confirmed handoff count=%d err=%v", confirmedCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE id = $1::uuid`, confirmed.Task.ID).Scan(&confirmedTaskCount); err != nil || confirmedTaskCount != 1 {
		t.Fatalf("blocked delete confirmed task count=%d err=%v", confirmedTaskCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM conversations WHERE id = $1::uuid`, confirmed.Task.ConversationID).Scan(&confirmedConversationCount); err != nil || confirmedConversationCount != 1 {
		t.Fatalf("blocked delete confirmed scheduled conversation count=%d err=%v", confirmedConversationCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM conversations WHERE id = $1::uuid`, deleteSource.ID).Scan(&sourceCount); err != nil || sourceCount != 1 {
		t.Fatalf("blocked delete source conversation count=%d err=%v", sourceCount, err)
	}
}

// TestChatRepositoryDeleteBlockedByActiveDeliveryReturnsSentinel covers the
// error-mapping half of the blocked-delete path exercised by
// TestChatRepositoryEditRewindCleansDraftHandoffAndDeleteBlockedByActiveDelivery:
// when the delivery_conversation_id FK (ON DELETE RESTRICT) blocks the delete,
// conversations.Delete must return the distinguishable
// store.ErrConversationHasActiveDelivery sentinel (via errors.Is), not just a
// generic wrapped error, so handlers can map it to a friendly 409.
func TestChatRepositoryDeleteBlockedByActiveDeliveryReturnsSentinel(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	handoffs := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "delete-sentinel", "delete-sentinel@example.com")
	source, err := conversations.Create(ctx, owner.ID, "Source")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := messages.AddChatUser(ctx, source.ID, "Schedule this")
	if err != nil {
		t.Fatal(err)
	}
	confirmed, _, err := handoffs.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID,
		SourceUserMessageID: prompt.ID, SourceContentFingerprint: handoffFingerprint(70), InvocationOrdinal: 1, Title: "Confirmed", Timezone: scheduledTimezoneUTC})
	if err != nil {
		t.Fatal(err)
	}
	if err := handoffs.MarkTaskReady(ctx, owner.ID, confirmed.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE scheduled_tasks SET state = 'active' WHERE id = $1::uuid`, confirmed.Task.ID); err != nil {
		t.Fatal(err)
	}
	err = conversations.Delete(ctx, source.ID, owner.ID)
	if !errors.Is(err, store.ErrConversationHasActiveDelivery) {
		t.Fatalf("Delete err = %v, want errors.Is(err, store.ErrConversationHasActiveDelivery)", err)
	}
}

// TestChatRepositoryDeleteChatWithOnlyDraftHandoffCleansUp covers the
// draft-only happy path of conversations.Delete: a source chat whose only
// handoff is still an unconfirmed draft (no active/confirmed task delivering
// into it) can be deleted, and cleanupDraftHandoffsForConversation hard-cleans
// the draft handoff, its draft scheduled_task, and that task's own Scheduled
// definition conversation.
func TestChatRepositoryDeleteChatWithOnlyDraftHandoffCleansUp(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	conversations := store.NewConversationRepository(pool)
	messages := store.NewMessageRepository(pool)
	handoffs := store.NewScheduledHandoffRepository(pool)
	owner := createScheduledUser(t, ctx, users, "delete-draft-handoff", "delete-draft-handoff@example.com")
	source, err := conversations.Create(ctx, owner.ID, "Source")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := messages.AddChatUser(ctx, source.ID, "Schedule this")
	if err != nil {
		t.Fatal(err)
	}
	draft, _, err := handoffs.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{UserID: owner.ID, SourceConversationID: source.ID,
		SourceUserMessageID: prompt.ID, SourceContentFingerprint: handoffFingerprint(63), InvocationOrdinal: 1, Title: testHandoffTitle, Timezone: scheduledTimezoneUTC})
	if err != nil {
		t.Fatal(err)
	}
	if err := conversations.Delete(ctx, source.ID, owner.ID); err != nil {
		t.Fatalf("Delete draft-only source chat err=%v, want nil", err)
	}
	var handoffCount, taskCount, scheduledConversationCount, sourceCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, draft.Handoff.ID).Scan(&handoffCount); err != nil || handoffCount != 0 {
		t.Fatalf("draft handoff count=%d err=%v, want 0", handoffCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduled_tasks WHERE id = $1::uuid`, draft.Task.ID).Scan(&taskCount); err != nil || taskCount != 0 {
		t.Fatalf("draft task count=%d err=%v, want 0", taskCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM conversations WHERE id = $1::uuid`, draft.Task.ConversationID).Scan(&scheduledConversationCount); err != nil || scheduledConversationCount != 0 {
		t.Fatalf("draft scheduled conversation count=%d err=%v, want 0", scheduledConversationCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM conversations WHERE id = $1::uuid`, source.ID).Scan(&sourceCount); err != nil || sourceCount != 0 {
		t.Fatalf("deleted source conversation count=%d err=%v, want 0", sourceCount, err)
	}
}
