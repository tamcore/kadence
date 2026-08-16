package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
)

func TestStreamNewConversationEmitsGeneratedTitleBeforeDone(t *testing.T) {
	pinnedAt := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	lastActivityAt := time.Date(2026, 8, 9, 8, 1, 0, 0, time.UTC)
	createdAt := time.Date(2026, 8, 9, 7, 59, 0, 0, time.UTC)
	convs := &fakeConvs{
		byID:               map[string]model.Conversation{},
		titleUpdateSwapped: true,
		titleUpdateResult: model.Conversation{
			ID: testNewConvID, UserID: testUserID, Title: testGeneratedTitle,
			Kind: model.ConversationKindChat, PinnedAt: &pinnedAt,
			LastActivityAt: lastActivityAt, CreatedAt: createdAt,
		},
	}
	msgs := &fakeMsgs{}
	titles := &fakeTitleGenerator{title: testGeneratedTitle}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs, TitleGenerator: titles})
	sink := &capturingSink{}

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, "", "Review my marathon pacing", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if got, want := titles.inputs, []chat.ConversationTitleInput{{
		UserText: "Review my marathon pacing", AssistantText: testReply,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("title inputs = %+v, want %+v", got, want)
	}
	if got, want := convs.titleUpdateCalls, []titleUpdateCall{{
		id: testNewConvID, userID: testUserID, currentTitle: "Review my marathon pacing", newTitle: testGeneratedTitle,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("title update calls = %+v, want %+v", got, want)
	}
	if got, want := []string{
		sink.events[0].Type, sink.events[1].Type, sink.events[2].Type, sink.events[3].Type, sink.events[4].Type,
	}, []string{chat.EventMeta, chat.EventToken, chat.EventToken, chat.EventTitle, chat.EventDone}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event order = %v, want %v", got, want)
	}
	wantPinnedAt := "2026-08-09T08:00:00.000000Z"
	wantConversation := chat.EventConversation{
		ID: testNewConvID, Title: testGeneratedTitle, PinnedAt: &wantPinnedAt,
		LastActivityAt: "2026-08-09T08:01:00.000000Z",
		CreatedAt:      "2026-08-09T07:59:00.000000Z",
	}
	if got, want := sink.events[3].Conversation, &wantConversation; !reflect.DeepEqual(got, want) {
		t.Fatalf("title conversation = %+v, want %+v", got, want)
	}
}

func TestStreamTitleGenerationFailureKeepsSuccessfulChat(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	titles := &fakeTitleGenerator{err: errors.New("title provider marker")}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: &fakeMsgs{}, TitleGenerator: titles})
	sink := &capturingSink{}

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, "", "Review my marathon pacing", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(titles.inputs) != 1 || len(convs.titleUpdateCalls) != 0 {
		t.Fatalf("title calls inputs=%+v updates=%+v", titles.inputs, convs.titleUpdateCalls)
	}
	if last := sink.events[len(sink.events)-1]; last.Type != chat.EventDone {
		t.Fatalf("last event = %+v, want done", last)
	}
	for _, event := range sink.events {
		if event.Type == chat.EventTitle {
			t.Fatalf("unexpected title event: %+v", event)
		}
	}
	encoded, err := json.Marshal(sink.events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	if strings.Contains(string(encoded), "title provider marker") {
		t.Fatalf("events exposed title error: %s", encoded)
	}
}

func TestStreamTitlePersistenceFailureKeepsSuccessfulChat(t *testing.T) {
	errTitlePersistence := errors.New("title persistence marker")
	convs := &fakeConvs{
		byID: map[string]model.Conversation{}, titleUpdateErr: errTitlePersistence,
	}
	titles := &fakeTitleGenerator{title: testGeneratedTitle}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: &fakeMsgs{}, TitleGenerator: titles})
	sink := &capturingSink{}

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, "", "Review my marathon pacing", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(convs.titleUpdateCalls) != 1 {
		t.Fatalf("title update calls = %+v, want one", convs.titleUpdateCalls)
	}
	if last := sink.events[len(sink.events)-1]; last.Type != chat.EventDone {
		t.Fatalf("last event = %+v, want done", last)
	}
	for _, event := range sink.events {
		if event.Type == chat.EventTitle {
			t.Fatalf("persistence failure emitted title event: %+v", event)
		}
	}
	encoded, err := json.Marshal(sink.events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	if strings.Contains(string(encoded), errTitlePersistence.Error()) {
		t.Fatalf("events exposed title error: %s", encoded)
	}
}

func TestStreamTitleCompareAndSetMissKeepsManualRename(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	titles := &fakeTitleGenerator{title: testGeneratedTitle}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: &fakeMsgs{}, TitleGenerator: titles})
	sink := &capturingSink{}

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, "", "Review my marathon pacing", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(convs.titleUpdateCalls) != 1 {
		t.Fatalf("title update calls = %+v, want one", convs.titleUpdateCalls)
	}
	if last := sink.events[len(sink.events)-1]; last.Type != chat.EventDone {
		t.Fatalf("last event = %+v, want done", last)
	}
	for _, event := range sink.events {
		if event.Type == chat.EventTitle {
			t.Fatalf("manual rename miss emitted title event: %+v", event)
		}
	}
}

