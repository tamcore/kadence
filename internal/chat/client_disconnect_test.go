package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
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
