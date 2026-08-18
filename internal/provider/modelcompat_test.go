package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/provider"
)

// This is a compatibility harness, not a unit test: it answers "can this model
// drive Kadence?" against a live OpenAI-compatible endpoint.
//
// It is skipped unless the three environment variables below are set, so it
// never runs in CI and costs nothing by default:
//
//	KADENCE_COMPAT_BASE_URL   the OpenAI-compatible /v1 endpoint
//	KADENCE_COMPAT_API_KEY    a key for it
//	KADENCE_COMPAT_MODEL      the model id to evaluate
//
// It drives the real provider client rather than raw HTTP on purpose. What
// matters is not whether the endpoint implements the OpenAI schema in the
// abstract, but whether it works through the exact code path chat uses —
// including how this client serialises tools, streams deltas and reassembles
// tool calls. An endpoint can be "OpenAI compatible" and still fail that.

const (
	compatBaseURLEnv = "KADENCE_COMPAT_BASE_URL"
	compatAPIKeyEnv  = "KADENCE_COMPAT_API_KEY" // #nosec G101 -- an env var name, not a credential
	compatModelEnv   = "KADENCE_COMPAT_MODEL"
	compatTempEnv    = "KADENCE_COMPAT_TEMPERATURE"

	// compatDefaultTemperature matches the app's own KADENCE_LLM_TEMPERATURE
	// default. Evaluating at a temperature the deployment does not use proves
	// nothing about the deployment.
	compatDefaultTemperature = 0.3

	// compatTimeout bounds one probe. A model slower than this is not usable
	// for interactive chat regardless of how well it answers.
	compatTimeout = 90 * time.Second

	// compatToolCount is how many tools the scale probe sends. It matches the
	// KADENCE_MCP_MAX_TOOLS default, because that is what a real deployment
	// with a large MCP server actually puts in front of the model.
	compatToolCount = 100

	countTool = "garmin__count_activities"
	sleepName = "garmin__get_sleep_data"
)

// compatEnv returns the harness configuration, skipping the test when it is
// absent.
func compatEnv(t *testing.T) (baseURL, apiKey, model string) {
	t.Helper()
	baseURL, apiKey, model = os.Getenv(compatBaseURLEnv), os.Getenv(compatAPIKeyEnv), os.Getenv(compatModelEnv)
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skipf("set %s, %s and %s to evaluate a model", compatBaseURLEnv, compatAPIKeyEnv, compatModelEnv)
	}
	return baseURL, apiKey, model
}

// countActivitiesTool and sleepTool mirror the shape of real Garmin tools:
// one that takes no arguments, one that takes a required date.
func countActivitiesTool() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        countTool,
		Description: "read how many activities the authenticated account holds. Takes no arguments.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
	}
}

func sleepTool() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        sleepName,
		Description: "read one calendar day of the account's sleep for the given date.",
		Parameters: json.RawMessage(
			`{"type":"object","properties":{"cdate":{"type":"string","description":"YYYY-MM-DD"}},"required":["cdate"]}`),
	}
}

// paddedTools returns n tools: the two real ones plus filler that is plausible
// enough to compete for the model's attention.
func paddedTools(n int) []provider.ToolDefinition {
	tools := []provider.ToolDefinition{countActivitiesTool(), sleepTool()}
	for i := len(tools); i < n; i++ {
		tools = append(tools, provider.ToolDefinition{
			Name:        fmt.Sprintf("browser__action_%02d", i),
			Description: "perform a headless browser action on a web page and return the result",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":[]}`),
		})
	}
	return tools
}

// compatTemperature is the deployment's temperature unless overridden, so the
// harness exercises the sampling the app actually configures.
func compatTemperature() float64 {
	if raw := os.Getenv(compatTempEnv); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
	}
	return compatDefaultTemperature
}

func compatRequest(model string, tools []provider.ToolDefinition, user string) provider.ChatRequest {
	return provider.ChatRequest{
		Model:       model,
		Temperature: compatTemperature(),
		MaxTokens:   1024,
		Tools:       tools,
		Messages: []provider.Message{
			{Role: "system", Content: "You are a running coach. Use tools only when the question depends on the athlete's own data."},
			{Role: "user", Content: user},
		},
	}
}

