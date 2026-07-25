package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

type mcpAuditRepoFake struct {
	filter store.MCPAuditFilter
	rows   []model.MCPAuditCall
	more   bool
}

func (f *mcpAuditRepoFake) List(_ context.Context, filter store.MCPAuditFilter) ([]model.MCPAuditCall, bool, error) {
	f.filter = filter
	return f.rows, f.more, nil
}

func (f *mcpAuditRepoFake) Get(_ context.Context, id int64, _ time.Time) (model.MCPAuditCall, error) {
	for _, row := range f.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return model.MCPAuditCall{}, store.ErrNotFound
}

func TestMCPAuditListFiltersAndOmitsLargeFields(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	finished := now.Add(time.Second)
	repo := &mcpAuditRepoFake{
		more: true,
		rows: []model.MCPAuditCall{{
			ID: 5, ActorUserID: 7, ActorUsername: testUsername,
			ConversationID: "11111111-1111-4111-8111-111111111111",
			Source:         model.MCPAuditSourceChat, Model: "coach", ToolName: "garmin__activities",
			Arguments: `{"large":"payload"}`, Result: `{"large":"result"}`,
			Status: model.MCPAuditStatusSucceeded, StartedAt: now, FinishedAt: &finished,
		}},
	}
	h := handlers.NewMCPAudit(repo, 48*time.Hour, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodGet,
		"/api/admin/mcp-audit?limit=25&userId=7&conversationId=11111111-1111-4111-8111-111111111111"+
			"&source=chat&status=succeeded&model=coach&tool=garmin__activities", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if repo.filter.Limit != 25 || repo.filter.ActorUserID == nil || *repo.filter.ActorUserID != 7 ||
		repo.filter.Source != model.MCPAuditSourceChat || repo.filter.Status != model.MCPAuditStatusSucceeded ||
		!repo.filter.Cutoff.Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("filter = %+v", repo.filter)
	}
	var body struct {
		Data struct {
			Items []map[string]any `json:"items"`
			Next  string           `json:"nextCursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Items) != 1 || body.Data.Next == "" {
		t.Fatalf("response = %+v", body.Data)
	}
	if _, ok := body.Data.Items[0]["arguments"]; ok {
		t.Fatal("list response exposed arguments")
	}
	if _, ok := body.Data.Items[0]["result"]; ok {
		t.Fatal("list response exposed result")
	}
}

func TestMCPAuditDetailReturnsFullRecord(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	repo := &mcpAuditRepoFake{rows: []model.MCPAuditCall{{
		ID: 5, ActorUserID: 7, ActorUsername: testUsername,
		ConversationID: "11111111-1111-4111-8111-111111111111",
		Source:         model.MCPAuditSourceChat, Model: "coach", ToolCallID: "call-1",
		ToolName: "garmin__activities", Arguments: `{"limit":1}`,
		Result: `{"count":1}`, Status: model.MCPAuditStatusSucceeded, StartedAt: now,
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
	var body struct {
		Data struct {
			Arguments string `json:"arguments"`
			Result    string `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Arguments != `{"limit":1}` || body.Data.Result != `{"count":1}` {
		t.Fatalf("detail = %+v", body.Data)
	}
}

func TestMCPAuditListRejectsInvalidFilters(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	h := handlers.NewMCPAudit(&mcpAuditRepoFake{}, 48*time.Hour, func() time.Time { return now })
	for _, query := range []string{
		"?conversationId=not-a-uuid",
		"?source=health",
		"?status=unknown",
		"?limit=101",
		"?from=2026-07-25T12:00:00Z&to=2026-07-25T10:00:00Z",
		"?cursor=broken",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/mcp-audit"+query, nil)
		rec := httptest.NewRecorder()
		h.List(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q status=%d body=%s", query, rec.Code, rec.Body.String())
		}
	}
}
