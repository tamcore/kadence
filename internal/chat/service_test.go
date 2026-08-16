package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

func TestStreamNewConversation(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), 7, chat.UserContext{Username: testUsername}, "", "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if sink.events[0].Type != chat.EventMeta || sink.events[0].ConversationID != testNewConvID ||
		sink.events[0].UserMessageID != 1 {
		t.Fatalf("first event = %+v, want meta with conv and user message ids", sink.events[0])
	}
	if last := sink.events[len(sink.events)-1]; last.Type != chat.EventDone || last.AssistantMessageID != 2 {
		t.Fatalf("last event = %+v, want done with assistant message id", last)
	}
	var streamed strings.Builder
	for _, e := range sink.events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if streamed.String() != testReply {
		t.Fatalf("streamed = %q", streamed.String())
	}
	if len(msgs.added) != 2 || msgs.added[0].Role != model.MsgRoleUser || msgs.added[1].Role != model.MsgRoleAssistant || msgs.added[1].Content != testReply {
		t.Fatalf("persisted messages wrong: %+v", msgs.added)
	}
	if msgs.createdConversation == nil {
		t.Fatal("expected a conversation to be created")
	}
}

func TestStreamExistingConversation(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if sink.events[0].Type != chat.EventMeta || sink.events[0].ConversationID != testConvID {
		t.Fatalf("first event = %+v, want meta with conv id %s", sink.events[0], testConvID)
	}
	if convs.created != nil {
		t.Fatal("should not create new conversation when id provided")
	}
}

func TestEditRewindsAndGeneratesFromEditedPromptWithoutDuplicate(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle},
	}}
	msgs := &fakeMsgs{added: []model.Message{
		{ID: 1, ConversationID: testConvID, Role: model.MsgRoleUser, Content: testFirstUserMessage},
		{ID: 2, ConversationID: testConvID, Role: model.MsgRoleAssistant, Content: testAssistantAnswer},
		{ID: 3, ConversationID: testConvID, Role: model.MsgRoleUser, Content: "old prompt"},
		{ID: 4, ConversationID: testConvID, Role: model.MsgRoleAssistant, Content: testOldAssistantResponse},
		{ID: 5, ConversationID: testConvID, Role: model.MsgRoleUser, Content: testUserLater},
	}}
	provider := &requestCapturingProvider{reply: replacementReply}
	svc := chat.NewService(provider,
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Edit(
		context.Background(), testUserID, chat.UserContext{Username: testUsername},
		testConvID, 3, "edited prompt", sink,
	); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if len(msgs.added) != 4 || msgs.added[2].ID != 3 || msgs.added[2].Content != "edited prompt" ||
		msgs.added[3].Role != model.MsgRoleAssistant || msgs.added[3].Content != replacementReply {
		t.Fatalf("persisted messages = %+v", msgs.added)
	}
	if got := countProviderMessage(provider.request.Messages, model.MsgRoleUser, "edited prompt"); got != 1 {
		t.Fatalf("edited prompt provider count = %d, want 1; messages=%+v", got, provider.request.Messages)
	}
	if sink.events[0].UserMessageID != 3 || sink.events[len(sink.events)-1].AssistantMessageID != 4 {
		t.Fatalf("events = %+v", sink.events)
	}
}

func TestRegenerateRewindsAndReusesPromptWithoutDuplicate(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle},
	}}
	msgs := &fakeMsgs{added: []model.Message{
		{ID: 1, ConversationID: testConvID, Role: model.MsgRoleUser, Content: testFirstUserMessage},
		{ID: 2, ConversationID: testConvID, Role: model.MsgRoleAssistant, Content: testAssistantAnswer},
		{ID: 3, ConversationID: testConvID, Role: model.MsgRoleUser, Content: "retry me"},
		{ID: 4, ConversationID: testConvID, Role: model.MsgRoleAssistant, Content: testOldAssistantResponse},
		{ID: 5, ConversationID: testConvID, Role: model.MsgRoleUser, Content: testUserLater},
	}}
	provider := &requestCapturingProvider{reply: replacementReply}
	svc := chat.NewService(provider,
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Regenerate(
		context.Background(), testUserID, chat.UserContext{Username: testUsername},
		testConvID, 4, sink,
	); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}

	if len(msgs.added) != 4 || msgs.added[2].ID != 3 || msgs.added[3].Content != replacementReply {
		t.Fatalf("persisted messages = %+v", msgs.added)
	}
	if got := countProviderMessage(provider.request.Messages, model.MsgRoleUser, "retry me"); got != 1 {
		t.Fatalf("regenerated prompt provider count = %d, want 1; messages=%+v", got, provider.request.Messages)
	}
	if sink.events[0].UserMessageID != 3 || sink.events[len(sink.events)-1].AssistantMessageID != 4 {
		t.Fatalf("events = %+v", sink.events)
	}
}

type requestCapturingProvider struct {
	reply   string
	request provider.ChatRequest
}

