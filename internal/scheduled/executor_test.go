package scheduled

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

const (
	executorTestUsername    = "alice"
	executorInvalidTimezone = "bad"
	executorDataTool        = "data__read"
)

type executorProvider struct {
	toolReplies []provider.StreamResult
	reply       string
	err         error
	toolCalls   int
	chatCalls   int
	requests    []provider.ChatRequest
	block       bool
	skipToken   bool
	ignoreToken bool
}

func (p *executorProvider) StreamChat(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	p.chatCalls++
	p.requests = append(p.requests, req)
	if p.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if p.err != nil {
		return "", p.err
	}
	if !p.skipToken {
		if err := onToken(p.reply); err != nil && !p.ignoreToken {
			return "", err
		}
	}
	return p.reply, nil
}

func (p *executorProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	p.toolCalls++
	p.requests = append(p.requests, req)
	if p.block {
		<-ctx.Done()
		return provider.StreamResult{}, ctx.Err()
	}
	if p.err != nil {
		return provider.StreamResult{}, p.err
	}
	reply := p.toolReplies[p.toolCalls-1]
	if !p.skipToken {
		if err := onToken(reply.Content); err != nil && !p.ignoreToken {
			return provider.StreamResult{}, err
		}
	}
	return reply, nil
}

type executorCatalog struct {
	byUser map[string]*executorSnapshot
	err    error
	users  []string
}

func (c *executorCatalog) SnapshotFor(_ context.Context, username string) (ExecutionToolSnapshot, error) {
	c.users = append(c.users, username)
	if c.err != nil {
		return nil, c.err
	}
	snapshot := c.byUser[username]
	if snapshot == nil {
		return nil, nil
	}
	return snapshot, nil
}

type executorSnapshot struct {
	tools   []provider.ToolDefinition
	results map[string]string
	calls   []string
	listErr error
	callErr error
}

func (s *executorSnapshot) ToolsFor(context.Context) ([]provider.ToolDefinition, error) {
	return append([]provider.ToolDefinition(nil), s.tools...), s.listErr
}

func (s *executorSnapshot) Call(_ context.Context, name, args string) (string, error) {
	s.calls = append(s.calls, name+":"+args)
	if s.callErr != nil {
		return "", s.callErr
	}
	return s.results[name], nil
}

type executorStore struct {
	successes []ExecutionSuccess
	failures  []ExecutionFailure
	err       error
}

func (s *executorStore) FinishSuccess(_ context.Context, success ExecutionSuccess) error {
	s.successes = append(s.successes, success)
	return s.err
}

func (s *executorStore) FinishFailure(_ context.Context, failure ExecutionFailure) error {
	s.failures = append(s.failures, failure)
	return s.err
}

func claimedTask(kind string, now time.Time) model.ClaimedScheduledTask {
	next := now.Add(-time.Minute)
	started := now.Add(-time.Second)
	return model.ClaimedScheduledTask{
		FirstRun: true,
		Username: executorTestUsername,
		Task: model.ScheduledTask{
			ID: "task", UserID: 7, ConversationID: "conversation", Kind: kind,
			State: model.ScheduledTaskStateActive, CompiledPrompt: "check training data",
			Timezone: defaultTimezoneUTC, AuthorizedTools: []string{executorDataTool}, NextRunAt: nil,
			LastRunAt: &next,
		},
		Run: model.ScheduledTaskRun{
			ID: 11, TaskID: "task", State: model.ScheduledTaskRunStateRunning,
			ScheduledFor: next, StartedAt: &started,
		},
	}
}

func executorFor(worker, synthesis provider.Provider, catalog ExecutionToolCatalog, store ExecutionStore, now time.Time) *Executor {
	return NewExecutor(ExecutorDeps{
		Worker: worker, Synthesis: synthesis, Tools: catalog, Store: store,
		Config: ExecutorConfig{
			WorkerModel: "worker", WorkerMaxTokens: 1000, WorkerMaxIterations: 3, WorkerTimeout: time.Second,
			SynthesisModel: "primary", SynthesisMaxTokens: 1000, SynthesisTimeout: time.Second,
		},
		Now: func() time.Time { return now },
	})
}

