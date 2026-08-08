package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/mcpaudit"
	"github.com/tamcore/kadence/internal/mcpintent"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

const (
	testUnattendedUsername = "alice"
	testSharedPrivateTool  = "shared__private"
	testFITServerName      = "GARMIN"
	testInvalidDescription = "bad"
	testDeniedReason       = "not needed"
	testWeatherIntent      = "Read weather"
	testWeatherArguments   = `{"id":1}`
	testWeatherIntentArgs  = `{"id":1,"_kadence_intent":"Read weather"}`
	testDownloadFITName    = "download_activity_fit"
	testDownloadFITTool    = "garmin__download_activity_fit"
	testDownloadFITDesc    = "Download activity FIT file"
	testAnalyzeFITIntent   = "Analyze activity 42"
	testFITArguments       = `{"activity_id":42}`
	testFITIntentArgs      = `{"activity_id":42,"_kadence_intent":"Analyze activity 42"}`
)

type catalogRegistry struct {
	snapshots map[string]*catalogMCPSnapshot
}

func (r *catalogRegistry) Enabled() bool { return true }

func (r *catalogRegistry) SnapshotFor(_ context.Context, username string) MCPUserSnapshot {
	return r.snapshots[username]
}

type catalogMCPSnapshot struct {
	definitions []provider.ToolDefinition
	prefixes    map[string]string
	calls       []string
	arguments   []string
	result      string
	err         error
}

func (s *catalogMCPSnapshot) ToolsFor(context.Context) ([]provider.ToolDefinition, error) {
	return append([]provider.ToolDefinition(nil), s.definitions...), s.err
}

func (s *catalogMCPSnapshot) Call(_ context.Context, name, arguments string) (string, error) {
	s.calls = append(s.calls, name)
	s.arguments = append(s.arguments, arguments)
	return s.result, s.err
}

func (*catalogMCPSnapshot) ToolHints() []string { return nil }

func (s *catalogMCPSnapshot) ServerPrefix(name, scope string) (string, bool) {
	prefix, ok := s.prefixes[name+"/"+scope]
	return prefix, ok
}