func (p *requestCapturingProvider) StreamChat(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	result, err := p.StreamChatWithTools(ctx, req, onToken)
	return result.Content, err
}

func (p *requestCapturingProvider) StreamChatWithTools(
	_ context.Context, req provider.ChatRequest, onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	p.request = req
	if err := onToken(p.reply); err != nil {
		return provider.StreamResult{}, err
	}
	return provider.StreamResult{Content: p.reply}, nil
}

func countProviderMessage(messages []provider.Message, role, content string) int {
	count := 0
	for _, message := range messages {
		if message.Role == role && message.Content == content {
			count++
		}
	}
	return count
}

func TestStreamConversationNotFound(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{reply: testReply},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, "missing-uuid", "hi coach", sink)
	if err == nil || err.Error() != "conversation not found" {
		t.Fatalf("expected 'conversation not found' error, got: %v", err)
	}
	if len(sink.events) == 0 || sink.events[0].Type != chat.EventError {
		t.Fatalf("expected error event, got: %v", sink.events)
	}
}

func TestStreamProviderError(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(fakeProvider{err: &providerErr{}},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "hi coach", sink)
	if err == nil || err.Error() != "the assistant could not complete the response" {
		t.Fatalf("expected provider error, got: %v", err)
	}
	if len(sink.events) == 0 || sink.events[len(sink.events)-1].Type != chat.EventError {
		t.Fatalf("expected error event in sink, got: %v", sink.events)
	}
}

type partialErrorProvider struct{}

func (partialErrorProvider) StreamChat(_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	return "", &providerErr{}
}

func (partialErrorProvider) StreamChatWithTools(
	_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	if err := onToken("partial reply"); err != nil {
		return provider.StreamResult{}, err
	}
	return provider.StreamResult{Content: "partial reply"}, &providerErr{}
}

func TestStreamProviderErrorCarriesPersistedPartialAssistant(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle},
	}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(partialErrorProvider{},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "hi coach", sink); err == nil {
		t.Fatal("Stream should fail after persisting the partial response")
	}
	last := sink.events[len(sink.events)-1]
	if last.Type != chat.EventError || last.AssistantMessageID != 2 ||
		last.AssistantContent == nil || *last.AssistantContent != "partial reply" {
		t.Fatalf("provider error event = %+v, want persisted partial assistant", last)
	}
}

func TestStreamProviderErrorOmitsAssistantIdentityWhenPartialIsStale(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle},
	}}
	msgs := &fakeMsgs{rejectAssistant: true}
	svc := chat.NewService(partialErrorProvider{},
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "hi coach", sink); err == nil {
		t.Fatal("Stream should fail when the partial response is stale")
	}
	last := sink.events[len(sink.events)-1]
	if last.Type != chat.EventError || last.AssistantMessageID != 0 || last.AssistantContent != nil {
		t.Fatalf("stale provider error event = %+v, want no assistant identity", last)
	}
	if len(msgs.added) != 1 || msgs.added[0].Role != model.MsgRoleUser {
		t.Fatalf("messages after stale partial = %+v, want only the user message", msgs.added)
	}
}

type preliminaryToolProvider struct{ calls int }

func (p *preliminaryToolProvider) StreamChat(_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	return "", errors.New("StreamChat should not be called")
}

func (p *preliminaryToolProvider) StreamChatWithTools(
	_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	p.calls++
	if p.calls == 1 {
		if err := onToken("preliminary text"); err != nil {
			return provider.StreamResult{}, err
		}
		return provider.StreamResult{Content: "preliminary text", ToolCalls: []provider.ToolCall{{
			ID: "call-1", Name: testToolName, Arguments: "{}",
		}}}, nil
	}
	if err := onToken("canonical answer"); err != nil {
		return provider.StreamResult{}, err
	}
	return provider.StreamResult{Content: "canonical answer"}, nil
}

func TestStreamContinuesTruncatedAnswer(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	// First completion stops on "length" (hit the token cap); the second
	// finishes normally. The service should stitch them into one answer.
	p := &scriptedProvider{results: []provider.StreamResult{
		{Content: "part one ", FinishReason: provider.FinishLength},
		{Content: "part two.", FinishReason: "stop"},
	}}
	svc := chat.NewService(p,
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial + one continuation)", p.calls)
	}

	const want = "part one part two."
	var streamed strings.Builder
	for _, e := range sink.events {
		if e.Type == chat.EventToken {
			streamed.WriteString(e.Delta)
		}
	}
	if streamed.String() != want {
		t.Fatalf("streamed = %q, want %q", streamed.String(), want)
	}
	assistant := msgs.added[len(msgs.added)-1]
	if assistant.Role != model.MsgRoleAssistant || assistant.Content != want {
		t.Fatalf("persisted assistant = %+v, want content %q", assistant, want)
	}
}

