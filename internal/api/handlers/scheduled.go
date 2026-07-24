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
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/scheduled"
	"github.com/tamcore/kadence/internal/store"
)

// ScheduledLifecycle is the HTTP seam for owner-scoped Scheduled operations.
type ScheduledLifecycle interface {
	Create(context.Context, scheduled.Actor, string) (scheduled.DefinitionResult, error)
	Refine(context.Context, scheduled.Actor, string, string) (scheduled.DefinitionResult, error)
	Confirm(context.Context, scheduled.Actor, string, int) (model.ScheduledTask, error)
	List(context.Context, int64) (scheduled.ListResult, error)
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
	scheduledEventType  = "type"
	scheduledEventError = "error"
	scheduledEventDone  = "done"
)

type scheduledTaskDTO struct {
	ID               string   `json:"id"`
	ConversationID   string   `json:"conversationId"`
	Version          int      `json:"version"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	State            string   `json:"state"`
	CompiledPrompt   string   `json:"compiledPrompt"`
	OneOffAt         *string  `json:"oneOffAt,omitempty"`
	DTStart          *string  `json:"dtStart,omitempty"`
	RRULE            string   `json:"rrule,omitempty"`
	Timezone         string   `json:"timezone"`
	ExecutionMode    string   `json:"executionMode"`
	AuthorizedTools  []string `json:"authorizedTools"`
	DeliveryPolicy   string   `json:"deliveryPolicy"`
	InitialRun       string   `json:"initialRun"`
	StopCondition    string   `json:"stopCondition,omitempty"`
	StaticMessage    string   `json:"staticMessage,omitempty"`
	NextRunAt        *string  `json:"nextRunAt,omitempty"`
	LastRunAt        *string  `json:"lastRunAt,omitempty"`
	ConsecutiveFails int      `json:"consecutiveFailures"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
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
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		RespondError(w, http.StatusBadRequest, "message is required")
		return
	}
	stream, stop := beginScheduledStream(w)
	defer stop()
	result, err := h.service.Create(r.Context(), scheduledActor(r), body.Message)
	if err != nil {
		stream.event(map[string]any{scheduledEventType: scheduledEventError, scheduledEventError: "could not create scheduled task"})
		stream.event(map[string]any{scheduledEventType: scheduledEventDone})
		return
	}
	h.streamDefinition(stream, result)
}

// Refine handles POST /api/scheduled/tasks/{id}/messages.
func (h *Scheduled) Refine(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Message string `json:"message"`
	}
	if id == "" || json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Message) == "" {
		RespondError(w, http.StatusBadRequest, "id and message are required")
		return
	}
	stream, stop := beginScheduledStream(w)
	defer stop()
	result, err := h.service.Refine(r.Context(), scheduledActor(r), id, body.Message)
	if err != nil {
		stream.event(map[string]any{scheduledEventType: scheduledEventError, scheduledEventError: "could not refine scheduled task"})
		stream.event(map[string]any{scheduledEventType: scheduledEventDone})
		return
	}
	h.streamDefinition(stream, result)
}

func (h *Scheduled) streamDefinition(stream *scheduledSSE, result scheduled.DefinitionResult) {
	stream.event(map[string]any{scheduledEventType: "meta", "taskId": result.Task.ID, "conversationId": result.Task.ConversationID})
	stream.event(map[string]any{scheduledEventType: "text", "delta": result.Refinement.Text})
	if result.Refinement.Question != nil {
		stream.event(map[string]any{scheduledEventType: "task_question", "question": result.Refinement.Question})
	} else if result.Refinement.Proposal != nil {
		stream.event(map[string]any{scheduledEventType: "task_proposal", "proposal": result.Refinement.Proposal})
	}
	stream.event(map[string]any{scheduledEventType: scheduledEventDone})
}

type scheduledSSE struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

func beginScheduledStream(w http.ResponseWriter) (*scheduledSSE, func()) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := &scheduledSSE{w: w, rc: http.NewResponseController(w)}
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
				stream.keepalive()
			}
		}
	}()
	return stream, func() { close(done); <-stopped; _ = stream.rc.Flush() }
}

func (s *scheduledSSE) event(event any) {
	b, err := json.Marshal(event)
	if err == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, _ = fmt.Fprintf(s.w, "data: %s\n\n", b)
		_ = s.rc.Flush()
	}
}

func (s *scheduledSSE) keepalive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprint(s.w, ": keepalive\n\n")
	_ = s.rc.Flush()
}

func (h *Scheduled) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	result, err := h.service.List(r.Context(), u.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list scheduled tasks")
		return
	}
	tasks := make([]scheduledTaskDTO, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		tasks = append(tasks, taskDTO(task))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"tasks": tasks, "unreadCount": result.Unread})
}

func (h *Scheduled) Detail(w http.ResponseWriter, r *http.Request) {
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
	RespondJSON(w, http.StatusOK, map[string]any{"task": taskDTO(result.Task), "runs": runs})
}

func (h *Scheduled) Confirm(w http.ResponseWriter, r *http.Request) {
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
	u := auth.UserFromContext(r.Context())
	if err := h.service.Delete(r.Context(), u.ID, chi.URLParam(r, "id")); err != nil {
		h.writeLifecycleError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Scheduled) RunNow(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	run, err := h.service.RunNow(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		h.writeLifecycleError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, runDTO(run))
}

func (h *Scheduled) MarkRead(w http.ResponseWriter, r *http.Request) {
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
	case errors.Is(err, store.ErrActiveTaskLimit), errors.Is(err, scheduled.ErrStaleProposal):
		RespondError(w, http.StatusConflict, "scheduled task conflict")
	case errors.Is(err, scheduled.ErrInvalidTransition), strings.HasPrefix(err.Error(), "scheduled:"):
		RespondError(w, http.StatusBadRequest, "invalid scheduled task request")
	default:
		RespondError(w, http.StatusInternalServerError, "could not update scheduled task")
	}
}
