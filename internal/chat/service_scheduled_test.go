package chat_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
)

func TestStreamNewScheduledConversationSkipsTitleGeneration(t *testing.T) {
	handoff := &fakeScheduledHandoff{artifacts: []scheduled.ChatArtifact{{
		HandoffID: testHandoffOne, TaskID: testScheduledTaskOne, Ordinal: 1, ArtifactState: testScheduledArtifactReady,
	}}}
	provider := &scriptedProvider{results: []provider.StreamResult{
		{ToolCalls: []provider.ToolCall{{
			ID: testScheduledCallID, Name: testScheduledToolName, Arguments: testScheduledArguments,
		}}},
		{Content: "I drafted a recovery check."},
	}}
	convs := &fakeConvs{
		byID: map[string]model.Conversation{}, titleUpdateSwapped: true,
		titleUpdateResult: model.Conversation{ID: testNewConvID, Title: testGeneratedTitle},
	}
	titles := &fakeTitleGenerator{title: testGeneratedTitle}
	svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, SystemPrompt: testSystemMsg}, chat.Deps{
		Convs: convs, Msgs: &fakeMsgs{}, Scheduled: handoff, TitleGenerator: titles,
	})
	sink := &capturingSink{}

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, "", "schedule a recovery check", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(titles.inputs) != 0 || len(convs.titleUpdateCalls) != 0 {
		t.Fatalf("title calls inputs=%+v updates=%+v", titles.inputs, convs.titleUpdateCalls)
	}
	artifactCount := 0
	for _, event := range sink.events {
		if event.Type == chat.EventTitle {
			t.Fatalf("scheduled handoff emitted title event: %+v", event)
		}
		if event.Type == chat.EventScheduledArtifact {
			artifactCount++
		}
	}
	if artifactCount != 1 {
		t.Fatalf("scheduled artifact events = %d, want one", artifactCount)
	}
	if last := sink.events[len(sink.events)-1]; last.Type != chat.EventDone {
		t.Fatalf("last event = %+v, want done", last)
	}
}

func TestServiceDraftsScheduledTasksAsArtifactsWithoutGenericToolEvents(t *testing.T) {
	handoff := &fakeScheduledHandoff{artifacts: []scheduled.ChatArtifact{
		{HandoffID: testHandoffOne, TaskID: testScheduledTaskOne, Ordinal: 1, ArtifactState: testScheduledArtifactReady},
		{HandoffID: "handoff-two", TaskID: "task-two", Ordinal: 2, ArtifactState: testScheduledArtifactReady},
	}}
	provider := &scriptedProvider{results: []provider.StreamResult{
		{ToolCalls: []provider.ToolCall{
			{ID: "call-one", Name: testScheduledToolName, Arguments: `{"instruction":"check my recovery"}`},
			{ID: "call-two", Name: testScheduledToolName, Arguments: `{"instruction":"watch my weekly load"}`},
		}},
		{Content: "I drafted both tasks; review and confirm them."},
	}}
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens, SystemPrompt: testSystemMsg}, chat.Deps{
		Convs: convs, Msgs: msgs, Scheduled: handoff,
	})
	sink := &capturingSink{}
	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername, Timezone: testTimezoneBerlin}, testConvID, "schedule both", sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got, want := msgs.assistantHandoffIDs, []string{testHandoffOne, "handoff-two"}; !slices.Equal(got, want) {
		t.Fatalf("persisted handoff IDs = %v, want %v", got, want)
	}
	if len(handoff.requests) != 2 || handoff.requests[0].Ordinal != 1 || handoff.requests[1].Ordinal != 2 {
		t.Fatalf("handoff requests = %+v, want ordinal 1 then 2", handoff.requests)
	}
	if handoff.requests[0].SourceContent != "schedule both" || handoff.requests[0].SourceConversationID != testConvID {
		t.Fatalf("source request = %+v", handoff.requests[0])
	}
	if len(handoff.actors) != 2 || handoff.actors[0].Timezone != testTimezoneBerlin {
		t.Fatalf("handoff actors = %+v, want timezone forwarded", handoff.actors)
	}

	var artifacts []chat.ChatEvent
	for _, event := range sink.events {
		if event.Type == chat.EventTool && event.Tool == testScheduledToolName {
			t.Fatalf("scheduling emitted generic tool event: %+v", event)
		}
		if event.Type == chat.EventScheduledArtifact {
			artifacts = append(artifacts, event)
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("marshal artifact event: %v", err)
			}
			if strings.Contains(string(encoded), "check my recovery") || strings.Contains(string(encoded), "watch my weekly load") {
				t.Fatalf("artifact leaked tool instruction: %s", encoded)
			}
		}
	}
	if len(artifacts) != 2 || artifacts[0].ScheduledArtifact == nil || artifacts[0].ScheduledArtifact.Ordinal != 1 ||
		artifacts[1].ScheduledArtifact == nil || artifacts[1].ScheduledArtifact.Ordinal != 2 {
		t.Fatalf("artifact events = %+v", artifacts)
	}
}

