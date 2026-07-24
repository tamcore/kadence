package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/store"
)

// ScheduledLifecycle is the HTTP seam for owner-scoped Scheduled operations.
type ScheduledLifecycle interface {
	Create(context.Context, scheduled.Actor, string) (scheduled.DefinitionResult, error)
	Refine(context.Context, scheduled.Actor, string, string) (scheduled.DefinitionResult, error)
	Confirm(context.Context, scheduled.Actor, string, int) (model.ScheduledTask, error)
	List(context.Context, int64, int) (scheduled.ListResult, error)
	Detail(context.Context, int64, string) (scheduled.Detail, error)
	Pause(context.Context, int64, string) (model.ScheduledTask, error)
	Resume(context.Context, int64, string) (model.ScheduledTask, error)
	Delete(context.Context, int64, string) error
	RunNow(context.Context, int64, string) (model.ScheduledTaskRun, error)
	MarkRead(context.Context, int64, string) error
}

// Scheduled exposes the opt-in authenticated definition and lifecycle API.
type Scheduled struct{ service ScheduledLifecycle }

func NewScheduled(service ScheduledLifecycle) *Scheduled { return &Scheduled{service: service} }

const (
	scheduledEventType   = "type"
	scheduledEventError  = "error"
	scheduledEventDone   = "done"
	maxScheduledSSEBytes = 128 << 10
)

type scheduledTaskDTO struct {
	ID               string           `json:"id"`
	ConversationID   string           `json:"conversationId"`
	Version          int              `json:"version"`
	Name             string           `json:"name"`
	Kind             string           `json:"kind"`
	State            string           `json:"state"`
	CompiledPrompt   string           `json:"compiledPrompt"`
	OneOffAt         *string          `json:"oneOffAt,omitempty"`
	DTStart          *string          `json:"dtStart,omitempty"`
	RRULE            string           `json:"rrule,omitempty"`
	Timezone         string           `json:"timezone"`
	ExecutionMode    string           `json:"executionMode"`
	AuthorizedTools  []string         `json:"authorizedTools"`
	DeliveryPolicy   string           `json:"deliveryPolicy"`
	InitialRun       string           `json:"initialRun"`
	StopCondition    string           `json:"stopCondition,omitempty"`
	StaticMessage    string           `json:"staticMessage,omitempty"`
	NextRunAt        *string          `json:"nextRunAt,omitempty"`
	LastRunAt        *string          `json:"lastRunAt,omitempty"`
	ConsecutiveFails int              `json:"consecutiveFailures"`
	UnreadCount      int              `json:"unreadCount"`
	RecentRun        *scheduledRunDTO `json:"recentRun,omitempty"`
	CreatedAt        string           `json:"createdAt"`
	UpdatedAt        string           `json:"updatedAt"`
}

type scheduledRunDTO struct {
	ID            int64   `json:"id"`
	OccurrenceKey string  `json:"occurrenceKey"`
	ScheduledFor  string  `json:"scheduledFor"`
	State         string  `json:"state"`
	StartedAt     *string `json:"startedAt,omitempty"`
	FinishedAt    *string `json:"finishedAt,omitempty"`
	Result        string  `json:"result,omitempty"`
	Error         string  `json:"error,omitempty"`
	Unread        bool    `json:"unread"`
	CreatedAt     string  `json:"createdAt"`
}

type scheduledDefinitionMessageDTO struct {
	Role     string                  `json:"role"`
	Text     string                  `json:"text"`
	Question *scheduled.QuestionCard `json:"question,omitempty"`
}

func taskDTO(task model.ScheduledTask) scheduledTaskDTO {
	return scheduledTaskDTO{ID: task.ID, ConversationID: task.ConversationID, Version: task.Version, Name: task.Name,
		Kind: task.Kind, State: task.State, CompiledPrompt: task.CompiledPrompt, OneOffAt: timeString(task.OneOffAt),
		DTStart: timeString(task.DTStart), RRULE: task.RRULE, Timezone: task.Timezone, ExecutionMode: task.ExecutionMode,
		AuthorizedTools: append([]string{}, task.AuthorizedTools...), DeliveryPolicy: task.DeliveryPolicy, InitialRun: task.InitialRun,
		StopCondition: task.StopCondition, StaticMessage: task.StaticMessage, NextRunAt: timeString(task.NextRunAt),
		LastRunAt: timeString(task.LastRunAt), ConsecutiveFails: task.ConsecutiveFailures, CreatedAt: task.CreatedAt.Format(time.RFC3339), UpdatedAt: task.UpdatedAt.Format(time.RFC3339)}
}

