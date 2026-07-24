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
	"strings"
	"time"
)

const (
	defaultStubAddr = ":9099"
	// defaultEmbeddingVectorLen mirrors config.EmbedDimensions' own default
	// (KADENCE_EMBED_DIMENSIONS=1024): used whenever a request omits
	// "dimensions", so the stub's vectors fit the chunks.embedding
	// vector(1024) column without the caller having to ask for it explicitly.
	defaultEmbeddingVectorLen = 1024
	stubModelName             = "stub"
)

// chatContentTokens are the deterministic content deltas streamed back for
// every chat completion request, regardless of the request body.
var chatContentTokens = []string{"This is ", "a test ", "coaching reply."}

const (
	messageRoleSystem       = "system"
	messageRoleUser         = "user"
	scheduledCompilerPrompt = "You refine one Scheduled task from the complete conversation."
	scheduledQuestionReply  = `{
		"assistantText": "Let’s tailor the check-in to your routine.",
		"question": {
			"id": "cadence",
			"prompt": "How often should the check-in run?",
			"kind": "single_select",
			"options": [
				{"label": "Daily", "value": "daily"},
				{"label": "Weekly", "value": "weekly"}
			],
			"allowCustom": false,
			"optional": false
		}
	}`
	scheduledProposalReply = `{
		"assistantText": "Your daily training check-in is ready to schedule.",
		"proposal": {
			"version": 0,
			"name": "Daily training check-in",
			"taskKind": "reminder",
			"compiledPrompt": "Prompt the user to review the day’s training and recovery.",
			"executionMode": "static",
			"schedule": {
				"dtStart": "2040-01-02T08:00:00Z",
				"rrule": "FREQ=DAILY",
				"timezone": "UTC"
			},
			"timezone": "UTC",
			"authorizedTools": [],
			"deliveryPolicy": "always",
			"initialRun": "wait",
			"stopCondition": "",
			"staticMessage": "Take a moment to review today’s training and recovery."
		}
	}`
	scheduledReminderReply = `{
		"assistantText": "Your hydration reminder is ready to schedule.",
		"proposal": {
			"version": 0,
			"name": "Hydration reminder",
			"taskKind": "reminder",
			"compiledPrompt": "Remind the user to drink water.",
			"executionMode": "static",
			"schedule": {
				"at": "2040-01-02T15:04:05Z",
				"timezone": "UTC"
			},
			"timezone": "UTC",
			"authorizedTools": [],
			"deliveryPolicy": "always",
			"initialRun": "wait",
			"stopCondition": "",
			"staticMessage": "Time to drink some water."
		}
	}`
)

type chatCompletionRequest struct {
	Messages []chatCompletionRequestMessage `json:"messages"`
}

type chatCompletionRequestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

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
// stub needs: the list of input strings to embed, and the optional
// "dimensions" field (honored so callers that pin KADENCE_EMBED_DIMENSIONS
// get vectors of the width they asked for, same as a real MRL-capable
// provider would return).
type embeddingsRequest struct {
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions"`
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

// handleChatCompletions streams deterministic chat or Scheduled compiler
// content followed by a terminating "data: [DONE]" frame.
func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	tokens := chatContentTokens
	if reply, ok := scheduledReply(req.Messages); ok {
		tokens = []string{reply}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	for _, token := range tokens {
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

func scheduledReply(messages []chatCompletionRequestMessage) (string, bool) {
	var compilerRequest bool
	var firstUser string
	userMessages := 0
	for _, message := range messages {
		if message.Role == messageRoleSystem && strings.Contains(message.Content, scheduledCompilerPrompt) {
			compilerRequest = true
		}
		if message.Role == messageRoleUser {
			userMessages++
			if firstUser == "" {
				firstUser = message.Content
			}
		}
	}
	if !compilerRequest {
		return "", false
	}
	if strings.Contains(strings.ToLower(firstUser), "drink water") {
		return scheduledReminderReply, true
	}
	if userMessages > 1 {
		return scheduledProposalReply, true
	}
	return scheduledQuestionReply, true
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

// handleEmbeddings returns a deterministic, fixed-pattern embedding vector
// for every input string in the request, one datum per input (in order). The
// vector length honors the request's "dimensions" field when present,
// falling back to defaultEmbeddingVectorLen otherwise — this keeps the stub
// dumb (no real embedding math) while still matching the width the caller
// (and the fixed-width chunks.embedding column) expects.
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

	dims := req.Dimensions
	if dims <= 0 {
		dims = defaultEmbeddingVectorLen
	}
	embedding := fixedEmbedding(dims)

	data := make([]embeddingDatum, 0, inputCount)
	for i := 0; i < inputCount; i++ {
		data = append(data, embeddingDatum{
			Object:    "embedding",
			Index:     i,
			Embedding: embedding,
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

// fixedEmbeddingPattern repeats to fill vectors of any requested length; kept
// short and deterministic rather than random so test assertions can rely on
// exact values.
var fixedEmbeddingPattern = []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}

// fixedEmbedding returns a deterministic vector of exactly n dimensions by
// tiling fixedEmbeddingPattern.
func fixedEmbedding(n int) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = fixedEmbeddingPattern[i%len(fixedEmbeddingPattern)]
	}
	return v
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
	// A real http.Server with a read-header timeout (satisfies gosec G114;
	// standalone gosec honours #nosec, not //nolint).
	srv := &http.Server{Addr: addr, Handler: handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("e2e-stub server error", "error", err)
		os.Exit(1)
	}
}