func TestServiceKeepsDirectDomainSchedulesOutOfScheduledHandoffs(t *testing.T) {
	for _, tc := range []struct {
		name             string
		toolCalls        []provider.ToolCall
		directDomainErr  error
		wantHandoffCount int
	}{
		{
			name: "direct domain schedule only",
			toolCalls: []provider.ToolCall{{
				ID: testDirectDomainCallID, Name: testDirectDomainToolName, Arguments: testDirectDomainArguments,
			}},
		},
		{
			name: "failed direct domain schedule only",
			toolCalls: []provider.ToolCall{{
				ID: testDirectDomainCallID, Name: testDirectDomainToolName, Arguments: testDirectDomainArguments,
			}},
			directDomainErr: errors.New("calendar unavailable"),
		},
		{
			name: "explicit future unattended call creates handoff",
			toolCalls: []provider.ToolCall{
				{ID: testDirectDomainCallID, Name: testDirectDomainToolName, Arguments: testDirectDomainArguments},
				{ID: testScheduledCallID, Name: testScheduledToolName, Arguments: testScheduledArguments},
			},
			wantHandoffCount: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handoff := &fakeScheduledHandoff{}
			mcp := &fakeMCPTools{
				enabled:    true,
				tools:      []provider.ToolDefinition{{Name: testDirectDomainToolName}},
				callResult: `{"id":"event-1"}`,
				callErr:    tc.directDomainErr,
			}
			provider := &scriptedProvider{results: []provider.StreamResult{
				{ToolCalls: tc.toolCalls},
				{Content: "Done."},
			}}
			convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID}}}
			msgs := &fakeMsgs{}
			svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{
				Convs: convs, Msgs: msgs, MCP: mcp, Scheduled: handoff,
			})

			if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "schedule my calendar event now", &capturingSink{}); err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if !mcp.callInvoked || mcp.gotToolName != testDirectDomainToolName {
				t.Fatalf("direct domain call = %q, invoked=%t", mcp.gotToolName, mcp.callInvoked)
			}
			if got := len(handoff.requests); got != tc.wantHandoffCount {
				t.Fatalf("DraftFromChat calls = %d, want %d", got, tc.wantHandoffCount)
			}
		})
	}
}

func TestServiceRejectsLegacyScheduledToolCallWithoutMCPOrHandoff(t *testing.T) {
	handoff := &fakeScheduledHandoff{}
	mcp := &fakeMCPTools{enabled: true}
	provider := &scriptedProvider{results: []provider.StreamResult{
		{ToolCalls: []provider.ToolCall{{
			ID: testScheduledCallID, Name: "kadence__draft_scheduled_task", Arguments: testScheduledArguments,
		}}},
		{Content: "I cannot create that handoff."},
	}}
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{
		Convs: convs, Msgs: msgs, Scheduled: handoff, MCP: mcp,
	})

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "schedule it", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(handoff.requests) != 0 {
		t.Fatalf("DraftFromChat requests = %+v, want none", handoff.requests)
	}
	if mcp.callInvoked {
		t.Fatalf("legacy tool call reached MCP: tool=%q args=%q", mcp.gotToolName, mcp.gotArgsJSON)
	}
	if len(provider.requests) != 2 || len(provider.requests[1].Messages) == 0 {
		t.Fatalf("provider requests = %+v, want tool result in second request", provider.requests)
	}
	last := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if last.Role != toolMsgRole || last.Content != "error: legacy scheduled handoff tool is unavailable" {
		t.Fatalf("legacy tool result = %+v, want rejection", last)
	}
}