func TestStreamExistingConversationSkipsTitleGeneration(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle},
	}}
	titles := &fakeTitleGenerator{title: testGeneratedTitle}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: &fakeMsgs{}, TitleGenerator: titles})

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "Review my marathon pacing", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(titles.inputs) != 0 || len(convs.titleUpdateCalls) != 0 {
		t.Fatalf("title calls inputs=%+v updates=%+v", titles.inputs, convs.titleUpdateCalls)
	}
}

func TestEditAndRegenerateSkipTitleGeneration(t *testing.T) {
	for _, operation := range []string{testOperationEdit, "regenerate"} {
		t.Run(operation, func(t *testing.T) {
			convs := &fakeConvs{byID: map[string]model.Conversation{
				testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle},
			}}
			msgs := &fakeMsgs{added: []model.Message{
				{ID: 1, ConversationID: testConvID, Role: model.MsgRoleUser, Content: testFirstUserMessage},
				{ID: 2, ConversationID: testConvID, Role: model.MsgRoleAssistant, Content: testAssistantAnswer},
				{ID: 3, ConversationID: testConvID, Role: model.MsgRoleUser, Content: "retry me"},
				{ID: 4, ConversationID: testConvID, Role: model.MsgRoleAssistant, Content: testOldAssistantResponse},
			}}
			titles := &fakeTitleGenerator{title: testGeneratedTitle}
			svc := chat.NewService(fakeProvider{reply: replacementReply},
				chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
				chat.Deps{Convs: convs, Msgs: msgs, TitleGenerator: titles})

			var err error
			if operation == testOperationEdit {
				err = svc.Edit(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, 3, "edited prompt", &capturingSink{})
			} else {
				err = svc.Regenerate(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, 4, &capturingSink{})
			}
			if err != nil {
				t.Fatalf("%s: %v", operation, err)
			}
			if len(titles.inputs) != 0 || len(convs.titleUpdateCalls) != 0 {
				t.Fatalf("title calls inputs=%+v updates=%+v", titles.inputs, convs.titleUpdateCalls)
			}
		})
	}
}

func TestStreamAssistantFailureSkipsTitleGeneration(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	titles := &fakeTitleGenerator{title: testGeneratedTitle}
	svc := chat.NewService(fakeProvider{err: &providerErr{}},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: &fakeMsgs{}, TitleGenerator: titles})

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, "", "Review my marathon pacing", &capturingSink{}); err == nil {
		t.Fatal("Stream succeeded, want assistant failure")
	}
	if len(titles.inputs) != 0 || len(convs.titleUpdateCalls) != 0 {
		t.Fatalf("title calls inputs=%+v updates=%+v", titles.inputs, convs.titleUpdateCalls)
	}
}

func TestStreamTitleDeliveryFailureStillAttemptsDone(t *testing.T) {
	for _, test := range []struct {
		name      string
		failSend  bool
		failFlush bool
	}{
		{name: "send", failSend: true},
		{name: "flush", failFlush: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			convs := &fakeConvs{
				byID: map[string]model.Conversation{}, titleUpdateSwapped: true,
				titleUpdateResult: model.Conversation{ID: testNewConvID, Title: testGeneratedTitle},
			}
			svc := chat.NewService(fakeProvider{reply: testReply},
				chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
				chat.Deps{Convs: convs, Msgs: &fakeMsgs{}, TitleGenerator: &fakeTitleGenerator{title: testGeneratedTitle}})
			sink := &titleDeliveryFailSink{failSend: test.failSend, failFlush: test.failFlush}

			if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, "", "Review my marathon pacing", sink); err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if sink.doneSends != 1 || sink.doneFlush != 1 {
				t.Fatalf("done delivery attempts sends=%d flushes=%d, want 1/1", sink.doneSends, sink.doneFlush)
			}
			if last := sink.events[len(sink.events)-1]; last.Type != chat.EventDone {
				t.Fatalf("last event = %+v, want done", last)
			}
		})
	}
}

