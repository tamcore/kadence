package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const (
	auditConversationID = "11111111-1111-4111-8111-111111111111"
	testAuditModel      = "coach-model"
)

func TestMCPAuditRepositoryLifecycleFilteringAndRetention(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	repo := store.NewMCPAuditRepository(pool)
	ctx := context.Background()
	started := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)

	id, err := repo.Start(ctx, model.MCPAuditCall{
		ActorUserID: 7, ActorUsername: testAliceUsername, ConversationID: auditConversationID,
		Source: model.MCPAuditSourceChat, Model: testAuditModel, ToolCallID: "call-1",
		ToolName: "garmin__activities", Arguments: `{"limit":5}`,
		StartedAt: started,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	finished := started.Add(1250 * time.Millisecond)
	if err := repo.Finish(ctx, id, model.MCPAuditStatusSucceeded, `{"count":1}`, "", finished); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	got, err := repo.Get(ctx, id, started.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != model.MCPAuditStatusSucceeded || got.Result != `{"count":1}` ||
		got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("finished audit = %+v", got)
	}

	rows, more, err := repo.List(ctx, store.MCPAuditFilter{
		Cutoff: started.Add(-time.Minute), ActorUserID: new(int64(7)),
		ConversationID: auditConversationID, Model: testAuditModel,
		Tool: "garmin__activities", Source: model.MCPAuditSourceChat,
		Status: model.MCPAuditStatusSucceeded, Limit: 10,
	})
	if err != nil || more || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("List = %+v more=%v err=%v", rows, more, err)
	}
	if rows[0].Arguments != "" || rows[0].Result != "" || rows[0].Error != "" {
		t.Fatalf("List loaded full payload fields: %+v", rows[0])
	}

	rows, more, err = repo.List(ctx, store.MCPAuditFilter{
		Cutoff: started.Add(-time.Minute), Limit: 10,
	})
	if err != nil || more || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("unfiltered List = %+v more=%v err=%v", rows, more, err)
	}

	if _, err := repo.Get(ctx, id, started.Add(time.Second)); err == nil {
		t.Fatal("Get after cutoff = nil error, want expired record hidden")
	}
	deleted, err := repo.DeleteBefore(ctx, started.Add(time.Second))
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteBefore = %d, %v; want 1, nil", deleted, err)
	}
}

func TestMCPAuditRepositoryStoresAndFiltersGuardDecision(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	repo := store.NewMCPAuditRepository(pool)
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	finished := now.Add(time.Second)

	id, err := repo.Start(ctx, model.MCPAuditCall{
		ActorUserID: 7, ActorUsername: testAliceUsername, ConversationID: auditConversationID,
		Source: model.MCPAuditSourceChat, Model: testAuditModel, ToolCallID: "call-blocked",
		ToolName: "weather__forecast", Arguments: `{"location":"Bratislava"}`,
		Intent: "Show weather forecast", GuardVerdict: model.MCPAuditGuardDenied,
		GuardReason: "Tool mutates external data", Status: model.MCPAuditStatusBlocked,
		StartedAt: now, FinishedAt: &finished,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	got, err := repo.Get(ctx, id, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Intent != "Show weather forecast" || got.GuardVerdict != model.MCPAuditGuardDenied ||
		got.GuardReason != "Tool mutates external data" || got.Status != model.MCPAuditStatusBlocked ||
		got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("blocked audit = %+v", got)
	}

	rows, more, err := repo.List(ctx, store.MCPAuditFilter{
		Cutoff: now.Add(-time.Hour), ActorUserID: new(int64(7)),
		Source: model.MCPAuditSourceChat, Status: model.MCPAuditStatusBlocked,
		Intent: "WEATHER", GuardVerdict: model.MCPAuditGuardDenied,
		BeforeStartedAt: new(now.Add(time.Minute)), BeforeID: id + 1, Limit: 10,
	})
	if err != nil || more || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("List = %+v more=%v err=%v", rows, more, err)
	}
	if rows[0].Arguments != "" || rows[0].Result != "" || rows[0].Error != "" || rows[0].GuardReason != "" {
		t.Fatalf("List loaded sensitive fields: %+v", rows[0])
	}
}
