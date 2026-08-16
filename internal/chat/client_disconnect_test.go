package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
)

// disconnectingProvider streams some content, then cancels the caller's request
// context and fails — exactly what a browser closing an SSE connection mid-answer
// looks like from inside the turn.
type disconnectingProvider struct {
	cancel  context.CancelFunc
	partial string
}

func (p *disconnectingProvider) StreamChat(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	r, err := p.StreamChatWithTools(ctx, req, onToken)
	return r.Content, err
}

func (p *disconnectingProvider) StreamChatWithTools(
	_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	if err := onToken(p.partial); err != nil {
		return provider.StreamResult{}, err
	}
	p.cancel()
	return provider.StreamResult{Content: p.partial}, context.Canceled
}

// A client that disconnects mid-answer must not cost the user what the model
// already produced: the salvage write has to outlive the cancelled request
// context, or the turn leaves a user message with no reply and nothing to
// regenerate from.
func TestStreamPersistsPartialAnswerAfterClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prov := &disconnectingProvider{cancel: cancel, partial: "Here is the first half"}
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID},
	}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{
		Convs: convs, Msgs: msgs,
	})

	err := svc.Stream(ctx, testUserID, chat.UserContext{Username: testUsername}, testConvID,
		"tell me about my training", &capturingSink{})
	if err == nil {
		t.Fatal("Stream succeeded, want the cancelled turn to fail")
	}

	for _, ctxErr := range msgs.assistantSaveContextErrors {
		if errors.Is(ctxErr, context.Canceled) {
			t.Fatal("assistant save ran on the cancelled request context; the partial answer is lost")
		}
	}
	last := msgs.added[len(msgs.added)-1]
	if last.Role != model.MsgRoleAssistant || last.Content != prov.partial {
		t.Fatalf("persisted last message = %+v, want the partial assistant answer %q", last, prov.partial)
	}
}

// A save that burns its whole deadline before failing must not hand that spent
// context to the draft cleanup: the drafts would silently survive as orphans.
func TestScheduledDraftCleanupGetsLiveContextAfterSaveDeadlineExpires(t *testing.T) {
	handoff := &fakeScheduledHandoff{artifacts: []scheduled.ChatArtifact{{
		HandoffID: "orphan-handoff", TaskID: "orphan-task", Ordinal: 1,
		ArtifactState: testScheduledArtifactReady,
	}}}
	prov := &scriptedProvider{results: []provider.StreamResult{
		{ToolCalls: []provider.ToolCall{{
			ID: testScheduledCallID, Name: testScheduledToolName, Arguments: testScheduledArguments,
		}}},
		{Content: "Drafted."},
	}}
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID},
	}}
	msgs := &fakeMsgs{assistantSaveExhaustsDeadline: true}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs, Scheduled: handoff})

	_ = svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername},
		testConvID, "schedule it", &capturingSink{})

	if len(handoff.cleanupContextErrors) == 0 {
		t.Fatal("cleanup never ran, want the orphaned drafts cleaned up")
	}
	for _, ctxErr := range handoff.cleanupContextErrors {
		if ctxErr != nil {
			t.Fatalf("cleanup ran on an expired context (%v); drafts stay orphaned", ctxErr)
		}
	}
}

// assistantSaveTimeout must stay bounded. An unbounded detached context would
// let a hung database pin the write forever, which is what the deadline exists
// to prevent.
func TestAssistantSaveContextIsBounded(t *testing.T) {
	prov := &scriptedProvider{results: []provider.StreamResult{{Content: "answer"}}}
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID},
	}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
		chat.Deps{Convs: convs, Msgs: msgs})

	if err := svc.Stream(context.Background(), testUserID, chat.UserContext{Username: testUsername},
		testConvID, "hello", &capturingSink{}); err != nil {
		t.Fatal(err)
	}

	if len(msgs.assistantSaveHadDeadlines) != 1 || !msgs.assistantSaveHadDeadlines[0] {
		t.Fatalf("assistant save deadlines = %v, want exactly one bounded save",
			msgs.assistantSaveHadDeadlines)
	}
	if remaining := msgs.assistantSaveDeadlineIn[0]; remaining <= 0 || remaining > time.Minute {
		t.Fatalf("assistant save deadline in %v, want a bounded, generous window", remaining)
	}
}

// cancellingVerdictProvider returns an off-topic verdict and then cancels the
// request, so the guardrail refusal is persisted after the client is gone.
type cancellingVerdictProvider struct {
	cancel  context.CancelFunc
	verdict string
}

func (p *cancellingVerdictProvider) StreamChat(
	_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc,
) (string, error) {
	p.cancel()
	return p.verdict, nil
}

func (p *cancellingVerdictProvider) StreamChatWithTools(
	_ context.Context, _ provider.ChatRequest, _ provider.TokenFunc,
) (provider.StreamResult, error) {
	p.cancel()
	return provider.StreamResult{Content: p.verdict}, nil
}

// The completed-turn save and the guardrail refusal must survive a disconnect
// for the same reason the salvage path must: reverting either of them to the
// request context would silently drop the reply.
func TestAssistantSavesSurviveClientDisconnect(t *testing.T) {
	t.Run("completed turn", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Cancels mid-stream but still returns successfully, so the turn
		// completes normally and reaches the completed-turn save rather than
		// the salvage path.
		prov := &cancellingVerdictProvider{cancel: cancel, verdict: "the whole answer"}
		convs := &fakeConvs{byID: map[string]model.Conversation{
			testConvID: {ID: testConvID, UserID: testUserID},
		}}
		msgs := &fakeMsgs{}
		svc := chat.NewService(prov, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
			chat.Deps{Convs: convs, Msgs: msgs})

		_ = svc.Stream(ctx, testUserID, chat.UserContext{Username: testUsername},
			testConvID, "tell me", &capturingSink{})

		assertAssistantSaveSurvived(t, msgs, "the whole answer")
	})

	t.Run("guardrail refusal", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		guard := chat.NewGuardrail(
			&cancellingVerdictProvider{cancel: cancel, verdict: testGuardrailOffTopic},
			chat.GuardrailConfig{
				Model: testGuardrailClassifierModel, DomainName: testGuardrailDomain,
				AllowedTopics: testGuardrailTopics, RefusalMessage: testGuardrailRefusal,
				HistoryWindow: 6,
			})
		msgs := &fakeMsgs{}
		svc := chat.NewService(&recordingProvider{},
			chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
			chat.Deps{Convs: &fakeConvs{byID: map[string]model.Conversation{}}, Msgs: msgs, Guardrail: guard})

		_ = svc.Stream(ctx, testUserID, chat.UserContext{Username: testUsername},
			"", "what's the stock market doing?", &capturingSink{})

		assertAssistantSaveSurvived(t, msgs, testGuardrailRefusal)
	})
}

func assertAssistantSaveSurvived(t *testing.T, msgs *fakeMsgs, wantContent string) {
	t.Helper()
	for _, ctxErr := range msgs.assistantSaveContextErrors {
		if errors.Is(ctxErr, context.Canceled) {
			t.Fatal("assistant save ran on the cancelled request context; the reply is lost")
		}
	}
	if len(msgs.added) == 0 {
		t.Fatal("nothing persisted, want the assistant reply")
	}
	if last := msgs.added[len(msgs.added)-1]; last.Role != model.MsgRoleAssistant ||
		last.Content != wantContent {
		t.Fatalf("persisted last message = %+v, want assistant %q", last, wantContent)
	}
}
