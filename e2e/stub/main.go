// Command e2e-stub is a minimal OpenAI-compatible LLM + embeddings server
// used by end-to-end tests. It returns deterministic, canned responses so
// e2e tests do not depend on a real model provider.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultStubAddr = ":9099"
	// defaultEmbeddingVectorLen mirrors config.EmbedDimensions' own default
	// (KADENCE_EMBED_DIMENSIONS=1024): used whenever a request omits
	// "dimensions", so the stub's vectors fit the chunks.embedding
	// vector(1024) column without the caller having to ask for it explicitly.
	defaultEmbeddingVectorLen = 1024
	stubModelName             = "stub"
	functionToolType          = "function"
	finishToolCalls           = "tool_calls"
	messageRoleTool           = "tool"
	draftScheduledToolName    = "kadence__draft_future_unattended_task"
	workerPromptPrefix        = "Gather evidence using only the offered tools."
	synthesisPromptPrefix     = "Write the concise user-facing Scheduled result"
	browserNavigateTool       = "browser__browser_navigate"
	browserSnapshotTool       = "browser__browser_snapshot"
	preRaceWeatherInstruction = "Fetch fresh race weather two days before the race and deliver updated " +
		"pacing, hydration, and kit guidance."
	raceDayWeatherInstruction = "Fetch fresh race weather on race morning and deliver updated pacing, " +
		"hydration, and kit guidance."
	weatherSuggestionReply = "Two future weather checks would help: one two days before your race and " +
		"another on race morning. Please explicitly ask me to schedule them if you want drafts."
	//nolint:lll // Exact single-line worker JSON outcome exercises strict parsing.
	workerWeatherOutcomeReply = `{"status":"deliver","summary":"Fresh race weather is cool and breezy.","evidence":["race weather fixture: 12C, light rain"],"monitoringState":{}}`
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
	preRaceWeatherProposalReply = `{
		"assistantText": "Your pre-race weather check is ready for review.",
		"proposal": {
			"name": "Pre-race weather check",
			"taskKind": "data",
			"compiledPrompt": "` + preRaceWeatherInstruction + `",
			"executionMode": "data",
			"schedule": {"at": "2040-01-03T08:00:00Z", "timezone": "UTC"},
			"timezone": "UTC",
			"authorizedTools": ["` + browserNavigateTool + `", "` + browserSnapshotTool + `"],
			"deliveryPolicy": "always",
			"initialRun": "wait"
		}
	}`
	raceDayWeatherProposalReply = `{
		"assistantText": "Your race-day weather check is ready for review.",
		"proposal": {
			"name": "Race-day weather check",
			"taskKind": "data",
			"compiledPrompt": "` + raceDayWeatherInstruction + `",
			"executionMode": "data",
			"schedule": {"at": "2040-01-05T05:30:00Z", "timezone": "UTC"},
			"timezone": "UTC",
			"authorizedTools": ["` + browserNavigateTool + `", "` + browserSnapshotTool + `"],
			"deliveryPolicy": "always",
			"initialRun": "wait"
		}
	}`
)

type chatCompletionRequest struct {
	Messages []chatCompletionRequestMessage `json:"messages"`
	Tools    []chatCompletionToolDefinition `json:"tools"`
}

type chatCompletionRequestMessage struct {
	Role       string            `json:"role"`
	Content    json.RawMessage   `json:"content"`
	ToolCalls  []json.RawMessage `json:"tool_calls"`
	ToolCallID string            `json:"tool_call_id"`
	Name       string            `json:"name"`
}

type chatCompletionToolDefinition struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

// chatCompletionChunk mirrors the shape the openai-go/v3 stream decoder
// consumes (see internal/provider/openaicompat_test.go): a "choices" array
// whose first element carries a "delta.content" string.
type chatCompletionChunk struct {
	Choices []chatCompletionChunkChoice `json:"choices"`
}

type chatCompletionChunkChoice struct {
	Delta        chatCompletionChunkDelta `json:"delta"`
	FinishReason string                   `json:"finish_reason,omitempty"`
}

type chatCompletionChunkDelta struct {
	Content   string                        `json:"content,omitempty"`
	ToolCalls []chatCompletionChunkToolCall `json:"tool_calls,omitempty"`
}