func TestExecutorStaticReminderUsesZeroInferenceAndAdvances(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	worker, synthesis := &executorProvider{}, &executorProvider{}
	store := &executorStore{}
	claimed := claimedTask(model.ScheduledTaskKindReminder, now)
	claimed.Task.ExecutionMode = string(ExecutionModeStatic)
	claimed.Task.StaticMessage = "Drink water."
	claimed.Task.OneOffAt = new(claimed.Run.ScheduledFor)

	if err := executorFor(worker, synthesis, nil, store, now).Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimed); err != nil {
		t.Fatal(err)
	}
	if worker.toolCalls != 0 || worker.chatCalls != 0 || synthesis.toolCalls != 0 || synthesis.chatCalls != 0 {
		t.Fatalf("providers called: worker=%d/%d synthesis=%d/%d", worker.toolCalls, worker.chatCalls, synthesis.toolCalls, synthesis.chatCalls)
	}
	if len(store.successes) != 1 {
		t.Fatalf("successes = %d", len(store.successes))
	}
	success := store.successes[0]
	if success.Content != "Drink water." || success.RunState != model.ScheduledTaskRunStateDelivered ||
		success.TaskState != model.ScheduledTaskStateCompleted || !success.Unread || success.NextRunAt != nil {
		t.Fatalf("success = %+v", success)
	}
}

func TestExecutorUsesExactFreshOwnerToolsAndRejectsMissingOrUnauthorized(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		tools      []provider.ToolDefinition
		reply      provider.StreamResult
		wantCode   string
		wantPaused bool
	}{
		{
			name:       "missing",
			tools:      []provider.ToolDefinition{{Name: "other"}},
			wantCode:   failureMissingTool,
			wantPaused: true,
		},
		{
			name:     "unauthorized call",
			tools:    []provider.ToolDefinition{{Name: executorDataTool}},
			reply:    provider.StreamResult{ToolCalls: []provider.ToolCall{{ID: "1", Name: "other", Arguments: `{}`}}},
			wantCode: failureUnauthorizedTool,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker := &executorProvider{toolReplies: []provider.StreamResult{test.reply}}
			store := &executorStore{}
			snapshot := &executorSnapshot{tools: test.tools, results: map[string]string{}}
			catalog := &executorCatalog{byUser: map[string]*executorSnapshot{executorTestUsername: snapshot}}
			err := executorFor(worker, &executorProvider{}, catalog, store, now).Execute(
				t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimedTask(model.ScheduledTaskKindData, now),
			)
			if err == nil || len(store.failures) != 1 {
				t.Fatalf("err=%v failures=%+v", err, store.failures)
			}
			if store.failures[0].Code != test.wantCode || store.failures[0].Pause != test.wantPaused {
				t.Fatalf("failure = %+v", store.failures[0])
			}
			if store.failures[0].IncrementFailures == (test.wantCode == failureMissingTool) {
				t.Fatalf("failure counting policy = %+v", store.failures[0])
			}
		})
	}
}

