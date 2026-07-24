package scheduled_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/store"
)

const (
	serviceTaskID         = "task-1"
	serviceConversationID = "conv-1"
	serviceCompiledPrompt = "prompt"
	serviceTimezoneBerlin = "Europe/Berlin"
	serviceTimezoneUTC    = "UTC"
)

type serviceConversations struct {
	created model.Conversation
	err     error
	title   string
}

func (f *serviceConversations) CreateWithKind(_ context.Context, userID int64, title string, kind string) (model.Conversation, error) {
	if f.err != nil {
		return model.Conversation{}, f.err
	}
	f.title = title
	f.created = model.Conversation{ID: serviceConversationID, UserID: userID, Kind: kind}
	return f.created, nil
}

type serviceMessages struct {
	messages  []model.Message
	addErrAt  map[int]error
	addCalls  int
	listError error
}

func (f *serviceMessages) Add(_ context.Context, conversationID, role, content string) (model.Message, error) {
	f.addCalls++
	if err := f.addErrAt[f.addCalls]; err != nil {
		return model.Message{}, err
	}
	m := model.Message{ConversationID: conversationID, Role: role, Content: content}
	f.messages = append(f.messages, m)
	return m, nil
}
func (f *serviceMessages) ListByConversation(context.Context, string) ([]model.Message, error) {
	if f.listError != nil {
		return nil, f.listError
	}
	return append([]model.Message(nil), f.messages...), nil
}

type serviceTasks struct {
	task         model.ScheduledTask
	createError  error
	getError     error
	listError    error
	updateError  error
	deleteError  error
	createRunErr error
	listRunsErr  error
	readError    error
	unreadError  error
}

func (f *serviceTasks) Create(_ context.Context, task model.ScheduledTask) (model.ScheduledTask, error) {
	if f.createError != nil {
		return model.ScheduledTask{}, f.createError
	}
	task.ID = serviceTaskID
	f.task = task
	return task, nil
}
func (f *serviceTasks) GetByID(context.Context, string, int64) (model.ScheduledTask, error) {
	if f.getError != nil {
		return model.ScheduledTask{}, f.getError
	}
	return f.task, nil
}
func (f *serviceTasks) ListByUser(context.Context, int64) ([]model.ScheduledTask, error) {
	if f.listError != nil {
		return nil, f.listError
	}
	return []model.ScheduledTask{f.task}, nil
}
func (f *serviceTasks) Update(_ context.Context, task model.ScheduledTask, _ int64) (model.ScheduledTask, error) {
	if f.updateError != nil {
		return model.ScheduledTask{}, f.updateError
	}
	f.task = task
	return task, nil
}
func (f *serviceTasks) SoftDelete(context.Context, string, int64) error { return f.deleteError }
func (f *serviceTasks) CreateRun(_ context.Context, _ int64, run model.ScheduledTaskRun) (model.ScheduledTaskRun, error) {
	if f.createRunErr != nil {
		return model.ScheduledTaskRun{}, f.createRunErr
	}
	run.ID = 1
	return run, nil
}
func (f *serviceTasks) ListRuns(context.Context, string, int64) ([]model.ScheduledTaskRun, error) {
	if f.listRunsErr != nil {
		return nil, f.listRunsErr
	}
	return nil, nil
}
func (f *serviceTasks) MarkRead(context.Context, string, int64) error { return f.readError }
func (f *serviceTasks) UnreadCount(context.Context, int64) (int, error) {
	if f.unreadError != nil {
		return 0, f.unreadError
	}
	return 0, nil
}

type serviceCompiler struct {
	proposal    scheduled.Proposal
	history     []provider.Message
	err         error
	refinement  *scheduled.Refinement
	nextVersion int
}

