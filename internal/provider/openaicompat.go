package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

// imageDetailHigh requests the API's highest-fidelity image analysis for
// every attached image, since chat attachments (charts, workout screenshots)
// often need fine detail to be useful.
const imageDetailHigh = "high"

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
	res, err := p.StreamChatWithTools(ctx, req, onToken)
	return res.Content, err
}

// StreamChatWithTools streams a chat completion like StreamChat, but also
// supports tool-calling: any tool calls the model requests are assembled
// from the stream and returned alongside the content.
func (p *OpenAICompat) StreamChatWithTools(ctx context.Context, req ChatRequest, onToken TokenFunc) (StreamResult, error) {
	msgs := buildMessages(req.Messages)

	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
		// max_completion_tokens (not the deprecated max_tokens) — required by
		// GPT-5-family models and accepted by older ones.
		MaxCompletionTokens: param.NewOpt(int64(req.MaxTokens)),
		Temperature:         param.NewOpt(req.Temperature),
	}
	if len(req.Tools) > 0 {
		tools, toolsErr := buildTools(req.Tools)
		if toolsErr != nil {
			return StreamResult{}, fmt.Errorf("build tools: %w", toolsErr)
		}
		params.Tools = tools
	}

	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		if err := onToken(delta); err != nil {
			return StreamResult{Content: acc.Choices[0].Message.Content}, err
		}
	}
	if err := stream.Err(); err != nil {
		result := accumulatedStreamResult(acc)
		if isVisionUnsupportedError(err) {
			return result, fmt.Errorf("stream chat: %w: %w", ErrVisionUnsupported, err)
		}
		return result, fmt.Errorf("stream chat: %w", err)
	}
	if len(acc.Choices) == 0 {
		return StreamResult{}, nil
	}

	msg := acc.Choices[0].Message
	toolCalls := make([]ToolCall, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return StreamResult{
		Content:      msg.Content,
		ToolCalls:    toolCalls,
		FinishReason: acc.Choices[0].FinishReason,
	}, nil
}

func accumulatedStreamResult(acc openai.ChatCompletionAccumulator) StreamResult {
	if len(acc.Choices) == 0 {
		return StreamResult{}
	}
	message := acc.Choices[0].Message
	return StreamResult{
		Content:      message.Content,
		FinishReason: acc.Choices[0].FinishReason,
	}
}

func isVisionUnsupportedError(err error) bool {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
	default:
		return false
	}
	code := strings.ToLower(apiErr.Code)
	message := strings.ToLower(apiErr.Message)
	param := strings.ToLower(apiErr.Param)
	mentionsVision := strings.Contains(code, "image") ||
		strings.Contains(code, "vision") ||
		strings.Contains(code, "multimodal") ||
		strings.Contains(message, "image") ||
		strings.Contains(message, "vision") ||
		strings.Contains(message, "multimodal") ||
		strings.Contains(param, "image")
	if !mentionsVision {
		return false
	}
	unsupported := strings.Contains(code, "unsupported") ||
		strings.Contains(message, "not supported") ||
		strings.Contains(message, "unsupported") ||
		strings.Contains(message, "only supports text")
	return unsupported
}

// buildMessages converts provider messages into openai-go message params,
// including tool-call requests (assistant) and tool results (role="tool").
func buildMessages(reqMessages []Message) []openai.ChatCompletionMessageParamUnion {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(reqMessages))
	for _, m := range reqMessages {
		switch m.Role {
		case "system":
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case "tool":
			msgs = append(msgs, openai.ToolMessage(m.Content, m.ToolCallID))
		case "assistant":
			if len(m.ToolCalls) == 0 {
				msgs = append(msgs, openai.AssistantMessage(m.Content))
				continue
			}
			msgs = append(msgs, buildAssistantToolCallMessage(m))
		case "user":
			msgs = append(msgs, buildUserMessage(m))
		default:
			msgs = append(msgs, openai.UserMessage(m.Content))
		}
	}
	return msgs
}

func buildUserMessage(m Message) openai.ChatCompletionMessageParamUnion {
	if len(m.Images) == 0 {
		return openai.UserMessage(m.Content)
	}

	parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(m.Images)+1)
	if m.Content != "" {
		parts = append(parts, openai.TextContentPart(m.Content))
	}
	for _, image := range m.Images {
		dataURL := fmt.Sprintf(
			"data:%s;base64,%s",
			image.MIMEType,
			base64.StdEncoding.EncodeToString(image.Data),
		)
		parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			URL:    dataURL,
			Detail: imageDetailHigh,
		}))
	}
	return openai.UserMessage(parts)
}

// buildAssistantToolCallMessage builds an assistant message param carrying
// the tool calls the assistant previously requested.
func buildAssistantToolCallMessage(m Message) openai.ChatCompletionMessageParamUnion {
	assistant := openai.ChatCompletionAssistantMessageParam{}
	if m.Content != "" {
		assistant.Content.OfString = param.NewOpt(m.Content)
	}
	assistant.ToolCalls = make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: tc.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			},
		})
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
}

// buildTools converts provider tool definitions into openai-go tool params.
func buildTools(defs []ToolDefinition) ([]openai.ChatCompletionToolUnionParam, error) {
	tools := make([]openai.ChatCompletionToolUnionParam, 0, len(defs))
	for _, def := range defs {
		var schema shared.FunctionParameters
		if len(def.Parameters) > 0 {
			if err := json.Unmarshal(def.Parameters, &schema); err != nil {
				return nil, fmt.Errorf("unmarshal tool %q parameters: %w", def.Name, err)
			}
		}
		tools = append(tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        def.Name,
			Description: param.NewOpt(def.Description),
			Parameters:  schema,
		}))
	}
	return tools, nil
}
