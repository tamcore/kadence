package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

// sseKeepaliveInterval is how often an SSE comment line is written to keep
// proxies from closing an idle connection during long tool-loop turns.
// sseKeepaliveInterval is a var (not const) so tests can shorten it.
var sseKeepaliveInterval = 15 * time.Second

// ChatStreamer runs a streaming chat turn.
type ChatStreamer interface {
	Stream(ctx context.Context, userID int64, uc chat.UserContext, conversationID string, text string, sink chat.EventSink) error
}

// ConvLister lists/gets/renames/deletes conversations for a user.
type ConvLister interface {
	ListByUser(ctx context.Context, userID int64) ([]model.Conversation, error)
	GetByID(ctx context.Context, id string, userID int64) (model.Conversation, error)
	UpdateTitle(ctx context.Context, id string, userID int64, title string) (model.Conversation, error)
	Delete(ctx context.Context, id string, userID int64) error
}

// MsgLister lists messages for a conversation.
type MsgLister interface {
	ListByConversation(ctx context.Context, conversationID string) ([]model.Message, error)
}

// ScheduledConversationPauser preserves the definition/audit relationship
// when a linked Scheduled conversation is removed from the ordinary chat UI.
type ScheduledConversationPauser interface {
	PauseByConversation(ctx context.Context, conversationID string, userID int64) (bool, error)
}

// Chat handles the chat + conversation HTTP endpoints.
type Chat struct {
	svc       ChatStreamer
	convs     ConvLister
	msgs      MsgLister
	scheduled ScheduledConversationPauser
}

// NewChat constructs the Chat handler.
func NewChat(svc ChatStreamer, convs ConvLister, msgs MsgLister, scheduled ...ScheduledConversationPauser) *Chat {
	h := &Chat{svc: svc, convs: convs, msgs: msgs}
	if len(scheduled) > 0 {
		h.scheduled = scheduled[0]
	}
	return h
}

// sseSink writes chat.ChatEvent as SSE frames. mu guards w against concurrent
// writes from the Stream goroutine and the keepalive ticker goroutine.
type sseSink struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *sseSink) Send(e chat.ChatEvent) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = fmt.Fprintf(s.w, "data: %s\n\n", b)
	return err
}

func (s *sseSink) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rc.Flush()
}

// keepalive writes an SSE comment line to keep proxies from closing an idle
// connection during long tool-loop turns.
func (s *sseSink) keepalive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprint(s.w, ": keepalive\n\n"); err != nil {
		return err
	}
	return s.rc.Flush()
}

// Send handles POST /api/chat (SSE stream).
func (h *Chat) Send(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	var body struct {
		ConversationID string `json:"conversationId"`
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
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(sseKeepaliveInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = sink.keepalive()
			}
		}
	}()
	uc := chat.UserContext{Username: u.Username, UnitSystem: u.UnitSystem, Location: u.Location, AboutMe: u.AboutMe}
	_ = h.svc.Stream(r.Context(), u.ID, uc, body.ConversationID, body.Message, sink)
	// Signal the keepalive goroutine to stop and wait for it to exit before
	// returning, so it can never write to w after the handler has returned.
	close(done)
	<-stopped
}

type conversationDTO struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
}

// ListConversations handles GET /api/conversations.
func (h *Chat) ListConversations(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
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
	id := chi.URLParam(r, "id")
	if id == "" {
		RespondError(w, http.StatusBadRequest, "id is required")
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

// PatchConversation handles PATCH /api/conversations/{id}, currently supporting
// only a title rename. Reuses chat.TitleMaxLen so explicit user renames are
// bound by the same limit as auto-derived titles.
func (h *Chat) PatchConversation(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if id == "" {
		RespondError(w, http.StatusBadRequest, "id is required")
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		RespondError(w, http.StatusBadRequest, "title is required")
		return
	}
	if len([]rune(title)) > chat.TitleMaxLen {
		RespondError(w, http.StatusBadRequest, fmt.Sprintf("title must be %d characters or fewer", chat.TitleMaxLen))
		return
	}

	updated, err := h.convs.UpdateTitle(r.Context(), id, u.ID, title)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			RespondError(w, http.StatusNotFound, "conversation not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "could not rename conversation")
		return
	}
	RespondJSON(w, http.StatusOK, conversationDTO{
		ID: updated.ID, Title: updated.Title,
		CreatedAt: updated.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// DeleteConversation handles DELETE /api/conversations/{id}.
func (h *Chat) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if id == "" {
		RespondError(w, http.StatusBadRequest, "id is required")
		return
	}
	if h.scheduled != nil {
		linked, err := h.scheduled.PauseByConversation(r.Context(), id, u.ID)
		if err != nil {
			if errors.Is(err, store.ErrScheduledRunInProgress) {
				RespondError(w, http.StatusConflict, "scheduled task conflict")
				return
			}
			RespondError(w, http.StatusInternalServerError, "could not pause scheduled task")
			return
		}
		if linked {
			// scheduled_tasks.conversation_id is intentionally RESTRICT. Keep
			// this Scheduled thread soft-preserved after pausing its live task so
			// definitions and immutable runs remain auditable.
			RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	}
	if err := h.convs.Delete(r.Context(), id, u.ID); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not delete conversation")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