type chatCompletionChunkToolCall struct {
	Index    int                             `json:"index"`
	ID       string                          `json:"id,omitempty"`
	Type     string                          `json:"type,omitempty"`
	Function chatCompletionChunkToolFunction `json:"function"`
}

type chatCompletionChunkToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
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
	mux.Handle("/mcp", browserMCPHandler())
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
	writeChunk := func(chunk chatCompletionChunk) bool {
		if err := writeSSEChunk(w, chunk); err != nil {
			slog.Error("write chat completion chunk", "error", err)
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}

	if isWorkerRequest(req.Messages) {
		if hasToolResult(req.Messages) {
			tokens = []string{workerWeatherOutcomeReply}
		} else {
			writeWorkerToolCalls(w, canFlush, flusher)
			return
		}
	} else if isSynthesisRequest(req.Messages) {
		tokens = []string{"Fresh race weather is ready: adjust pacing for conditions, " +
			"hydrate steadily, and bring the appropriate kit."}
	} else if !isScheduledCompiler(req.Messages) {
		switch {
		case hasToolResult(req.Messages):
			tokens = []string{"I prepared two weather checks for review."}
		case hasDraftScheduledTool(req.Tools) && explicitlySchedulesWeatherChecks(req.Messages):
			for _, chunk := range scheduledToolCallChunks() {
				if !writeChunk(chunk) {
					return
				}
			}
			if !writeChunk(chatCompletionChunk{Choices: []chatCompletionChunkChoice{{FinishReason: finishToolCalls}}}) {
				return
			}
			writeDone(w, canFlush, flusher)
			return
		case asksForWeatherForecast(req.Messages):
			tokens = []string{weatherSuggestionReply}
		}
	}

	for _, token := range tokens {
		chunk := chatCompletionChunk{
			Choices: []chatCompletionChunkChoice{
				{Delta: chatCompletionChunkDelta{Content: token}},
			},
		}
		if !writeChunk(chunk) {
			return
		}
	}

	writeDone(w, canFlush, flusher)
}

func isScheduledCompiler(messages []chatCompletionRequestMessage) bool {
	_, ok := scheduledReply(messages)
	return ok
}

func asksForWeatherForecast(messages []chatCompletionRequestMessage) bool {
	for _, message := range messages {
		if message.Role == messageRoleUser && strings.Contains(strings.ToLower(messageText(message.Content)), "weather") {
			return true
		}
	}
	return false
}

func explicitlySchedulesWeatherChecks(messages []chatCompletionRequestMessage) bool {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role == messageRoleUser {
			return strings.Contains(strings.ToLower(messageText(message.Content)), "schedule it")
		}
	}
	return false
}

func hasToolResult(messages []chatCompletionRequestMessage) bool {
	for _, message := range messages {
		if message.Role == messageRoleTool && message.ToolCallID != "" {
			return true
		}
	}
	return false
}

func hasDraftScheduledTool(tools []chatCompletionToolDefinition) bool {
	for _, tool := range tools {
		if tool.Type == functionToolType && tool.Function.Name == draftScheduledToolName {
			return true
		}
	}
	return false
}

func scheduledToolCallChunks() []chatCompletionChunk {
	return []chatCompletionChunk{
		{Choices: []chatCompletionChunkChoice{{Delta: chatCompletionChunkDelta{ToolCalls: []chatCompletionChunkToolCall{
			draftToolCall(0, "call_weather_before", `{"instruction":"fetch fresh race weather two `),
			draftToolCall(1, "call_weather_race_day", `{"instruction":"fetch fresh race weather on `),
		}}}}},
		{Choices: []chatCompletionChunkChoice{{Delta: chatCompletionChunkDelta{ToolCalls: []chatCompletionChunkToolCall{
			draftToolCall(0, "", `days before the race and deliver updated pacing, hydration, and kit guidance."}`),
			draftToolCall(1, "", `race morning and deliver updated pacing, hydration, and kit guidance."}`),
		}}}}},
	}
}