func TestStreamStopsContinuingAtCap(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	// Every completion reports "length": the service must not loop forever.
	results := make([]provider.StreamResult, 10)
	for i := range results {
		results[i] = provider.StreamResult{Content: "x", FinishReason: provider.FinishLength}
	}
	p := &scriptedProvider{results: results}
	svc := chat.NewService(p,
		chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp, SystemPrompt: testSystemMsg},
		chat.Deps{Convs: convs, Msgs: msgs})

	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "hi coach", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// initial call + maxContinuations (3) = 4, then it gives up.
	if p.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (initial + 3 continuations)", p.calls)
	}
}

type providerErr struct{}

func (*providerErr) Error() string { return "provider failed" }

const testTimeout = 5 * time.Second

type deadlineAssertingProvider struct {
	t     *testing.T
	reply string
}

func (p deadlineAssertingProvider) StreamChat(ctx context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	p.t.Helper()
	if _, ok := ctx.Deadline(); !ok {
		p.t.Fatal("expected ctx to have a deadline when ServiceConfig.Timeout is set")
	}
	if err := onToken(p.reply); err != nil {
		return "", err
	}
	return p.reply, nil
}

func (p deadlineAssertingProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

func TestStreamAppliesTimeout(t *testing.T) {
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID, Title: testConvTitle}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(deadlineAssertingProvider{t: t, reply: testReply},
		chat.ServiceConfig{
			Model: testModel, MaxTokens: testMaxTokens, Temperature: testTemp,
			SystemPrompt: testSystemMsg, Timeout: testTimeout,
		},
		chat.Deps{Convs: convs, Msgs: msgs})

	sink := &capturingSink{}
	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "hi coach", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
}

type timeoutPartialProvider struct{}

func (timeoutPartialProvider) StreamChat(
	ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc,
) (string, error) {
	result, err := (timeoutPartialProvider{}).StreamChatWithTools(ctx, req, onToken)
	return result.Content, err
}

func (timeoutPartialProvider) StreamChatWithTools(
	ctx context.Context, _ provider.ChatRequest, onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	if err := onToken("partial before timeout"); err != nil {
		return provider.StreamResult{}, err
	}
	<-ctx.Done()
	return provider.StreamResult{Content: "partial before timeout"}, ctx.Err()
}

type nearDeadlineProvider struct{}

func (nearDeadlineProvider) StreamChat(
	ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc,
) (string, error) {
	result, err := (nearDeadlineProvider{}).StreamChatWithTools(ctx, req, onToken)
	return result.Content, err
}

func (nearDeadlineProvider) StreamChatWithTools(
	ctx context.Context, _ provider.ChatRequest, onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return provider.StreamResult{}, errors.New("provider context has no deadline")
	}
	wait := time.Until(deadline) / 2
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return provider.StreamResult{}, ctx.Err()
	case <-timer.C:
	}
	if err := onToken("completed near deadline"); err != nil {
		return provider.StreamResult{}, err
	}
	return provider.StreamResult{Content: "completed near deadline"}, nil
}

func TestTurnDeadlineDoesNotReplaceAssistantPersistenceContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider provider.Provider
		wantErr  bool
	}{
		{name: "partial timeout", provider: timeoutPartialProvider{}, wantErr: true},
		{name: "near deadline completion", provider: nearDeadlineProvider{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := &fakeMsgs{}
			svc := chat.NewService(tc.provider,
				chat.ServiceConfig{
					Model: testModel, MaxTokens: testMaxTokens,
					Timeout: 40 * time.Millisecond,
				},
				chat.Deps{
					Convs: &fakeConvs{byID: map[string]model.Conversation{
						testConvID: {
							ID: testConvID, UserID: testUserID, Title: testConvTitle,
						},
					}},
					Msgs: msgs,
				},
			)

			err := svc.Stream(
				context.Background(), testUserID,
				chat.UserContext{Username: testUsername},
				testConvID, "persist this", &capturingSink{},
			)
			if tc.wantErr && err == nil {
				t.Fatal("Stream error = nil, want provider timeout")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if len(msgs.assistantSaveContextErrors) != 1 {
				t.Fatalf(
					"assistant save contexts = %v, want one persistence attempt",
					msgs.assistantSaveContextErrors,
				)
			}
			if msgs.assistantSaveContextErrors[0] != nil ||
				msgs.assistantSaveHadDeadlines[0] {
				t.Fatalf(
					"assistant persistence inherited external deadline: err=%v deadline=%v",
					msgs.assistantSaveContextErrors[0],
					msgs.assistantSaveHadDeadlines[0],
				)
			}
		})
	}
}

// recordingProvider records whether StreamChat was called; returns a canned reply.
const (
	testGuardrailClassifierModel = "c"
	testGuardrailDomain          = "Coach"
	testGuardrailTopics          = "training"
	testGuardrailRefusal         = "nope, coaching only"
	testGuardrailOffTopic        = "OFF_TOPIC"
)

type recordingProvider struct{ called bool }

func (p *recordingProvider) StreamChat(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	p.called = true
	_ = onToken("hello")
	return "hello", nil
}

func (p *recordingProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}