func TestServicePlainAffirmationConfirmsSoleScheduledDraftWithoutProvider(t *testing.T) {
	artifact := scheduled.ChatArtifact{
		HandoffID: testHandoffOne, TaskID: testScheduledTaskOne, Ordinal: 1,
		ArtifactState: testScheduledArtifactReady, TaskState: model.ScheduledTaskStateActive,
		Proposal: &scheduled.Proposal{Name: "Race weather"},
	}
	handoff := &fakeScheduledHandoff{confirmation: scheduled.ChatConfirmation{
		Status: scheduled.ChatConfirmationConfirmed, Artifact: &artifact,
	}}
	provider := &scriptedProvider{results: []provider.StreamResult{{Content: testProviderMustNotRun}}}
	guardProvider := &verdictProvider{verdict: testGuardrailOffTopic}
	guard := chat.NewGuardrail(guardProvider, chat.GuardrailConfig{
		Model: testGuardrailClassifierModel, DomainName: testGuardrailDomain,
		AllowedTopics: testGuardrailTopics, RefusalMessage: testGuardrailRefusal,
	})
	convs := &fakeConvs{byID: map[string]model.Conversation{
		testConvID: {ID: testConvID, UserID: testUserID},
	}}
	msgs := &fakeMsgs{added: []model.Message{
		{ID: 1, ConversationID: testConvID, Role: model.MsgRoleUser, Content: "schedule race weather"},
		{ID: 2, ConversationID: testConvID, Role: model.MsgRoleAssistant, Content: "Please confirm."},
	}}
	svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel}, chat.Deps{
		Convs: convs, Msgs: msgs, Scheduled: handoff, Guardrail: guard,
	})
	sink := &capturingSink{}

	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{
		Username: testUsername, Timezone: testTimezoneBerlin,
	}, testConvID, "Yes!", sink); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
	if len(guardProvider.gotReq.Messages) != 0 {
		t.Fatalf("guardrail provider received messages = %+v, want no call", guardProvider.gotReq.Messages)
	}
	if handoff.confirmationCalls != 1 || handoff.confirmationActor.ID != testUserID ||
		handoff.confirmationActor.Timezone != testTimezoneBerlin || handoff.confirmationChat != testConvID {
		t.Fatalf("confirmation calls=%d actor=%+v chat=%q", handoff.confirmationCalls, handoff.confirmationActor, handoff.confirmationChat)
	}
	last := msgs.added[len(msgs.added)-1]
	if last.Role != model.MsgRoleAssistant || !strings.Contains(last.Content, "Race weather") {
		t.Fatalf("persisted confirmation = %+v", last)
	}
	var artifacts []chat.ChatEvent
	for _, event := range sink.events {
		if event.Type == chat.EventScheduledArtifact {
			artifacts = append(artifacts, event)
		}
	}
	if len(artifacts) != 1 || artifacts[0].ScheduledArtifact == nil ||
		artifacts[0].ScheduledArtifact.TaskState != model.ScheduledTaskStateActive {
		t.Fatalf("artifact events = %+v", artifacts)
	}
	if sink.events[0].Type != chat.EventMeta || sink.events[len(sink.events)-1].Type != chat.EventDone {
		t.Fatalf("events = %+v", sink.events)
	}
}

// A plain "yes" must only confirm when exactly one complete draft is pending.
// Every other confirmation status has to answer the user instead of guessing,
// and must not reach the provider or redraft.
func TestServicePlainAffirmationDoesNotConfirmAmbiguousDraft(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      scheduled.ChatConfirmationStatus
		wantContent string
	}{
		{
			name:        "multiple pending drafts",
			status:      scheduled.ChatConfirmationMultiple,
			wantContent: "separately",
		},
		{
			name:        "draft still needs input",
			status:      scheduled.ChatConfirmationNeedsInput,
			wantContent: "needs input",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			handoff := &fakeScheduledHandoff{confirmation: scheduled.ChatConfirmation{Status: test.status}}
			provider := &scriptedProvider{results: []provider.StreamResult{{Content: testProviderMustNotRun}}}
			convs := &fakeConvs{byID: map[string]model.Conversation{
				testConvID: {ID: testConvID, UserID: testUserID},
			}}
			msgs := &fakeMsgs{}
			svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel}, chat.Deps{
				Convs: convs, Msgs: msgs, Scheduled: handoff,
			})

			if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername},
				testConvID, "yes", &capturingSink{}); err != nil {
				t.Fatal(err)
			}
			if provider.calls != 0 || len(handoff.requests) != 0 {
				t.Fatalf("provider calls=%d draft requests=%d", provider.calls, len(handoff.requests))
			}
			if last := msgs.added[len(msgs.added)-1]; last.Role != model.MsgRoleAssistant ||
				!strings.Contains(strings.ToLower(last.Content), test.wantContent) {
				t.Fatalf("persisted response = %+v, want one mentioning %q", last, test.wantContent)
			}
		})
	}
}