// digitsOnly strips everything but digits, so a thousands separator in the
// model's prose does not read as a wrong answer.
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// checkArgs reports whether a tool call's arguments are valid JSON and carry
// only properties the tool actually declares. Inventing a field is a real
// defect: the MCP server rejects the call, and the turn burns a round trip.
func checkArgs(tool provider.ToolDefinition, arguments string) error {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return nil
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(arguments), &got); err != nil {
		return fmt.Errorf("not a JSON object: %q", arguments)
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
		return nil // the harness's own schema is malformed; not the model's fault
	}
	for key, value := range got {
		declared, ok := schema.Properties[key]
		if !ok {
			return fmt.Errorf("invented property %q, which %s does not declare", key, tool.Name)
		}
		if declared.Type == "string" {
			if _, isString := value.(string); !isString {
				return fmt.Errorf("property %q is %T, want a string", key, value)
			}
		}
	}
	for _, key := range schema.Required {
		v, present := got[key]
		if !present {
			return fmt.Errorf("omitted required property %q", key)
		}
		if str, isString := v.(string); isString && strings.TrimSpace(str) == "" {
			return fmt.Errorf("required property %q is empty", key)
		}
	}
	return nil
}

// callNames lists the tools a result asked for, for readable failures.
func callNames(r provider.StreamResult) []string {
	out := make([]string, 0, len(r.ToolCalls))
	for _, c := range r.ToolCalls {
		out = append(out, c.Name)
	}
	return out
}

// onePixelPNG is a 1x1 red PNG, the smallest valid native image input.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde,
	0x00, 0x00, 0x00, 0x0c, 'I', 'D', 'A', 'T',
	0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00, 0x03, 0x01, 0x01, 0x00,
	0x18, 0xdd, 0x8d, 0xb0,
	0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// driveToolLoop runs the tool loop chat would run, and reports whether both
// tools the question needs were reached. Serialising is fine — chat loops —
// but a model that answers only half the question is not.
func driveToolLoop(
	ctx context.Context, t *testing.T, p provider.Provider,
	req provider.ChatRequest, res provider.StreamResult,
) {
	t.Helper()
	want := map[string]bool{countTool: false, sleepName: false}
	msgs := req.Messages
	parallel := len(res.ToolCalls) > 1
	var err error

	for hop := 0; hop < 3 && len(res.ToolCalls) > 0; hop++ {
		msgs = append(msgs, provider.Message{Role: "assistant", ToolCalls: res.ToolCalls})
		for _, c := range res.ToolCalls {
			if !strings.HasPrefix(c.Name, "garmin__") {
				t.Fatalf("picked filler tool %q; calls = %v", c.Name, callNames(res))
			}
			want[c.Name] = true
			checkSleepCall(t, c)
			msgs = append(msgs, provider.Message{
				Role: "tool", ToolCallID: c.ID, Name: c.Name, Content: "2614",
			})
		}
		if want[countTool] && want[sleepName] {
			break
		}
		next := req
		next.Messages = msgs
		if res, err = p.StreamChatWithTools(ctx, next, func(string) error { return nil }); err != nil {
			t.Fatalf("hop %d: %v", hop+1, err)
		}
	}
	for name, reached := range want {
		if !reached {
			t.Errorf("never called %s across the loop", name)
		}
	}
	t.Logf("ok (parallel=%v): both tools reached", parallel)
}

// checkSleepCall validates the dated call's arguments against its schema.
func checkSleepCall(t *testing.T, c provider.ToolCall) {
	t.Helper()
	if c.Name != sleepName {
		return
	}
	if err := checkArgs(sleepTool(), c.Arguments); err != nil {
		t.Errorf("sleep call: %v", err)
		return
	}
	if !strings.Contains(c.Arguments, "2026-08-17") {
		t.Errorf("sleep call used %q, want the date in the question", c.Arguments)
	}
}

