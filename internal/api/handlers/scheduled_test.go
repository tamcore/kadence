package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/store"
)

const (
	scheduledTestTask1   = "task-1"
	scheduledTestTask2   = "task-2"
	scheduledTestConv1   = "conv-1"
	scheduledTestConfirm = "confirm"
	scheduledTestList    = "list"
	scheduledTestDetail  = "detail"
	scheduledTestPause   = "pause"
	scheduledTestResume  = "resume"
	scheduledTestDelete  = "delete"
	scheduledTestRun     = "run"
	scheduledTestRead    = "read"
	scheduledTestMessage = `{"message":"x"}`
	scheduledTestPaused  = `{"state":"paused"}`
)

type fakeScheduledLifecycle struct {
	createResult scheduled.DefinitionResult
	createErr    error
	refineResult scheduled.DefinitionResult
	refineErr    error
	taskResult   model.ScheduledTask
	listResult   scheduled.ListResult
	detailResult scheduled.Detail
	detailErr    error
	runResult    model.ScheduledTaskRun
	lifecycleErr error
	gotActor     scheduled.Actor
	gotMessage   string
	gotID        string
	gotOwner     int64
	gotVersion   int
	gotMethod    string
}

func (f *fakeScheduledLifecycle) Create(_ context.Context, actor scheduled.Actor, message string) (scheduled.DefinitionResult, error) {
	f.gotActor, f.gotMessage = actor, message
	return f.createResult, f.createErr
}
func (f *fakeScheduledLifecycle) Refine(_ context.Context, actor scheduled.Actor, id, message string) (scheduled.DefinitionResult, error) {
	f.gotMethod, f.gotActor, f.gotID, f.gotMessage = "refine", actor, id, message
	return f.refineResult, f.refineErr
}
func (f *fakeScheduledLifecycle) Confirm(_ context.Context, actor scheduled.Actor, id string, version int) (model.ScheduledTask, error) {
	f.gotMethod, f.gotActor, f.gotID, f.gotVersion = scheduledTestConfirm, actor, id, version
	return f.taskResult, f.lifecycleErr
}
func (f *fakeScheduledLifecycle) List(_ context.Context, owner int64) (scheduled.ListResult, error) {
	f.gotMethod, f.gotOwner = scheduledTestList, owner
	return f.listResult, f.lifecycleErr
}
func (f *fakeScheduledLifecycle) Detail(_ context.Context, owner int64, id string) (scheduled.Detail, error) {
	f.gotMethod, f.gotOwner, f.gotID = scheduledTestDetail, owner, id
	return f.detailResult, f.detailErr
}
func (f *fakeScheduledLifecycle) Pause(_ context.Context, owner int64, id string) (model.ScheduledTask, error) {
	f.gotMethod, f.gotOwner, f.gotID = scheduledTestPause, owner, id
	return f.taskResult, f.lifecycleErr
}
func (f *fakeScheduledLifecycle) Resume(_ context.Context, owner int64, id string) (model.ScheduledTask, error) {
	f.gotMethod, f.gotOwner, f.gotID = scheduledTestResume, owner, id
	return f.taskResult, f.lifecycleErr
}
func (f *fakeScheduledLifecycle) Delete(_ context.Context, owner int64, id string) error {
	f.gotMethod, f.gotOwner, f.gotID = scheduledTestDelete, owner, id
	return f.lifecycleErr
}
func (f *fakeScheduledLifecycle) RunNow(_ context.Context, owner int64, id string) (model.ScheduledTaskRun, error) {
	f.gotMethod, f.gotOwner, f.gotID = scheduledTestRun, owner, id
	return f.runResult, f.lifecycleErr
}
func (f *fakeScheduledLifecycle) MarkRead(_ context.Context, owner int64, id string) error {
	f.gotMethod, f.gotOwner, f.gotID = scheduledTestRead, owner, id
	return f.lifecycleErr
}