func TestUnattendedCatalogResolvesExactOwnerSnapshotAndNativeFIT(t *testing.T) {
	alice := &catalogMCPSnapshot{
		definitions: []provider.ToolDefinition{
			{Name: testSharedPrivateTool, Description: testUnattendedUsername},
			{Name: loadSkillToolName},
			{Name: credsToolName},
			{Name: analyzeGarminFITToolName, Description: testSpoofedToolDescription},
			{Name: convertPaceToolName, Description: testSpoofedToolDescription},
		},
		prefixes: map[string]string{testFITServerName + "/" + testFITAliceScope: "shared"},
		result:   "alice-result",
	}
	bob := &catalogMCPSnapshot{
		definitions: []provider.ToolDefinition{{Name: testSharedPrivateTool, Description: "bob"}},
		prefixes:    map[string]string{testFITServerName + "/USER_bob": "shared"},
		result:      "bob-result",
	}
	catalog := NewUnattendedCatalog(&catalogRegistry{snapshots: map[string]*catalogMCPSnapshot{
		testUnattendedUsername: alice,
		"bob":                  bob,
	}}, []FITRoute{
		{ServerName: testFITServerName, ServerScope: testFITAliceScope, DownloadTool: testFITGenericTool, BridgeURL: "http://alice"},
		{ServerName: testFITServerName, ServerScope: "USER_bob", DownloadTool: testFITGenericTool, BridgeURL: "http://bob"},
	}, nil, nil)

	aliceSnapshot, err := catalog.SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	bobSnapshot, err := catalog.SnapshotFor(t.Context(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	aliceTools := toolNames(t, aliceSnapshot)
	bobTools := toolNames(t, bobSnapshot)
	if !slices.Contains(aliceTools, testSharedPrivateTool) ||
		!slices.Contains(aliceTools, analyzeGarminFITToolName) ||
		countName(aliceTools, convertPaceToolName) != 1 {
		t.Fatalf("alice tools = %v", aliceTools)
	}
	if !slices.Contains(bobTools, testSharedPrivateTool) ||
		!slices.Contains(bobTools, analyzeGarminFITToolName) ||
		countName(bobTools, convertPaceToolName) != 1 {
		t.Fatalf("bob tools = %v", bobTools)
	}

	if got, err := aliceSnapshot.Call(t.Context(), testSharedPrivateTool, `{}`); err != nil || got != "alice-result" {
		t.Fatalf("alice call = %q, %v", got, err)
	}
	if got, err := bobSnapshot.Call(t.Context(), testSharedPrivateTool, `{}`); err != nil || got != "bob-result" {
		t.Fatalf("bob call = %q, %v", got, err)
	}
	if !slices.Equal(alice.calls, []string{testSharedPrivateTool}) || !slices.Equal(bob.calls, []string{testSharedPrivateTool}) {
		t.Fatalf("calls crossed owners: alice=%v bob=%v", alice.calls, bob.calls)
	}
	if got, err := aliceSnapshot.Call(t.Context(), convertPaceToolName, testMetricPaceArgs); err != nil ||
		got != testMetricPaceResult {
		t.Fatalf("pace call = %q, %v", got, err)
	}
	if !slices.Equal(alice.calls, []string{testSharedPrivateTool}) {
		t.Fatalf("local pace call reached MCP: %v", alice.calls)
	}
	if _, err := aliceSnapshot.Call(t.Context(), analyzeGarminFITToolName, `{"activity_id":1,"unexpected":true}`); err == nil {
		t.Fatal("native FIT accepted unknown arguments")
	}
	if !slices.Equal(alice.calls, []string{testSharedPrivateTool}) {
		t.Fatalf("invalid FIT reached MCP: %v", alice.calls)
	}
}

func TestUnattendedCatalogFailsClosed(t *testing.T) {
	var nilCatalog *UnattendedCatalog
	if snapshot, err := nilCatalog.SnapshotFor(t.Context(), testUnattendedUsername); err != nil ||
		len(toolNames(t, snapshot)) != 1 {
		t.Fatalf("nil catalog snapshot = %+v, %v", snapshot, err)
	}

	catalog := NewUnattendedCatalog(nil, nil, nil, nil)
	snapshot, err := catalog.SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	if tools, err := snapshot.ToolsFor(t.Context()); err != nil ||
		len(tools) != 1 ||
		tools[0].Name != convertPaceToolName {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	if got, err := snapshot.Call(
		t.Context(),
		convertPaceToolName,
		`{"unit":"metric","targetpace":"4:52","output":"imperial"}`,
	); err != nil || got != `{"value":"7:50","unit":"min/mi"}` {
		t.Fatalf("pace call = %q, %v", got, err)
	}
	if _, err := snapshot.Call(t.Context(), "missing", `{}`); err == nil {
		t.Fatal("missing tool call succeeded")
	}

	want := errors.New("list failed")
	broken := NewUnattendedCatalog(&catalogRegistry{snapshots: map[string]*catalogMCPSnapshot{
		testUnattendedUsername: {err: want},
	}}, nil, nil, nil)
	if _, err := broken.SnapshotFor(t.Context(), testUnattendedUsername); !errors.Is(err, want) {
		t.Fatalf("snapshot error = %v", err)
	}
}

func toolNames(t *testing.T, snapshot *UnattendedSnapshot) []string {
	t.Helper()
	definitions, err := snapshot.ToolsFor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(definitions))
	for i, definition := range definitions {
		names[i] = definition.Name
	}
	return names
}

func countName(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

var guardedRemoteDefinition = provider.ToolDefinition{
	Name:        "weather__lookup",
	Description: "Look up the weather for a location.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`),
}

type recordingEvaluator struct {
	decision  mcpintent.Decision
	decisions []mcpintent.Decision
	err       error
	inputs    []mcpintent.Input
}

func (e *recordingEvaluator) Evaluate(_ context.Context, input mcpintent.Input) (mcpintent.Decision, error) {
	e.inputs = append(e.inputs, input)
	if len(e.decisions) >= len(e.inputs) {
		return e.decisions[len(e.inputs)-1], e.err
	}
	return e.decision, e.err
}

func allowEvaluator() *recordingEvaluator {
	return &recordingEvaluator{decision: mcpintent.Decision{Verdict: mcpintent.VerdictAllow, Reason: "needed"}}
}

func registryWithGuardedTools(definitions ...provider.ToolDefinition) *catalogRegistry {
	return &catalogRegistry{snapshots: map[string]*catalogMCPSnapshot{
		testUnattendedUsername: {definitions: definitions, prefixes: map[string]string{testFITServerName + "/" + testFITAliceScope: "garmin"}},
	}}
}

func TestIntentGuardAugmentsRemoteAndFITTools(t *testing.T) {
	catalog := NewUnattendedCatalog(
		registryWithGuardedTools(guardedRemoteDefinition),
		[]FITRoute{{ServerName: testFITServerName, ServerScope: testFITAliceScope, DownloadTool: testFITGenericTool}},
		nil,
		allowEvaluator(),
	)
	snapshot, err := catalog.SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := snapshot.ToolsFor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertRequiredProperty(t, findTool(t, tools, guardedRemoteDefinition.Name), mcpintent.ArgumentName)
	assertRequiredProperty(t, findTool(t, tools, analyzeGarminFITToolName), mcpintent.ArgumentName)
	assertNoRequiredProperty(t, findTool(t, tools, convertPaceToolName), mcpintent.ArgumentName)
}

func TestDisabledIntentGuardReturnsDefinitionsUnchanged(t *testing.T) {
	catalog := NewUnattendedCatalog(registryWithGuardedTools(guardedRemoteDefinition), nil, nil, nil)
	snapshot, err := catalog.SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := snapshot.ToolsFor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := findTool(t, tools, guardedRemoteDefinition.Name); !equalDefinitions(got, guardedRemoteDefinition) {
		t.Fatalf("definition = %+v, want %+v", got, guardedRemoteDefinition)
	}
}

func TestIntentGuardOmitsInvalidDefinitionsWithBoundedMetadataLog(t *testing.T) {
	definitions := []provider.ToolDefinition{
		{Name: "bad-json", Description: testInvalidDescription, Parameters: json.RawMessage(`{`)},
		{Name: "not-object", Description: testInvalidDescription, Parameters: json.RawMessage(`[]`)},
		{Name: "collision", Description: testInvalidDescription, Parameters: json.RawMessage(`{"type":"object","properties":{"_kadence_intent":{"type":"string"}}}`)},
	}
	catalog := NewUnattendedCatalog(registryWithGuardedTools(definitions...), nil, nil, allowEvaluator())
	var logs bytes.Buffer
	catalog.log = slog.New(slog.NewTextHandler(&logs, nil))
	snapshot, err := catalog.SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	if names := toolNames(t, snapshot); len(names) != 1 || names[0] != convertPaceToolName {
		t.Fatalf("tools = %v, want pace only", names)
	}
	gotLogs := logs.String()
	for _, name := range []string{"bad-json", "not-object", "collision"} {
		if !strings.Contains(gotLogs, "tool="+name) {
			t.Fatalf("log %q does not identify omitted tool %q", gotLogs, name)
		}
	}
	if strings.Contains(gotLogs, `"properties"`) || strings.Contains(gotLogs, "error=") {
		t.Fatalf("log includes definition payload or error detail: %q", gotLogs)
	}
}

func TestDeniedCallRunsNeitherTransformNorRemote(t *testing.T) {
	evaluator := &recordingEvaluator{decision: mcpintent.Decision{Verdict: mcpintent.VerdictDeny, Reason: testDeniedReason}}
	registry := registryWithGuardedTools(guardedRemoteDefinition)
	snapshot, err := NewUnattendedCatalog(registry, nil, nil, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	transforms := 0
	ctx := mcpintent.WithTrustedContext(t.Context(), mcpintent.TrustedContext{Request: testWeatherIntent})
	_, err = snapshot.CallWithTransform(ctx, guardedRemoteDefinition.Name, testWeatherIntentArgs, func(raw string) (string, error) {
		transforms++
		return raw, nil
	})
	if _, ok := mcpintent.AsBlocked(err); !ok || transforms != 0 || len(registry.snapshots[testUnattendedUsername].calls) != 0 {
		t.Fatalf("err=%v transforms=%d calls=%v", err, transforms, registry.snapshots[testUnattendedUsername].calls)
	}
}

func TestAllowedCallClassifiesCleanArgumentsBeforeOneTransformAndRemoteCall(t *testing.T) {
	evaluator := allowEvaluator()
	registry := registryWithGuardedTools(guardedRemoteDefinition)
	registry.snapshots[testUnattendedUsername].result = "ok"
	snapshot, err := NewUnattendedCatalog(registry, nil, nil, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	transforms := 0
	ctx := mcpintent.WithTrustedContext(t.Context(), mcpintent.TrustedContext{Request: testWeatherIntent})
	got, err := snapshot.CallWithTransform(ctx, guardedRemoteDefinition.Name, testWeatherIntentArgs, func(raw string) (string, error) {
		transforms++
		if raw != testWeatherArguments {
			t.Fatalf("transform arguments = %q", raw)
		}
		return `{"id":2}`, nil
	})
	if err != nil || got != "ok" {
		t.Fatalf("call = %q, %v", got, err)
	}
	if transforms != 1 || len(registry.snapshots[testUnattendedUsername].calls) != 1 {
		t.Fatalf("transforms=%d calls=%v", transforms, registry.snapshots[testUnattendedUsername].calls)
	}
	if got := evaluator.inputs; len(got) != 1 || got[0] != (mcpintent.Input{
		Intent: testWeatherIntent, ToolName: guardedRemoteDefinition.Name,
		ToolDescription: guardedRemoteDefinition.Description, Arguments: testWeatherArguments,
	}) {
		t.Fatalf("classifier inputs = %+v", got)
	}
}

func TestGuardedCallBlocksInvalidIntentBeforeClassifierTransformAndRemote(t *testing.T) {
	for _, arguments := range []string{
		testWeatherArguments,
		`{"id":1,"_kadence_intent":" "}`,
		`{"id":1,"_kadence_intent":"` + strings.Repeat("a", mcpintent.MaxIntentBytes+1) + `"}`,
	} {
		t.Run(arguments[:8], func(t *testing.T) {
			evaluator := allowEvaluator()
			registry := registryWithGuardedTools(guardedRemoteDefinition)
			snapshot, err := NewUnattendedCatalog(registry, nil, nil, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
			if err != nil {
				t.Fatal(err)
			}
			transforms := 0
			_, err = snapshot.CallWithTransform(t.Context(), guardedRemoteDefinition.Name, arguments, func(raw string) (string, error) {
				transforms++
				return raw, nil
			})
			if _, ok := mcpintent.AsBlocked(err); !ok || len(evaluator.inputs) != 0 || transforms != 0 || len(registry.snapshots[testUnattendedUsername].calls) != 0 {
				t.Fatalf("err=%v classifier=%d transforms=%d calls=%v", err, len(evaluator.inputs), transforms, registry.snapshots[testUnattendedUsername].calls)
			}
		})
	}
}

func TestGuardedCallHidesClassifierErrorAndDoesNotDispatch(t *testing.T) {
	evaluator := &recordingEvaluator{err: errors.New("provider secret failure")}
	registry := registryWithGuardedTools(guardedRemoteDefinition)
	snapshot, err := NewUnattendedCatalog(registry, nil, nil, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	_, err = snapshot.CallWithTransform(t.Context(), guardedRemoteDefinition.Name, testWeatherIntentArgs, IdentityArguments)
	if blocked, ok := mcpintent.AsBlocked(err); !ok || strings.Contains(blocked.Reason, "secret") || len(registry.snapshots[testUnattendedUsername].calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, registry.snapshots[testUnattendedUsername].calls)
	}
}

type failingAuditStore struct{}

func (failingAuditStore) Start(context.Context, model.MCPAuditCall) (int64, error) {
	return 0, errors.New("audit unavailable")
}

func (failingAuditStore) Finish(context.Context, int64, string, string, string, time.Time) error {
	return nil
}

type capturingAuditStore struct{ calls []model.MCPAuditCall }

func (s *capturingAuditStore) Start(_ context.Context, call model.MCPAuditCall) (int64, error) {
	s.calls = append(s.calls, call)
	return int64(len(s.calls)), nil
}

func (*capturingAuditStore) Finish(context.Context, int64, string, string, string, time.Time) error {
	return nil
}

func TestAuditPersistenceFailureDoesNotChangeAllowedCall(t *testing.T) {
	evaluator := allowEvaluator()
	registry := registryWithGuardedTools(guardedRemoteDefinition)
	registry.snapshots[testUnattendedUsername].result = "ok"
	audit := mcpaudit.NewRecorder(failingAuditStore{}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), time.Now)
	snapshot, err := NewUnattendedCatalog(registry, nil, audit, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mcpaudit.WithMetadata(mcpintent.WithTrustedContext(t.Context(), mcpintent.TrustedContext{Request: testWeatherIntent}), mcpaudit.Metadata{})
	got, err := snapshot.Call(ctx, guardedRemoteDefinition.Name, testWeatherIntentArgs)
	if err != nil || got != "ok" || len(registry.snapshots[testUnattendedUsername].calls) != 1 {
		t.Fatalf("call=%q err=%v calls=%v", got, err, registry.snapshots[testUnattendedUsername].calls)
	}
}

func TestDeniedCallAuditsCleanArgumentsAndGuardDecision(t *testing.T) {
	evaluator := &recordingEvaluator{decision: mcpintent.Decision{Verdict: mcpintent.VerdictDeny, Reason: testDeniedReason}}
	registry := registryWithGuardedTools(guardedRemoteDefinition)
	store := &capturingAuditStore{}
	audit := mcpaudit.NewRecorder(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), time.Now)
	snapshot, err := NewUnattendedCatalog(registry, nil, audit, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mcpintent.WithTrustedContext(t.Context(), mcpintent.TrustedContext{Request: testWeatherIntent})
	ctx = mcpaudit.WithMetadata(ctx, mcpaudit.Metadata{RequestedTool: guardedRemoteDefinition.Name, SafeArguments: testWeatherIntentArgs})
	_, err = snapshot.Call(ctx, guardedRemoteDefinition.Name, testWeatherIntentArgs)
	if _, ok := mcpintent.AsBlocked(err); !ok {
		t.Fatalf("error = %v, want blocked", err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("audit calls = %d, want 1", len(store.calls))
	}
	got := store.calls[0]
	if got.Arguments != testWeatherArguments || got.Intent != testWeatherIntent || got.GuardVerdict != mcpintent.VerdictDeny || got.GuardReason != testDeniedReason {
		t.Fatalf("audit call = %+v", got)
	}
}

func TestMalformedIntentArgumentsAuditPayloadFreeBeforeDispatch(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
	}{
		{name: "reserved intent", raw: `{"_kadence_intent":"sensitive-audit-payload","id":`},
		{name: "no reserved intent", raw: `{"id":"sensitive-audit-payload",`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := allowEvaluator()
			registry := registryWithGuardedTools(guardedRemoteDefinition)
			store := &capturingAuditStore{}
			audit := mcpaudit.NewRecorder(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), time.Now)
			snapshot, err := NewUnattendedCatalog(registry, nil, audit, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
			if err != nil {
				t.Fatal(err)
			}
			ctx := mcpaudit.WithMetadata(t.Context(), mcpaudit.Metadata{
				RequestedTool: guardedRemoteDefinition.Name,
				SafeArguments: tt.raw,
			})
			transforms := 0
			_, err = snapshot.CallWithTransform(ctx, guardedRemoteDefinition.Name, tt.raw, func(arguments string) (string, error) {
				transforms++
				return arguments, nil
			})
			if _, ok := mcpintent.AsBlocked(err); !ok {
				t.Fatalf("error = %v, want blocked", err)
			}
			if len(evaluator.inputs) != 0 || transforms != 0 || len(registry.snapshots[testUnattendedUsername].calls) != 0 {
				t.Fatalf("classifier=%d transforms=%d remote=%v", len(evaluator.inputs), transforms, registry.snapshots[testUnattendedUsername].calls)
			}
			if len(store.calls) != 1 {
				t.Fatalf("audit calls = %d, want 1", len(store.calls))
			}
			got := store.calls[0]
			if got.Arguments != `{}` || got.Intent != "" || strings.Contains(got.Arguments, "sensitive-audit-payload") || strings.Contains(got.Arguments, mcpintent.ArgumentName) {
				t.Fatalf("audit call = %+v", got)
			}
		})
	}
}

func TestFITPropagatesIntentAndClassifiesGeneratedDownload(t *testing.T) {
	evaluator := allowEvaluator()
	download := provider.ToolDefinition{
		Name:        testDownloadFITTool,
		Description: testDownloadFITDesc,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"activity_id":{"type":"integer"}},"required":["activity_id"]}`),
	}
	registry := registryWithGuardedTools(download)
	registry.snapshots[testUnattendedUsername].result = `{"path":"/data/fit/activity.fit"}`
	bridgeCalls := 0
	bridge := newTestBridge(t, &bridgeCalls)
	snapshot, err := NewUnattendedCatalog(registry, []FITRoute{{
		ServerName: testFITServerName, ServerScope: testFITAliceScope, DownloadTool: testDownloadFITName, BridgeURL: bridge,
	}}, nil, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mcpintent.WithTrustedContext(t.Context(), mcpintent.TrustedContext{Request: testAnalyzeFITIntent})
	_, err = snapshot.Call(ctx, analyzeGarminFITToolName, testFITIntentArgs)
	if err == nil {
		t.Fatal("FIT decoding unexpectedly succeeded")
	}
	if len(evaluator.inputs) != 2 || evaluator.inputs[1] != (mcpintent.Input{
		Intent: testAnalyzeFITIntent, ToolName: testDownloadFITTool,
		ToolDescription: testDownloadFITDesc, Arguments: testFITArguments,
	}) {
		t.Fatalf("classifier inputs = %+v", evaluator.inputs)
	}
	remote := registry.snapshots[testUnattendedUsername]
	if len(remote.calls) != 1 || remote.calls[0] != testDownloadFITTool || remote.arguments[0] != testFITArguments || bridgeCalls != 1 {
		t.Fatalf("remote calls=%v args=%v bridge=%d", remote.calls, remote.arguments, bridgeCalls)
	}
}

func TestFITDeniedDownloadSkipsRemoteAndBridge(t *testing.T) {
	evaluator := &recordingEvaluator{decisions: []mcpintent.Decision{
		{Verdict: mcpintent.VerdictAllow, Reason: "analyze requested activity"},
		{Verdict: mcpintent.VerdictDeny, Reason: "download not needed"},
	}}
	download := provider.ToolDefinition{
		Name: testDownloadFITTool, Description: testDownloadFITDesc,
		Parameters: json.RawMessage(`{"type":"object","properties":{"activity_id":{"type":"integer"}},"required":["activity_id"]}`),
	}
	registry := registryWithGuardedTools(download)
	bridgeCalls := 0
	bridge := newTestBridge(t, &bridgeCalls)
	snapshot, err := NewUnattendedCatalog(registry, []FITRoute{{
		ServerName: testFITServerName, ServerScope: testFITAliceScope, DownloadTool: testDownloadFITName, BridgeURL: bridge,
	}}, nil, evaluator).SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mcpintent.WithTrustedContext(t.Context(), mcpintent.TrustedContext{Request: testAnalyzeFITIntent})
	_, err = snapshot.Call(ctx, analyzeGarminFITToolName, testFITIntentArgs)
	if _, ok := mcpintent.AsBlocked(err); !ok || len(evaluator.inputs) != 2 || len(registry.snapshots[testUnattendedUsername].calls) != 0 || bridgeCalls != 0 {
		t.Fatalf("err=%v inputs=%+v remote=%v bridge=%d", err, evaluator.inputs, registry.snapshots[testUnattendedUsername].calls, bridgeCalls)
	}
}

func findTool(t *testing.T, tools []provider.ToolDefinition, name string) provider.ToolDefinition {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in %v", name, toolNames(t, &UnattendedSnapshot{tools: tools}))
	return provider.ToolDefinition{}
}

func assertRequiredProperty(t *testing.T, definition provider.ToolDefinition, name string) {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties[name]; !ok || !slices.Contains(schema.Required, name) {
		t.Fatalf("parameters = %s, want required %q", definition.Parameters, name)
	}
}

func assertNoRequiredProperty(t *testing.T, definition provider.ToolDefinition, name string) {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties[name]; ok || slices.Contains(schema.Required, name) {
		t.Fatalf("parameters = %s, do not want %q", definition.Parameters, name)
	}
}

func equalDefinitions(left, right provider.ToolDefinition) bool {
	return left.Name == right.Name && left.Description == right.Description && bytes.Equal(left.Parameters, right.Parameters)
}

func newTestBridge(t *testing.T, calls *int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		*calls++
	}))
	t.Cleanup(server.Close)
	return server.URL
}
