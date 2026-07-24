package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/store"
)

type fakeScheduledLifecycle struct {
	createResult scheduled.DefinitionResult
	createErr    error
	detailErr    error
	gotActor     scheduled.Actor
	gotMessage   string
}

func (f *fakeScheduledLifecycle) Create(_ context.Context, actor scheduled.Actor, message string) (scheduled.DefinitionResult, error) {
	f.gotActor, f.gotMessage = actor, message
	return f.createResult, f.createErr
}
func (f *fakeScheduledLifecycle) Refine(context.Context, scheduled.Actor, string, string) (scheduled.DefinitionResult, error) {
	return f.createResult, f.createErr
}
func (f *fakeScheduledLifecycle) Confirm(context.Context, scheduled.Actor, string, int) (model.ScheduledTask, error) {
	return model.ScheduledTask{}, f.createErr
}
func (f *fakeScheduledLifecycle) List(context.Context, int64) (scheduled.ListResult, error) {
	return scheduled.ListResult{}, f.createErr
}
func (f *fakeScheduledLifecycle) Detail(context.Context, int64, string) (scheduled.Detail, error) {
	return scheduled.Detail{}, f.detailErr
}
func (f *fakeScheduledLifecycle) Pause(context.Context, int64, string) (model.ScheduledTask, error) {
	return model.ScheduledTask{}, f.createErr
}
func (f *fakeScheduledLifecycle) Resume(context.Context, int64, string) (model.ScheduledTask, error) {
	return model.ScheduledTask{}, f.createErr
}
func (f *fakeScheduledLifecycle) Delete(context.Context, int64, string) error { return f.createErr }
func (f *fakeScheduledLifecycle) RunNow(context.Context, int64, string) (model.ScheduledTaskRun, error) {
	return model.ScheduledTaskRun{}, f.createErr
}
func (f *fakeScheduledLifecycle) MarkRead(context.Context, int64, string) error { return f.createErr }

func TestScheduledCreateStreamsBoundedDefinitionEvents(t *testing.T) {
	proposal := &scheduled.Proposal{Name: "Morning", Version: 1}
	fake := &fakeScheduledLifecycle{createResult: scheduled.DefinitionResult{Task: model.ScheduledTask{ID: "task-1", ConversationID: "conv-1"}, Refinement: scheduled.Refinement{Text: "Saved it.", Proposal: proposal}}}
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
	if fake.gotActor.ID != 7 || fake.gotMessage != "remind me" {
		t.Fatalf("owner/message not forwarded: %+v %q", fake.gotActor, fake.gotMessage)
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
