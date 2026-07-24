package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

type fakeStreamer struct{ gotText string }

func (f *fakeStreamer) Stream(_ context.Context, _ int64, _ chat.UserContext, _ string, text string, sink chat.EventSink) error {
	f.gotText = text
	_ = sink.Send(chat.ChatEvent{Type: chat.EventMeta, ConversationID: "conv-uuid-1"})
	_ = sink.Send(chat.ChatEvent{Type: chat.EventToken, Delta: "hi"})
	_ = sink.Send(chat.ChatEvent{Type: chat.EventDone})
	return sink.Flush()
}

type fakeConvLister struct {
	list            []model.Conversation
	getByIDError    error
	deleteError     error
	deleteCalls     int
	updateTitleErr  error
	updateTitleResp model.Conversation
	getByIDResp     model.Conversation
}

func (f fakeConvLister) ListByUser(context.Context, int64) ([]model.Conversation, error) {
	return f.list, nil
}
func (f fakeConvLister) GetByID(_ context.Context, id string, userID int64) (model.Conversation, error) {
	if f.getByIDError != nil {
		return model.Conversation{}, f.getByIDError
	}
	if f.getByIDResp.ID != "" {
		return f.getByIDResp, nil
	}
	return model.Conversation{ID: id, UserID: userID}, nil
}
func (f *fakeConvLister) Delete(context.Context, string, int64) error {
	f.deleteCalls++
	return f.deleteError
}
func (f fakeConvLister) UpdateTitle(_ context.Context, id string, userID int64, title string) (model.Conversation, error) {
	if f.updateTitleErr != nil {
		return model.Conversation{}, f.updateTitleErr
	}
	if f.updateTitleResp.ID != "" {
		return f.updateTitleResp, nil
	}
	return model.Conversation{ID: id, UserID: userID, Title: title}, nil
}

type fakeMsgLister struct{ msgs []model.Message }

func (f fakeMsgLister) ListByConversation(context.Context, string) ([]model.Message, error) {
	return f.msgs, nil
}

type fakeScheduledConversationPauser struct {
	calls  int
	linked bool
	err    error
	userID int64
	id     string
}

func (f *fakeScheduledConversationPauser) PauseByConversation(_ context.Context, id string, userID int64) (bool, error) {
	f.calls++
	f.id, f.userID = id, userID
	return f.linked, f.err
}

func withUser(r *http.Request, id int64) *http.Request { //nolint:unparam
	return r.WithContext(auth.ContextWithUser(r.Context(), &model.User{ID: id, Username: "u", Role: model.RoleUser}))
}

