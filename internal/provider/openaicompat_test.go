package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const (
	testRole  = "user"
	testModel = "test-model"
)

// Shared JSON field/value literals reused across the OpenAI-compatible
// vision content-part test fixtures below, to avoid goconst
// duplicate-literal warnings.
const (
	testImagePNGMime      = "image/png"
	jsonFieldType         = "type"
	jsonFieldTypeImageURL = "image_url"
)

// A minimal OpenAI-compatible streaming completion: two content chunks + [DONE].
const sseBody = "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
	"data: [DONE]\n\n"

// A streamed tool-call completion: the tool name and arguments arrive in
// pieces across chunks, followed by a finish_reason of "tool_calls".
const toolCallSSEBody = "" +
	"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"get_activities\",\"arguments\":\"\"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"limit\\\":\"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"5}\"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
	"data: [DONE]\n\n"

func TestOpenAICompatStreamChat(t *testing.T) {
	var requestBody struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	p := NewOpenAICompat(srv.URL, "test-key")

	var deltas []string
	full, err := p.StreamChat(context.Background(), ChatRequest{
		Messages:    []Message{{Role: testRole, Content: "hi"}},
		Model:       testModel,
		MaxTokens:   64,
		Temperature: 0.2,
	}, func(d string) error {
		deltas = append(deltas, d)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if full != "Hello world" {
		t.Fatalf("full text = %q, want %q", full, "Hello world")
	}
	if strings.Join(deltas, "|") != "Hello| world" {
		t.Fatalf("deltas = %v", deltas)
	}
	if len(requestBody.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(requestBody.Messages))
	}
	if got := string(requestBody.Messages[0].Content); got != `"hi"` {
		t.Fatalf("text-only content = %s, want JSON string", got)
	}
}

func TestOpenAICompatStreamChat_UserImagesUseOrderedContentParts(t *testing.T) {
	var requestBody struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	p := NewOpenAICompat(srv.URL, "test-key")
	_, err := p.StreamChat(context.Background(), ChatRequest{
		Messages: []Message{{
			Role:    testRole,
			Content: "describe these",
			Images: []ImageContent{
				{MIMEType: testImagePNGMime, Data: []byte{0, 1, 2}},
				{MIMEType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
			},
		}},
		Model: testModel,
	}, func(string) error { return nil })
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	if len(requestBody.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(requestBody.Messages))
	}
	if requestBody.Messages[0].Role != testRole {
		t.Fatalf("role = %q, want %q", requestBody.Messages[0].Role, testRole)
	}

	var got []any
	if err := json.Unmarshal(requestBody.Messages[0].Content, &got); err != nil {
		t.Fatalf("unmarshal user content: %v", err)
	}
	want := []any{
		map[string]any{jsonFieldType: "text", "text": "describe these"},
		map[string]any{
			jsonFieldType: jsonFieldTypeImageURL,
			jsonFieldTypeImageURL: map[string]any{
				"url":    "data:image/png;base64,AAEC",
				"detail": imageDetailHigh,
			},
		},
		map[string]any{
			jsonFieldType: jsonFieldTypeImageURL,
			jsonFieldTypeImageURL: map[string]any{
				"url":    "data:image/jpeg;base64,/9j/",
				"detail": imageDetailHigh,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("content = %#v, want %#v", got, want)
	}
}

func TestOpenAICompatStreamChat_UserImageWithoutTextOmitsTextPart(t *testing.T) {
	var requestBody struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	p := NewOpenAICompat(srv.URL, "test-key")
	_, err := p.StreamChat(context.Background(), ChatRequest{
		Messages: []Message{{
			Role:   testRole,
			Images: []ImageContent{{MIMEType: "image/webp", Data: []byte{1}}},
		}},
		Model: testModel,
	}, func(string) error { return nil })
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	if len(requestBody.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(requestBody.Messages))
	}
	var parts []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(requestBody.Messages[0].Content, &parts); err != nil {
		t.Fatalf("unmarshal user content: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("content parts = %d, want only the image part", len(parts))
	}
	if parts[0].Type != jsonFieldTypeImageURL {
		t.Fatalf("first content part type = %q, want image_url", parts[0].Type)
	}
}

func TestOpenAICompatMapsConservativeVisionCapabilityErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errorBody  string
	}{
		{
			name: "unsupported image code", statusCode: http.StatusBadRequest,
			errorBody: `{"error":{"message":"image input is not supported","type":"invalid_request_error","param":"messages[0].content[1].image_url","code":"unsupported_image"}}`,
		},
		{
			name: "unsupported multimodal media type", statusCode: http.StatusUnsupportedMediaType,
			errorBody: `{"error":{"message":"This model only supports text input, not vision","type":"invalid_request_error","param":"messages","code":"unsupported_media_type"}}`,
		},
		{
			name: "unprocessable image parameter", statusCode: http.StatusUnprocessableEntity,
			errorBody: `{"error":{"message":"multimodal content is not supported by this model","type":"invalid_request_error","param":"image_url","code":"invalid_value"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.errorBody))
			}))
			defer server.Close()

			_, err := NewOpenAICompat(server.URL, "test-key").StreamChatWithTools(
				context.Background(),
				ChatRequest{
					Model: testModel,
					Messages: []Message{{
						Role: testRole,
						Images: []ImageContent{{
							MIMEType: "image/png",
							Data:     []byte{1, 2, 3},
						}},
					}},
				},
				func(string) error { return nil },
			)
			if !errors.Is(err, ErrVisionUnsupported) {
				t.Fatalf("error = %v, want ErrVisionUnsupported", err)
			}
		})
	}
}

func TestOpenAICompatDoesNotMislabelOtherErrorsAsVisionUnsupported(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errorBody  string
	}{
		{
			name: "authentication", statusCode: http.StatusUnauthorized,
			errorBody: `{"error":{"message":"image model API key is invalid","type":"authentication_error","code":"invalid_api_key"}}`,
		},
		{
			name: "rate limit", statusCode: http.StatusTooManyRequests,
			errorBody: `{"error":{"message":"vision rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
		},
		{
			name: "general bad request", statusCode: http.StatusBadRequest,
			errorBody: `{"error":{"message":"invalid request body","type":"invalid_request_error","param":"messages","code":"invalid_request"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.errorBody))
			}))
			defer server.Close()

			_, err := NewOpenAICompat(server.URL, "test-key").StreamChatWithTools(
				context.Background(),
				ChatRequest{
					Model: testModel,
					Messages: []Message{{
						Role: testRole,
						Images: []ImageContent{{
							MIMEType: "image/png",
							Data:     []byte{1, 2, 3},
						}},
					}},
				},
				func(string) error { return nil },
			)
			if err == nil {
				t.Fatal("error = nil, want provider failure")
			}
			if errors.Is(err, ErrVisionUnsupported) {
				t.Fatalf("error = %v, must not be ErrVisionUnsupported", err)
			}
		})
	}
}

func TestOpenAICompatStreamChatWithTools_ContentOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	p := NewOpenAICompat(srv.URL, "test-key")

	var deltas []string
	res, err := p.StreamChatWithTools(context.Background(), ChatRequest{
		Messages:    []Message{{Role: testRole, Content: "hi"}},
		Model:       testModel,
		MaxTokens:   64,
		Temperature: 0.2,
	}, func(d string) error {
		deltas = append(deltas, d)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithTools: %v", err)
	}
	if res.Content != "Hello world" {
		t.Fatalf("content = %q, want %q", res.Content, "Hello world")
	}
	if len(res.ToolCalls) != 0 {
		t.Fatalf("tool calls = %v, want none", res.ToolCalls)
	}
	if strings.Join(deltas, "|") != "Hello| world" {
		t.Fatalf("deltas = %v", deltas)
	}
}

func TestOpenAICompatStreamChatWithTools_ToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(toolCallSSEBody))
	}))
	defer srv.Close()

	p := NewOpenAICompat(srv.URL, "test-key")

	params, err := json.Marshal(map[string]any{
		jsonFieldType: "object",
		"properties": map[string]any{
			"limit": map[string]any{jsonFieldType: "integer"},
		},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	var tokens int
	res, err := p.StreamChatWithTools(context.Background(), ChatRequest{
		Messages: []Message{{Role: testRole, Content: "show me my activities"}},
		Model:    testModel,
		Tools: []ToolDefinition{
			{Name: "get_activities", Description: "List recent activities", Parameters: params},
		},
	}, func(string) error {
		tokens++
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithTools: %v", err)
	}
	if res.Content != "" {
		t.Fatalf("content = %q, want empty", res.Content)
	}
	if tokens != 0 {
		t.Fatalf("onToken called %d times, want 0", tokens)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("tool calls = %v, want exactly 1", res.ToolCalls)
	}
	tc := res.ToolCalls[0]
	if tc.ID != "call_123" {
		t.Fatalf("tool call ID = %q, want %q", tc.ID, "call_123")
	}
	if tc.Name != "get_activities" {
		t.Fatalf("tool call name = %q, want %q", tc.Name, "get_activities")
	}
	if tc.Arguments != `{"limit":5}` {
		t.Fatalf("tool call arguments = %q, want %q", tc.Arguments, `{"limit":5}`)
	}
}
