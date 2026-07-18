package provider

import (
	"context"
	"fmt"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

// OpenAICompat is a Provider backed by any OpenAI-compatible chat API.
type OpenAICompat struct {
	client openai.Client
}

// NewOpenAICompat builds an OpenAI-compatible provider for the given base URL + key.
func NewOpenAICompat(baseURL, apiKey string) *OpenAICompat {
	return &OpenAICompat{
		client: openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey(apiKey),
		),
	}
}

// StreamChat streams a chat completion, calling onToken per delta.
func (p *OpenAICompat) StreamChat(ctx context.Context, req ChatRequest, onToken TokenFunc) (string, error) {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case "assistant":
			msgs = append(msgs, openai.AssistantMessage(m.Content))
		default:
			msgs = append(msgs, openai.UserMessage(m.Content))
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:       req.Model,
		Messages:    msgs,
		MaxTokens:   param.NewOpt(int64(req.MaxTokens)),
		Temperature: param.NewOpt(req.Temperature),
	}

	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	var buf strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		buf.WriteString(delta)
		if err := onToken(delta); err != nil {
			return buf.String(), err
		}
	}
	if err := stream.Err(); err != nil {
		return buf.String(), fmt.Errorf("stream chat: %w", err)
	}
	return buf.String(), nil
}