// TestModelCompatibility evaluates one model against everything Kadence's chat
// pipeline requires of it. Each subtest is one capability, so the output reads
// as a compatibility report rather than a single pass/fail.
func TestModelCompatibility(t *testing.T) {
	baseURL, apiKey, model := compatEnv(t)
	p := provider.NewOpenAICompat(baseURL, apiKey)
	t.Logf("evaluating %q at %s", model, baseURL)

	t.Run("streams content incrementally", func(t *testing.T) {
		// Chat is served over SSE. A provider that only returns the whole
		// answer at the end leaves the user watching an empty box.
		ctx, cancel := context.WithTimeout(context.Background(), compatTimeout)
		defer cancel()

		var deltas atomic.Int32
		req := compatRequest(model, nil, "Name three benefits of easy running. One short sentence each.")
		text, err := p.StreamChat(ctx, req, func(string) error {
			deltas.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("StreamChat: %v", err)
		}
		if strings.TrimSpace(text) == "" {
			t.Fatal("the model returned no content")
		}
		if n := deltas.Load(); n < 2 {
			t.Fatalf("%d content deltas: the endpoint is not streaming", n)
		}
		t.Logf("ok: %d deltas, %d chars", deltas.Load(), len(text))
	})

	t.Run("calls a tool when the question needs data", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), compatTimeout)
		defer cancel()

		req := compatRequest(model, []provider.ToolDefinition{countActivitiesTool(), sleepTool()},
			"How many activities do I have recorded?")
		res, err := p.StreamChatWithTools(ctx, req, func(string) error { return nil })
		if err != nil {
			t.Fatalf("StreamChatWithTools: %v", err)
		}
		if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != countTool {
			t.Fatalf("calls = %v, want exactly [%s]", callNames(res), countTool)
		}
		if err := checkArgs(countActivitiesTool(), res.ToolCalls[0].Arguments); err != nil {
			// Not fatal on its own: chat forwards the arguments to the MCP
			// server, which validates them itself. But a model that invents
			// fields wastes a round trip on every call that gets refused.
			t.Errorf("argument problem: %v", err)
		}
		t.Logf("ok: finish=%q args=%q", res.FinishReason, res.ToolCalls[0].Arguments)
	})

	t.Run("does not call a tool when none is needed", func(t *testing.T) {
		// The expensive failure mode. A model that reaches for tools
		// unprompted burns latency on every turn and, with the destructive
		// tier enabled, puts confirmation prompts in front of a user who
		// asked for nothing.
		ctx, cancel := context.WithTimeout(context.Background(), compatTimeout)
		defer cancel()

		req := compatRequest(model, []provider.ToolDefinition{countActivitiesTool(), sleepTool()},
			"Thanks, that's all I needed. Have a good one!")
		res, err := p.StreamChatWithTools(ctx, req, func(string) error { return nil })
		if err != nil {
			t.Fatalf("StreamChatWithTools: %v", err)
		}
		if len(res.ToolCalls) != 0 {
			t.Fatalf("a plain closing message triggered %v", callNames(res))
		}
		t.Logf("ok: answered in text, no tool call")
	})

	t.Run("continues after a tool result", func(t *testing.T) {
		// A tool call is worthless if the model cannot consume the answer.
		// This is the round trip chat performs on every tool-using turn.
		ctx, cancel := context.WithTimeout(context.Background(), compatTimeout)
		defer cancel()

		// Ask for real, then answer the call the model actually made. A
		// fabricated id would hide an endpoint that issues empty or duplicate
		// ids, which chat would then echo back as an invalid tool_call_id.
		first := compatRequest(model, []provider.ToolDefinition{countActivitiesTool()},
			"How many activities do I have recorded?")
		asked, err := p.StreamChatWithTools(ctx, first, func(string) error { return nil })
		if err != nil {
			t.Fatalf("first turn: %v", err)
		}
		if len(asked.ToolCalls) == 0 {
			t.Fatal("the model did not call a tool, so there is no result to feed back")
		}
		call := asked.ToolCalls[0]
		if strings.TrimSpace(call.ID) == "" {
			t.Fatal("the tool call carries no id; chat cannot answer it")
		}
		seen := map[string]bool{}
		for _, c := range asked.ToolCalls {
			if seen[c.ID] {
				t.Fatalf("duplicate tool-call id %q", c.ID)
			}
			seen[c.ID] = true
		}

		req := first
		req.Messages = append(append([]provider.Message(nil), first.Messages...),
			provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{call}},
			provider.Message{Role: "tool", ToolCallID: call.ID, Name: call.Name, Content: "2614"},
		)
		res, err := p.StreamChatWithTools(ctx, req, func(string) error { return nil })
		if err != nil {
			t.Fatalf("StreamChatWithTools: %v", err)
		}
		// Digits only: a model may format the figure as "2,614" or "2 614",
		// which is a presentation choice and not a failure to read the tool.
		if !strings.Contains(digitsOnly(res.Content), "2614") {
			t.Fatalf("the tool result did not reach the answer: %q", res.Content)
		}
		t.Logf("ok: %q", strings.TrimSpace(res.Content))
	})

	t.Run("still picks correctly with a full tool list", func(t *testing.T) {
		// A deployment with a large MCP server puts ~100 schemas in front of
		// the model on every turn. Selection accuracy at two tools says
		// nothing about accuracy at a hundred.
		ctx, cancel := context.WithTimeout(context.Background(), compatTimeout)
		defer cancel()

		req := compatRequest(model, paddedTools(compatToolCount),
			"How many activities do I have, and how did I sleep on 2026-08-17?")
		res, err := p.StreamChatWithTools(ctx, req, func(string) error { return nil })
		if err != nil {
			t.Fatalf("StreamChatWithTools with %d tools: %v", compatToolCount, err)
		}
		driveToolLoop(ctx, t, p, req, res)
	})

	t.Run("accepts native image input", func(t *testing.T) {
		// Uploaded PDFs are rendered to page images and sent as native image
		// content (internal/chat/pageimage.go). A text-only model cannot serve
		// that half of the product, so this reports rather than fails: the
		// deployment may not use document upload at all.
		ctx, cancel := context.WithTimeout(context.Background(), compatTimeout)
		defer cancel()

		req := compatRequest(model, nil, "Reply with the single word: seen")
		req.Messages[len(req.Messages)-1].Images = []provider.ImageContent{
			{Data: onePixelPNG, MIMEType: "image/png", Width: 1, Height: 1},
		}
		text, err := p.StreamChat(ctx, req, func(string) error { return nil })
		switch {
		case errors.Is(err, provider.ErrVisionUnsupported):
			t.Logf("NOT SUPPORTED: no native image input — document upload and PDF pages will not work")
		case err != nil:
			t.Logf("NOT SUPPORTED: image input rejected: %v", err)
		case strings.TrimSpace(text) == "":
			t.Logf("NOT SUPPORTED: image accepted but the model returned nothing")
		default:
			t.Logf("ok: image accepted, replied %q", strings.TrimSpace(text))
		}
	})

	t.Run("reports a finish reason", func(t *testing.T) {
		// Chat continues a truncated answer by checking for FinishLength. An
		// endpoint that never reports one silently drops long replies.
		ctx, cancel := context.WithTimeout(context.Background(), compatTimeout)
		defer cancel()

		// Deliberately ask for far more than the cap allows, so the endpoint
		// must report the cut. Accepting any non-empty reason would pass an
		// endpoint that always says "stop" and silently drops long answers.
		req := compatRequest(model, nil,
			"Write a detailed 800-word explanation of periodisation in marathon training.")
		req.MaxTokens = 24
		res, err := p.StreamChatWithTools(ctx, req, func(string) error { return nil })
		if err != nil {
			t.Fatalf("StreamChatWithTools: %v", err)
		}
		if res.FinishReason != provider.FinishLength {
			t.Errorf("finish=%q for a capped answer, want %q: chat cannot detect or continue truncation",
				res.FinishReason, provider.FinishLength)
			return
		}
		t.Logf("ok: finish=%q", res.FinishReason)
	})
}
