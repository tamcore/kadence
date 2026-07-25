package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	stubMessageRoleKey    = "role"
	stubMessageContentKey = "content"
	stubRequestModelKey   = "model"
	stubRequestStreamKey  = "stream"
	stubDoneFrame         = "[DONE]"
	stubToolCallIDKey     = "tool_call_id"
	stubToolCallNameKey   = "name"
	scheduleWeatherPrompt = "Please schedule it as suggested"
)

func TestChatCompletionsStreamsSSEChunks(t *testing.T) {
	reqBody := `{"model":"stub","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler().ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	contentType := res.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}

	respBody := rec.Body.String()

	frames := extractDataFrames(t, respBody)
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 data frames (content chunk + [DONE]), got %d: %v", len(frames), frames)
	}

	last := frames[len(frames)-1]
	if last != stubDoneFrame {
		t.Fatalf("last frame = %q, want %q", last, stubDoneFrame)
	}

	var sawContent bool
	var full strings.Builder
	for _, f := range frames[:len(frames)-1] {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(f), &chunk); err != nil {
			t.Fatalf("unmarshal chunk %q: %v", f, err)
		}
		if len(chunk.Choices) == 0 {
			t.Fatalf("chunk %q has no choices", f)
		}
		if chunk.Choices[0].Delta.Content != "" {
			sawContent = true
			full.WriteString(chunk.Choices[0].Delta.Content)
		}
	}

	if !sawContent {
		t.Fatalf("no chunk carried delta.content")
	}
	if full.Len() == 0 {
		t.Fatalf("accumulated content is empty")
	}
}

func TestChatCompletionsRefinesScheduledTasks(t *testing.T) {
	question := scheduledStubReply(t, []map[string]string{
		{stubMessageRoleKey: messageRoleSystem, stubMessageContentKey: scheduledCompilerPrompt},
		{stubMessageRoleKey: messageRoleUser, stubMessageContentKey: "Help me plan a daily training check-in"},
	})
	var questionBody struct {
		AssistantText string `json:"assistantText"`
		Question      *struct {
			Prompt string `json:"prompt"`
			Kind   string `json:"kind"`
		} `json:"question"`
	}
	if err := json.Unmarshal([]byte(question), &questionBody); err != nil {
		t.Fatalf("decode Scheduled question: %v\n%s", err, question)
	}
	if questionBody.AssistantText == "" || questionBody.Question == nil ||
		questionBody.Question.Prompt == "" || questionBody.Question.Kind != "single_select" {
		t.Fatalf("Scheduled question = %+v", questionBody)
	}

	proposal := scheduledStubReply(t, []map[string]string{
		{stubMessageRoleKey: messageRoleSystem, stubMessageContentKey: scheduledCompilerPrompt},
		{stubMessageRoleKey: messageRoleUser, stubMessageContentKey: "Help me plan a daily training check-in"},
		{stubMessageRoleKey: "assistant", stubMessageContentKey: question},
		{stubMessageRoleKey: messageRoleUser, stubMessageContentKey: "daily"},
	})
	var proposalBody struct {
		AssistantText string `json:"assistantText"`
		Proposal      *struct {
			Name          string `json:"name"`
			TaskKind      string `json:"taskKind"`
			ExecutionMode string `json:"executionMode"`
			StaticMessage string `json:"staticMessage"`
		} `json:"proposal"`
	}
	if err := json.Unmarshal([]byte(proposal), &proposalBody); err != nil {
		t.Fatalf("decode Scheduled proposal: %v\n%s", err, proposal)
	}
	if proposalBody.AssistantText == "" || proposalBody.Proposal == nil ||
		proposalBody.Proposal.Name == "" || proposalBody.Proposal.TaskKind != "reminder" ||
		proposalBody.Proposal.ExecutionMode != "static" || proposalBody.Proposal.StaticMessage == "" {
		t.Fatalf("Scheduled proposal = %+v", proposalBody)
	}
}

func TestChatCompletionsProposesStaticScheduledReminderDirectly(t *testing.T) {
	reply := scheduledStubReply(t, []map[string]string{
		{stubMessageRoleKey: messageRoleSystem, stubMessageContentKey: scheduledCompilerPrompt},
		{stubMessageRoleKey: messageRoleUser, stubMessageContentKey: "Remind me to drink water tomorrow"},
	})
	if !strings.Contains(reply, `"proposal"`) || !strings.Contains(reply, `"Hydration reminder"`) {
		t.Fatalf("static Scheduled reply = %s", reply)
	}
}

// TestChatSchedulingToolScript locks the deliberately two-step provider script
// used by the inline Scheduled E2E flow. A weather question only suggests the
// two checks; an explicit scheduling request is required before the stub emits
// either native draft tool call.
func TestChatSchedulingToolScript(t *testing.T) {
	weatherMessage := map[string]any{
		stubMessageRoleKey: messageRoleUser, stubMessageContentKey: "What is the weather forecast for my race?",
	}
	weatherFrames := chatStubFrames(t, stubChatRequest([]map[string]any{weatherMessage}, true))
	if got := joinedStubContent(weatherFrames); !strings.Contains(strings.ToLower(got), "two future weather checks") {
		t.Fatalf("weather reply = %q, want two future weather checks", got)
	}
	if got := stubToolCalls(weatherFrames); len(got) != 0 {
		t.Fatalf("weather request emitted implicit tool calls = %+v", got)
	}

	noDefinitionFrames := chatStubFrames(t, stubChatRequest(
		[]map[string]any{{stubMessageRoleKey: messageRoleUser, stubMessageContentKey: scheduleWeatherPrompt}}, false,
	))
	if got := stubToolCalls(noDefinitionFrames); len(got) != 0 {
		t.Fatalf("request without draft-tool definition emitted calls = %+v", got)
	}

	scheduledFrames := chatStubFrames(t, stubChatRequest(
		[]map[string]any{{stubMessageRoleKey: messageRoleUser, stubMessageContentKey: scheduleWeatherPrompt}}, true,
	))
	calls := stubToolCalls(scheduledFrames)
	if len(calls) != 2 {
		t.Fatalf("scheduled tool calls = %+v, want two", calls)
	}
	for i, call := range calls {
		if call.Index != i || call.ID == "" || call.Type != functionToolType || call.Name != draftScheduledToolName {
			t.Fatalf("call[%d] = %+v", i, call)
		}
		if !strings.Contains(call.Arguments, "fetch fresh race weather") ||
			!strings.Contains(call.Arguments, "pacing, hydration, and kit guidance") {
			t.Fatalf("call[%d] instruction = %q", i, call.Arguments)
		}
	}
	if !stubFinishReason(scheduledFrames, "tool_calls") {
		t.Fatalf("scheduled response did not end with tool_calls: %s", strings.Join(scheduledFrames, "\n"))
	}
	deltas := stubToolCallDeltas(scheduledFrames)
	if len(deltas) != 4 || deltas[0].Index != 0 || deltas[1].Index != 1 || deltas[2].Index != 0 || deltas[3].Index != 1 {
		t.Fatalf("indexed tool-call deltas = %+v", deltas)
	}
	if strings.HasSuffix(deltas[0].Arguments, "}") || strings.HasSuffix(deltas[1].Arguments, "}") ||
		!strings.HasSuffix(deltas[2].Arguments, "}") || !strings.HasSuffix(deltas[3].Arguments, "}") {
		t.Fatalf("tool-call JSON was not split across deltas: %+v", deltas)
	}

	finalFrames := chatStubFrames(t, stubChatRequest([]map[string]any{
		{stubMessageRoleKey: messageRoleUser, stubMessageContentKey: scheduleWeatherPrompt},
		scheduledToolRequestMessage(calls),
		stubToolResultMessage(calls[0], `{"taskId":"weather-check-one"}`),
		stubToolResultMessage(calls[1], `{"taskId":"weather-check-two"}`),
	}, true))
	if got := joinedStubContent(finalFrames); got != "I prepared two weather checks for review." {
		t.Fatalf("final reply = %q", got)
	}
	if got := stubToolCalls(finalFrames); len(got) != 0 {
		t.Fatalf("final reply emitted tool calls = %+v", got)
	}
}

// TestScheduledWeatherProposal ensures each delegated instruction compiles to
// a separate, one-off data task with the exact browser tools it is allowed to
// use. Keeping these checks in the stub makes the browser scenario independent
// of an external model provider.
func TestScheduledWeatherProposal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		instruction string
		proposal    string
	}{
		{name: "pre-race", instruction: preRaceWeatherInstruction, proposal: "Pre-race weather check"},
		{name: "race-day", instruction: raceDayWeatherInstruction, proposal: "Race-day weather check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := scheduledStubReply(t, []map[string]string{
				{stubMessageRoleKey: messageRoleSystem, stubMessageContentKey: scheduledCompilerPrompt},
				{stubMessageRoleKey: messageRoleUser, stubMessageContentKey: tc.instruction},
			})
			var body struct {
				Proposal struct {
					Name            string   `json:"name"`
					TaskKind        string   `json:"taskKind"`
					ExecutionMode   string   `json:"executionMode"`
					AuthorizedTools []string `json:"authorizedTools"`
					Schedule        struct {
						At string `json:"at"`
					} `json:"schedule"`
				} `json:"proposal"`
			}
			if err := json.Unmarshal([]byte(reply), &body); err != nil {
				t.Fatalf("decode weather proposal: %v\n%s", err, reply)
			}
			if body.Proposal.Name != tc.proposal || body.Proposal.TaskKind != "data" ||
				body.Proposal.ExecutionMode != "data" || body.Proposal.Schedule.At == "" {
				t.Fatalf("weather proposal = %+v", body.Proposal)
			}
			wantTools := browserNavigateTool + "," + browserSnapshotTool
			if got := strings.Join(body.Proposal.AuthorizedTools, ","); got != wantTools {
				t.Fatalf("authorized tools = %q, want %q", got, wantTools)
			}
		})
	}
}

func TestEmbeddingsReturnsFixedVector(t *testing.T) {
	reqBody := `{"model":"stub","input":["hello world"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler().ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Object != "list" {
		t.Fatalf("object = %q, want %q", body.Object, "list")
	}
	if len(body.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(body.Data))
	}
	if body.Data[0].Object != "embedding" {
		t.Fatalf("data[0].object = %q, want %q", body.Data[0].Object, "embedding")
	}
	if body.Data[0].Index != 0 {
		t.Fatalf("data[0].index = %d, want 0", body.Data[0].Index)
	}
	if len(body.Data[0].Embedding) != defaultEmbeddingVectorLen {
		t.Fatalf("data[0].embedding length = %d, want %d", len(body.Data[0].Embedding), defaultEmbeddingVectorLen)
	}
}

// TestEmbeddingsHonorsRequestedDimensions ensures the stub sizes its vectors
// to whatever "dimensions" the caller requests (e.g. KADENCE_EMBED_DIMENSIONS
// set to something other than the default), rather than a hardcoded length —
// this is what makes the stub safe to use against fitDimensions validation.
func TestEmbeddingsHonorsRequestedDimensions(t *testing.T) {
	reqBody := `{"model":"stub","input":["hello"],"dimensions":256}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler().ServeHTTP(rec, req)

	var body struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(body.Data))
	}
	if len(body.Data[0].Embedding) != 256 {
		t.Fatalf("embedding length = %d, want 256", len(body.Data[0].Embedding))
	}
}

func TestEmbeddingsReturnsOnePerInput(t *testing.T) {
	reqBody := `{"model":"stub","input":["a","b","c"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler().ServeHTTP(rec, req)

	var body struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.Data) != 3 {
		t.Fatalf("data length = %d, want 3", len(body.Data))
	}
	for i, d := range body.Data {
		if d.Index != i {
			t.Fatalf("data[%d].index = %d, want %d", i, d.Index, i)
		}
		if len(d.Embedding) != defaultEmbeddingVectorLen {
			t.Fatalf("data[%d].embedding length = %d, want %d", i, len(d.Embedding), defaultEmbeddingVectorLen)
		}
	}
}

// extractDataFrames splits an SSE body into the payload of each "data: " line,
// ignoring blank keep-alive lines.
func extractDataFrames(t *testing.T, body string) []string {
	t.Helper()
	var frames []string
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		frames = append(frames, strings.TrimPrefix(line, "data: "))
	}
	return frames
}

func scheduledStubReply(t *testing.T, messages []map[string]string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"model": "stub", "messages": messages, "stream": true})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(payload)))
	rec := httptest.NewRecorder()
	handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Scheduled stub status = %d, body=%s", rec.Code, rec.Body.String())
	}
	frames := extractDataFrames(t, rec.Body.String())
	var reply strings.Builder
	for _, frame := range frames {
		if frame == stubDoneFrame {
			continue
		}
		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(frame), &chunk); err != nil {
			t.Fatalf("decode Scheduled chunk: %v", err)
		}
		if len(chunk.Choices) > 0 {
			reply.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return reply.String()
}

