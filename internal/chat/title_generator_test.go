package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

const titleTestUserText = "run"

type titleProvider struct {
	reply string
	err   error
	req   provider.ChatRequest
}

func (p *titleProvider) StreamChat(
	_ context.Context,
	req provider.ChatRequest,
	_ provider.TokenFunc,
) (string, error) {
	p.req = req
	return p.reply, p.err
}

func (p *titleProvider) StreamChatWithTools(
	ctx context.Context,
	req provider.ChatRequest,
	onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

func TestConversationTitleGeneratorBuildsBoundedRequest(t *testing.T) {
	p := &titleProvider{reply: "Weekly Training Review"}
	generator := chat.NewLLMConversationTitleGenerator(p, "title-model")
	input := chat.ConversationTitleInput{
		UserText:      strings.Repeat("u", 4001),
		AssistantText: strings.Repeat("🎯", 4001),
	}

	_, err := generator.Generate(t.Context(), input)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got := p.req.Model; got != "title-model" {
		t.Fatalf("model = %q, want title-model", got)
	}
	if p.req.MaxTokens != 256 || p.req.Temperature != 0 || len(p.req.Tools) != 0 {
		t.Fatalf("request controls = %+v", p.req)
	}
	if len(p.req.Messages) != 2 || p.req.Messages[0].Role != model.MsgRoleSystem ||
		p.req.Messages[1].Role != model.MsgRoleUser {
		t.Fatalf("messages = %+v", p.req.Messages)
	}

	var payload struct {
		UserText      string `json:"userText"`
		AssistantText string `json:"assistantText"`
	}
	if err := json.Unmarshal([]byte(p.req.Messages[1].Content), &payload); err != nil {
		t.Fatalf("decode request payload: %v", err)
	}
	if got := len([]rune(payload.UserText)); got != 4000 {
		t.Errorf("user text rune length = %d, want 4000", got)
	}
	if got := len([]rune(payload.AssistantText)); got != 4000 {
		t.Errorf("assistant text rune length = %d, want 4000", got)
	}
}

func TestConversationTitleGeneratorNormalizesOutput(t *testing.T) {
	tests := []struct {
		reply string
		want  string
	}{
		{reply: "  Title:  Weekly   Training Review.  ", want: "Weekly Training Review."},
		{reply: "\n\"Maratón de Bratislava\"\n", want: "Maratón de Bratislava"},
		{reply: strings.Repeat("🎯", 70), want: strings.Repeat("🎯", 60)},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			generator := chat.NewLLMConversationTitleGenerator(&titleProvider{reply: tt.reply}, "title-model")
			got, err := generator.Generate(t.Context(), chat.ConversationTitleInput{UserText: titleTestUserText})
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Generate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConversationTitleGeneratorRejectsEmptyOutput(t *testing.T) {
	generator := chat.NewLLMConversationTitleGenerator(&titleProvider{reply: " \n\t "}, "title-model")
	_, err := generator.Generate(t.Context(), chat.ConversationTitleInput{UserText: titleTestUserText})
	if err == nil {
		t.Fatal("Generate() error = nil, want empty response error")
	}
	if strings.Contains(err.Error(), " \n\t ") {
		t.Errorf("error leaked model output: %v", err)
	}
}

func TestConversationTitleGeneratorHidesProviderError(t *testing.T) {
	providerErr := errors.New("raw provider response: credential-marker")
	generator := chat.NewLLMConversationTitleGenerator(&titleProvider{err: providerErr}, "title-model")

	_, err := generator.Generate(t.Context(), chat.ConversationTitleInput{UserText: titleTestUserText})
	if err == nil {
		t.Fatal("Generate() error = nil, want provider failure")
	}
	if got, want := err.Error(), "generate conversation title failed"; got != want {
		t.Errorf("Generate() error = %q, want %q", got, want)
	}
	if errors.Is(err, providerErr) {
		t.Error("Generate() error exposes provider error")
	}
}

type blockingTitleProvider struct{}

func (blockingTitleProvider) StreamChat(ctx context.Context, _ provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (p blockingTitleProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

func TestConversationTitleGeneratorPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	generator := chat.NewLLMConversationTitleGenerator(blockingTitleProvider{}, "title-model")

	start := time.Now()
	_, err := generator.Generate(ctx, chat.ConversationTitleInput{UserText: titleTestUserText})
	if err == nil {
		t.Fatal("Generate() error = nil, want cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Generate() error = %v, want context cancellation", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Generate() did not return promptly after cancellation")
	}
}