func TestExecutorGatherCallsOnlyAuthorizedToolsAndSynthesizesToolFree(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	worker := &executorProvider{toolReplies: []provider.StreamResult{
		{ToolCalls: []provider.ToolCall{{ID: "call", Name: executorDataTool, Arguments: `{"limit":1}`}}},
		{Content: `{"status":"deliver","summary":"New run","evidence":["5 km"],"monitoringState":{"cursor":1}}`},
	}}
	synthesis := &executorProvider{reply: "You ran 5 km."}
	store := &executorStore{}
	snapshot := &executorSnapshot{
		tools:   []provider.ToolDefinition{{Name: executorDataTool, Parameters: json.RawMessage(`{"type":"object"}`)}},
		results: map[string]string{executorDataTool: `{"distance":5000}`},
	}
	catalog := &executorCatalog{byUser: map[string]*executorSnapshot{executorTestUsername: snapshot}}
	claimed := claimedTask(model.ScheduledTaskKindData, now)
	claimed.FirstRun = false
	claimed.Task.DTStart = new(now.Add(-24 * time.Hour))
	claimed.Task.RRULE = "FREQ=DAILY"

	if err := executorFor(worker, synthesis, catalog, store, now).Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimed); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(catalog.users, []string{executorTestUsername}) || !slices.Equal(snapshot.calls, []string{`data__read:{"limit":1}`}) {
		t.Fatalf("owner dispatch users=%v calls=%v", catalog.users, snapshot.calls)
	}
	if synthesis.chatCalls != 1 || len(synthesis.requests[0].Tools) != 0 {
		t.Fatalf("synthesis request = %+v", synthesis.requests)
	}
	if len(store.successes) != 1 || store.successes[0].Content != "You ran 5 km." ||
		store.successes[0].RunState != model.ScheduledTaskRunStateDelivered || store.successes[0].NextRunAt == nil ||
		!store.successes[0].NextRunAt.After(now) {
		t.Fatalf("success = %+v", store.successes)
	}
}

func TestExecutorMonitoringBaselineNoChangeAndComplete(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name          string
		status        string
		wantRun       string
		wantTask      string
		wantSynthesis int
	}{
		{name: "baseline suppresses delivery", status: "deliver", wantRun: model.ScheduledTaskRunStateNoChange, wantTask: model.ScheduledTaskStateActive},
		{name: "semantic complete delivers", status: "complete", wantRun: model.ScheduledTaskRunStateCompleted, wantTask: model.ScheduledTaskStateCompleted, wantSynthesis: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker := &executorProvider{toolReplies: []provider.StreamResult{{Content: `{"status":"` + test.status + `","summary":"Observed","evidence":["fact"],"monitoringState":{"baseline":true}}`}}}
			synthesis := &executorProvider{reply: "Finished."}
			store := &executorStore{}
			snapshot := &executorSnapshot{tools: []provider.ToolDefinition{{Name: executorDataTool}}}
			claimed := claimedTask(model.ScheduledTaskKindMonitoring, now)
			claimed.Task.InitialRun = string(InitialRunBaseline)
			claimed.Task.DTStart = new(now.Add(-time.Hour))
			claimed.Task.RRULE = "FREQ=HOURLY"
			err := executorFor(worker, synthesis, &executorCatalog{byUser: map[string]*executorSnapshot{executorTestUsername: snapshot}}, store, now).
				Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimed)
			if err != nil {
				t.Fatal(err)
			}
			if synthesis.chatCalls != test.wantSynthesis || len(store.successes) != 1 {
				t.Fatalf("synthesis=%d successes=%+v", synthesis.chatCalls, store.successes)
			}
			success := store.successes[0]
			if success.RunState != test.wantRun || success.TaskState != test.wantTask ||
				string(success.MonitoringState) != `{"baseline":true}` || success.Unread != (test.wantSynthesis == 1) {
				t.Fatalf("success = %+v", success)
			}
		})
	}
}

