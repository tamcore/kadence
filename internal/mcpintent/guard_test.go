package mcpintent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/provider"
)

const (
	testSearchWeb     = "Search web"
	testWebSearch     = "web__search"
	testAllowResponse = `{"verdict":"ALLOW","reason":"ok"}`
)

type fakeProvider struct {
	content string
	err     error
	request provider.ChatRequest
	called  bool
}

func (p *fakeProvider) StreamChat(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	p.called = true
	p.request = req
	if p.err != nil {
		return "", p.err
	}
	if err := onToken(p.content); err != nil {
		return "", err
	}
	return p.content, nil
}

func (p *fakeProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

func TestGuardAllowsStrictJSONAndFramesData(t *testing.T) {
	p := &fakeProvider{content: `{"verdict":"ALLOW","reason":"Tool matches request."}`}
	g := NewGuard(p, Config{Model: "classifier", HistoryWindow: 6})
	ctx := WithTrustedContext(t.Context(), TrustedContext{Request: testCheckWeather})
	decision, err := g.Evaluate(ctx, validInput())
	if err != nil || decision != (Decision{Verdict: VerdictAllow, Reason: "Tool matches request."}) {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if len(p.request.Messages) != 2 || !strings.Contains(p.request.Messages[1].Content, `"toolName":"web__search"`) {
		t.Fatalf("request=%+v", p.request)
	}
	if p.request.Model != "classifier" || p.request.MaxTokens != classifierMaxTokens || p.request.Temperature != 0 {
		t.Fatalf("request=%+v", p.request)
	}
}

func TestGuardLimitsFramedHistory(t *testing.T) {
	p := &fakeProvider{content: `{"verdict":"ALLOW","reason":"Tool matches request."}`}
	g := NewGuard(p, Config{HistoryWindow: 1})
	ctx := WithTrustedContext(t.Context(), TrustedContext{History: []provider.Message{
		{Role: classifierUserRole, Content: "old request"},
		{Role: "assistant", Content: "current request"},
	}})
	if _, err := g.Evaluate(ctx, validInput()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.request.Messages[1].Content, "old request") || !strings.Contains(p.request.Messages[1].Content, "current request") {
		t.Fatalf("request=%s", p.request.Messages[1].Content)
	}
}

func TestGuardRejectsNonStrictResponses(t *testing.T) {
	for _, response := range []string{
		`ALLOW`,
		"```json\n{\"verdict\":\"ALLOW\",\"reason\":\"ok\"}\n```",
		`{"verdict":"MAYBE","reason":"no"}`,
		`{"verdict":"allow","reason":"no"}`,
		`{"verdict":"DENY","reason":""}`,
		`{"verdict":"DENY","reason":"no","extra":true}`,
		`{"verdict":"ALLOW","verdict":"DENY","reason":"no"}`,
		`{"verdict":"ALLOW","reason":"yes","reason":"no"}`,
		`{"verdict":"DENY","reason":"` + strings.Repeat("x", MaxReasonBytes+1) + `"}`,
		`{"verdict":"DENY","reason":"\ud800"}`,
	} {
		t.Run(response, func(t *testing.T) {
			decision, err := newGuardWithResponse(response).Evaluate(trustedContext(), validInput())
			assertUnavailableBlock(t, decision, err)
		})
	}
}

func TestGuardBlocksDenyWithRevisionInstruction(t *testing.T) {
	decision, err := newGuardWithResponse(`{"verdict":"DENY","reason":"Tool would disclose private data."}`).Evaluate(trustedContext(), validInput())
	if decision != (Decision{Verdict: VerdictDeny, Reason: "Tool would disclose private data."}) {
		t.Fatalf("decision=%+v", decision)
	}
	blocked, ok := AsBlocked(err)
	if !ok {
		t.Fatalf("err=%v", err)
	}
	if blocked.Kind != BlockKindDenied || blocked.Reason != "Tool would disclose private data. Revise the tool intent and try again." {
		t.Fatalf("blocked=%+v", blocked)
	}
}

func TestGuardBoundsDenyReasonWithRevisionInstruction(t *testing.T) {
	reason := strings.Repeat("x", MaxReasonBytes)
	decision, err := newGuardWithResponse(`{"verdict":"DENY","reason":"`+reason+`"}`).Evaluate(trustedContext(), validInput())
	blocked, ok := AsBlocked(err)
	if !ok || decision.Verdict != VerdictDeny {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	want := strings.Repeat("x", MaxReasonBytes-len(revisionInstruction)-1) + " " + revisionInstruction
	if blocked.Reason != want || len(blocked.Reason) > MaxReasonBytes {
		t.Fatalf("reason length=%d reason=%q", len(blocked.Reason), blocked.Reason)
	}
}

func TestGuardFailsClosedForProviderErrors(t *testing.T) {
	p := &fakeProvider{err: errors.New("backend token: secret")}
	decision, err := NewGuard(p, Config{}).Evaluate(trustedContext(), validInput())
	assertUnavailableBlock(t, decision, err)
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error=%q", err)
	}
}

func TestGuardFailsClosedForCancelledOrExpiredContext(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "cancelled", ctx: cancelledContext()},
		{name: "deadline", ctx: expiredContext()},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &fakeProvider{content: testAllowResponse}
			decision, err := NewGuard(p, Config{}).Evaluate(WithTrustedContext(test.ctx, TrustedContext{Request: testCheckWeather}), validInput())
			assertUnavailableBlock(t, decision, err)
			if p.called {
				t.Fatal("provider called after context ended")
			}
		})
	}
}