type stubToolCall struct {
	Index     int
	ID        string
	Type      string
	Name      string
	Arguments string
}

func chatStubFrames(t *testing.T, payload map[string]any) []string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stub status = %d, body=%s", rec.Code, rec.Body.String())
	}
	return extractDataFrames(t, rec.Body.String())
}

func stubChatRequest(messages []map[string]any, includeDraftTool bool) map[string]any {
	payload := map[string]any{
		stubRequestModelKey:  stubModelName,
		"messages":           messages,
		stubRequestStreamKey: true,
	}
	if includeDraftTool {
		payload["tools"] = []map[string]any{{
			"type": functionToolType,
			"function": map[string]any{
				"name": draftScheduledToolName,
			},
		}}
	}
	return payload
}

func scheduledToolRequestMessage(calls []stubToolCall) map[string]any {
	toolCalls := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		toolCalls = append(toolCalls, map[string]any{
			"id": call.ID, "type": functionToolType,
			"function": map[string]any{"name": call.Name, "arguments": callArguments(call)},
		})
	}
	return map[string]any{
		stubMessageRoleKey: "assistant", stubMessageContentKey: "", "tool_calls": toolCalls,
	}
}

func stubToolResultMessage(call stubToolCall, content string) map[string]any {
	return map[string]any{
		stubMessageRoleKey:    messageRoleTool,
		stubToolCallIDKey:     call.ID,
		stubToolCallNameKey:   call.Name,
		stubMessageContentKey: content,
	}
}

