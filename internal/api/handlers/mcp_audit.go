package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

type MCPAuditRepo interface {
	List(context.Context, store.MCPAuditFilter) ([]model.MCPAuditCall, bool, error)
	Get(context.Context, int64, time.Time) (model.MCPAuditCall, error)
}

type MCPAudit struct {
	repo MCPAuditRepo
	ttl  time.Duration
	now  func() time.Time
}

func NewMCPAudit(repo MCPAuditRepo, ttl time.Duration, now func() time.Time) *MCPAudit {
	if now == nil {
		now = time.Now
	}
	return &MCPAudit{repo: repo, ttl: ttl, now: now}
}

type mcpAuditSummaryDTO struct {
	ID              int64      `json:"id"`
	ActorUserID     int64      `json:"actorUserId"`
	ActorUsername   string     `json:"actorUsername"`
	ConversationID  string     `json:"conversationId"`
	Source          string     `json:"source"`
	ScheduledTaskID *string    `json:"scheduledTaskId,omitempty"`
	ScheduledRunID  *int64     `json:"scheduledRunId,omitempty"`
	Model           string     `json:"model"`
	ToolCallID      string     `json:"toolCallId"`
	ToolName        string     `json:"toolName"`
	Status          string     `json:"status"`
	Intent          string     `json:"intent"`
	GuardVerdict    string     `json:"guardVerdict"`
	StartedAt       time.Time  `json:"startedAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
}

type mcpAuditDetailDTO struct {
	mcpAuditSummaryDTO
	Arguments   string `json:"arguments"`
	GuardReason string `json:"guardReason"`
	Result      string `json:"result"`
	Error       string `json:"error"`
}

type mcpAuditCursor struct {
	StartedAt time.Time `json:"startedAt"`
	ID        int64     `json:"id"`
}

func (h *MCPAudit) List(w http.ResponseWriter, r *http.Request) {
	filter, err := h.parseFilter(r)
	if err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, more, err := h.repo.List(r.Context(), filter)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list MCP audit calls")
		return
	}
	items := make([]mcpAuditSummaryDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, mcpAuditSummary(row))
	}
	var next string
	if more && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = encodeMCPAuditCursor(mcpAuditCursor{StartedAt: last.StartedAt, ID: last.ID})
	}
	RespondJSON(w, http.StatusOK, struct {
		Items      []mcpAuditSummaryDTO `json:"items"`
		NextCursor string               `json:"nextCursor,omitempty"`
	}{Items: items, NextCursor: next})
}

func (h *MCPAudit) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		RespondError(w, http.StatusBadRequest, "invalid audit id")
		return
	}
	call, err := h.repo.Get(r.Context(), id, h.now().Add(-h.ttl))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			RespondError(w, http.StatusNotFound, "MCP audit call not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "could not load MCP audit call")
		return
	}
	RespondJSON(w, http.StatusOK, mcpAuditDetailDTO{
		mcpAuditSummaryDTO: mcpAuditSummary(call),
		Arguments:          call.Arguments, GuardReason: call.GuardReason, Result: call.Result, Error: call.Error,
	})
}

func (h *MCPAudit) parseFilter(r *http.Request) (store.MCPAuditFilter, error) {
	q := r.URL.Query()
	filter := store.MCPAuditFilter{
		Cutoff: h.now().Add(-h.ttl), ConversationID: q.Get("conversationId"),
		Source: q.Get("source"), Status: q.Get("status"), Model: q.Get("model"), Tool: q.Get("tool"),
		Intent: q.Get("intent"), GuardVerdict: q.Get("guardVerdict"),
	}
	if value := q.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			return filter, errors.New("limit must be between 1 and 100")
		}
		filter.Limit = limit
	}
	if value := q.Get("userId"); value != "" {
		userID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || userID <= 0 {
			return filter, errors.New("userId must be a positive integer")
		}
		filter.ActorUserID = &userID
	}
	if filter.ConversationID != "" {
		if _, err := uuid.Parse(filter.ConversationID); err != nil {
			return filter, errors.New("conversationId must be a UUID")
		}
	}
	if filter.Source != "" && filter.Source != model.MCPAuditSourceChat &&
		filter.Source != model.MCPAuditSourceScheduled {
		return filter, errors.New("source must be chat or scheduled")
	}
	if filter.Status != "" && filter.Status != model.MCPAuditStatusRunning &&
		filter.Status != model.MCPAuditStatusSucceeded && filter.Status != model.MCPAuditStatusFailed &&
		filter.Status != model.MCPAuditStatusBlocked {
		return filter, errors.New("status must be running, succeeded, failed, or blocked")
	}
	if filter.GuardVerdict != "" && filter.GuardVerdict != model.MCPAuditGuardNotEvaluated &&
		filter.GuardVerdict != model.MCPAuditGuardAllowed && filter.GuardVerdict != model.MCPAuditGuardDenied &&
		filter.GuardVerdict != model.MCPAuditGuardError {
		return filter, errors.New("guardVerdict must be not_evaluated, allowed, denied, or error")
	}
	var err error
	if filter.From, err = parseOptionalAuditTime(q.Get("from")); err != nil {
		return filter, errors.New("from must be RFC3339")
	}
	if filter.To, err = parseOptionalAuditTime(q.Get("to")); err != nil {
		return filter, errors.New("to must be RFC3339")
	}
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		return filter, errors.New("from must not be after to")
	}
	if value := q.Get("cursor"); value != "" {
		cursor, err := decodeMCPAuditCursor(value)
		if err != nil {
			return filter, errors.New("invalid cursor")
		}
		filter.BeforeStartedAt = &cursor.StartedAt
		filter.BeforeID = cursor.ID
	}
	return filter, nil
}

func mcpAuditSummary(call model.MCPAuditCall) mcpAuditSummaryDTO {
	return mcpAuditSummaryDTO{
		ID: call.ID, ActorUserID: call.ActorUserID, ActorUsername: call.ActorUsername,
		ConversationID: call.ConversationID, Source: call.Source,
		ScheduledTaskID: call.ScheduledTaskID, ScheduledRunID: call.ScheduledRunID,
		Model: call.Model, ToolCallID: call.ToolCallID, ToolName: call.ToolName,
		Status: call.Status, Intent: call.Intent, GuardVerdict: call.GuardVerdict,
		StartedAt: call.StartedAt, FinishedAt: call.FinishedAt,
	}
}

func parseOptionalAuditTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func encodeMCPAuditCursor(cursor mcpAuditCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeMCPAuditCursor(value string) (mcpAuditCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return mcpAuditCursor{}, err
	}
	var cursor mcpAuditCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.ID <= 0 || cursor.StartedAt.IsZero() {
		return mcpAuditCursor{}, errors.New("invalid cursor payload")
	}
	return cursor, nil
}
