package scheduled_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
)

const (
	serviceTaskID         = "task-1"
	serviceTimezoneBerlin = "Europe/Berlin"
	serviceTimezoneUTC    = "UTC"
)

type serviceConversations struct{ created model.Conversation }

func (f *serviceConversations) CreateWithKind(_ context.Context, userID int64, _ string, kind string) (model.Conversation, error) {
	f.created = model.Conversation{ID: "conv-1", UserID: userID, Kind: kind}
	return f.created, nil
}

type serviceMessages struct{ messages []model.Message }

func (f *serviceMessages) Add(_ context.Context, conversationID, role, content string) (model.Message, error) {
	m := model.Message{ConversationID: conversationID, Role: role, Content: content}
	f.messages = append(f.messages, m)
	return m, nil
}
func (f *serviceMessages) ListByConversation(context.Context, string) ([]model.Message, error) {
	return append([]model.Message(nil), f.messages...), nil
}

type serviceTasks struct{ task model.ScheduledTask }

func (f *serviceTasks) Create(_ context.Context, task model.ScheduledTask) (model.ScheduledTask, error) {
	task.ID = serviceTaskID
	f.task = task
	return task, nil
}
func (f *serviceTasks) GetByID(context.Context, string, int64) (model.ScheduledTask, error) {
	return f.task, nil
}
func (f *serviceTasks) ListByUser(context.Context, int64) ([]model.ScheduledTask, error) {
	return []model.ScheduledTask{f.task}, nil
}
func (f *serviceTasks) Update(_ context.Context, task model.ScheduledTask, _ int64) (model.ScheduledTask, error) {
	f.task = task
	return task, nil
}
func (f *serviceTasks) SoftDelete(context.Context, string, int64) error { return nil }
func (f *serviceTasks) CreateRun(_ context.Context, _ int64, run model.ScheduledTaskRun) (model.ScheduledTaskRun, error) {
	run.ID = 1
	return run, nil
}
func (f *serviceTasks) ListRuns(context.Context, string, int64) ([]model.ScheduledTaskRun, error) {
	return nil, nil
}
func (f *serviceTasks) MarkRead(context.Context, string, int64) error   { return nil }
func (f *serviceTasks) UnreadCount(context.Context, int64) (int, error) { return 0, nil }

type serviceCompiler struct {
	proposal scheduled.Proposal
	history  []provider.Message
}

func (f *serviceCompiler) Refine(_ context.Context, history []provider.Message, _ []provider.ToolDefinition, _ int) (scheduled.Refinement, error) {
	f.history = history
	return scheduled.Refinement{Text: "I will remind you.", Proposal: &f.proposal}, nil
}

func TestServiceCreatesDraftBeforeRefinementAndConfirmsPersistedProposal(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	conversations := &serviceConversations{}
	messages := &serviceMessages{}
	tasks := &serviceTasks{}
	compiler := &serviceCompiler{proposal: scheduled.Proposal{Version: 1, Name: "Water", TaskKind: scheduled.TaskKindReminder, CompiledPrompt: "Remind the user to drink water", ExecutionMode: scheduled.ExecutionModeStatic, Timezone: serviceTimezoneBerlin, Schedule: scheduled.Schedule{At: now.Add(time.Hour), Timezone: serviceTimezoneBerlin}, DeliveryPolicy: scheduled.DeliveryPolicyAlways, InitialRun: scheduled.InitialRunWait, StaticMessage: "Drink water."}}
	svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: conversations, Messages: messages, Tasks: tasks, Compiler: compiler, Now: func() time.Time { return now }})
	result, err := svc.Create(context.Background(), scheduled.Actor{ID: 7, Username: "alice", Timezone: serviceTimezoneBerlin}, "remind me")
	if err != nil {
		t.Fatal(err)
	}
	if conversations.created.Kind != model.ConversationKindScheduled || len(messages.messages) != 2 || compiler.history[0].Content != "remind me" {
		t.Fatalf("definition history was not persisted before refine: conv=%+v messages=%+v history=%+v", conversations.created, messages.messages, compiler.history)
	}
	if result.Task.State != model.ScheduledTaskStateDraft || result.Task.StaticMessage != "Drink water." || result.Task.DeliveryPolicy != string(scheduled.DeliveryPolicyAlways) || result.Task.InitialRun != string(scheduled.InitialRunWait) {
		t.Fatalf("proposal did not round trip into draft: %+v", result.Task)
	}
	confirmed, err := svc.Confirm(context.Background(), scheduled.Actor{ID: 7}, result.Task.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.State != model.ScheduledTaskStateActive || confirmed.NextRunAt == nil || !confirmed.NextRunAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("confirmation did not schedule proposal: %+v", confirmed)
	}
}

func TestServiceLifecycleControlsAreOwnerScopedAtStoreBoundary(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	tasks := &serviceTasks{task: model.ScheduledTask{
		ID: serviceTaskID, UserID: 7, ConversationID: "conv-1", Version: 1, Name: "Water",
		Kind: string(scheduled.TaskKindReminder), State: model.ScheduledTaskStateActive, CompiledPrompt: "prompt",
		Timezone: serviceTimezoneUTC, OneOffAt: new(now.Add(time.Hour)), ExecutionMode: string(scheduled.ExecutionModeStatic),
		DeliveryPolicy: string(scheduled.DeliveryPolicyAlways), InitialRun: string(scheduled.InitialRunWait),
	}}
	svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: &serviceMessages{}, Tasks: tasks, Compiler: &serviceCompiler{}, Now: func() time.Time { return now }})
	listed, err := svc.List(context.Background(), 7)
	if err != nil || len(listed.Tasks) != 1 {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	detail, err := svc.Detail(context.Background(), 7, serviceTaskID)
	if err != nil || detail.Task.ID != serviceTaskID {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	paused, err := svc.Pause(context.Background(), 7, serviceTaskID)
	if err != nil || paused.State != model.ScheduledTaskStatePaused {
		t.Fatalf("pause = %+v, %v", paused, err)
	}
	resumed, err := svc.Resume(context.Background(), 7, serviceTaskID)
	if err != nil || resumed.State != model.ScheduledTaskStateActive || resumed.NextRunAt == nil {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	run, err := svc.RunNow(context.Background(), 7, serviceTaskID)
	if err != nil || run.State != model.ScheduledTaskRunStatePending || !strings.HasPrefix(run.OccurrenceKey, "manual:") {
		t.Fatalf("run now = %+v, %v", run, err)
	}
	if err := svc.MarkRead(context.Background(), 7, serviceTaskID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), 7, serviceTaskID); err != nil {
		t.Fatal(err)
	}
}
