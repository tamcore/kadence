package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const (
	mcpAuditTestModel       = "coach"
	mcpAuditTestIntent      = "Read weather"
	mcpAuditTestGuardReason = "Tool mismatch"
	mcpAuditTestTool        = "garmin__activities"
	mcpAuditTestError       = "denied"
)

type mcpAuditRepoFake struct {
	filter store.MCPAuditFilter
	rows   []model.MCPAuditCall
	more   bool
}

type mcpAuditListResponse struct {
	Data struct {
		Items      []mcpAuditSummaryResponse `json:"items"`
		NextCursor string                    `json:"nextCursor"`
	} `json:"data"`
}

type mcpAuditSummaryResponse struct {
	ID           int64   `json:"id"`
	Intent       string  `json:"intent"`
	GuardVerdict string  `json:"guardVerdict"`
	Arguments    *string `json:"arguments"`
	GuardReason  *string `json:"guardReason"`
	Result       *string `json:"result"`
	Error        *string `json:"error"`
}

type mcpAuditDetailResponse struct {
	Data struct {
		mcpAuditSummaryResponse
		Arguments   string `json:"arguments"`
		GuardReason string `json:"guardReason"`
		Result      string `json:"result"`
		Error       string `json:"error"`
	} `json:"data"`
}

type mcpAuditErrorResponse struct {
	Error string `json:"error"`
}

func (f *mcpAuditRepoFake) List(_ context.Context, filter store.MCPAuditFilter) ([]model.MCPAuditCall, bool, error) {
	f.filter = filter
	return f.rows, f.more, nil
}

func (f *mcpAuditRepoFake) Get(_ context.Context, id int64, _ time.Time) (model.MCPAuditCall, error) {
	i := slices.IndexFunc(f.rows, func(row model.MCPAuditCall) bool { return row.ID == id })
	if i < 0 {
		return model.MCPAuditCall{}, store.ErrNotFound
	}
	return f.rows[i], nil
}

func TestMCPAuditListIncludesIntentDecisionWithoutDetailPayloads(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	finished := now.Add(time.Second)
	repo := &mcpAuditRepoFake{
		more: true,
		rows: []model.MCPAuditCall{{
			ID: 5, ActorUserID: 7, ActorUsername: testUsername,
			ConversationID: "11111111-1111-4111-8111-111111111111",
			Source:         model.MCPAuditSourceChat, Model: mcpAuditTestModel, ToolName: mcpAuditTestTool,
			Arguments: `{"large":"payload"}`, Intent: mcpAuditTestIntent, GuardVerdict: model.MCPAuditGuardDenied,
			GuardReason: mcpAuditTestGuardReason, Result: `{"large":"result"}`, Error: mcpAuditTestError,
			Status: model.MCPAuditStatusSucceeded, StartedAt: now, FinishedAt: &finished,
		}},
	}
	h := handlers.NewMCPAudit(repo, 48*time.Hour, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodGet,
		"/api/admin/mcp-audit?limit=25&userId=7&conversationId=11111111-1111-4111-8111-111111111111"+
			"&source=chat&status=succeeded&model="+mcpAuditTestModel+"&tool="+mcpAuditTestTool+"&intent=weather&guardVerdict=denied", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if repo.filter.Limit != 25 || repo.filter.ActorUserID == nil || *repo.filter.ActorUserID != 7 ||
		repo.filter.Source != model.MCPAuditSourceChat || repo.filter.Status != model.MCPAuditStatusSucceeded ||
		repo.filter.Model != mcpAuditTestModel || repo.filter.Tool != mcpAuditTestTool || repo.filter.Intent != "weather" ||
		repo.filter.GuardVerdict != model.MCPAuditGuardDenied ||
		!repo.filter.Cutoff.Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("filter = %+v", repo.filter)
	}
	var body mcpAuditListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Items) != 1 || body.Data.NextCursor == "" {
		t.Fatalf("response = %+v", body.Data)
	}
	item := body.Data.Items[0]
	if item.Intent != mcpAuditTestIntent || item.GuardVerdict != model.MCPAuditGuardDenied {
		t.Fatalf("item = %+v", item)
	}
	if item.Arguments != nil || item.GuardReason != nil || item.Result != nil || item.Error != nil {
		t.Fatalf("list response exposed detail fields: %+v", item)
	}
}