func TestStreamTitleFailureWarningsIncludeSafeStageElapsedMilliseconds(t *testing.T) {
	const (
		userPayload      = "private user title payload"
		assistantPayload = "private assistant title payload"
		generatedTitle   = "private generated title"
	)
	tests := []struct {
		name      string
		message   string
		errorText string
		setup     func() (*fakeConvs, *fakeTitleGenerator, chat.EventSink)
	}{
		{
			name: "generation", message: "conversation title generation skipped",
			errorText: "private generation error",
			setup: func() (*fakeConvs, *fakeTitleGenerator, chat.EventSink) {
				return &fakeConvs{byID: map[string]model.Conversation{}},
					&fakeTitleGenerator{err: errors.New("private generation error")},
					&capturingSink{}
			},
		},
		{
			name: "persistence", message: "conversation title persistence skipped",
			errorText: "private persistence error",
			setup: func() (*fakeConvs, *fakeTitleGenerator, chat.EventSink) {
				return &fakeConvs{
					byID:           map[string]model.Conversation{},
					titleUpdateErr: errors.New("private persistence error"),
				}, &fakeTitleGenerator{title: generatedTitle}, &capturingSink{}
			},
		},
		{
			name: "delivery send", message: "conversation title delivery skipped",
			errorText: "title delivery marker",
			setup: func() (*fakeConvs, *fakeTitleGenerator, chat.EventSink) {
				return &fakeConvs{
					byID: map[string]model.Conversation{}, titleUpdateSwapped: true,
					titleUpdateResult: model.Conversation{ID: testNewConvID, Title: generatedTitle},
				}, &fakeTitleGenerator{title: generatedTitle}, &titleDeliveryFailSink{failSend: true}
			},
		},
		{
			name: "delivery flush", message: "conversation title delivery skipped",
			errorText: "title delivery marker",
			setup: func() (*fakeConvs, *fakeTitleGenerator, chat.EventSink) {
				return &fakeConvs{
					byID: map[string]model.Conversation{}, titleUpdateSwapped: true,
					titleUpdateResult: model.Conversation{ID: testNewConvID, Title: generatedTitle},
				}, &fakeTitleGenerator{title: generatedTitle}, &titleDeliveryFailSink{failFlush: true}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			convs, titles, sink := test.setup()
			svc := chat.NewService(fakeProvider{reply: assistantPayload},
				chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
				chat.Deps{Convs: convs, Msgs: &fakeMsgs{}, TitleGenerator: titles})
			var logs bytes.Buffer
			previousLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, "", userPayload, sink)
			slog.SetDefault(previousLogger)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			for _, privateValue := range []string{userPayload, assistantPayload, generatedTitle, test.errorText} {
				if strings.Contains(logs.String(), privateValue) {
					t.Fatalf("logs exposed private value %q: %s", privateValue, logs.String())
				}
			}
			decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
			var warning struct {
				Message   string `json:"msg"`
				ElapsedMS *int64 `json:"elapsed_ms"`
			}
			for decoder.More() {
				var entry struct {
					Message   string `json:"msg"`
					ElapsedMS *int64 `json:"elapsed_ms"`
				}
				if err := decoder.Decode(&entry); err != nil {
					t.Fatalf("decode log: %v", err)
				}
				if entry.Message == test.message {
					warning = entry
					break
				}
			}
			if warning.Message == "" {
				t.Fatalf("warning %q missing from logs: %s", test.message, logs.String())
			}
			if warning.ElapsedMS == nil || *warning.ElapsedMS < 0 {
				t.Fatalf("elapsed_ms=%v, want non-negative integer in warning: %s", warning.ElapsedMS, logs.String())
			}
		})
	}
}

func TestStreamTruncatesTitleASCII(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs})

	// ASCII string with 70 characters → should be truncated to 60 runes.
	longASCII := strings.Repeat("a", 70)
	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", longASCII, sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if msgs.createdConversation == nil {
		t.Fatal("expected a conversation to be created")
	}
	if len(msgs.createdConversation.Title) != 60 {
		t.Fatalf("title length = %d, want 60 (runes)", len(msgs.createdConversation.Title))
	}
	if msgs.createdConversation.Title != strings.Repeat("a", 60) {
		t.Fatalf("title = %q, want 60 'a' characters", msgs.createdConversation.Title)
	}
}

func TestStreamTruncatesTitleMultibyte(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs})

	// String with emoji (multi-byte in UTF-8).
	// Create a string with 70 runes (all emoji) → should be truncated to 60 runes.
	longMultibyte := strings.Repeat("🎯", 70) // Dart/target emoji, 4 bytes each
	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", longMultibyte, sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if msgs.createdConversation == nil {
		t.Fatal("expected a conversation to be created")
	}

	// Verify it's valid UTF-8
	if !utf8.ValidString(msgs.createdConversation.Title) {
		t.Fatalf("title is not valid UTF-8: %q", msgs.createdConversation.Title)
	}

	// Verify it's 60 runes (not bytes)
	runes := []rune(msgs.createdConversation.Title)
	if len(runes) != 60 {
		t.Fatalf("title has %d runes, want 60", len(runes))
	}

	// Verify it's the correct content (60 fire emojis)
	if msgs.createdConversation.Title != strings.Repeat("🎯", 60) {
		t.Fatalf("title = %q, want 60 fire emojis", msgs.createdConversation.Title)
	}
}

func TestStreamKeepsTitleUnchangedWhenShort(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs})

	// Short string with mixed ASCII and emoji.
	shortTitle := "Hello 👋 World"
	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "", shortTitle, sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if msgs.createdConversation == nil {
		t.Fatal("expected a conversation to be created")
	}

	// Short strings should be unchanged.
	if msgs.createdConversation.Title != shortTitle {
		t.Fatalf("title = %q, want %q", msgs.createdConversation.Title, shortTitle)
	}
}
