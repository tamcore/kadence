package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/mcpaudit"
	"github.com/tamcore/kadence/internal/mcpintent"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const (
	intentIntegrationTool = "weather__lookup"
	intentIntegrationArgs = `{"location":"Bratislava","_kadence_intent":"Read the forecast"}`
)

type intentIntegrationProvider struct {
	reply string
	err   error
}

func (p intentIntegrationProvider) StreamChat(
	_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc,
) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	if err := onToken(p.reply); err != nil {
		return "", err
	}
	return p.reply, nil
}

func (p intentIntegrationProvider) StreamChatWithTools(
	ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc,
) (provider.StreamResult, error) {
	reply, err := p.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: reply}, err
}

type intentIntegrationRegistry struct {
	snapshot *intentIntegrationSnapshot
}

func (r intentIntegrationRegistry) Enabled() bool { return true }

func (r intentIntegrationRegistry) SnapshotFor(context.Context, string) chat.MCPUserSnapshot {
	return r.snapshot
}

type intentIntegrationSnapshot struct {
	calls int
}

func (*intentIntegrationSnapshot) ToolsFor(context.Context) ([]provider.ToolDefinition, error) {
	return []provider.ToolDefinition{{
		Name: intentIntegrationTool, Description: "Read a weather forecast.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
	}}, nil
}

func (s *intentIntegrationSnapshot) Call(context.Context, string, string) (string, error) {
	s.calls++
	return `{"forecast":"clear"}`, nil
}

func (s *intentIntegrationSnapshot) CallWithTransform(
	ctx context.Context, name, arguments string, transform chat.ArgumentTransform,
) (string, error) {
	if transform != nil {
		var err error
		arguments, err = transform(arguments)
		if err != nil {
			return "", err
		}
	}
	return s.Call(ctx, name, arguments)
}

func (*intentIntegrationSnapshot) ToolHints() []string { return nil }

func TestMCPIntentGuardPersistsPostgresAuditOutcomes(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	repo := store.NewMCPAuditRepository(pool)
	started := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		reply       string
		arguments   string
		wantVerdict string
		wantReason  string
		wantStatus  string
		wantBlocked bool
	}{
		{
			name: "allowed", reply: `{"verdict":"ALLOW","reason":"The forecast serves the request."}`,
			arguments: intentIntegrationArgs, wantVerdict: model.MCPAuditGuardAllowed,
			wantReason: "The forecast serves the request.", wantStatus: model.MCPAuditStatusSucceeded,
		},
		{
			name: "denied", reply: `{"verdict":"DENY","reason":"The tool does not serve the request."}`,
			arguments: intentIntegrationArgs, wantVerdict: model.MCPAuditGuardDenied,
			wantReason: "The tool does not serve the request.", wantStatus: model.MCPAuditStatusBlocked,
			wantBlocked: true,
		},
		{
			name: "classifier error", reply: `{"verdict":"UNKNOWN","reason":"private provider output"}`,
			arguments: intentIntegrationArgs, wantVerdict: model.MCPAuditGuardError,
			wantReason: "intent validation unavailable; revise or retry later",
			wantStatus: model.MCPAuditStatusBlocked, wantBlocked: true,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			remote := &intentIntegrationSnapshot{}
			guard := mcpintent.NewGuard(intentIntegrationProvider{reply: test.reply}, mcpintent.Config{Model: "classifier"})
			catalog := chat.NewUnattendedCatalog(
				intentIntegrationRegistry{snapshot: remote}, nil,
				mcpaudit.NewRecorder(repo, nil, func() time.Time { return started.Add(time.Duration(index) * time.Second) }),
				guard,
			)
			snapshot, err := catalog.SnapshotFor(t.Context(), testAliceUsername)
			if err != nil {
				t.Fatal(err)
			}
			ctx := mcpintent.WithTrustedContext(t.Context(), mcpintent.TrustedContext{Request: "Read the forecast"})
			ctx = mcpaudit.WithMetadata(ctx, mcpaudit.Metadata{
				ActorUserID: 7, ActorUsername: testAliceUsername,
				ConversationID: auditConversationID, Source: model.MCPAuditSourceChat,
				Model: testAuditModel, ToolCallID: "intent-" + test.name,
				RequestedTool: intentIntegrationTool,
				SafeArguments: mcpintent.StripArguments(test.arguments),
			})

			_, callErr := snapshot.Call(ctx, intentIntegrationTool, test.arguments)
			_, blocked := mcpintent.AsBlocked(callErr)
			if blocked != test.wantBlocked {
				t.Fatalf("blocked=%t err=%v want %t", blocked, callErr, test.wantBlocked)
			}
			if !test.wantBlocked && callErr != nil {
				t.Fatal(callErr)
			}
			if remote.calls != 1-boolInt(test.wantBlocked) {
				t.Fatalf("remote calls=%d blocked=%t", remote.calls, test.wantBlocked)
			}
		})
	}

	rows, more, err := repo.List(t.Context(), store.MCPAuditFilter{Cutoff: started.Add(-time.Minute), Limit: 10})
	if err != nil || more || len(rows) != len(tests) {
		t.Fatalf("audit rows=%+v more=%t err=%v", rows, more, err)
	}
	wants := make(map[string]struct {
		verdict string
		reason  string
		status  string
	}, len(tests))
	for _, test := range tests {
		wants["intent-"+test.name] = struct {
			verdict string
			reason  string
			status  string
		}{test.wantVerdict, test.wantReason, test.wantStatus}
	}
	for _, row := range rows {
		want, ok := wants[row.ToolCallID]
		if !ok {
			t.Fatalf("unexpected audit row %+v", row)
		}
		got, getErr := repo.Get(t.Context(), row.ID, started.Add(-time.Minute))
		if getErr != nil {
			t.Fatal(getErr)
		}
		if got.GuardVerdict != want.verdict || got.GuardReason != want.reason || got.Status != want.status {
			t.Fatalf("audit=%+v want verdict=%q reason=%q status=%q", got, want.verdict, want.reason, want.status)
		}
		if got.Arguments != `{"location":"Bratislava"}` || got.Intent != "Read the forecast" {
			t.Fatalf("unsafe audit payload %+v", got)
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

var _ provider.Provider = intentIntegrationProvider{}
var _ chat.MCPTools = intentIntegrationRegistry{}
var _ chat.MCPUserSnapshot = (*intentIntegrationSnapshot)(nil)
