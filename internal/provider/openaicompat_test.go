package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A minimal OpenAI-compatible streaming completion: two content chunks + [DONE].
const sseBody = "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
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
		Messages:    []Message{{Role: "user", Content: "hi"}},
		Model:       "test-model",
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