func TestExecutorBoundsIterationTimeoutAndBytesWithSafeCodes(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		worker *executorProvider
		snap   *executorSnapshot
		config func(*Executor)
		code   string
	}{
		{
			name: "iteration",
			worker: &executorProvider{toolReplies: []provider.StreamResult{
				{ToolCalls: []provider.ToolCall{{ID: "1", Name: executorDataTool, Arguments: `{}`}}},
			}},
			snap: &executorSnapshot{tools: []provider.ToolDefinition{{Name: executorDataTool}}, results: map[string]string{executorDataTool: "x"}},
			config: func(e *Executor) {
				e.cfg.WorkerMaxIterations = 1
			},
			code: failureIterationLimit,
		},
		{
			name: "timeout", worker: &executorProvider{block: true},
			snap: &executorSnapshot{tools: []provider.ToolDefinition{{Name: executorDataTool}}},
			config: func(e *Executor) {
				e.cfg.WorkerTimeout = time.Millisecond
			},
			code: failureTimeout,
		},
		{
			name:   "model bytes",
			worker: &executorProvider{toolReplies: []provider.StreamResult{{Content: strings.Repeat("x", maxModelResponseBytes+1)}}},
			snap:   &executorSnapshot{tools: []provider.ToolDefinition{{Name: executorDataTool}}},
			config: func(*Executor) {},
			code:   failureResponseTooLarge,
		},
		{
			name: "tool bytes",
			worker: &executorProvider{toolReplies: []provider.StreamResult{
				{ToolCalls: []provider.ToolCall{{ID: "1", Name: executorDataTool, Arguments: `{}`}}},
			}},
			snap:   &executorSnapshot{tools: []provider.ToolDefinition{{Name: executorDataTool}}, results: map[string]string{executorDataTool: strings.Repeat("x", maxToolResultBytes+1)}},
			config: func(*Executor) {},
			code:   failureToolResultTooLarge,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := &executorStore{}
			executor := executorFor(test.worker, &executorProvider{}, &executorCatalog{byUser: map[string]*executorSnapshot{executorTestUsername: test.snap}}, store, now)
			test.config(executor)
			err := executor.Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimedTask(model.ScheduledTaskKindData, now))
			if err == nil || len(store.failures) != 1 || store.failures[0].Code != test.code {
				t.Fatalf("err=%v failures=%+v", err, store.failures)
			}
			if strings.Contains(store.failures[0].Code, "x") {
				t.Fatalf("unsafe code = %q", store.failures[0].Code)
			}
		})
	}
}

