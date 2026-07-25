package scheduled

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/store"
)

const (
	handoffTestTaskID         = "task-handoff"
	handoffTestTimezoneBerlin = "Europe/Berlin"
	handoffOtherTool          = "other"
	handoffDefinitionID       = "definition-1"
)

type handoffStore struct {
	row         store.HydratedChatHandoff
	fresh       bool
	createCalls int
	readyCalls  int
	failedCalls int
	failedCode  string
	failedRetry bool
	list        []store.HydratedChatHandoff
	discardErr  error
	cleanupErr  error
}

type handoffConversations struct{}

func (handoffConversations) CreateWithKind(context.Context, int64, string, string) (model.Conversation, error) {
	return model.Conversation{}, errors.New("unused")
}

func (f *handoffStore) CreateOrGetDraft(_ context.Context, in store.CreateChatHandoffInput) (store.HydratedChatHandoff, bool, error) {
	f.createCalls++
	if f.row.Handoff.ID == "" {
		f.row = handoffRow(in.InvocationOrdinal, model.ScheduledHandoffStateCreating)
	}
	return f.row, f.fresh, nil
}
func (f *handoffStore) MarkTaskReady(context.Context, int64, string) error {
	f.readyCalls++
	return nil
}
func (f *handoffStore) MarkTaskFailed(_ context.Context, _ int64, _ string, code string, retryable bool) error {
	f.failedCalls++
	f.failedCode, f.failedRetry = code, retryable
	return nil
}
func (f *handoffStore) ListByAssistantMessages(context.Context, int64, string, []int64) ([]store.HydratedChatHandoff, error) {
	return f.list, nil
}
func (f *handoffStore) DiscardDraft(context.Context, int64, string) error    { return f.discardErr }
func (f *handoffStore) CleanupDrafts(context.Context, int64, []string) error { return f.cleanupErr }

type handoffMessages struct{ messages []model.Message }

func (f *handoffMessages) AddDefinition(_ context.Context, conversationID, role, content string) (model.Message, error) {
	m := model.Message{ConversationID: conversationID, Role: role, Content: content}
	f.messages = append(f.messages, m)
	return m, nil
}
func (f *handoffMessages) ListRecentDefinitionByConversation(context.Context, string, int) ([]model.Message, error) {
	return append([]model.Message(nil), f.messages...), nil
}

type handoffTasks struct{ task model.ScheduledTask }

func (f *handoffTasks) Create(context.Context, model.ScheduledTask) (model.ScheduledTask, error) {
	return model.ScheduledTask{}, errors.New("unused")
}
func (f *handoffTasks) GetByID(context.Context, string, int64) (model.ScheduledTask, error) {
	return f.task, nil
}
func (f *handoffTasks) ListByUser(context.Context, int64, int, int) ([]model.ScheduledTask, error) {
	return nil, nil
}
func (f *handoffTasks) BeginDraftRevision(_ context.Context, taskID string, _ int64, version int) (model.ScheduledTask, error) {
	if f.task.ID == "" {
		f.task = model.ScheduledTask{ID: taskID, ConversationID: handoffDefinitionID, Version: version + 1, State: model.ScheduledTaskStateDraft, Timezone: handoffTestTimezoneBerlin}
	}
	return f.task, nil
}
func (f *handoffTasks) Pause(context.Context, string, int64, int) (model.ScheduledTask, error) {
	return model.ScheduledTask{}, errors.New("unused")
}
func (f *handoffTasks) Resume(context.Context, string, int64, int, time.Time) (model.ScheduledTask, error) {
	return model.ScheduledTask{}, errors.New("unused")
}
func (f *handoffTasks) SaveProposal(_ context.Context, task model.ScheduledTask, _ int64, _ int) (model.ScheduledTask, error) {
	f.task = task
	return task, nil
}
func (f *handoffTasks) ConfirmProposal(context.Context, string, int64, int, time.Time) (model.ScheduledTask, error) {
	return model.ScheduledTask{}, errors.New("unused")
}
func (f *handoffTasks) SoftDelete(context.Context, string, int64) error { return errors.New("unused") }
func (f *handoffTasks) RunNow(context.Context, int64, string, string, time.Time) (model.ScheduledTaskRun, error) {
	return model.ScheduledTaskRun{}, errors.New("unused")
}
func (f *handoffTasks) ListRuns(context.Context, string, int64) ([]model.ScheduledTaskRun, error) {
	return nil, nil
}
func (f *handoffTasks) ListRunSummaries(context.Context, int64, int, int) ([]model.ScheduledTaskRunSummary, error) {
	return nil, nil
}
func (f *handoffTasks) MarkRead(context.Context, string, int64) error   { return nil }
func (f *handoffTasks) UnreadCount(context.Context, int64) (int, error) { return 0, nil }