func (f *serviceCompiler) Refine(_ context.Context, history []provider.Message, _ []provider.ToolDefinition, nextVersion int) (scheduled.Refinement, error) {
	if f.err != nil {
		return scheduled.Refinement{}, f.err
	}
	f.history = history
	f.nextVersion = nextVersion
	if f.refinement != nil {
		return *f.refinement, nil
	}
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
		ID: serviceTaskID, UserID: 7, ConversationID: serviceConversationID, Version: 1, Name: "Water",
		Kind: string(scheduled.TaskKindReminder), State: model.ScheduledTaskStateActive, CompiledPrompt: serviceCompiledPrompt,
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

func TestServiceDefinitionFailurePaths(t *testing.T) {
	ctx := context.Background()
	actor := scheduled.Actor{ID: 7, Username: "alice"}
	failure := errors.New("store unavailable")

	t.Run("create rejects blank and missing dependencies", func(t *testing.T) {
		svc := scheduled.NewService(scheduled.ServiceDeps{})
		if _, err := svc.Create(ctx, actor, " "); err == nil {
			t.Fatal("blank create succeeded")
		}
		if _, err := svc.Create(ctx, actor, "idea"); err == nil {
			t.Fatal("missing dependencies succeeded")
		}
	})
	t.Run("create wraps conversation and draft failures", func(t *testing.T) {
		compiler := &serviceCompiler{}
		if _, err := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{err: failure}, Messages: &serviceMessages{}, Tasks: &serviceTasks{}, Compiler: compiler}).Create(ctx, actor, "idea"); !errors.Is(err, failure) {
			t.Fatalf("conversation err=%v", err)
		}
		if _, err := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: &serviceMessages{}, Tasks: &serviceTasks{createError: failure}, Compiler: compiler}).Create(ctx, actor, "idea"); !errors.Is(err, failure) {
			t.Fatalf("draft err=%v", err)
		}
	})
	t.Run("refine guards draft ownership and message", func(t *testing.T) {
		tasks := &serviceTasks{getError: failure}
		svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: &serviceMessages{}, Tasks: tasks, Compiler: &serviceCompiler{}})
		if _, err := svc.Refine(ctx, actor, serviceTaskID, ""); err == nil {
			t.Fatal("blank refine succeeded")
		}
		if _, err := svc.Refine(ctx, actor, serviceTaskID, "answer"); !errors.Is(err, failure) {
			t.Fatalf("get err=%v", err)
		}
		tasks.getError = nil
		tasks.task = model.ScheduledTask{ID: serviceTaskID, State: model.ScheduledTaskStateActive}
		if _, err := svc.Refine(ctx, actor, serviceTaskID, "answer"); !errors.Is(err, scheduled.ErrInvalidTransition) {
			t.Fatalf("state err=%v", err)
		}
	})
	t.Run("refine rejects missing dependencies", func(t *testing.T) {
		if _, err := scheduled.NewService(scheduled.ServiceDeps{}).Refine(ctx, actor, serviceTaskID, "answer"); err == nil {
			t.Fatal("missing dependencies succeeded")
		}
	})
	for _, tc := range []struct {
		name     string
		messages *serviceMessages
		tasks    *serviceTasks
		compiler *serviceCompiler
		tools    scheduled.ToolResolver
	}{
		{name: "user message", messages: &serviceMessages{addErrAt: map[int]error{1: failure}}, tasks: &serviceTasks{}, compiler: &serviceCompiler{}},
		{name: "history", messages: &serviceMessages{listError: failure}, tasks: &serviceTasks{}, compiler: &serviceCompiler{}},
		{name: "tools", messages: &serviceMessages{}, tasks: &serviceTasks{}, compiler: &serviceCompiler{}, tools: func(context.Context, string) ([]provider.ToolDefinition, error) { return nil, failure }},
		{name: "compiler", messages: &serviceMessages{}, tasks: &serviceTasks{}, compiler: &serviceCompiler{err: failure}},
		{name: "assistant message", messages: &serviceMessages{addErrAt: map[int]error{2: failure}}, tasks: &serviceTasks{}, compiler: &serviceCompiler{}},
		{name: "proposal update", messages: &serviceMessages{}, tasks: &serviceTasks{updateError: failure}, compiler: &serviceCompiler{}},
	} {
		t.Run("refine wraps "+tc.name+" failure", func(t *testing.T) {
			task := model.ScheduledTask{ID: serviceTaskID, ConversationID: serviceConversationID, Version: 1, State: model.ScheduledTaskStateDraft}
			tc.tasks.task = task
			svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: tc.messages, Tasks: tc.tasks, Compiler: tc.compiler, ToolsForUser: tc.tools})
			if _, err := svc.Refine(ctx, actor, serviceTaskID, "answer"); !errors.Is(err, failure) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	t.Run("refine keeps question draft and bumps existing proposal version", func(t *testing.T) {
		question := scheduled.Refinement{Text: "Which day?", Question: &scheduled.QuestionCard{ID: "day"}}
		compiler := &serviceCompiler{refinement: &question}
		tasks := &serviceTasks{task: model.ScheduledTask{ID: serviceTaskID, ConversationID: serviceConversationID, Version: 3, CompiledPrompt: "existing", State: model.ScheduledTaskStateDraft}}
		messages := &serviceMessages{messages: []model.Message{{Role: model.MsgRoleSystem, Content: "hidden"}}}
		toolsCalled := false
		svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: messages, Tasks: tasks, Compiler: compiler, ToolsForUser: func(context.Context, string) ([]provider.ToolDefinition, error) {
			toolsCalled = true
			return []provider.ToolDefinition{{Name: "weather"}}, nil
		}})
		result, err := svc.Refine(ctx, actor, serviceTaskID, "Tuesday")
		if err != nil || result.Task.CompiledPrompt != "existing" || compiler.nextVersion != 4 || !toolsCalled {
			t.Fatalf("result=%+v err=%v next=%d tools=%t", result, err, compiler.nextVersion, toolsCalled)
		}
	})
	t.Run("create bounds the scheduled conversation title", func(t *testing.T) {
		conversations := &serviceConversations{}
		svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: conversations, Messages: &serviceMessages{}, Tasks: &serviceTasks{}, Compiler: &serviceCompiler{}})
		if _, err := svc.Create(ctx, actor, strings.Repeat("a", 61)); err != nil {
			t.Fatal(err)
		}
		if len([]rune(conversations.title)) != 60 {
			t.Fatalf("title length=%d", len([]rune(conversations.title)))
		}
	})
}