func TestExecutorFailureAdvancementAndPausePolicies(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		failures  int
		oneOff    bool
		wantPause bool
		wantTask  string
		wantNext  bool
	}{
		{name: "recurring schedules next", wantTask: model.ScheduledTaskStateActive, wantNext: true},
		{name: "third failure pauses", failures: 2, wantPause: true, wantTask: model.ScheduledTaskStatePaused},
		{name: "one off fails", oneOff: true, wantTask: model.ScheduledTaskStateFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &executorStore{}
			claimed := claimedTask(model.ScheduledTaskKindData, now)
			claimed.Task.ConsecutiveFailures = test.failures
			if test.oneOff {
				claimed.Task.OneOffAt = new(claimed.Run.ScheduledFor)
			} else {
				claimed.Task.DTStart = new(now.Add(-24 * time.Hour))
				claimed.Task.RRULE = "FREQ=DAILY"
			}
			worker := &executorProvider{err: errors.New("raw provider secret")}
			snapshot := &executorSnapshot{tools: []provider.ToolDefinition{{Name: executorDataTool}}}
			err := executorFor(worker, &executorProvider{}, &executorCatalog{byUser: map[string]*executorSnapshot{executorTestUsername: snapshot}}, store, now).
				Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimed)
			if err == nil || len(store.failures) != 1 {
				t.Fatalf("err=%v failures=%+v", err, store.failures)
			}
			failure := store.failures[0]
			if failure.Pause != test.wantPause || failure.TaskState != test.wantTask || (failure.NextRunAt != nil) != test.wantNext ||
				!failure.IncrementFailures || strings.Contains(failure.Code, "secret") {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
}

func TestExecutorDefaultsAndInputGuards(t *testing.T) {
	executor := NewExecutor(ExecutorDeps{})
	if executor.cfg.WorkerMaxTokens != defaultExecutorMaxTokens ||
		executor.cfg.WorkerTimeout != defaultExecutorTimeout ||
		executor.cfg.WorkerMaxIterations != defaultExecutorMaxIterations ||
		executor.cfg.SynthesisMaxTokens != defaultExecutorMaxTokens ||
		executor.cfg.SynthesisTimeout != defaultExecutorTimeout || executor.now == nil {
		t.Fatalf("defaults = %+v", executor.cfg)
	}
	now := time.Now()
	claimed := claimedTask(model.ScheduledTaskKindData, now)
	if err := (*Executor)(nil).Execute(t.Context(), Actor{}, claimed); err == nil {
		t.Fatal("nil executor succeeded")
	}
	executor.store = &executorStore{}
	if err := executor.Execute(t.Context(), Actor{ID: 8}, claimed); err == nil {
		t.Fatal("wrong actor succeeded")
	}
	if err := executor.Execute(t.Context(), Actor{ID: 7, Username: "bob"}, claimed); err == nil {
		t.Fatal("mismatched username succeeded")
	}
	store := &executorStore{}
	executor = executorFor(&executorProvider{}, &executorProvider{}, &executorCatalog{byUser: map[string]*executorSnapshot{}}, store, now)
	if err := executor.Execute(t.Context(), Actor{ID: 7}, claimed); err == nil ||
		len(store.failures) != 0 {
		t.Fatalf("blank username failures=%+v err=%v", store.failures, err)
	}
}

func TestExecutorCoversGatherValidationFailures(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	validSnapshot := &executorSnapshot{tools: []provider.ToolDefinition{{Name: executorDataTool}}}
	for _, test := range []struct {
		name    string
		mutate  func(*Executor, *model.ClaimedScheduledTask, *executorCatalog, *executorSnapshot)
		replies []provider.StreamResult
		code    string
	}{
		{name: "missing worker", mutate: func(e *Executor, _ *model.ClaimedScheduledTask, _ *executorCatalog, _ *executorSnapshot) {
			e.worker = nil
		}, code: failureInvalidTask},
		{name: "max tokens", mutate: func(e *Executor, _ *model.ClaimedScheduledTask, _ *executorCatalog, _ *executorSnapshot) {
			e.cfg.WorkerMaxTokens = maxExecutorMaxTokens + 1
		}, code: failureInvalidTask},
		{name: "state bytes", mutate: func(_ *Executor, c *model.ClaimedScheduledTask, _ *executorCatalog, _ *executorSnapshot) {
			c.Task.MonitoringState = json.RawMessage(strings.Repeat("x", maxMonitoringStateBytes+1))
		}, code: failureResponseTooLarge},
		{name: "catalog error", mutate: func(_ *Executor, _ *model.ClaimedScheduledTask, c *executorCatalog, _ *executorSnapshot) {
			c.err = errors.New("catalog")
		}, code: failureProvider},
		{name: "nil snapshot", mutate: func(_ *Executor, _ *model.ClaimedScheduledTask, c *executorCatalog, _ *executorSnapshot) {
			c.byUser[executorTestUsername] = nil
		}, code: failureProvider},
		{name: "list error", mutate: func(_ *Executor, _ *model.ClaimedScheduledTask, _ *executorCatalog, s *executorSnapshot) {
			s.listErr = errors.New("list")
		}, code: failureProvider},
		{name: "prompt aggregate", mutate: func(_ *Executor, c *model.ClaimedScheduledTask, _ *executorCatalog, _ *executorSnapshot) {
			c.Task.CompiledPrompt = strings.Repeat("p", maxEvidenceContextBytes+1)
		}, code: failureEvidenceTooLarge},
		{name: "invalid outcome", replies: []provider.StreamResult{{Content: `{}`}}, mutate: func(*Executor, *model.ClaimedScheduledTask, *executorCatalog, *executorSnapshot) {}, code: failureInvalidOutcome},
		{name: "malformed arguments", replies: []provider.StreamResult{{ToolCalls: []provider.ToolCall{{Name: executorDataTool, Arguments: `[]`}}}}, mutate: func(*Executor, *model.ClaimedScheduledTask, *executorCatalog, *executorSnapshot) {}, code: failureMalformedToolCall},
		{name: "tool error", replies: []provider.StreamResult{{ToolCalls: []provider.ToolCall{{Name: executorDataTool, Arguments: `{}`}}}}, mutate: func(_ *Executor, _ *model.ClaimedScheduledTask, _ *executorCatalog, s *executorSnapshot) {
			s.callErr = errors.New("call")
		}, code: failureTool},
		{name: "aggregate evidence", replies: []provider.StreamResult{
			{ToolCalls: []provider.ToolCall{{Name: executorDataTool, Arguments: `{}`}}},
			{ToolCalls: []provider.ToolCall{{Name: executorDataTool, Arguments: `{}`}}},
			{ToolCalls: []provider.ToolCall{{Name: executorDataTool, Arguments: `{}`}}},
			{ToolCalls: []provider.ToolCall{{Name: executorDataTool, Arguments: `{}`}}},
		}, mutate: func(e *Executor, _ *model.ClaimedScheduledTask, _ *executorCatalog, s *executorSnapshot) {
			e.cfg.WorkerMaxIterations = 5
			s.results[executorDataTool] = strings.Repeat("e", maxToolResultBytes)
		}, code: failureEvidenceTooLarge},
		{name: "per response tool call limit", replies: []provider.StreamResult{{
			ToolCalls: repeatedToolCalls(maxToolCallsPerResponse + 1),
		}}, mutate: func(*Executor, *model.ClaimedScheduledTask, *executorCatalog, *executorSnapshot) {}, code: failureToolCallLimit},
		{name: "tool argument aggregate", replies: []provider.StreamResult{{
			ToolCalls: repeatedToolCallsWithPayload(maxToolCallsPerResponse, 20<<10),
		}}, mutate: func(*Executor, *model.ClaimedScheduledTask, *executorCatalog, *executorSnapshot) {}, code: failureEvidenceTooLarge},
		{name: "total tool call limit", replies: []provider.StreamResult{
			{ToolCalls: repeatedToolCalls(maxToolCallsPerResponse)},
			{ToolCalls: repeatedToolCalls(maxToolCallsPerResponse)},
			{ToolCalls: repeatedToolCalls(maxToolCallsPerResponse)},
			{ToolCalls: repeatedToolCalls(maxToolCallsPerResponse)},
			{ToolCalls: repeatedToolCalls(maxToolCallsPerResponse)},
		}, mutate: func(e *Executor, _ *model.ClaimedScheduledTask, _ *executorCatalog, _ *executorSnapshot) {
			e.cfg.WorkerMaxIterations = 6
		}, code: failureToolCallLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &executorStore{}
			snapshot := *validSnapshot
			snapshot.results = map[string]string{}
			catalog := &executorCatalog{byUser: map[string]*executorSnapshot{executorTestUsername: &snapshot}}
			worker := &executorProvider{toolReplies: test.replies}
			executor := executorFor(worker, &executorProvider{}, catalog, store, now)
			claimed := claimedTask(model.ScheduledTaskKindData, now)
			test.mutate(executor, &claimed, catalog, &snapshot)
			if err := executor.Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimed); err == nil ||
				len(store.failures) != 1 || store.failures[0].Code != test.code {
				t.Fatalf("failures=%+v err=%v", store.failures, err)
			}
			if test.code == failureToolCallLimit && len(snapshot.calls) > maxTotalToolCalls {
				t.Fatalf("dispatched %d calls past cap", len(snapshot.calls))
			}
		})
	}
}