func TestServiceOnlyInterceptsPlainAffirmationWithPendingDraft(t *testing.T) {
	for _, test := range []struct {
		name         string
		text         string
		confirmation scheduled.ChatConfirmation
		wantChecks   int
	}{
		{name: "no pending draft", text: "yes", confirmation: scheduled.ChatConfirmation{Status: scheduled.ChatConfirmationNone}, wantChecks: 1},
		{name: "new scheduling instruction", text: "yes, schedule another check"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handoff := &fakeScheduledHandoff{confirmation: test.confirmation}
			provider := &scriptedProvider{results: []provider.StreamResult{{Content: "Normal answer."}}}
			convs := &fakeConvs{byID: map[string]model.Conversation{
				testConvID: {ID: testConvID, UserID: testUserID},
			}}
			msgs := &fakeMsgs{}
			svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel}, chat.Deps{
				Convs: convs, Msgs: msgs, Scheduled: handoff,
			})

			if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, test.text, &capturingSink{}); err != nil {
				t.Fatal(err)
			}
			if provider.calls != 1 || handoff.confirmationCalls != test.wantChecks {
				t.Fatalf("provider calls=%d confirmation checks=%d", provider.calls, handoff.confirmationCalls)
			}
			if got := msgs.added[len(msgs.added)-1].Content; got != "Normal answer." {
				t.Fatalf("assistant content = %q", got)
			}
		})
	}
}

// When a turn drafts scheduling tasks but cannot be completed, the drafts must
// not be left behind — whichever step failed. The two cases reach the failure
// from opposite directions: a provider that answers but whose assistant message
// cannot be persisted, and a provider that fails outright on its second call.
func TestServiceCleansScheduledDraftsWhenTurnCannotComplete(t *testing.T) {
	draftToolCall := provider.StreamResult{ToolCalls: []provider.ToolCall{{
		ID: testScheduledCallID, Name: testScheduledToolName, Arguments: testScheduledArguments,
	}}}
	for _, test := range []struct {
		name      string
		handoffID string
		taskID    string
		newProv   func() provider.Provider
		wantFail  string
	}{
		{
			name:      "assistant message cannot be persisted",
			handoffID: "draft-handoff",
			taskID:    "draft-task",
			newProv: func() provider.Provider {
				return &scriptedProvider{results: []provider.StreamResult{draftToolCall, {Content: "Drafted."}}}
			},
			wantFail: "persistence failure",
		},
		{
			name:      "provider fails after the draft tool call",
			handoffID: "empty-failed-handoff",
			taskID:    "empty-failed-task",
			newProv: func() provider.Provider {
				return &scheduledFailingProvider{
					results: []provider.StreamResult{draftToolCall, {}}, failAt: 2,
				}
			},
			wantFail: "provider failure",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			handoff := &fakeScheduledHandoff{artifacts: []scheduled.ChatArtifact{{
				HandoffID: test.handoffID, TaskID: test.taskID, Ordinal: 1,
				ArtifactState: testScheduledArtifactReady,
			}}}
			convs := &fakeConvs{byID: map[string]model.Conversation{
				testConvID: {ID: testConvID, UserID: testUserID},
			}}
			msgs := &fakeMsgs{rejectAssistant: true}
			svc := chat.NewService(test.newProv(),
				chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens},
				chat.Deps{Convs: convs, Msgs: msgs, Scheduled: handoff})

			if err := svc.Stream(t.Context(), testUserID,
				chat.UserContext{Username: testUsername}, testConvID, "schedule it",
				&capturingSink{}); err == nil {
				t.Fatalf("Stream succeeded, want %s", test.wantFail)
			}
			if got, want := handoff.cleanup, [][]string{{test.handoffID}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("cleanup calls = %v, want %v", got, want)
			}
		})
	}
}