func TestGuardFailsClosedWithoutTrustedContext(t *testing.T) {
	p := &fakeProvider{content: testAllowResponse}
	decision, err := NewGuard(p, Config{}).Evaluate(t.Context(), validInput())
	assertUnavailableBlock(t, decision, err)
	if p.called {
		t.Fatal("provider called without trusted context")
	}
}

func TestGuardRejectsInvalidInputWithoutProviderCall(t *testing.T) {
	for _, input := range []Input{
		{ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `{}`},
		{Intent: strings.Repeat("x", MaxIntentBytes+1), ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `{}`},
		{Intent: string([]byte{0xff}), ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `{}`},
	} {
		p := &fakeProvider{content: testAllowResponse}
		decision, err := NewGuard(p, Config{}).Evaluate(trustedContext(), input)
		blocked, ok := AsBlocked(err)
		if !ok || decision != (Decision{Reason: "intent is required and must be non-empty UTF-8 text of at most 512 bytes"}) ||
			blocked.Kind != BlockKindError || blocked.Reason != "intent is required and must be non-empty UTF-8 text of at most 512 bytes" {
			t.Fatalf("decision=%+v err=%v", decision, err)
		}
		if p.called {
			t.Fatal("provider called with invalid input")
		}
	}
}

func TestGuardRejectsInvalidToolInputWithoutProviderCall(t *testing.T) {
	for _, test := range []struct {
		name  string
		input Input
	}{
		{name: "empty tool name", input: Input{Intent: testCheckWeather, ToolDescription: testSearchWeb, Arguments: `{}`}},
		{name: "whitespace tool name", input: Input{Intent: testCheckWeather, ToolName: " \t", ToolDescription: testSearchWeb, Arguments: `{}`}},
		{name: "invalid UTF-8 tool name", input: Input{Intent: testCheckWeather, ToolName: string([]byte{0xff}), ToolDescription: testSearchWeb, Arguments: `{}`}},
		{name: "empty tool description", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, Arguments: `{}`}},
		{name: "whitespace tool description", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: " \t", Arguments: `{}`}},
		{name: "invalid UTF-8 tool description", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: string([]byte{0xff}), Arguments: `{}`}},
		{name: "array arguments", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `[]`}},
		{name: "trailing arguments", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `{} {}`}},
		{name: "invalid UTF-8 arguments", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: string([]byte{0xff})}},
		{name: "unpaired surrogate argument", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `{"q":"\ud800"}`}},
		{name: "reserved argument", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `{"_kadence_intent":"weather"}`}},
		{name: "duplicate reserved argument", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `{"_kadence_intent":"weather","_kadence_intent":"forecast"}`}},
		{name: "duplicate ordinary argument", input: Input{Intent: testCheckWeather, ToolName: testWebSearch, ToolDescription: testSearchWeb, Arguments: `{"q":"weather","q":"forecast"}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &fakeProvider{content: testAllowResponse}
			decision, err := NewGuard(p, Config{}).Evaluate(trustedContext(), test.input)
			if p.called {
				t.Fatal("provider called with invalid tool input")
			}
			assertUnavailableBlock(t, decision, err)
		})
	}
}

func TestGuardAllowsEmptyArgumentsObjectVerbatim(t *testing.T) {
	p := &fakeProvider{content: testAllowResponse}
	input := validInput()
	input.Arguments = `{}`
	decision, err := NewGuard(p, Config{}).Evaluate(trustedContext(), input)
	if err != nil || decision.Verdict != VerdictAllow {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	var envelope classifierEnvelope
	if err := json.Unmarshal([]byte(p.request.Messages[1].Content), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Input.Arguments != `{}` {
		t.Fatalf("arguments=%q", envelope.Input.Arguments)
	}
}

func TestAsBlockedFindsWrappedBlockedError(t *testing.T) {
	want := &BlockedError{Verdict: VerdictDeny, Kind: BlockKindDenied, Reason: "blocked"}
	got, ok := AsBlocked(fmt.Errorf("evaluate: %w", want))
	if !ok || got != want {
		t.Fatalf("got=%+v ok=%t", got, ok)
	}
}

func validInput() Input {
	return Input{
		Intent:          testCheckWeather,
		ToolName:        testWebSearch,
		ToolDescription: testSearchWeb,
		Arguments:       `{"q":"weather"}`,
	}
}

func trustedContext() context.Context {
	return WithTrustedContext(context.Background(), TrustedContext{Request: testCheckWeather})
}

func newGuardWithResponse(response string) *Guard {
	return NewGuard(&fakeProvider{content: response}, Config{})
}

func assertUnavailableBlock(t *testing.T, decision Decision, err error) {
	t.Helper()
	blocked, ok := AsBlocked(err)
	if !ok || decision != (Decision{Reason: "intent validation unavailable; revise or retry later"}) ||
		blocked.Kind != BlockKindError || blocked.Reason != "intent validation unavailable; revise or retry later" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func expiredContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	return ctx
}