func repeatedToolCalls(count int) []provider.ToolCall {
	calls := make([]provider.ToolCall, count)
	for i := range calls {
		calls[i] = provider.ToolCall{ID: "call", Name: executorDataTool, Arguments: `{}`}
	}
	return calls
}

func repeatedToolCallsWithPayload(count, bytes int) []provider.ToolCall {
	calls := repeatedToolCalls(count)
	args := `{"value":"` + strings.Repeat("x", bytes) + `"}`
	for i := range calls {
		calls[i].Arguments = args
	}
	return calls
}

func TestExecutorCoversStaticSynthesisAndAdvancementFailures(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	t.Run("invalid static", func(t *testing.T) {
		store := &executorStore{}
		claimed := claimedTask(model.ScheduledTaskKindReminder, now)
		claimed.Task.ExecutionMode = string(ExecutionModeStatic)
		claimed.Task.StaticMessage = ""
		if err := executorFor(nil, nil, nil, store, now).Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimed); err == nil ||
			store.failures[0].Code != failureInvalidTask {
			t.Fatalf("failures=%+v err=%v", store.failures, err)
		}
	})
	t.Run("invalid static recurrence", func(t *testing.T) {
		store := &executorStore{}
		claimed := claimedTask(model.ScheduledTaskKindReminder, now)
		claimed.Task.ExecutionMode = string(ExecutionModeStatic)
		claimed.Task.StaticMessage = "x"
		claimed.Task.Timezone = executorInvalidTimezone
		if err := executorFor(nil, nil, nil, store, now).Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimed); err == nil ||
			store.failures[0].Code != failureInvalidTask {
			t.Fatalf("failures=%+v err=%v", store.failures, err)
		}
	})
	for _, test := range []struct {
		name      string
		synthesis *executorProvider
		mutate    func(*Executor, *model.ClaimedScheduledTask)
		code      string
	}{
		{name: "missing synthesis", synthesis: &executorProvider{}, mutate: func(e *Executor, _ *model.ClaimedScheduledTask) { e.synthesis = nil }, code: failureInvalidTask},
		{name: "synthesis max tokens", synthesis: &executorProvider{}, mutate: func(e *Executor, _ *model.ClaimedScheduledTask) { e.cfg.SynthesisMaxTokens = maxExecutorMaxTokens + 1 }, code: failureInvalidTask},
		{name: "synthesis error", synthesis: &executorProvider{err: errors.New("provider")}, mutate: func(*Executor, *model.ClaimedScheduledTask) {}, code: failureProvider},
		{name: "synthesis blank", synthesis: &executorProvider{reply: " "}, mutate: func(*Executor, *model.ClaimedScheduledTask) {}, code: failureInvalidOutcome},
		{name: "invalid success schedule", synthesis: &executorProvider{reply: "x"}, mutate: func(_ *Executor, c *model.ClaimedScheduledTask) { c.Task.Timezone = executorInvalidTimezone }, code: failureInvalidTask},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &executorStore{}
			worker := &executorProvider{toolReplies: []provider.StreamResult{{Content: `{"status":"deliver","summary":"x","evidence":[],"monitoringState":{}}`}}}
			snapshot := &executorSnapshot{tools: []provider.ToolDefinition{{Name: executorDataTool}}}
			claimed := claimedTask(model.ScheduledTaskKindData, now)
			claimed.Task.DTStart = new(now.Add(-time.Hour))
			claimed.Task.RRULE = "FREQ=HOURLY"
			executor := executorFor(worker, test.synthesis, &executorCatalog{byUser: map[string]*executorSnapshot{executorTestUsername: snapshot}}, store, now)
			test.mutate(executor, &claimed)
			if err := executor.Execute(t.Context(), Actor{ID: 7, Username: executorTestUsername}, claimed); err == nil ||
				len(store.failures) != 1 || store.failures[0].Code != test.code {
				t.Fatalf("failures=%+v err=%v", store.failures, err)
			}
		})
	}
}

