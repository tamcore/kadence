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
	task           model.ScheduledTask
	createError    error
	getError       error
	listError      error
	pauseError     error
	resumeError    error
	deleteError    error
	createRunErr   error
	listRunsErr    error
	readError      error
	unreadError    error
	beginError     error
	saveError      error
	confirmError   error
	runNowError    error
	beginCalls     int
	saveVersion    int
	confirmVersion int
	pauseVersion   int
	resumeVersion  int
	resumeNext     time.Time
	ownerIDs       []int64
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
func (f *serviceTasks) BeginDraftRevision(_ context.Context, _ string, userID int64) (model.ScheduledTask, error) {
	f.ownerIDs = append(f.ownerIDs, userID)
	f.beginCalls++
	if f.beginError != nil {
		return model.ScheduledTask{}, f.beginError
	}
	if f.task.State != model.ScheduledTaskStateDraft && f.task.State != model.ScheduledTaskStateActive && f.task.State != model.ScheduledTaskStatePaused {
		return model.ScheduledTask{}, store.ErrInvalidScheduledTaskState
	}
	f.task.Version++
	f.task.State = model.ScheduledTaskStateDraft
	f.task.CompiledPrompt = ""
	f.task.NextRunAt = nil
	return f.task, nil
}
func (f *serviceTasks) Pause(_ context.Context, _ string, userID int64, expectedVersion int) (model.ScheduledTask, error) {
	f.ownerIDs = append(f.ownerIDs, userID)
	f.pauseVersion = expectedVersion
	if f.pauseError != nil {
		return model.ScheduledTask{}, f.pauseError
	}
	f.task.State = model.ScheduledTaskStatePaused
	f.task.NextRunAt = nil
	return f.task, nil
}
func (f *serviceTasks) Resume(_ context.Context, _ string, userID int64, expectedVersion int, next time.Time) (model.ScheduledTask, error) {
	f.ownerIDs = append(f.ownerIDs, userID)
	f.resumeVersion = expectedVersion
	f.resumeNext = next
	if f.resumeError != nil {
		return model.ScheduledTask{}, f.resumeError
	}
	f.task.State = model.ScheduledTaskStateActive
	f.task.NextRunAt = &next
	return f.task, nil
}
func (f *serviceTasks) SaveProposal(_ context.Context, task model.ScheduledTask, userID int64, expectedVersion int) (model.ScheduledTask, error) {
	f.ownerIDs = append(f.ownerIDs, userID)
	f.saveVersion = expectedVersion
	if f.saveError != nil {
		return model.ScheduledTask{}, f.saveError
	}
	if f.task.State != model.ScheduledTaskStateDraft || f.task.Version != expectedVersion {
		return model.ScheduledTask{}, scheduled.ErrStaleProposal
	}
	f.task = task
	return task, nil
}
func (f *serviceTasks) ConfirmProposal(_ context.Context, _ string, userID int64, expectedVersion int, next time.Time) (model.ScheduledTask, error) {
	f.ownerIDs = append(f.ownerIDs, userID)
	f.confirmVersion = expectedVersion
	if f.confirmError != nil {
		return model.ScheduledTask{}, f.confirmError
	}
	if f.task.State != model.ScheduledTaskStateDraft || f.task.Version != expectedVersion || f.task.CompiledPrompt == "" {
		return model.ScheduledTask{}, scheduled.ErrStaleProposal
	}
	f.task.State = model.ScheduledTaskStateActive
	f.task.NextRunAt = &next
	return f.task, nil
}
func (f *serviceTasks) SoftDelete(context.Context, string, int64) error { return f.deleteError }
func (f *serviceTasks) CreateRun(_ context.Context, _ int64, run model.ScheduledTaskRun) (model.ScheduledTaskRun, error) {
	if f.createRunErr != nil {
		return model.ScheduledTaskRun{}, f.createRunErr
	}
	run.ID = 1
	return run, nil
}
func (f *serviceTasks) RunNow(_ context.Context, userID int64, _ string, occurrenceKey string, now time.Time) (model.ScheduledTaskRun, error) {
	f.ownerIDs = append(f.ownerIDs, userID)
	if f.runNowError != nil {
		return model.ScheduledTaskRun{}, f.runNowError
	}
	if (f.task.State != model.ScheduledTaskStateActive && f.task.State != model.ScheduledTaskStatePaused) || f.task.CompiledPrompt == "" {
		return model.ScheduledTaskRun{}, store.ErrInvalidScheduledTaskState
	}
	f.task.State = model.ScheduledTaskStateActive
	f.task.NextRunAt = &now
	return model.ScheduledTaskRun{ID: 1, TaskID: f.task.ID, OccurrenceKey: occurrenceKey, ScheduledFor: now, State: model.ScheduledTaskRunStatePending}, nil
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
	proposal := f.proposal
	proposal.Version = nextVersion
	return scheduled.Refinement{Text: "I will remind you.", Proposal: &proposal}, nil
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
	if !strings.Contains(messages.messages[1].Content, `"compiledPrompt":"Remind the user to drink water"`) {
		t.Fatalf("proposal audit missing from conversation: %q", messages.messages[1].Content)
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
		tasks := &serviceTasks{beginError: failure}
		svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: &serviceMessages{}, Tasks: tasks, Compiler: &serviceCompiler{}})
		if _, err := svc.Refine(ctx, actor, serviceTaskID, ""); err == nil {
			t.Fatal("blank refine succeeded")
		}
		if _, err := svc.Refine(ctx, actor, serviceTaskID, "answer"); !errors.Is(err, failure) {
			t.Fatalf("begin err=%v", err)
		}
		tasks.beginError = store.ErrScheduledRunInProgress
		if _, err := svc.Refine(ctx, actor, serviceTaskID, "answer"); !errors.Is(err, scheduled.ErrRunInProgress) {
			t.Fatalf("run conflict err=%v", err)
		}
		tasks.beginError = nil
		tasks.task = model.ScheduledTask{ID: serviceTaskID, State: model.ScheduledTaskStateCompleted}
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
		{name: "proposal update", messages: &serviceMessages{}, tasks: &serviceTasks{saveError: failure}, compiler: &serviceCompiler{}},
	} {
		t.Run("refine wraps "+tc.name+" failure", func(t *testing.T) {
			task := model.ScheduledTask{ID: serviceTaskID, ConversationID: serviceConversationID, Version: 0, State: model.ScheduledTaskStateDraft}
			tc.tasks.task = task
			svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: tc.messages, Tasks: tc.tasks, Compiler: tc.compiler, ToolsForUser: tc.tools})
			if _, err := svc.Refine(ctx, actor, serviceTaskID, "answer"); !errors.Is(err, failure) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	t.Run("refine invalidates old proposal before a clarification question", func(t *testing.T) {
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
		if err != nil || result.Task.CompiledPrompt != "" || result.Task.Version != 4 || compiler.nextVersion != 4 || tasks.beginCalls != 1 || !toolsCalled {
			t.Fatalf("result=%+v err=%v next=%d tools=%t", result, err, compiler.nextVersion, toolsCalled)
		}
	})
	t.Run("active and paused tasks begin a new draft revision", func(t *testing.T) {
		for _, state := range []string{model.ScheduledTaskStateActive, model.ScheduledTaskStatePaused} {
			t.Run(state, func(t *testing.T) {
				question := scheduled.Refinement{Text: "What should change?", Question: &scheduled.QuestionCard{ID: "change"}}
				tasks := &serviceTasks{task: model.ScheduledTask{ID: serviceTaskID, ConversationID: serviceConversationID, Version: 7, State: state, CompiledPrompt: "confirmed", NextRunAt: new(time.Now())}}
				svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: &serviceMessages{}, Tasks: tasks, Compiler: &serviceCompiler{refinement: &question}})
				result, err := svc.Refine(ctx, actor, serviceTaskID, "change it")
				if err != nil || result.Task.State != model.ScheduledTaskStateDraft || result.Task.Version != 8 || result.Task.CompiledPrompt != "" || result.Task.NextRunAt != nil {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			})
		}
	})
	t.Run("proposal save is compare and swap", func(t *testing.T) {
		tasks := &serviceTasks{task: model.ScheduledTask{ID: serviceTaskID, ConversationID: serviceConversationID, State: model.ScheduledTaskStateDraft}}
		compiler := &serviceCompiler{proposal: scheduled.Proposal{Name: "new", CompiledPrompt: "new prompt"}}
		svc := scheduled.NewService(scheduled.ServiceDeps{Conversations: &serviceConversations{}, Messages: &serviceMessages{}, Tasks: tasks, Compiler: compiler})
		tasks.saveError = store.ErrStaleScheduledProposal
		if _, err := svc.Refine(ctx, actor, serviceTaskID, "new definition"); !errors.Is(err, scheduled.ErrStaleProposal) {
			t.Fatalf("err=%v", err)
		}
		if tasks.saveVersion != 1 || tasks.task.CompiledPrompt != "" {
			t.Fatalf("version=%d task=%+v", tasks.saveVersion, tasks.task)
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

func TestServiceAllPublicMethodsRejectMissingDependencies(t *testing.T) {
	ctx := context.Background()
	svc := scheduled.NewService(scheduled.ServiceDeps{})
	actor := scheduled.Actor{ID: 7}
	checks := []struct {
		name string
		call func() error
	}{
		{name: "confirm", call: func() error { _, err := svc.Confirm(ctx, actor, serviceTaskID, 1); return err }},
		{name: "list", call: func() error { _, err := svc.List(ctx, actor.ID); return err }},
		{name: "detail", call: func() error { _, err := svc.Detail(ctx, actor.ID, serviceTaskID); return err }},
		{name: "pause", call: func() error { _, err := svc.Pause(ctx, actor.ID, serviceTaskID); return err }},
		{name: "resume", call: func() error { _, err := svc.Resume(ctx, actor.ID, serviceTaskID); return err }},
		{name: "delete", call: func() error { return svc.Delete(ctx, actor.ID, serviceTaskID) }},
		{name: "run", call: func() error { _, err := svc.RunNow(ctx, actor.ID, serviceTaskID); return err }},
		{name: "read", call: func() error { return svc.MarkRead(ctx, actor.ID, serviceTaskID) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); err == nil || !strings.Contains(err.Error(), "dependencies") {
				t.Fatalf("err=%v", err)
			}
		})
	}
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
		tasks.confirmError = failure
		if _, err := newService(tasks).Confirm(ctx, scheduled.Actor{}, serviceTaskID, 1); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.confirmError = store.ErrStaleScheduledProposal
		if _, err := newService(tasks).Confirm(ctx, scheduled.Actor{}, serviceTaskID, 1); !errors.Is(err, scheduled.ErrStaleProposal) {
			t.Fatal(err)
		}
		tasks.task = base()
		tasks.confirmError = store.ErrActiveTaskLimit
		if _, err := newService(tasks).Confirm(ctx, scheduled.Actor{}, serviceTaskID, 1); !errors.Is(err, store.ErrActiveTaskLimit) {
			t.Fatal(err)
		}
		tasks.confirmError = nil
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
		tasks.pauseError = failure
		if _, err := newService(tasks).Pause(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.pauseError = store.ErrInvalidScheduledTaskState
		if _, err := newService(tasks).Pause(ctx, 7, serviceTaskID); !errors.Is(err, scheduled.ErrInvalidTransition) {
			t.Fatalf("stale pause err=%v", err)
		}
		tasks.getError = failure
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.getError = nil
		tasks.pauseError = nil
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
		tasks.resumeError = failure
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.resumeError = store.ErrInvalidScheduledTaskState
		if _, err := newService(tasks).Resume(ctx, 7, serviceTaskID); !errors.Is(err, scheduled.ErrInvalidTransition) {
			t.Fatalf("stale resume err=%v", err)
		}
		tasks.resumeError = nil
		tasks.task = base()
		tasks.task.State = model.ScheduledTaskStatePaused
		tasks.task.OneOffAt = nil
		tasks.task.DTStart = new(now)
		tasks.task.RRULE = "FREQ=DAILY"
		original := tasks.task
		resumed, err := newService(tasks).Resume(ctx, 7, serviceTaskID)
		if err != nil {
			t.Fatal(err)
		}
		if tasks.resumeVersion != original.Version || !tasks.resumeNext.Equal(now.Add(24*time.Hour)) {
			t.Fatalf("resume CAS version=%d next=%v", tasks.resumeVersion, tasks.resumeNext)
		}
		if resumed.Version != original.Version || resumed.CompiledPrompt != original.CompiledPrompt ||
			resumed.Name != original.Name || resumed.RRULE != original.RRULE {
			t.Fatalf("resume overwrote definition: before=%+v after=%+v", original, resumed)
		}
		tasks.task = base()
		tasks.task.State = model.ScheduledTaskStateActive
		original = tasks.task
		paused, err := newService(tasks).Pause(ctx, 7, serviceTaskID)
		if err != nil {
			t.Fatal(err)
		}
		if tasks.pauseVersion != original.Version || paused.Version != original.Version ||
			paused.CompiledPrompt != original.CompiledPrompt || paused.Name != original.Name {
			t.Fatalf("pause overwrote definition: before=%+v after=%+v", original, paused)
		}
	})
	t.Run("run now and simple operations propagate failures", func(t *testing.T) {
		tasks := &serviceTasks{runNowError: failure}
		if _, err := newService(tasks).RunNow(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.runNowError = nil
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
		tasks.runNowError = failure
		if _, err := newService(tasks).RunNow(ctx, 7, serviceTaskID); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		tasks.runNowError = nil
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
