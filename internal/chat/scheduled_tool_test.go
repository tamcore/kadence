package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
)

type scheduledHandoffStub struct{}

func (scheduledHandoffStub) DraftFromChat(context.Context, scheduled.Actor, scheduled.HandoffRequest) (scheduled.ChatArtifact, error) {
	return scheduled.ChatArtifact{}, nil
}

func (scheduledHandoffStub) ConfirmSoleChatDraft(context.Context, scheduled.Actor, string) (scheduled.ChatConfirmation, error) {
	return scheduled.ChatConfirmation{Status: scheduled.ChatConfirmationNone}, nil
}

func (scheduledHandoffStub) CleanupChatDrafts(context.Context, int64, []string) error { return nil }

func TestScheduledToolDefinitionContract(t *testing.T) {
	definition := draftFutureUnattendedTaskToolDefinition()
	if definition.Name != "kadence__draft_future_unattended_task" {
		t.Fatalf("name = %q, want future unattended task tool", definition.Name)
	}
	if definition.Name == "kadence__draft_scheduled_task" {
		t.Fatal("definition exposed legacy scheduled task tool")
	}
	for _, want := range []string{
		"future unattended task", "retry", "follow-up",
		"do not use it to execute or schedule work now", "direct calendar or domain operation",
	} {
		if !strings.Contains(strings.ToLower(definition.Description), want) {
			t.Errorf("description missing %q: %s", want, definition.Description)
		}
	}
	parameters := string(definition.Parameters)
	for _, want := range []string{
		`"instruction"`, `"type":"string"`, `"minLength":1`, `"maxLength":4096`,
		`"required":["instruction"]`, `"additionalProperties":false`,
	} {
		if !strings.Contains(parameters, want) {
			t.Errorf("parameters = %s, missing %s", parameters, want)
		}
	}
}

func TestParseDraftScheduledTaskArgsRejectsInvalidJSON(t *testing.T) {
	valid := ` { "instruction": "  check my training load  " } `
	args, err := parseDraftScheduledTaskArgs(valid)
	if err != nil {
		t.Fatalf("parse valid args: %v", err)
	}
	if args.Instruction != "check my training load" {
		t.Fatalf("instruction = %q", args.Instruction)
	}

	for _, raw := range []string{
		`{}`, `{"instruction":""}`, `{"instruction":"   "}`,
		`{"instruction":"x","extra":true}`, `[]`,
		`{"instruction":"x"} {"instruction":"y"}`,
		`{"instruction":"` + strings.Repeat("x", 4097) + `"}`,
	} {
		if _, err := parseDraftScheduledTaskArgs(raw); err == nil {
			t.Errorf("parse %q succeeded, want error", raw[:min(len(raw), 80)])
		}
	}
}

func TestAssembleToolsOffersScheduledToolOnlyWhenEnabled(t *testing.T) {
	disabled := NewService(nil, ServiceConfig{}, Deps{})
	if hasToolNamed(disabled.assembleTools(t.Context(), nil), draftFutureUnattendedTaskToolName) {
		t.Fatal("disabled service offered scheduled tool")
	}

	enabled := NewService(nil, ServiceConfig{}, Deps{Scheduled: scheduledHandoffStub{}})
	tools := enabled.assembleTools(t.Context(), nil)
	if !hasToolNamed(tools, draftFutureUnattendedTaskToolName) {
		t.Fatal("enabled service did not offer scheduled tool")
	}
	if hasToolNamed(tools, legacyDraftScheduledTaskToolName) {
		t.Fatal("enabled service offered legacy scheduled tool")
	}
}

func TestSystemPromptAddsSchedulingGuidanceOnlyWhenEnabled(t *testing.T) {
	disabled := NewService(nil, ServiceConfig{}, Deps{})
	if strings.Contains(disabled.systemPrompt(UserContext{}), draftFutureUnattendedTaskToolName) {
		t.Fatal("disabled system prompt included scheduling guidance")
	}
	enabled := NewService(nil, ServiceConfig{}, Deps{Scheduled: scheduledHandoffStub{}})
	prompt := enabled.systemPrompt(UserContext{})
	for _, want := range []string{
		draftFutureUnattendedTaskToolName, "future unattended task", "retry", "follow-up",
		"execute or schedule work now", "direct calendar or domain operation", "does not imply creating a handoff",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("scheduling guidance missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, legacyDraftScheduledTaskToolName) {
		t.Fatal("system prompt included legacy scheduled tool")
	}
}

func TestScheduledToolResultAndSSEDoNotExposeInstructionOrHistory(t *testing.T) {
	service := NewService(nil, ServiceConfig{}, Deps{Scheduled: scheduledHandoffStub{}})
	state := &toolTurnState{}
	sink := &fitEventSink{}
	message := service.handleDraftScheduledTask(
		t.Context(), "conversation", scheduled.Actor{ID: 7}, "source history must stay private", 11,
		[]model.Message{{Role: model.MsgRoleAssistant, Content: "prior history must stay private"}}, state,
		provider.ToolCall{ID: "call", Name: draftFutureUnattendedTaskToolName, Arguments: `{"instruction":"instruction must stay private"}`}, sink,
	)
	for _, secret := range []string{"instruction must stay private", "source history must stay private", "prior history must stay private"} {
		if strings.Contains(message.Content, secret) {
			t.Fatalf("tool result leaked %q: %s", secret, message.Content)
		}
	}
	if len(sink.events) != 1 || sink.events[0].Type != EventScheduledArtifact || sink.events[0].ScheduledArtifact == nil {
		t.Fatalf("events = %+v, want one scheduled artifact", sink.events)
	}
	if encoded, err := json.Marshal(sink.events[0]); err != nil || strings.Contains(string(encoded), "instruction must stay private") {
		t.Fatalf("artifact event leaked instruction: event=%+v err=%v", sink.events[0], err)
	}
}