func joinedStubContent(frames []string) string {
	var content strings.Builder
	for _, frame := range frames {
		if frame == stubDoneFrame {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(frame), &chunk) == nil && len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return content.String()
}

func stubToolCalls(frames []string) []stubToolCall {
	calls := map[int]stubToolCall{}
	for _, frame := range frames {
		if frame == stubDoneFrame {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(frame), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		for _, delta := range chunk.Choices[0].Delta.ToolCalls {
			call := calls[delta.Index]
			call.Index = delta.Index
			if delta.ID != "" {
				call.ID = delta.ID
			}
			if delta.Type != "" {
				call.Type = delta.Type
			}
			if delta.Function.Name != "" {
				call.Name = delta.Function.Name
			}
			call.Arguments += delta.Function.Arguments
			calls[delta.Index] = call
		}
	}
	ordered := make([]stubToolCall, 0, len(calls))
	for index := 0; index < len(calls); index++ {
		if call, ok := calls[index]; ok {
			ordered = append(ordered, call)
		}
	}
	return ordered
}

func stubToolCallDeltas(frames []string) []stubToolCall {
	var calls []stubToolCall
	for _, frame := range frames {
		if frame == stubDoneFrame {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(frame), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		for _, call := range chunk.Choices[0].Delta.ToolCalls {
			calls = append(calls, stubToolCall{
				Index: call.Index, ID: call.ID, Type: call.Type, Name: call.Function.Name, Arguments: call.Function.Arguments,
			})
		}
	}
	return calls
}

func stubFinishReason(frames []string, want string) bool {
	for _, frame := range frames {
		var chunk struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(frame), &chunk) == nil && len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason == want {
			return true
		}
	}
	return false
}

func callArguments(call stubToolCall) string { return call.Arguments }