func withChiParam(r *http.Request, param, val string) *http.Request { //nolint:unparam
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(param, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestChatSendStreamsSSE(t *testing.T) {
	fs := &fakeStreamer{}
	h := handlers.NewChat(fs, &fakeConvLister{}, fakeMsgLister{})

	req := withUser(httptest.NewRequest(http.MethodPost, "/api/chat",
		strings.NewReader(`{"message":"hello there"}`)), 7)
	rec := httptest.NewRecorder()
	h.Send(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"meta"`) || !strings.Contains(body, `"type":"done"`) {
		t.Fatalf("sse body missing events: %s", body)
	}
	if fs.gotText != "hello there" {
		t.Fatalf("streamer got %q", fs.gotText)
	}
}

func TestListConversations(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{list: []model.Conversation{{ID: "conv-uuid-1", Title: "a"}}}, fakeMsgLister{})
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/conversations", nil), 7)
	rec := httptest.NewRecorder()
	h.ListConversations(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"a"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMessagesSuccess(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{},
		fakeMsgLister{msgs: []model.Message{{ID: 1, Role: model.MsgRoleUser, Content: "hi"}}})
	req := withChiParam(withUser(httptest.NewRequest(http.MethodGet, "/api/conversations/1/messages", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.Messages(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hi"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMessagesEmptyID(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{})
	req := withChiParam(withUser(httptest.NewRequest(http.MethodGet, "/api/conversations//messages", nil), 7), "id", "")
	rec := httptest.NewRecorder()
	h.Messages(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestMessagesOwnershipMiss(t *testing.T) {
	convErr := &convNotFoundErr{}
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{getByIDError: convErr}, fakeMsgLister{})
	req := withChiParam(withUser(httptest.NewRequest(http.MethodGet, "/api/conversations/1/messages", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.Messages(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

type convNotFoundErr struct{}

func (*convNotFoundErr) Error() string { return "not found" }

func TestDeleteConversationSuccess(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{})
	req := withChiParam(withUser(httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteConversation(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteConversationEmptyID(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{})
	req := withChiParam(withUser(httptest.NewRequest(http.MethodDelete, "/api/conversations/", nil), 7), "id", "")
	rec := httptest.NewRecorder()
	h.DeleteConversation(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestDeleteScheduledConversationPausesAndSoftPreservesIt(t *testing.T) {
	pauser := &fakeScheduledConversationPauser{linked: true}
	convs := &fakeConvLister{}
	h := handlers.NewChat(&fakeStreamer{}, convs, fakeMsgLister{}, pauser)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteConversation(rec, req)
	if rec.Code != http.StatusOK || pauser.calls != 1 || pauser.id != "1" || pauser.userID != 7 || convs.deleteCalls != 0 {
		t.Fatalf("status=%d pauses=%d id=%q owner=%d deletes=%d", rec.Code, pauser.calls, pauser.id, pauser.userID, convs.deleteCalls)
	}
}

func TestDeleteOrdinaryConversationPreservesDeleteSemanticsWhenScheduledEnabled(t *testing.T) {
	for _, tc := range []struct {
		name       string
		deleteErr  error
		wantStatus int
	}{
		{name: "success", wantStatus: http.StatusOK},
		{name: "not found keeps legacy status", deleteErr: store.ErrNotFound, wantStatus: http.StatusInternalServerError},
		{name: "internal", deleteErr: errors.New("db unavailable"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			convs := &fakeConvLister{deleteError: tc.deleteErr}
			pauser := &fakeScheduledConversationPauser{}
			h := handlers.NewChat(&fakeStreamer{}, convs, fakeMsgLister{}, pauser)
			req := withChiParam(withUser(httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil), 7), "id", "1")
			rec := httptest.NewRecorder()
			h.DeleteConversation(rec, req)
			if rec.Code != tc.wantStatus || pauser.calls != 1 || convs.deleteCalls != 1 {
				t.Fatalf("status=%d pauses=%d deletes=%d body=%s", rec.Code, pauser.calls, convs.deleteCalls, rec.Body.String())
			}
		})
	}
}

func TestDeleteConversationScheduledLookupFailureIsInternal(t *testing.T) {
	convs := &fakeConvLister{}
	pauser := &fakeScheduledConversationPauser{err: errors.New("db unavailable")}
	h := handlers.NewChat(&fakeStreamer{}, convs, fakeMsgLister{}, pauser)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteConversation(rec, req)
	if rec.Code != http.StatusInternalServerError || convs.deleteCalls != 0 {
		t.Fatalf("status=%d deletes=%d body=%s", rec.Code, convs.deleteCalls, rec.Body.String())
	}
}

func patchReq(t *testing.T, body string) *http.Request { //nolint:unparam
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/conversations/1", strings.NewReader(body))
	return withChiParam(withUser(req, 7), "id", "1")
}

func TestPatchConversationSuccess(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{})
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `{"title":"  New title  "}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"New title"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchConversationEmptyID(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{})
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, withChiParam(withUser(httptest.NewRequest(http.MethodPatch, "/api/conversations/", strings.NewReader(`{"title":"x"}`)), 7), "id", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestPatchConversationBlankTitle(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{})
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `{"title":"   "}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestPatchConversationTitleTooLong(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{})
	rec := httptest.NewRecorder()
	longTitle := strings.Repeat("x", 61)
	h.PatchConversation(rec, patchReq(t, `{"title":"`+longTitle+`"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestPatchConversationInvalidBody(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{})
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `not json`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestPatchConversationNotFound(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{updateTitleErr: store.ErrNotFound}, fakeMsgLister{})
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `{"title":"new"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

func TestPatchConversationRepoError(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{updateTitleErr: &convNotFoundErr{}}, fakeMsgLister{})
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `{"title":"new"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500 for a generic repo error", rec.Code)
	}
}