//nolint:gocyclo // This characterization test deliberately enumerates every lifecycle branch.
func TestServiceLifecycleFailurePaths(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	failure := errors.New("store unavailable")
	base := func() model.ScheduledTask {
		return model.ScheduledTask{ID: serviceTaskID, ConversationID: serviceConversationID, Version: 1, State: model.ScheduledTaskStateDraft, CompiledPrompt: serviceCompiledPrompt, Timezone: serviceTimezoneUTC, OneOffAt: new(now.Add(time.Hour)), InitialRun: string(scheduled.InitialRunWait)}
	}
	newService := func(tasks *serviceTasks) *scheduled.Service {
		return scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: &serviceMessages{}, Tasks: tasks, Compiler: &serviceCompiler{}, Now: func() time.Time { return now }})
	}

	t.Run("confirm rejects get stale schedule and update failures", func(t *testing.T) {
		tasks := &serviceTasks{getError: failure}
		if _, err := newService(tasks).Confirm(ctx, scheduled.Actor{ID: 7}, serviceTaskID, 1); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.getError = nil
		tasks.task = base()
		tasks.task.State = model.ScheduledTaskStateActive
		if _, err := newService(tasks).Confirm(ctx, scheduled.Actor{}, serviceTaskID, 1); !errors.Is(err, scheduled.ErrStaleProposal) {
			t.Fatal(err)
		}
		tasks.task = base()
		tasks.task.OneOffAt = nil
		if _, err := newService(tasks).Confirm(ctx, scheduled.Actor{}, serviceTaskID, 1); err == nil {
			t.Fatal("invalid schedule confirmed")
		}
		tasks.task = base()
		tasks.updateError = failure
		if _, err := newService(tasks).Confirm(ctx, scheduled.Actor{}, serviceTaskID, 1); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.task = base()
		tasks.updateError = store.ErrActiveTaskLimit
		if _, err := newService(tasks).Confirm(ctx, scheduled.Actor{}, serviceTaskID, 1); !errors.Is(err, store.ErrActiveTaskLimit) {
			t.Fatal(err)
		}
		tasks.updateError = nil
		tasks.task = base()
		tasks.task.OneOffAt = nil
		tasks.task.InitialRun = string(scheduled.InitialRunPreview)
		confirmed, err := newService(tasks).Confirm(ctx, scheduled.Actor{}, serviceTaskID, 1)
		if err != nil || confirmed.NextRunAt == nil || !confirmed.NextRunAt.Equal(now) {
			t.Fatalf("preview=%+v err=%v", confirmed, err)
		}
	})
	t.Run("list and detail return repository failures", func(t *testing.T) {
		tasks := &serviceTasks{listError: failure}
		if _, err := newService(tasks).List(ctx, 7); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.listError = nil
		tasks.unreadError = failure
		if _, err := newService(tasks).List(ctx, 7); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.unreadError = nil
		tasks.getError = failure
		if _, err := newService(tasks).Detail(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.getError = nil
		tasks.task = base()
		tasks.listRunsErr = failure
		if _, err := newService(tasks).Detail(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	})
	t.Run("pause and resume enforce state schedule and update", func(t *testing.T) {
		tasks := &serviceTasks{getError: failure}
		if _, err := newService(tasks).Pause(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.getError = nil
		tasks.task = base()
		if _, err := newService(tasks).Pause(ctx, 7, serviceTaskID); !errors.Is(err, scheduled.ErrInvalidTransition) {
			t.Fatal(err)
		}
		tasks.task.State = model.ScheduledTaskStateActive
		tasks.updateError = failure
		if _, err := newService(tasks).Pause(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.getError = failure
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.getError = nil
		tasks.updateError = nil
		tasks.task = base()
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); !errors.Is(err, scheduled.ErrInvalidTransition) {
			t.Fatal(err)
		}
		tasks.task.State = model.ScheduledTaskStatePaused
		tasks.task.CompiledPrompt = ""
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); !errors.Is(err, scheduled.ErrInvalidTransition) {
			t.Fatal(err)
		}
		tasks.task.CompiledPrompt = serviceCompiledPrompt
		tasks.task.OneOffAt = nil
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); err == nil {
			t.Fatal("invalid resume schedule succeeded")
		}
		tasks.task = base()
		tasks.task.State = model.ScheduledTaskStatePaused
		tasks.updateError = failure
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.updateError = nil
		tasks.task = base()
		tasks.task.State = model.ScheduledTaskStatePaused
		tasks.task.OneOffAt = nil
		tasks.task.DTStart = new(now)
		tasks.task.RRULE = "FREQ=DAILY"
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("run now and simple operations propagate failures", func(t *testing.T) {
		tasks := &serviceTasks{getError: failure}
		if _, err := newService(tasks).RunNow(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.getError = nil
		tasks.task = base()
		tasks.task.State = model.ScheduledTaskStateDraft
		if _, err := newService(tasks).RunNow(ctx, 7, serviceTaskID); !errors.Is(err, scheduled.ErrInvalidTransition) {
			t.Fatal(err)
		}
		tasks.task.State = model.ScheduledTaskStateActive
		tasks.task.CompiledPrompt = ""
		if _, err := newService(tasks).RunNow(ctx, 7, serviceTaskID); !errors.Is(err, scheduled.ErrInvalidTransition) {
			t.Fatal(err)
		}
		tasks.task.CompiledPrompt = serviceCompiledPrompt
		tasks.createRunErr = failure
		if _, err := newService(tasks).RunNow(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.createRunErr = nil
		tasks.updateError = failure
		if _, err := newService(tasks).RunNow(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.updateError = nil
		tasks.task = base()
		tasks.task.State = model.ScheduledTaskStatePaused
		run, err := newService(tasks).RunNow(ctx, 7, serviceTaskID)
		if err != nil || run.ID == 0 || tasks.task.State != model.ScheduledTaskStateActive {
			t.Fatalf("paused run=%+v task=%+v err=%v", run, tasks.task, err)
		}
		tasks.deleteError = failure
		if err := newService(tasks).Delete(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.readError = failure
		if err := newService(tasks).MarkRead(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	})
}