type handoffCompiler struct {
	calls   int
	history []provider.Message
	tools   []provider.ToolDefinition
	version int
	err     error
}

func (f *handoffCompiler) Refine(_ context.Context, history []provider.Message, tools []provider.ToolDefinition, version int) (Refinement, error) {
	f.calls++
	f.history, f.tools = history, tools
	f.version = version
	if f.err != nil {
		return Refinement{}, f.err
	}
	return Refinement{Text: "Ready", Proposal: &Proposal{Version: version, Name: "Weather", TaskKind: TaskKindData, CompiledPrompt: "Check weather", ExecutionMode: ExecutionModeData, Timezone: handoffTestTimezoneBerlin, Schedule: Schedule{At: time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC), Timezone: handoffTestTimezoneBerlin}, DeliveryPolicy: DeliveryPolicyAlways, InitialRun: InitialRunWait}}, nil
}

func handoffRow(ordinal int, state string) store.HydratedChatHandoff {
	taskID := handoffTestTaskID
	return store.HydratedChatHandoff{Handoff: model.ScheduledHandoff{ID: "handoff-1", InvocationOrdinal: ordinal, ScheduledTaskID: &taskID, ArtifactState: state}, Task: &model.ScheduledTask{ID: taskID, ConversationID: handoffDefinitionID, State: model.ScheduledTaskStateDraft, Timezone: handoffTestTimezoneBerlin}}
}