func TestMCPAuditDetailIncludesGuardReason(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	repo := &mcpAuditRepoFake{rows: []model.MCPAuditCall{{
		ID: 5, ActorUserID: 7, ActorUsername: testUsername,
		ConversationID: "11111111-1111-4111-8111-111111111111",
		Source:         model.MCPAuditSourceChat, Model: mcpAuditTestModel, ToolCallID: "call-1",
		ToolName: mcpAuditTestTool, Arguments: `{"limit":1}`,
		Intent: mcpAuditTestIntent, GuardVerdict: model.MCPAuditGuardDenied, GuardReason: mcpAuditTestGuardReason,
		Result: `{"count":1}`, Error: mcpAuditTestError, Status: model.MCPAuditStatusSucceeded, StartedAt: now,
	}}}
	h := handlers.NewMCPAudit(repo, 48*time.Hour, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodGet, "/api/admin/mcp-audit/5", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "5")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rec := httptest.NewRecorder()
	h.Detail(rec, req)

	if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body mcpAuditDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Intent != mcpAuditTestIntent || body.Data.GuardVerdict != model.MCPAuditGuardDenied ||
		body.Data.Arguments != `{"limit":1}` || body.Data.GuardReason != mcpAuditTestGuardReason ||
		body.Data.Result != `{"count":1}` || body.Data.Error != mcpAuditTestError {
		t.Fatalf("detail = %+v", body.Data)
	}
}

func TestMCPAuditListParsesIntentVerdictAndBlockedFilters(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	repo := &mcpAuditRepoFake{}
	h := handlers.NewMCPAudit(repo, 48*time.Hour, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodGet, "/api/admin/mcp-audit?intent=weather&guardVerdict=denied&status=blocked", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if repo.filter.Intent != "weather" || repo.filter.GuardVerdict != model.MCPAuditGuardDenied ||
		repo.filter.Status != model.MCPAuditStatusBlocked {
		t.Fatalf("filter=%+v", repo.filter)
	}
}

func TestMCPAuditListRejectsInvalidFilters(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	h := handlers.NewMCPAudit(&mcpAuditRepoFake{}, 48*time.Hour, func() time.Time { return now })
	for _, test := range []struct {
		query string
		want  string
	}{
		{query: "?conversationId=not-a-uuid", want: "conversationId must be a UUID"},
		{query: "?source=health", want: "source must be chat or scheduled"},
		{query: "?status=unknown", want: "status must be running, succeeded, failed, or blocked"},
		{query: "?status=Blocked", want: "status must be running, succeeded, failed, or blocked"},
		{query: "?guardVerdict=unknown", want: "guardVerdict must be not_evaluated, allowed, denied, or error"},
		{query: "?guardVerdict=Denied", want: "guardVerdict must be not_evaluated, allowed, denied, or error"},
		{query: "?limit=101", want: "limit must be between 1 and 100"},
		{query: "?from=2026-07-25T12:00:00Z&to=2026-07-25T10:00:00Z", want: "from must not be after to"},
		{query: "?cursor=broken", want: "invalid cursor"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/mcp-audit"+test.query, nil)
		rec := httptest.NewRecorder()
		h.List(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q status=%d body=%s", test.query, rec.Code, rec.Body.String())
			continue
		}
		var body mcpAuditErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("query %q decode error: %v", test.query, err)
			continue
		}
		if body.Error != test.want {
			t.Errorf("query %q error=%q want=%q", test.query, body.Error, test.want)
		}
	}
}
