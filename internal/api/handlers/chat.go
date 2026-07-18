package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
)

// ChatStreamer runs a streaming chat turn.
type ChatStreamer interface {
	Stream(ctx context.Context, userID, conversationID int64, text string, sink chat.EventSink) error
}

// ConvLister lists/gets/deletes conversations for a user.
type ConvLister interface {
	ListByUser(ctx context.Context, userID int64) ([]model.Conversation, error)
	GetByID(ctx context.Context, id, userID int64) (model.Conversation, error)
	Delete(ctx context.Context, id, userID int64) error
}

// MsgLister lists messages for a conversation.
type MsgLister interface {
	ListByConversation(ctx context.Context, conversationID int64) ([]model.Message, error)
}

// Chat handles the chat + conversation HTTP endpoints.
type Chat struct {
	svc   ChatStreamer
	convs ConvLister
	msgs  MsgLister
}

// NewChat constructs the Chat handler.
func NewChat(svc ChatStreamer, convs ConvLister, msgs MsgLister) *Chat {
	return &Chat{svc: svc, convs: convs, msgs: msgs}
}

// sseSink writes chat.ChatEvent as SSE frames.
type sseSink struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *sseSink) Send(e chat.ChatEvent) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.w, "data: %s\n\n", b)
	return err
}
func (s *sseSink) Flush() error { return s.rc.Flush() }

// Send handles POST /api/chat (SSE stream).
func (h *Chat) Send(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var body struct {
		ConversationID int64  `json:"conversationId"`
		Message        string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
		RespondError(w, http.StatusBadRequest, "message is required")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sink := &sseSink{w: w, rc: http.NewResponseController(w)}
	_ = h.svc.Stream(r.Context(), u.ID, body.ConversationID, body.Message, sink)
}

type conversationDTO struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
}

// ListConversations handles GET /api/conversations.
func (h *Chat) ListConversations(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	list, err := h.convs.ListByUser(r.Context(), u.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list conversations")
		return
	}
	out := make([]conversationDTO, 0, len(list))
	for _, c := range list {
		out = append(out, conversationDTO{ID: c.ID, Title: c.Title, CreatedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
	}
	RespondJSON(w, http.StatusOK, out)
}

type messageDTO struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Messages handles GET /api/conversations/{id}/messages (ownership enforced).
func (h *Chat) Messages(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid conversation id")
		return
	}
	if _, err := h.convs.GetByID(r.Context(), id, u.ID); err != nil {
		RespondError(w, http.StatusNotFound, "conversation not found")
		return
	}
	msgs, err := h.msgs.ListByConversation(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not load messages")
		return
	}
	out := make([]messageDTO, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == model.MsgRoleSystem {
			continue
		}
		out = append(out, messageDTO{Role: m.Role, Content: m.Content})
	}
	RespondJSON(w, http.StatusOK, out)
}

// DeleteConversation handles DELETE /api/conversations/{id}.
func (h *Chat) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid conversation id")
		return
	}
	if err := h.convs.Delete(r.Context(), id, u.ID); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not delete conversation")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