func draftToolCall(index int, id, arguments string) chatCompletionChunkToolCall {
	if id == "" {
		return chatCompletionChunkToolCall{
			Index: index, Function: chatCompletionChunkToolFunction{Arguments: arguments},
		}
	}
	return chatCompletionChunkToolCall{
		Index: index, ID: id, Type: functionToolType,
		Function: chatCompletionChunkToolFunction{Name: draftScheduledToolName, Arguments: arguments},
	}
}

func writeWorkerToolCalls(w http.ResponseWriter, canFlush bool, flusher http.Flusher) {
	chunk := chatCompletionChunk{Choices: []chatCompletionChunkChoice{{
		Delta: chatCompletionChunkDelta{ToolCalls: []chatCompletionChunkToolCall{
			{Index: 0, ID: "call_weather_navigate", Type: functionToolType,
				//nolint:lll // Exact URL is part of deterministic browser fixture.
				Function: chatCompletionChunkToolFunction{Name: browserNavigateTool, Arguments: `{"url":"https://weather.example.test/race"}`}},
			{Index: 1, ID: "call_weather_snapshot", Type: functionToolType,
				Function: chatCompletionChunkToolFunction{Name: browserSnapshotTool, Arguments: `{}`}},
		}},
	}}}
	if err := writeSSEChunk(w, chunk); err != nil {
		slog.Error("write worker tool-call chunk", "error", err)
		return
	}
	if canFlush {
		flusher.Flush()
	}
	finish := chatCompletionChunk{Choices: []chatCompletionChunkChoice{{FinishReason: finishToolCalls}}}
	if err := writeSSEChunk(w, finish); err != nil {
		slog.Error("write worker finish chunk", "error", err)
		return
	}
	writeDone(w, canFlush, flusher)
}

func isWorkerRequest(messages []chatCompletionRequestMessage) bool {
	return len(messages) > 0 && strings.HasPrefix(messageText(messages[0].Content), workerPromptPrefix)
}

func isSynthesisRequest(messages []chatCompletionRequestMessage) bool {
	return len(messages) > 0 && strings.HasPrefix(messageText(messages[0].Content), synthesisPromptPrefix)
}

func browserMCPHandler() http.Handler {
	srv := mcpserver.NewMCPServer("e2e-browser", "0.0.1")
	navigate := mcpgo.NewTool("browser_navigate", mcpgo.WithString("url"))
	srv.AddTool(navigate, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(`{"forecast":"race weather fixture: cool and breezy"}`), nil
	})
	snapshot := mcpgo.NewTool("browser_snapshot")
	srv.AddTool(snapshot, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(`{"snapshot":"race weather fixture: 12C, light rain"}`), nil
	})
	return mcpserver.NewStreamableHTTPServer(srv)
}

func writeDone(w http.ResponseWriter, canFlush bool, flusher http.Flusher) {
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
		content := messageText(message.Content)
		if message.Role == messageRoleSystem && strings.Contains(content, scheduledCompilerPrompt) {
			compilerRequest = true
		}
		if message.Role == messageRoleUser {
			userMessages++
			if firstUser == "" {
				firstUser = content
			}
		}
	}
	if !compilerRequest {
		return "", false
	}
	instruction := scheduledInstruction(firstUser)
	if strings.Contains(strings.ToLower(instruction), "drink water") {
		return scheduledReminderReply, true
	}
	if strings.Contains(strings.ToLower(instruction), "fetch fresh race weather") {
		if strings.Contains(strings.ToLower(instruction), "race morning") {
			return raceDayWeatherProposalReply, true
		}
		return preRaceWeatherProposalReply, true
	}
	if userMessages > 1 {
		return scheduledProposalReply, true
	}
	return scheduledQuestionReply, true
}

func messageText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var joined strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			joined.WriteString(part.Text)
		}
	}
	return joined.String()
}

func scheduledInstruction(definition string) string {
	const instructionPrefix = "Instruction:\n"
	const currentUTCMarker = "\n\nCurrent UTC:\n"
	if !strings.HasPrefix(definition, instructionPrefix) {
		return definition
	}
	framed := strings.TrimPrefix(definition, instructionPrefix)
	if before, _, ok := strings.Cut(framed, currentUTCMarker); ok {
		return before
	}
	return framed
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
