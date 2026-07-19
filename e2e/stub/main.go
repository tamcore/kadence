// Command e2e-stub is a minimal OpenAI-compatible LLM + embeddings server
// used by end-to-end tests. It returns deterministic, canned responses so
// e2e tests do not depend on a real model provider.
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

const (
	defaultStubAddr    = ":9099"
	embeddingVectorLen = 8
	stubModelName      = "stub"
)

// chatContentTokens are the deterministic content deltas streamed back for
// every chat completion request, regardless of the request body.
var chatContentTokens = []string{"This is ", "a test ", "coaching reply."}

// fixedEmbedding is the deterministic embedding vector returned for every
// input string.
var fixedEmbedding = [embeddingVectorLen]float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}

// chatCompletionChunk mirrors the shape the openai-go/v3 stream decoder
// consumes (see internal/provider/openaicompat_test.go): a "choices" array
// whose first element carries a "delta.content" string.
type chatCompletionChunk struct {
	Choices []chatCompletionChunkChoice `json:"choices"`
}

type chatCompletionChunkChoice struct {
	Delta chatCompletionChunkDelta `json:"delta"`
}

type chatCompletionChunkDelta struct {
	Content string `json:"content"`
}

// embeddingsRequest is the subset of the OpenAI embeddings request body this
// stub needs: the list of input strings to embed.
type embeddingsRequest struct {
	Input []string `json:"input"`
}

// embeddingsResponse mirrors the OpenAI embeddings response envelope.
type embeddingsResponse struct {
	Object string           `json:"object"`
	Data   []embeddingDatum `json:"data"`
	Model  string           `json:"model"`
	Usage  embeddingsUsage  `json:"usage"`
}

type embeddingDatum struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type embeddingsUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// handler builds the stub's HTTP routes. Exposed as a func (rather than
// wiring http.DefaultServeMux directly) so tests can exercise it via
// httptest without starting a real listener.
func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", handleChatCompletions)
	mux.HandleFunc("POST /v1/embeddings", handleEmbeddings)
	return mux
}

// handleChatCompletions streams a deterministic chat.completion.chunk SSE
// response: a few content-bearing chunks followed by a terminating
// "data: [DONE]" frame. Auth and the request body are ignored.
func handleChatCompletions(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	for _, token := range chatContentTokens {
		chunk := chatCompletionChunk{
			Choices: []chatCompletionChunkChoice{
				{Delta: chatCompletionChunkDelta{Content: token}},
			},
		}
		if err := writeSSEChunk(w, chunk); err != nil {
			slog.Error("write chat completion chunk", "error", err)
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}

	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		slog.Error("write [DONE] frame", "error", err)
		return
	}
	if canFlush {
		flusher.Flush()
	}
}

// writeSSEChunk marshals v and writes it as a single "data: <json>\n\n" SSE
// frame.
func writeSSEChunk(w http.ResponseWriter, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal SSE chunk: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return fmt.Errorf("write SSE chunk: %w", err)
	}
	return nil
}

// handleEmbeddings returns the same fixed embedding vector for every input
// string in the request, one datum per input (in order).
func handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req embeddingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	inputCount := len(req.Input)
	if inputCount == 0 {
		inputCount = 1
	}

	data := make([]embeddingDatum, 0, inputCount)
	for i := 0; i < inputCount; i++ {
		data = append(data, embeddingDatum{
			Object:    "embedding",
			Index:     i,
			Embedding: fixedEmbedding[:],
		})
	}

	res := embeddingsResponse{
		Object: "list",
		Data:   data,
		Model:  stubModelName,
		Usage:  embeddingsUsage{PromptTokens: 1, TotalTokens: 1},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.Error("write embeddings response", "error", err)
	}
}

// stubAddr resolves the listen address from $STUB_ADDR, defaulting to
// defaultStubAddr.
func stubAddr() string {
	if addr := os.Getenv("STUB_ADDR"); addr != "" {
		return addr
	}
	return defaultStubAddr
}

func main() {
	addr := stubAddr()
	slog.Info("e2e-stub listening", "addr", addr)
	if err := http.ListenAndServe(addr, handler()); err != nil { //nolint:gosec // test-only stub, no need for timeouts
		slog.Error("e2e-stub server error", "error", err)
		os.Exit(1)
	}
}