func TestScheduledCreateStreamsBoundedDefinitionEvents(t *testing.T) {
	proposal := &scheduled.Proposal{Name: "Morning", Version: 1}
	fake := &fakeScheduledLifecycle{createResult: scheduled.DefinitionResult{Task: model.ScheduledTask{ID: scheduledTestTask1, ConversationID: scheduledTestConv1}, Refinement: scheduled.Refinement{Text: "Saved it.", Proposal: proposal}}}
	h := handlers.NewScheduled(fake)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/scheduled/tasks", strings.NewReader(`{"message":"remind me"}`)), 7)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`"type":"meta"`, `"taskId":"task-1"`, `"conversationId":"conv-1"`, `"type":"text"`, `"type":"task_proposal"`, `"type":"done"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE missing %s: %s", want, body)
		}
	}
	if strings.Count(body, `"type":"task_proposal"`) != 1 || strings.Count(body, `"type":"task_question"`) != 0 {
		t.Fatalf("unexpected structured event count: %s", body)
	}
	if len(body) > 128<<10 {
		t.Fatalf("SSE response bytes=%d, want <= 128 KiB", len(body))
	}
	if fake.gotActor.ID != 7 || fake.gotMessage != "remind me" {
		t.Fatalf("owner/message not forwarded: %+v %q", fake.gotActor, fake.gotMessage)
	}
}

