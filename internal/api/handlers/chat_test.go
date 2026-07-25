package handlers_test

import (
	"context"
	"encoding/json"
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
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/store"
)

const (
	chatTestConversationID = "chat-conv-1"
	chatTestTimezone       = "UTC"
	messageIDParam         = "messageId"
	editedMessageJSON      = `{"message":"edited"}`
)

type fakeStreamer struct {
	gotText           string
	gotConversationID string
	gotMessageID      int64
	gotAction         string
	gotUserContext    chat.UserContext
}

func (f *fakeStreamer) Stream(_ context.Context, _ int64, uc chat.UserContext, _ string, text string, sink chat.EventSink) error {
	f.gotUserContext = uc
	f.gotText = text
	_ = sink.Send(chat.ChatEvent{Type: chat.EventMeta, ConversationID: "conv-uuid-1"})
	_ = sink.Send(chat.ChatEvent{Type: chat.EventToken, Delta: "hi"})
	_ = sink.Send(chat.ChatEvent{Type: chat.EventDone})
	return sink.Flush()
}
func (f *fakeStreamer) Edit(
	_ context.Context, _ int64, uc chat.UserContext, conversationID string,
	messageID int64, text string, sink chat.EventSink,
) error {
	f.gotAction, f.gotConversationID, f.gotMessageID, f.gotText, f.gotUserContext = "edit", conversationID, messageID, text, uc
	_ = sink.Send(chat.ChatEvent{Type: chat.EventMeta, ConversationID: conversationID, UserMessageID: messageID})
	_ = sink.Send(chat.ChatEvent{Type: chat.EventDone, AssistantMessageID: messageID + 1})
	return sink.Flush()
}
func (f *fakeStreamer) Regenerate(
	_ context.Context, _ int64, uc chat.UserContext, conversationID string,
	messageID int64, sink chat.EventSink,
) error {
	f.gotAction, f.gotConversationID, f.gotMessageID, f.gotUserContext = "regenerate", conversationID, messageID, uc
	_ = sink.Send(chat.ChatEvent{Type: chat.EventMeta, ConversationID: conversationID, UserMessageID: messageID - 1})
	_ = sink.Send(chat.ChatEvent{Type: chat.EventDone, AssistantMessageID: messageID + 1})
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

type fakeMsgLister struct {
	msgs   []model.Message
	byID   model.Message
	getErr error
}

func (f fakeMsgLister) ListByConversation(context.Context, string) ([]model.Message, error) {
	return f.msgs, nil
}
func (f fakeMsgLister) GetByID(context.Context, string, int64) (model.Message, error) {
	if f.getErr != nil {
		return model.Message{}, f.getErr
	}
	return f.byID, nil
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

type fakeChatArtifactHydrator struct {
	calls          int
	owner          int64
	conversationID string
	messageIDs     []int64
	artifacts      map[int64][]scheduled.ChatArtifact
	err            error
}

func (f *fakeChatArtifactHydrator) HydrateChatArtifacts(
	_ context.Context, owner int64, conversationID string, messageIDs []int64,
) (map[int64][]scheduled.ChatArtifact, error) {
	f.calls++
	f.owner, f.conversationID = owner, conversationID
	f.messageIDs = append([]int64(nil), messageIDs...)
	return f.artifacts, f.err
}

func withUser(r *http.Request, id int64) *http.Request { //nolint:unparam
	return r.WithContext(auth.ContextWithUser(r.Context(), &model.User{ID: id, Username: "u", Role: model.RoleUser, Timezone: chatTestTimezone}))
}

func withChiParam(r *http.Request, param, val string) *http.Request { //nolint:unparam
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(param, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for name, value := range params {
		rctx.URLParams.Add(name, value)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestChatSendStreamsSSE(t *testing.T) {
	fs := &fakeStreamer{}
	h := handlers.NewChat(fs, &fakeConvLister{}, fakeMsgLister{}, nil, nil)

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
	if fs.gotUserContext.Timezone != chatTestTimezone {
		t.Fatalf("streamer timezone=%q", fs.gotUserContext.Timezone)
	}
}

func TestListConversations(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{list: []model.Conversation{{ID: "conv-uuid-1", Title: "a"}}}, fakeMsgLister{}, nil, nil)
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/conversations", nil), 7)
	rec := httptest.NewRecorder()
	h.ListConversations(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"a"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMessagesSuccess(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{},
		fakeMsgLister{msgs: []model.Message{{ID: 1, Role: model.MsgRoleUser, Content: "hi"}}}, nil, nil)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodGet, "/api/conversations/1/messages", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.Messages(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":1`) ||
		!strings.Contains(rec.Body.String(), `"hi"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestChatMessagesHydratesScheduledArtifactsOnce(t *testing.T) {
	hydrator := &fakeChatArtifactHydrator{artifacts: map[int64][]scheduled.ChatArtifact{
		9: {
			{HandoffID: "handoff-2", TaskID: "task-2", Ordinal: 2, ArtifactState: "ready", TaskState: "draft", Version: 1},
			{HandoffID: "handoff-1", TaskID: "task-1", Ordinal: 1, ArtifactState: "ready", TaskState: "draft", Version: 1,
				Proposal: &scheduled.Proposal{Version: 1, Name: "Morning check"}},
		},
	}}
	messages := fakeMsgLister{msgs: []model.Message{
		{ID: 1, Role: model.MsgRoleSystem, Content: "hidden"},
		{ID: 2, Role: model.MsgRoleUser, Content: "first"},
		{ID: 9, Role: model.MsgRoleAssistant, Content: "I prepared both checks."},
		{ID: 11, Role: model.MsgRoleUser, Content: "thanks"},
		{ID: 12, Role: model.MsgRoleAssistant, Content: "You're welcome."},
	}}
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, messages, nil, hydrator)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodGet, "/api/conversations/chat-conv-1/messages", nil), 7), "id", chatTestConversationID)
	rec := httptest.NewRecorder()

	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if hydrator.calls != 1 || hydrator.owner != 7 || hydrator.conversationID != chatTestConversationID ||
		len(hydrator.messageIDs) != 2 || hydrator.messageIDs[0] != 9 || hydrator.messageIDs[1] != 12 {
		t.Fatalf("hydration call=%d owner=%d conversation=%q messageIDs=%v", hydrator.calls, hydrator.owner, hydrator.conversationID, hydrator.messageIDs)
	}
	var response struct {
		Data []struct {
			ID                 int64                    `json:"id"`
			ScheduledArtifacts []scheduled.ChatArtifact `json:"scheduledArtifacts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := response.Data
	if len(got) != 4 || got[0].ID != 2 || got[1].ID != 9 || got[2].ID != 11 || got[3].ID != 12 {
		t.Fatalf("messages are not chronological or system message leaked: %+v", got)
	}
	if len(got[1].ScheduledArtifacts) != 2 || got[1].ScheduledArtifacts[0].Ordinal != 1 ||
		got[1].ScheduledArtifacts[1].Ordinal != 2 || got[1].ScheduledArtifacts[0].Proposal == nil {
		t.Fatalf("artifacts = %+v", got[1].ScheduledArtifacts)
	}
	if strings.Contains(rec.Body.String(), `{"id":12,"role":"assistant","content":"You're welcome.","scheduledArtifacts"`) {
		t.Fatalf("text-only assistant message should omit artifacts: %s", rec.Body.String())
	}
}

func TestChatMessagesReturnsSafeErrorWhenArtifactHydrationFails(t *testing.T) {
	hydrator := &fakeChatArtifactHydrator{err: errors.New("database unavailable")}
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{msgs: []model.Message{{ID: 9, Role: model.MsgRoleAssistant, Content: "reply"}}}, nil, hydrator)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodGet, "/api/conversations/chat-conv-1/messages", nil), 7), "id", chatTestConversationID)
	rec := httptest.NewRecorder()

	h.Messages(rec, req)

	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "database unavailable") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEditMessageStreamsReplacement(t *testing.T) {
	streamer := &fakeStreamer{}
	conversations := &fakeConvLister{getByIDResp: model.Conversation{
		ID: chatTestConversationID, UserID: 7, Kind: model.ConversationKindChat,
	}}
	messages := fakeMsgLister{byID: model.Message{
		ID: 12, ConversationID: chatTestConversationID, Role: model.MsgRoleUser, Content: "old",
	}}
	handler := handlers.NewChat(streamer, conversations, messages, nil, nil)
	request := withChiParams(
		withUser(httptest.NewRequest(http.MethodPost, "/api/conversations/conv-1/messages/12/edit",
			strings.NewReader(`{"message":"  edited prompt  "}`)), 7),
		map[string]string{"id": chatTestConversationID, messageIDParam: "12"})
	response := httptest.NewRecorder()

	handler.EditMessage(response, request)

	if response.Code != http.StatusOK || streamer.gotAction != "edit" ||
		streamer.gotConversationID != chatTestConversationID || streamer.gotMessageID != 12 ||
		streamer.gotText != "edited prompt" {
		t.Fatalf("status=%d streamer=%+v body=%s", response.Code, streamer, response.Body.String())
	}
	if streamer.gotUserContext.Timezone != chatTestTimezone {
		t.Fatalf("edit timezone=%q", streamer.gotUserContext.Timezone)
	}
	if !strings.Contains(response.Body.String(), `"assistantMessageId":13`) {
		t.Fatalf("missing persisted assistant id: %s", response.Body.String())
	}
}

func TestRegenerateMessageStreamsReplacement(t *testing.T) {
	streamer := &fakeStreamer{}
	conversations := &fakeConvLister{getByIDResp: model.Conversation{
		ID: chatTestConversationID, UserID: 7, Kind: model.ConversationKindChat,
	}}
	messages := fakeMsgLister{byID: model.Message{
		ID: 12, ConversationID: chatTestConversationID, Role: model.MsgRoleAssistant, Content: "old",
	}}
	handler := handlers.NewChat(streamer, conversations, messages, nil, nil)
	request := withChiParams(
		withUser(httptest.NewRequest(http.MethodPost, "/api/conversations/conv-1/messages/12/regenerate", nil), 7),
		map[string]string{"id": chatTestConversationID, messageIDParam: "12"})
	response := httptest.NewRecorder()

	handler.RegenerateMessage(response, request)

	if response.Code != http.StatusOK || streamer.gotAction != "regenerate" ||
		streamer.gotConversationID != chatTestConversationID || streamer.gotMessageID != 12 {
		t.Fatalf("status=%d streamer=%+v body=%s", response.Code, streamer, response.Body.String())
	}
	if streamer.gotUserContext.Timezone != chatTestTimezone {
		t.Fatalf("regenerate timezone=%q", streamer.gotUserContext.Timezone)
	}
}

func TestMessageRewriteValidation(t *testing.T) {
	tests := []struct {
		name       string
		pathID     string
		convs      *fakeConvLister
		messages   fakeMsgLister
		body       string
		regenerate bool
		wantStatus int
	}{
		{
			name: "invalid message id", pathID: "bad", body: editedMessageJSON,
			convs: &fakeConvLister{}, wantStatus: http.StatusBadRequest,
		},
		{
			name: "blank edit", pathID: "12", body: `{"message":"   "}`,
			convs: &fakeConvLister{}, wantStatus: http.StatusBadRequest,
		},
		{
			name: "conversation missing", pathID: "12", body: editedMessageJSON,
			convs: &fakeConvLister{getByIDError: store.ErrNotFound}, wantStatus: http.StatusNotFound,
		},
		{
			name: "scheduled conversation", pathID: "12", body: editedMessageJSON,
			convs: &fakeConvLister{getByIDResp: model.Conversation{
				ID: chatTestConversationID, UserID: 7, Kind: model.ConversationKindScheduled,
			}}, wantStatus: http.StatusNotFound,
		},
		{
			name: "message missing", pathID: "12", body: editedMessageJSON,
			convs: &fakeConvLister{getByIDResp: model.Conversation{
				ID: chatTestConversationID, UserID: 7, Kind: model.ConversationKindChat,
			}},
			messages: fakeMsgLister{getErr: store.ErrNotFound}, wantStatus: http.StatusNotFound,
		},
		{
			name: "edit assistant conflict", pathID: "12", body: editedMessageJSON,
			convs: &fakeConvLister{getByIDResp: model.Conversation{
				ID: chatTestConversationID, UserID: 7, Kind: model.ConversationKindChat,
			}},
			messages:   fakeMsgLister{byID: model.Message{ID: 12, Role: model.MsgRoleAssistant}},
			wantStatus: http.StatusConflict,
		},
		{
			name: "regenerate user conflict", pathID: "12", regenerate: true,
			convs: &fakeConvLister{getByIDResp: model.Conversation{
				ID: chatTestConversationID, UserID: 7, Kind: model.ConversationKindChat,
			}},
			messages:   fakeMsgLister{byID: model.Message{ID: 12, Role: model.MsgRoleUser}},
			wantStatus: http.StatusConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := handlers.NewChat(&fakeStreamer{}, test.convs, test.messages, nil, nil)
			request := withChiParams(
				withUser(httptest.NewRequest(http.MethodPost, "/rewrite", strings.NewReader(test.body)), 7),
				map[string]string{"id": chatTestConversationID, messageIDParam: test.pathID})
			response := httptest.NewRecorder()
			if test.regenerate {
				handler.RegenerateMessage(response, request)
			} else {
				handler.EditMessage(response, request)
			}
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestMessagesEmptyID(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{}, nil, nil)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodGet, "/api/conversations//messages", nil), 7), "id", "")
	rec := httptest.NewRecorder()
	h.Messages(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestMessagesOwnershipMiss(t *testing.T) {
	convErr := &convNotFoundErr{}
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{getByIDError: convErr}, fakeMsgLister{}, nil, nil)
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
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{}, nil, nil)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteConversation(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteConversationEmptyID(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{}, nil, nil)
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
	h := handlers.NewChat(&fakeStreamer{}, convs, fakeMsgLister{}, pauser, nil)
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
			h := handlers.NewChat(&fakeStreamer{}, convs, fakeMsgLister{}, pauser, nil)
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
	h := handlers.NewChat(&fakeStreamer{}, convs, fakeMsgLister{}, pauser, nil)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteConversation(rec, req)
	if rec.Code != http.StatusInternalServerError || convs.deleteCalls != 0 {
		t.Fatalf("status=%d deletes=%d body=%s", rec.Code, convs.deleteCalls, rec.Body.String())
	}
}

func TestDeleteConversationScheduledRunConflict(t *testing.T) {
	convs := &fakeConvLister{}
	pauser := &fakeScheduledConversationPauser{err: store.ErrScheduledRunInProgress}
	h := handlers.NewChat(&fakeStreamer{}, convs, fakeMsgLister{}, pauser, nil)
	req := withChiParam(withUser(httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil), 7), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteConversation(rec, req)
	if rec.Code != http.StatusConflict || convs.deleteCalls != 0 {
		t.Fatalf("status=%d deletes=%d body=%s", rec.Code, convs.deleteCalls, rec.Body.String())
	}
}

func patchReq(t *testing.T, body string) *http.Request { //nolint:unparam
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/conversations/1", strings.NewReader(body))
	return withChiParam(withUser(req, 7), "id", "1")
}

func TestPatchConversationSuccess(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{}, nil, nil)
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `{"title":"  New title  "}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"New title"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchConversationEmptyID(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{}, nil, nil)
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, withChiParam(withUser(httptest.NewRequest(http.MethodPatch, "/api/conversations/", strings.NewReader(`{"title":"x"}`)), 7), "id", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestPatchConversationBlankTitle(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{}, nil, nil)
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `{"title":"   "}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestPatchConversationTitleTooLong(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{}, nil, nil)
	rec := httptest.NewRecorder()
	longTitle := strings.Repeat("x", 61)
	h.PatchConversation(rec, patchReq(t, `{"title":"`+longTitle+`"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestPatchConversationInvalidBody(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{}, fakeMsgLister{}, nil, nil)
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `not json`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestPatchConversationNotFound(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{updateTitleErr: store.ErrNotFound}, fakeMsgLister{}, nil, nil)
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `{"title":"new"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

func TestPatchConversationRepoError(t *testing.T) {
	h := handlers.NewChat(&fakeStreamer{}, &fakeConvLister{updateTitleErr: &convNotFoundErr{}}, fakeMsgLister{}, nil, nil)
	rec := httptest.NewRecorder()
	h.PatchConversation(rec, patchReq(t, `{"title":"new"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500 for a generic repo error", rec.Code)
	}
}
