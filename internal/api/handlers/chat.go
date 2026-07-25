package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
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

// ChatTurnStreamer runs a streaming chat turn with files and explicit
// knowledge-document references.
type ChatTurnStreamer interface {
	StreamTurn(
		ctx context.Context,
		userID int64,
		uc chat.UserContext,
		conversationID string,
		input chat.TurnInput,
		sink chat.EventSink,
	) error
}

// ChatRewriter runs destructive edit/regenerate chat turns.
type ChatRewriter interface {
	Edit(ctx context.Context, userID int64, uc chat.UserContext, conversationID string, messageID int64, text string, sink chat.EventSink) error
	Regenerate(ctx context.Context, userID int64, uc chat.UserContext, conversationID string, messageID int64, sink chat.EventSink) error
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
	GetByID(ctx context.Context, conversationID string, messageID int64) (model.Message, error)
}

// ScheduledConversationPauser preserves the definition/audit relationship
// when a linked Scheduled conversation is removed from the ordinary chat UI.
type ScheduledConversationPauser interface {
	PauseByConversation(ctx context.Context, conversationID string, userID int64) (bool, error)
}

// Chat handles the chat + conversation HTTP endpoints.
type Chat struct {
	svc            ChatStreamer
	rewriter       ChatRewriter
	convs          ConvLister
	msgs           MsgLister
	scheduled      ScheduledConversationPauser
	uploadMaxBytes int64
}

// NewChat constructs the Chat handler.
func NewChat(svc ChatStreamer, convs ConvLister, msgs MsgLister, scheduled ...ScheduledConversationPauser) *Chat {
	return NewChatWithUploadLimit(svc, convs, msgs, defaultChatUploadMaxBytes, scheduled...)
}

// NewChatWithUploadLimit constructs the Chat handler with the aggregate byte
// limit applied to files in one multipart turn.
func NewChatWithUploadLimit(
	svc ChatStreamer,
	convs ConvLister,
	msgs MsgLister,
	uploadMaxBytes int,
	scheduled ...ScheduledConversationPauser,
) *Chat {
	if uploadMaxBytes <= 0 {
		uploadMaxBytes = defaultChatUploadMaxBytes
	}
	h := &Chat{
		svc: svc, convs: convs, msgs: msgs,
		uploadMaxBytes: int64(uploadMaxBytes),
	}
	h.rewriter, _ = svc.(ChatRewriter)
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
	mediaType, _, mediaTypeErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaTypeErr == nil && mediaType == "multipart/form-data" {
		conversationID, input, err := parseMultipartChat(w, r, h.uploadMaxBytes)
		if err != nil {
			respondMultipartChatError(w, err)
			return
		}
		streamer, ok := h.svc.(ChatTurnStreamer)
		if !ok {
			RespondError(w, http.StatusInternalServerError, "chat file uploads are unavailable")
			return
		}
		uc := chat.UserContext{
			Username: u.Username, UnitSystem: u.UnitSystem,
			Location: u.Location, AboutMe: u.AboutMe,
		}
		h.streamSSE(w, func(sink chat.EventSink) {
			_ = streamer.StreamTurn(r.Context(), u.ID, uc, conversationID, input, sink)
		})
		return
	}
	if mediaTypeErr != nil && strings.HasPrefix(
		strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data",
	) {
		RespondError(w, http.StatusBadRequest, "invalid multipart chat request")
		return
	}

	var body struct {
		ConversationID string `json:"conversationId"`
		Message        string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
		RespondError(w, http.StatusBadRequest, "message is required")
		return
	}

	uc := chat.UserContext{Username: u.Username, UnitSystem: u.UnitSystem, Location: u.Location, AboutMe: u.AboutMe}
	h.streamSSE(w, func(sink chat.EventSink) {
		_ = h.svc.Stream(r.Context(), u.ID, uc, body.ConversationID, body.Message, sink)
	})
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
	ID                 int64                         `json:"id"`
	Role               string                        `json:"role"`
	Content            string                        `json:"content"`
	Attachments        []chat.EventAttachment        `json:"attachments"`
	DocumentReferences []chat.EventDocumentReference `json:"documentReferences"`
}