func TestScheduledDefinitionRouteBodiesQuestionsErrorsAndBounds(t *testing.T) {
	question := &scheduled.QuestionCard{ID: "when", Prompt: "When?", Kind: scheduled.QuestionKindText}
	fake := &fakeScheduledLifecycle{refineResult: scheduled.DefinitionResult{
		Task:       model.ScheduledTask{ID: scheduledTestTask2, ConversationID: "conv-2"},
		Refinement: scheduled.Refinement{Text: "Need a time.", Question: question},
	}}
	h := handlers.NewScheduled(fake)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodPost, "/api/scheduled/tasks/task-2/messages", strings.NewReader(`{"message":"tomorrow"}`)), 7), "id", scheduledTestTask2)
	rec := httptest.NewRecorder()
	h.Refine(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"type":"task_question"`) ||
		strings.Contains(rec.Body.String(), `"type":"task_proposal"`) || fake.gotActor.ID != 7 ||
		fake.gotID != scheduledTestTask2 || fake.gotMessage != "tomorrow" {
		t.Fatalf("status=%d body=%s fake=%+v", rec.Code, rec.Body.String(), fake)
	}

	for _, tc := range []struct {
		name   string
		create bool
		body   string
		id     string
	}{
		{name: "create malformed", create: true, body: `{`},
		{name: "create blank", create: true, body: `{"message":" "}`},
		{name: "refine malformed", body: `{`, id: scheduledTestTask2},
		{name: "refine blank", body: `{"message":" "}`, id: scheduledTestTask2},
		{name: "refine missing id", body: scheduledTestMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)), 7)
			if tc.create {
				h.Create(rec, req)
			} else {
				h.Refine(rec, withChiParam(req, "id", tc.id))
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	huge := &fakeScheduledLifecycle{createResult: scheduled.DefinitionResult{
		Task:       model.ScheduledTask{ID: "task", ConversationID: "conv"},
		Refinement: scheduled.Refinement{Text: strings.Repeat("x", 256<<10), Question: question},
	}}
	rec = httptest.NewRecorder()
	handlers.NewScheduled(huge).Create(rec, withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(scheduledTestMessage)), 7))
	if rec.Body.Len() > 128<<10 {
		t.Fatalf("bounded response bytes=%d", rec.Body.Len())
	}

	fake.refineErr = errors.New("provider secret detail")
	rec = httptest.NewRecorder()
	h.Refine(rec, withChiParam(withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(scheduledTestMessage)), 7), "id", scheduledTestTask2))
	if !strings.Contains(rec.Body.String(), `"type":"error"`) || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestScheduledLifecycleRoutesSuccessAndOwnerForwarding(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	task := model.ScheduledTask{
		ID: scheduledTestTask1, ConversationID: scheduledTestConv1, Version: 2, Name: "Morning", Kind: model.ScheduledTaskKindReminder,
		State: model.ScheduledTaskStateActive, CompiledPrompt: "prompt", Timezone: "UTC",
		ExecutionMode: "static", AuthorizedTools: []string{}, DeliveryPolicy: "always", InitialRun: "wait",
		NextRunAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	run := model.ScheduledTaskRun{
		ID: 9, TaskID: task.ID, OccurrenceKey: "manual:1", ScheduledFor: now,
		State: model.ScheduledTaskRunStatePending, Error: "execution_failed", CreatedAt: now,
	}
	tests := []struct {
		name       string
		method     string
		body       string
		setup      func(*fakeScheduledLifecycle)
		invoke     func(*handlers.Scheduled, http.ResponseWriter, *http.Request)
		wantMethod string
		wantBody   string
	}{
		{name: scheduledTestList, method: http.MethodGet, setup: func(f *fakeScheduledLifecycle) {
			f.listResult = scheduled.ListResult{Tasks: []model.ScheduledTask{task}, Unread: 3}
		}, invoke: (*handlers.Scheduled).List, wantMethod: scheduledTestList, wantBody: `"unreadCount":3`},
		{name: scheduledTestDetail, method: http.MethodGet, setup: func(f *fakeScheduledLifecycle) {
			f.detailResult = scheduled.Detail{Task: task, Runs: []model.ScheduledTaskRun{run}}
		}, invoke: (*handlers.Scheduled).Detail, wantMethod: scheduledTestDetail, wantBody: `"error":"execution_failed"`},
		{name: scheduledTestConfirm, method: http.MethodPost, body: `{"expectedVersion":2}`, setup: func(f *fakeScheduledLifecycle) { f.taskResult = task }, invoke: (*handlers.Scheduled).Confirm, wantMethod: scheduledTestConfirm, wantBody: `"version":2`},
		{name: scheduledTestPause, method: http.MethodPatch, body: scheduledTestPaused, setup: func(f *fakeScheduledLifecycle) { f.taskResult = task }, invoke: (*handlers.Scheduled).Patch, wantMethod: scheduledTestPause, wantBody: `"id":"task-1"`},
		{name: scheduledTestResume, method: http.MethodPatch, body: `{"state":"active"}`, setup: func(f *fakeScheduledLifecycle) { f.taskResult = task }, invoke: (*handlers.Scheduled).Patch, wantMethod: scheduledTestResume, wantBody: `"id":"task-1"`},
		{name: scheduledTestDelete, method: http.MethodDelete, invoke: (*handlers.Scheduled).Delete, wantMethod: scheduledTestDelete, wantBody: `"ok":true`},
		{name: scheduledTestRun, method: http.MethodPost, setup: func(f *fakeScheduledLifecycle) { f.runResult = run }, invoke: (*handlers.Scheduled).RunNow, wantMethod: scheduledTestRun, wantBody: `"occurrenceKey":"manual:1"`},
		{name: scheduledTestRead, method: http.MethodPost, invoke: (*handlers.Scheduled).MarkRead, wantMethod: scheduledTestRead, wantBody: `"ok":true`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeScheduledLifecycle{}
			if tc.setup != nil {
				tc.setup(fake)
			}
			h := handlers.NewScheduled(fake)
			req := withChiParam(withUser(httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body)), 7), "id", scheduledTestTask1)
			rec := httptest.NewRecorder()
			tc.invoke(h, rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.wantBody) ||
				fake.gotMethod != tc.wantMethod || fake.gotOwner != 7 && tc.wantMethod != scheduledTestConfirm ||
				fake.gotID != scheduledTestTask1 && tc.wantMethod != scheduledTestList {
				t.Fatalf("status=%d body=%s fake=%+v", rec.Code, rec.Body.String(), fake)
			}
			if tc.wantMethod == scheduledTestConfirm && (fake.gotActor.ID != 7 || fake.gotVersion != 2) {
				t.Fatalf("confirm forwarding=%+v", fake)
			}
		})
	}
}

func TestScheduledLifecycleBodyAndErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*handlers.Scheduled, http.ResponseWriter, *http.Request)
		body   string
	}{
		{name: "confirm malformed", invoke: (*handlers.Scheduled).Confirm, body: `{`},
		{name: "confirm version missing", invoke: (*handlers.Scheduled).Confirm, body: `{}`},
		{name: "patch malformed", invoke: (*handlers.Scheduled).Patch, body: `{`},
		{name: "patch invalid state", invoke: (*handlers.Scheduled).Patch, body: `{"state":"draft"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := withChiParam(withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)), 7), "id", "task")
			tc.invoke(handlers.NewScheduled(&fakeScheduledLifecycle{}), rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: store.ErrNotFound, want: http.StatusNotFound},
		{name: "active limit", err: store.ErrActiveTaskLimit, want: http.StatusConflict},
		{name: "stale", err: scheduled.ErrStaleProposal, want: http.StatusConflict},
		{name: "transition", err: scheduled.ErrInvalidTransition, want: http.StatusBadRequest},
		{name: "public validation", err: errors.New("scheduled: invalid"), want: http.StatusBadRequest},
		{name: "internal", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, route := range []struct {
				name   string
				invoke func(*handlers.Scheduled, http.ResponseWriter, *http.Request)
			}{
				{name: scheduledTestDetail, invoke: (*handlers.Scheduled).Detail},
				{name: scheduledTestConfirm, invoke: (*handlers.Scheduled).Confirm},
				{name: scheduledTestPause, invoke: (*handlers.Scheduled).Patch},
				{name: scheduledTestDelete, invoke: (*handlers.Scheduled).Delete},
				{name: scheduledTestRun, invoke: (*handlers.Scheduled).RunNow},
				{name: scheduledTestRead, invoke: (*handlers.Scheduled).MarkRead},
			} {
				fake := &fakeScheduledLifecycle{lifecycleErr: tc.err, detailErr: tc.err}
				body := ""
				if route.name == scheduledTestConfirm {
					body = `{"expectedVersion":1}`
				}
				if route.name == scheduledTestPause {
					body = scheduledTestPaused
				}
				rec := httptest.NewRecorder()
				req := withChiParam(withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), 7), "id", "task")
				route.invoke(handlers.NewScheduled(fake), rec, req)
				if rec.Code != tc.want {
					t.Fatalf("%s status=%d want=%d body=%s", route.name, rec.Code, tc.want, rec.Body.String())
				}
			}
		})
	}
	listFake := &fakeScheduledLifecycle{lifecycleErr: errors.New("database unavailable")}
	listRec := httptest.NewRecorder()
	handlers.NewScheduled(listFake).List(listRec, withUser(httptest.NewRequest(http.MethodGet, "/", nil), 7))
	if listRec.Code != http.StatusInternalServerError || listFake.gotOwner != 7 {
		t.Fatalf("list status=%d owner=%d body=%s", listRec.Code, listFake.gotOwner, listRec.Body.String())
	}
}