func TestExecutorHelperBoundsAndMetadata(t *testing.T) {
	if validToolArguments("") || validToolArguments("[]") || validToolArguments(`{} {}`) ||
		validToolArguments(strings.Repeat("x", maxToolResultBytes+1)) || !validToolArguments(`{}`) {
		t.Fatal("tool argument validation mismatch")
	}
	metadataCases := [][]provider.ToolDefinition{
		{{Name: interactiveCredentialsTool}, {Name: "kadence__load_skill"}, {Name: "ok"}},
		{{Name: ""}},
		{{Name: "x", Parameters: json.RawMessage(`{`)}},
		{{Name: "x", Description: strings.Repeat("d", maxToolDescriptionBytes+1)}},
	}
	if offered, _, code := exactExecutionTools(metadataCases[0], []string{"ok"}); code != "" || len(offered) != 1 {
		t.Fatalf("filtered metadata=%+v code=%s", offered, code)
	}
	for _, definitions := range metadataCases[1:] {
		name := definitions[0].Name
		if _, _, code := exactExecutionTools(definitions, []string{name}); code != failureInvalidTask {
			t.Fatalf("metadata code=%s", code)
		}
	}
	if offered, _, code := exactExecutionTools(
		[]provider.ToolDefinition{{Name: "new", Parameters: json.RawMessage(`{`)}, {Name: "ok"}},
		[]string{"ok"},
	); code != "" || len(offered) != 1 {
		t.Fatalf("unoffered metadata changed snapshot: offered=%+v code=%s", offered, code)
	}
	many := make([]provider.ToolDefinition, 17)
	manyNames := make([]string, len(many))
	for i := range many {
		many[i] = provider.ToolDefinition{Name: string(rune('a' + i)), Description: strings.Repeat("d", maxToolDescriptionBytes)}
		manyNames[i] = many[i].Name
	}
	if _, _, code := exactExecutionTools(many, manyNames); code != failureInvalidTask {
		t.Fatalf("aggregate metadata code=%s", code)
	}
	if _, _, code := exactExecutionTools([]provider.ToolDefinition{{Name: "x"}}, []string{"x", "x"}); code != failureInvalidTask {
		t.Fatalf("duplicate auth code=%s", code)
	}

	ctx := t.Context()
	oversized := strings.Repeat("x", maxModelResponseBytes+1)
	if _, err := boundedToolStream(ctx, &executorProvider{toolReplies: []provider.StreamResult{{Content: oversized}}, skipToken: true}, provider.ChatRequest{}); !errors.Is(err, errExecutionResponseTooLarge) {
		t.Fatalf("direct tool bound err=%v", err)
	}
	if _, err := boundedChatStream(ctx, &executorProvider{reply: oversized}, provider.ChatRequest{}); !errors.Is(err, errExecutionResponseTooLarge) {
		t.Fatalf("stream chat bound err=%v", err)
	}
	if _, err := boundedChatStream(ctx, &executorProvider{reply: oversized, skipToken: true}, provider.ChatRequest{}); !errors.Is(err, errExecutionResponseTooLarge) {
		t.Fatalf("direct chat bound err=%v", err)
	}
	if _, err := boundedChatStream(ctx, &executorProvider{err: errors.New("x")}, provider.ChatRequest{}); err == nil {
		t.Fatal("chat error accepted")
	}
}