func toMessageDTO(message model.Message) messageDTO {
	attachments := make([]chat.EventAttachment, 0, len(message.Attachments))
	for _, attachment := range message.Attachments {
		attachments = append(attachments, chat.EventAttachment{
			ID: attachment.ID, Filename: attachment.Filename,
			MIME: attachment.MIME, Kind: attachment.Kind, SizeBytes: attachment.SizeBytes,
			ImageWidth: attachment.ImageWidth, ImageHeight: attachment.ImageHeight,
			Ordinal: attachment.Ordinal,
		})
	}
	references := make([]chat.EventDocumentReference, 0, len(message.DocumentReferences))
	for _, reference := range message.DocumentReferences {
		references = append(references, chat.EventDocumentReference{
			ID: reference.ID, DocumentID: reference.DocumentID,
			Filename: reference.Filename, Scope: reference.Scope,
			Ordinal: reference.Ordinal, Available: reference.Available,
		})
	}
	return messageDTO{
		ID: message.ID, Role: message.Role, Content: message.Content,
		Attachments: attachments, DocumentReferences: references,
	}
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
		out = append(out, toMessageDTO(m))
	}
	RespondJSON(w, http.StatusOK, out)
}

// EditMessage handles POST /api/conversations/{id}/messages/{messageId}/edit.
func (h *Chat) EditMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	if body.Message == "" {
		RespondError(w, http.StatusBadRequest, "message is required")
		return
	}
	conversationID, messageID, ok := h.validateMessageAction(w, r, model.MsgRoleUser)
	if !ok {
		return
	}
	u := auth.UserFromContext(r.Context())
	uc := chat.UserContext{Username: u.Username, UnitSystem: u.UnitSystem, Location: u.Location, AboutMe: u.AboutMe}
	if h.rewriter == nil {
		RespondError(w, http.StatusInternalServerError, "message editing is unavailable")
		return
	}
	h.streamSSE(w, func(sink chat.EventSink) {
		_ = h.rewriter.Edit(r.Context(), u.ID, uc, conversationID, messageID, body.Message, sink)
	})
}

// RegenerateMessage handles
// POST /api/conversations/{id}/messages/{messageId}/regenerate.
func (h *Chat) RegenerateMessage(w http.ResponseWriter, r *http.Request) {
	conversationID, messageID, ok := h.validateMessageAction(w, r, model.MsgRoleAssistant)
	if !ok {
		return
	}
	u := auth.UserFromContext(r.Context())
	uc := chat.UserContext{Username: u.Username, UnitSystem: u.UnitSystem, Location: u.Location, AboutMe: u.AboutMe}
	if h.rewriter == nil {
		RespondError(w, http.StatusInternalServerError, "response regeneration is unavailable")
		return
	}
	h.streamSSE(w, func(sink chat.EventSink) {
		_ = h.rewriter.Regenerate(r.Context(), u.ID, uc, conversationID, messageID, sink)
	})
}

func (h *Chat) validateMessageAction(
	w http.ResponseWriter, r *http.Request, role string,
) (string, int64, bool) {
	conversationID := chi.URLParam(r, "id")
	messageID, err := strconv.ParseInt(chi.URLParam(r, "messageId"), 10, 64)
	if conversationID == "" || err != nil || messageID <= 0 {
		RespondError(w, http.StatusBadRequest, "valid conversation and message ids are required")
		return "", 0, false
	}
	u := auth.UserFromContext(r.Context())
	conversation, err := h.convs.GetByID(r.Context(), conversationID, u.ID)
	if err != nil || conversation.Kind != model.ConversationKindChat {
		RespondError(w, http.StatusNotFound, "conversation not found")
		return "", 0, false
	}
	message, err := h.msgs.GetByID(r.Context(), conversationID, messageID)
	if errors.Is(err, store.ErrNotFound) {
		RespondError(w, http.StatusNotFound, "message not found")
		return "", 0, false
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not load message")
		return "", 0, false
	}
	if message.Role != role {
		RespondError(w, http.StatusConflict, "message has wrong role")
		return "", 0, false
	}
	return conversationID, messageID, true
}

func (h *Chat) streamSSE(w http.ResponseWriter, run func(chat.EventSink)) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sink := &sseSink{w: w, rc: http.NewResponseController(w)}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(sseKeepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = sink.keepalive()
			}
		}
	}()
	run(sink)
	close(done)
	<-stopped
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