func TestServiceBindsScheduledHandoffsWhenProviderFailsAfterToolCalls(t *testing.T) {
	handoff := &fakeScheduledHandoff{artifacts: []scheduled.ChatArtifact{{
		HandoffID: "partial-handoff", TaskID: "partial-task", Ordinal: 1, ArtifactState: testScheduledArtifactReady,
	}}}
	provider := &scheduledFailingProvider{results: []provider.StreamResult{
		{ToolCalls: []provider.ToolCall{{ID: testScheduledCallID, Name: testScheduledToolName, Arguments: testScheduledArguments}}},
		{Content: "I drafted the task."},
	}, failAt: 2}
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{
		Convs: convs, Msgs: msgs, Scheduled: handoff,
	})
	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "schedule it", &capturingSink{}); err == nil {
		t.Fatal("Stream succeeded, want provider failure")
	}
	if got, want := msgs.assistantHandoffIDs, []string{"partial-handoff"}; !slices.Equal(got, want) {
		t.Fatalf("persisted partial handoff IDs = %v, want %v", got, want)
	}
	if len(handoff.cleanup) != 0 {
		t.Fatalf("cleanup = %v, want none after successful partial persistence", handoff.cleanup)
	}
}

func TestServiceBindsScheduledHandoffsWhenProviderFailsWithoutContent(t *testing.T) {
	handoff := &fakeScheduledHandoff{artifacts: []scheduled.ChatArtifact{{
		HandoffID: "empty-partial-handoff", TaskID: "empty-partial-task", Ordinal: 1, ArtifactState: testScheduledArtifactReady,
	}}}
	provider := &scheduledFailingProvider{results: []provider.StreamResult{
		{ToolCalls: []provider.ToolCall{{ID: testScheduledCallID, Name: testScheduledToolName, Arguments: testScheduledArguments}}},
		{},
	}, failAt: 2}
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{
		Convs: convs, Msgs: msgs, Scheduled: handoff,
	})
	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "schedule it", &capturingSink{}); err == nil {
		t.Fatal("Stream succeeded, want provider failure")
	}
	if got, want := msgs.assistantHandoffIDs, []string{"empty-partial-handoff"}; !slices.Equal(got, want) {
		t.Fatalf("persisted empty partial handoff IDs = %v, want %v", got, want)
	}
	if len(msgs.added) != 2 || msgs.added[1].Content != "I prepared the scheduling task drafts below, but could not finish the response." {
		t.Fatalf("persisted messages = %+v, want scheduled partial fallback", msgs.added)
	}
	if len(handoff.cleanup) != 0 {
		t.Fatalf("cleanup = %v, want none after successful empty partial persistence", handoff.cleanup)
	}
}

func TestServiceLimitsScheduledDraftCallsPerTurn(t *testing.T) {
	calls := make([]provider.ToolCall, 0, 6)
	for i := 1; i <= 6; i++ {
		calls = append(calls, provider.ToolCall{ID: "call-" + strconv.Itoa(i), Name: testScheduledToolName, Arguments: testScheduledArguments})
	}
	handoff := &fakeScheduledHandoff{}
	provider := &scriptedProvider{results: []provider.StreamResult{{ToolCalls: calls}, {Content: "Only five drafts were created."}}}
	convs := &fakeConvs{byID: map[string]model.Conversation{testConvID: {ID: testConvID, UserID: testUserID}}}
	msgs := &fakeMsgs{}
	svc := chat.NewService(provider, chat.ServiceConfig{Model: testModel, MaxTokens: testMaxTokens}, chat.Deps{
		Convs: convs, Msgs: msgs, Scheduled: handoff,
	})
	if err := svc.Stream(t.Context(), testUserID, chat.UserContext{Username: testUsername}, testConvID, "schedule tasks", &capturingSink{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(handoff.requests) != 5 {
		t.Fatalf("DraftFromChat calls = %d, want 5", len(handoff.requests))
	}
	if got, want := msgs.assistantHandoffIDs, []string{"handoff-1", "handoff-2", "handoff-3", "handoff-4", "handoff-5"}; !slices.Equal(got, want) {
		t.Fatalf("persisted handoff IDs = %v, want %v", got, want)
	}
}