func TestHandoffRequestBounds(t *testing.T) {
	actor := Actor{ID: 7, Username: executorTestUsername, Timezone: handoffTestTimezoneBerlin}
	base := HandoffRequest{SourceConversationID: "chat-1", SourceUserMessageID: 11, SourceContent: "exact source", Ordinal: 1, Instruction: " Check weather "}
	for _, tc := range []struct {
		name    string
		mutate  func(*HandoffRequest)
		wantErr bool
	}{
		{name: "trimmed instruction", mutate: func(*HandoffRequest) {}},
		{name: "four KiB", mutate: func(r *HandoffRequest) { r.Instruction = strings.Repeat("a", maxHandoffInstructionBytes) }},
		{name: "four KiB plus one", mutate: func(r *HandoffRequest) { r.Instruction = strings.Repeat("a", maxHandoffInstructionBytes+1) }, wantErr: true},
		{name: "zero ordinal", mutate: func(r *HandoffRequest) { r.Ordinal = 0 }, wantErr: true},
		{name: "six ordinal", mutate: func(r *HandoffRequest) { r.Ordinal = 6 }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.mutate(&req)
			handoffs, compiler := &handoffStore{fresh: true}, &handoffCompiler{}
			svc := NewService(ServiceDeps{Conversations: handoffConversations{}, Messages: &handoffMessages{}, Tasks: &handoffTasks{}, Compiler: compiler, ChatHandoffs: handoffs})
			artifact, err := svc.DraftFromChat(context.Background(), actor, req)
			if tc.wantErr {
				if err == nil {
					t.Fatal("DraftFromChat succeeded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "trimmed instruction" && !strings.Contains(compiler.history[0].Content, "Check weather") {
				t.Fatalf("definition=%q", compiler.history[0].Content)
			}
			if compiler.calls != 1 || compiler.version != 1 {
				t.Fatalf("compiler calls=%d version=%d", compiler.calls, compiler.version)
			}
			if artifact.TaskID == "" {
				t.Fatalf("artifact=%+v", artifact)
			}
		})
	}
}

func TestBoundedHandoffDefinitionIsSafeAndBounded(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	recent := make([]model.Message, 18)
	for i := range recent {
		recent[i] = model.Message{Role: model.MsgRoleUser, Content: strings.Repeat("😀", 5000), ToolCalls: []model.MessageToolCall{{Name: "visible", Arguments: `{"secret":"never-copy"}`}}}
	}
	recent[17].Role = model.MsgRoleAssistant
	definition := boundedHandoffDefinition(now, handoffTestTimezoneBerlin, "inspect weather", recent, []provider.ToolDefinition{{Name: "visible"}, {Name: handoffOtherTool}})
	if len(definition) > maxHandoffContextBytes || !utf8.ValidString(definition) {
		t.Fatalf("bytes=%d valid=%t", len(definition), utf8.ValidString(definition))
	}
	if strings.Contains(definition, "never-copy") || strings.Contains(definition, "secret") {
		t.Fatalf("raw tool arguments leaked: %q", definition)
	}
	if strings.Count(definition, "[user]") != maxHandoffMessages-1 || strings.Count(definition, "[assistant]") != 1 {
		t.Fatalf("message count=%q", definition)
	}
	for _, want := range []string{"Current UTC:\n2026-07-25T12:00:00Z", "Actor timezone:\n" + handoffTestTimezoneBerlin, "Prior safe tool names:\nvisible", "Current visible tool names:\n" + handoffOtherTool + "\nvisible"} {
		if !strings.Contains(definition, want) {
			t.Fatalf("definition missing %q", want)
		}
	}
}

func TestSourceFingerprintUsesExactContent(t *testing.T) {
	want := sha256.Sum256([]byte(" exact \n"))
	if got := sourceFingerprint(" exact \n"); string(got) != string(want[:]) {
		t.Fatalf("fingerprint=%x want=%x", got, want)
	}
}

func TestDraftFromChatReusesExistingAndPersistsSafeFailure(t *testing.T) {
	actor := Actor{ID: 7, Username: executorTestUsername, Timezone: handoffTestTimezoneBerlin}
	req := HandoffRequest{SourceConversationID: "chat-1", SourceUserMessageID: 11, SourceContent: "source", Ordinal: 1, Instruction: "weather"}
	t.Run("confirmed existing is reused without compiler", func(t *testing.T) {
		handoffs, compiler := &handoffStore{row: handoffRow(1, model.ScheduledHandoffStateReady)}, &handoffCompiler{}
		svc := NewService(ServiceDeps{Conversations: handoffConversations{}, Messages: &handoffMessages{}, Tasks: &handoffTasks{}, Compiler: compiler, ChatHandoffs: handoffs})
		artifact, err := svc.DraftFromChat(context.Background(), actor, req)
		if err != nil || !artifact.Reused || compiler.calls != 0 {
			t.Fatalf("artifact=%+v compiler=%d err=%v", artifact, compiler.calls, err)
		}
	})
	t.Run("compiler failure becomes retryable card", func(t *testing.T) {
		handoffs, compiler := &handoffStore{fresh: true}, &handoffCompiler{err: errors.New("provider detail")}
		svc := NewService(ServiceDeps{Conversations: handoffConversations{}, Messages: &handoffMessages{}, Tasks: &handoffTasks{}, Compiler: compiler, ChatHandoffs: handoffs})
		artifact, err := svc.DraftFromChat(context.Background(), actor, req)
		if err != nil || artifact.ArtifactState != model.ScheduledHandoffStateFailed || artifact.ErrorCode != handoffCompilerFailed || !artifact.Retryable || handoffs.failedCalls != 1 || handoffs.failedCode != handoffCompilerFailed || !handoffs.failedRetry {
			t.Fatalf("artifact=%+v handoffs=%+v err=%v", artifact, handoffs, err)
		}
	})
}

func TestRefineUpdatesOnlyLinkedChatHandoffState(t *testing.T) {
	actor := Actor{ID: 7, Username: executorTestUsername, Timezone: handoffTestTimezoneBerlin}
	tasks := &handoffTasks{task: model.ScheduledTask{ID: handoffTestTaskID, ConversationID: handoffDefinitionID, Version: 1, State: model.ScheduledTaskStateDraft, Timezone: handoffTestTimezoneBerlin}}
	handoffs := &handoffStore{}
	svc := NewService(ServiceDeps{Conversations: handoffConversations{}, Messages: &handoffMessages{}, Tasks: tasks, Compiler: &handoffCompiler{}, ChatHandoffs: handoffs})
	if _, err := svc.Refine(context.Background(), actor, handoffTestTaskID, "refine"); err != nil {
		t.Fatal(err)
	}
	if handoffs.readyCalls != 1 || handoffs.failedCalls != 0 {
		t.Fatalf("ready=%d failed=%d", handoffs.readyCalls, handoffs.failedCalls)
	}

	failing := &handoffStore{}
	svc = NewService(ServiceDeps{Conversations: handoffConversations{}, Messages: &handoffMessages{}, Tasks: tasks, Compiler: &handoffCompiler{err: errors.New("compiler detail")}, ChatHandoffs: failing})
	if _, err := svc.Refine(context.Background(), actor, handoffTestTaskID, "retry"); err == nil {
		t.Fatal("compiler failure succeeded")
	}
	if failing.readyCalls != 0 || failing.failedCalls != 1 || failing.failedCode != handoffCompilerFailed || !failing.failedRetry {
		t.Fatalf("ready=%d failed=%d code=%q retryable=%t", failing.readyCalls, failing.failedCalls, failing.failedCode, failing.failedRetry)
	}
}

func TestHydrateChatArtifactsGroupsAndSorts(t *testing.T) {
	first, second := handoffRow(2, model.ScheduledHandoffStateReady), handoffRow(1, model.ScheduledHandoffStateReady)
	assistantID := int64(42)
	first.Handoff.AssistantMessageID, second.Handoff.AssistantMessageID = &assistantID, &assistantID
	svc := NewService(ServiceDeps{Conversations: handoffConversations{}, Messages: &handoffMessages{}, Tasks: &handoffTasks{}, Compiler: &handoffCompiler{}, ChatHandoffs: &handoffStore{list: []store.HydratedChatHandoff{first, second}}})
	artifacts, err := svc.HydrateChatArtifacts(context.Background(), 7, "chat-1", []int64{assistantID})
	if err != nil || len(artifacts[assistantID]) != 2 || artifacts[assistantID][0].Ordinal != 1 || artifacts[assistantID][1].Ordinal != 2 {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
}

func TestDiscardChatDraftDelegatesOnlyToHandoffStore(t *testing.T) {
	handoffs := &handoffStore{}
	svc := NewService(ServiceDeps{Conversations: handoffConversations{}, Messages: &handoffMessages{}, Tasks: &handoffTasks{}, Compiler: &handoffCompiler{}, ChatHandoffs: handoffs})
	if err := svc.DiscardChatDraft(context.Background(), 7, handoffTestTaskID); err != nil {
		t.Fatal(err)
	}
	if err := svc.CleanupChatDrafts(context.Background(), 7, []string{"handoff-1"}); err != nil {
		t.Fatal(err)
	}
}