func runDTO(run model.ScheduledTaskRun) scheduledRunDTO {
	return scheduledRunDTO{ID: run.ID, OccurrenceKey: run.OccurrenceKey, ScheduledFor: run.ScheduledFor.Format(time.RFC3339),
		State: run.State, StartedAt: timeString(run.StartedAt), FinishedAt: timeString(run.FinishedAt), Result: run.Result,
		Error: run.Error, Unread: run.Unread, CreatedAt: run.CreatedAt.Format(time.RFC3339)}
}

func timeString(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.Format(time.RFC3339)
	return &formatted
}

func scheduledActor(r *http.Request) scheduled.Actor {
	u := auth.UserFromContext(r.Context())
	return scheduled.Actor{ID: u.ID, Username: u.Username, Timezone: u.Timezone}
}

// Create handles POST /api/scheduled/tasks and streams one refinement.
func (h *Scheduled) Create(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		RespondError(w, http.StatusBadRequest, "message is required")
		return
	}
	streamCtx, cancel := context.WithCancel(r.Context())
	stream, stop := beginScheduledStream(w, cancel)
	defer stop()
	result, err := h.service.Create(streamCtx, scheduledActor(r), body.Message)
	if err != nil {
		if stream.event(map[string]any{scheduledEventType: scheduledEventError, scheduledEventError: "could not create scheduled task"}) == nil {
			_ = stream.event(map[string]any{scheduledEventType: scheduledEventDone})
		}
		return
	}
	_ = h.streamDefinition(stream, result)
}

// Refine handles POST /api/scheduled/tasks/{id}/messages.
func (h *Scheduled) Refine(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Message string `json:"message"`
	}
	if id == "" || json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Message) == "" {
		RespondError(w, http.StatusBadRequest, "id and message are required")
		return
	}
	streamCtx, cancel := context.WithCancel(r.Context())
	stream, stop := beginScheduledStream(w, cancel)
	defer stop()
	result, err := h.service.Refine(streamCtx, scheduledActor(r), id, body.Message)
	if err != nil {
		message := "could not refine scheduled task"
		if errors.Is(err, scheduled.ErrDefinitionLimit) {
			message = "refinement limit reached; start a new scheduled task"
		}
		if stream.event(map[string]any{scheduledEventType: scheduledEventError, scheduledEventError: message}) == nil {
			_ = stream.event(map[string]any{scheduledEventType: scheduledEventDone})
		}
		return
	}
	_ = h.streamDefinition(stream, result)
}

func (h *Scheduled) streamDefinition(stream *scheduledSSE, result scheduled.DefinitionResult) error {
	if err := stream.event(map[string]any{scheduledEventType: "meta", "taskId": result.Task.ID, "conversationId": result.Task.ConversationID}); err != nil {
		return err
	}
	if err := stream.event(map[string]any{scheduledEventType: "text", "delta": result.Refinement.Text}); err != nil {
		return err
	}
	if result.Refinement.Question != nil {
		if err := stream.event(map[string]any{scheduledEventType: "task_question", "question": result.Refinement.Question}); err != nil {
			return err
		}
	} else if result.Refinement.Proposal != nil {
		if err := stream.event(map[string]any{scheduledEventType: "task_proposal", "proposal": result.Refinement.Proposal}); err != nil {
			return err
		}
	}
	return stream.event(map[string]any{scheduledEventType: scheduledEventDone})
}

type scheduledSSE struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	rc     *http.ResponseController
	cancel context.CancelFunc
	err    error
	bytes  int
}

func beginScheduledStream(w http.ResponseWriter, cancel context.CancelFunc) (*scheduledSSE, func()) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := &scheduledSSE{w: w, rc: http.NewResponseController(w), cancel: cancel}
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
				if err := stream.keepalive(); err != nil {
					return
				}
			}
		}
	}()
	var once sync.Once
	return stream, func() {
		once.Do(func() {
			close(done)
			<-stopped
			stream.mu.Lock()
			defer stream.mu.Unlock()
			if stream.err == nil {
				_ = stream.failLocked(stream.rc.Flush())
			}
		})
	}
}

func (s *scheduledSSE) event(event any) error {
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return s.write(fmt.Appendf(nil, "data: %s\n\n", b))
}

func (s *scheduledSSE) keepalive() error {
	return s.write([]byte(": keepalive\n\n"))
}

