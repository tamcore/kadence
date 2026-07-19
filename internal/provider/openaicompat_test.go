package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testRole  = "user"
	testModel = "test-model"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "integer"},
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