func TestExecutorDirectStateAndStoreErrorBranches(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	exhausted := model.ScheduledTask{DTStart: new(now.Add(-2 * time.Hour)), RRULE: "FREQ=HOURLY;COUNT=1", Timezone: defaultTimezoneUTC}
	if state, next, err := nextSuccessfulState(exhausted, now, false); err != nil || state != model.ScheduledTaskStateCompleted || next != nil {
		t.Fatalf("exhausted state=%s next=%v err=%v", state, next, err)
	}
	if _, _, err := nextSuccessfulState(model.ScheduledTask{Timezone: executorInvalidTimezone}, now, false); err == nil {
		t.Fatal("invalid schedule accepted")
	}
	storeErr := errors.New("store")
	store := &executorStore{err: storeErr}
	executor := executorFor(nil, nil, nil, store, now)
	claimed := claimedTask(model.ScheduledTaskKindData, now)
	if err := executor.recordFailure(t.Context(), claimed, failureProvider); !errors.Is(err, storeErr) {
		t.Fatalf("store err=%v", err)
	}
	outcome := WorkerOutcome{
		Status: OutcomeDeliver, Summary: "x",
		Evidence:        []string{strings.Repeat("e", maxEvidenceContextBytes)},
		MonitoringState: json.RawMessage(`{}`),
	}
	executor.synthesis = &executorProvider{}
	if _, result := executor.synthesize(t.Context(), strings.Repeat("c", maxEvidenceContextBytes), outcome); result == nil || result.code != failureEvidenceTooLarge {
		t.Fatalf("synthesis result=%+v", result)
	}
}