func (s *scheduledSSE) write(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.bytes+len(payload) > maxScheduledSSEBytes {
		return s.failLocked(errors.New("scheduled SSE response exceeds limit"))
	}
	n, err := s.w.Write(payload)
	s.bytes += n
	if err != nil {
		return s.failLocked(err)
	}
	if n != len(payload) {
		return s.failLocked(io.ErrShortWrite)
	}
	return s.failLocked(s.rc.Flush())
}

func (s *scheduledSSE) failLocked(err error) error {
	if err == nil {
		return nil
	}
	if s.err == nil {
		s.err = err
		s.cancel()
	}
	return s.err
}

func (h *Scheduled) ready(w http.ResponseWriter) bool {
	if h == nil || h.service == nil {
		RespondError(w, http.StatusInternalServerError, "scheduled service unavailable")
		return false
	}
	return true
}

func (h *Scheduled) List(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	u := auth.UserFromContext(r.Context())
	offset := 0
	if value := r.URL.Query().Get("offset"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			RespondError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		offset = parsed
	}
	result, err := h.service.List(r.Context(), u.ID, offset)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list scheduled tasks")
		return
	}
	tasks := make([]scheduledTaskDTO, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		dto := taskDTO(task)
		if summary, ok := result.RunSummaries[task.ID]; ok {
			dto.UnreadCount = summary.UnreadCount
			if summary.RecentRun != nil {
				recent := runDTO(*summary.RecentRun)
				dto.RecentRun = &recent
			}
		}
		tasks = append(tasks, dto)
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"tasks": tasks, "unreadCount": result.Unread,
		"hasMore": result.HasMore, "nextOffset": result.NextOffset,
	})
}

func (h *Scheduled) Detail(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	u := auth.UserFromContext(r.Context())
	result, err := h.service.Detail(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		h.writeLifecycleError(w, err)
		return
	}
	runs := make([]scheduledRunDTO, 0, len(result.Runs))
	for _, run := range result.Runs {
		runs = append(runs, runDTO(run))
	}
	messages := make([]scheduledDefinitionMessageDTO, 0, len(result.DefinitionMessages))
	for _, message := range result.DefinitionMessages {
		messages = append(messages, scheduledDefinitionMessageDTO{
			Role: message.Role, Text: message.Text, Question: message.Question,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"task": taskDTO(result.Task), "runs": runs, "definitionMessages": messages,
	})
}

func (h *Scheduled) Confirm(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body struct {
		ExpectedVersion int `json:"expectedVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedVersion <= 0 {
		RespondError(w, http.StatusBadRequest, "expectedVersion is required")
		return
	}
	task, err := h.service.Confirm(r.Context(), scheduledActor(r), chi.URLParam(r, "id"), body.ExpectedVersion)
	if err != nil {
		h.writeLifecycleError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, taskDTO(task))
}

func (h *Scheduled) Patch(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	u, id := auth.UserFromContext(r.Context()), chi.URLParam(r, "id")
	var task model.ScheduledTask
	var err error
	switch body.State {
	case model.ScheduledTaskStatePaused:
		task, err = h.service.Pause(r.Context(), u.ID, id)
	case model.ScheduledTaskStateActive:
		task, err = h.service.Resume(r.Context(), u.ID, id)
	default:
		RespondError(w, http.StatusBadRequest, "state must be active or paused")
		return
	}
	if err != nil {
		h.writeLifecycleError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, taskDTO(task))
}

func (h *Scheduled) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	u := auth.UserFromContext(r.Context())
	if err := h.service.Delete(r.Context(), u.ID, chi.URLParam(r, "id")); err != nil {
		h.writeLifecycleError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Scheduled) RunNow(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	u := auth.UserFromContext(r.Context())
	run, err := h.service.RunNow(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		h.writeLifecycleError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, runDTO(run))
}

func (h *Scheduled) MarkRead(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	u := auth.UserFromContext(r.Context())
	if err := h.service.MarkRead(r.Context(), u.ID, chi.URLParam(r, "id")); err != nil {
		h.writeLifecycleError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Scheduled) writeLifecycleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		RespondError(w, http.StatusNotFound, "scheduled task not found")
	case errors.Is(err, store.ErrActiveTaskLimit), errors.Is(err, scheduled.ErrStaleProposal), errors.Is(err, scheduled.ErrRunInProgress):
		RespondError(w, http.StatusConflict, "scheduled task conflict")
	case errors.Is(err, scheduled.ErrInvalidTransition), strings.HasPrefix(err.Error(), "scheduled:"):
		RespondError(w, http.StatusBadRequest, "invalid scheduled task request")
	default:
		RespondError(w, http.StatusInternalServerError, "could not update scheduled task")
	}
}