func TestScheduledAllHandlersRejectMissingDependencyWithoutPanic(t *testing.T) {
	routes := []struct {
		name   string
		body   string
		invoke func(*handlers.Scheduled, http.ResponseWriter, *http.Request)
	}{
		{name: "create", body: scheduledTestMessage, invoke: (*handlers.Scheduled).Create},
		{name: "refine", body: scheduledTestMessage, invoke: (*handlers.Scheduled).Refine},
		{name: scheduledTestList, invoke: (*handlers.Scheduled).List},
		{name: scheduledTestDetail, invoke: (*handlers.Scheduled).Detail},
		{name: scheduledTestConfirm, body: `{"expectedVersion":1}`, invoke: (*handlers.Scheduled).Confirm},
		{name: "patch", body: scheduledTestPaused, invoke: (*handlers.Scheduled).Patch},
		{name: scheduledTestDelete, invoke: (*handlers.Scheduled).Delete},
		{name: scheduledTestRun, invoke: (*handlers.Scheduled).RunNow},
		{name: scheduledTestRead, invoke: (*handlers.Scheduled).MarkRead},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			req := withChiParam(withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(route.body)), 7), "id", "task")
			rec := httptest.NewRecorder()
			route.invoke(handlers.NewScheduled(nil), rec, req)
			var envelope map[string]any
			if rec.Code != http.StatusInternalServerError || json.Unmarshal(rec.Body.Bytes(), &envelope) != nil {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestScheduledDetailMapsOwnerMissToNotFound(t *testing.T) {
	h := handlers.NewScheduled(&fakeScheduledLifecycle{detailErr: store.ErrNotFound})
	req := withChiParam(withUser(httptest.NewRequest(http.MethodGet, "/api/scheduled/tasks/x", nil), 7), "id", "x")
	rec := httptest.NewRecorder()
	h.Detail(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestScheduledCreateStreamsDefinitionErrorsWithoutProviderDetails(t *testing.T) {
	h := handlers.NewScheduled(&fakeScheduledLifecycle{createErr: errors.New("scheduled: malformed compiler output")})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/scheduled/tasks", strings.NewReader(`{"message":"remind me"}`)), 7)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"type":"error"`) || strings.Contains(rec.Body.String(), "malformed compiler") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
