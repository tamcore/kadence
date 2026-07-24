package chat

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/tamcore/kadence/internal/provider"
)

const (
	testUnattendedUsername = "alice"
	testSharedPrivateTool  = "shared__private"
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
	result      string
	err         error
}

func (s *catalogMCPSnapshot) ToolsFor(context.Context) ([]provider.ToolDefinition, error) {
	return append([]provider.ToolDefinition(nil), s.definitions...), s.err
}

func (s *catalogMCPSnapshot) Call(_ context.Context, name, _ string) (string, error) {
	s.calls = append(s.calls, name)
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
			{Name: analyzeGarminFITToolName, Description: "spoofed"},
		},
		prefixes: map[string]string{"GARMIN/USER_alice": "shared"},
		result:   "alice-result",
	}
	bob := &catalogMCPSnapshot{
		definitions: []provider.ToolDefinition{{Name: testSharedPrivateTool, Description: "bob"}},
		prefixes:    map[string]string{"GARMIN/USER_bob": "shared"},
		result:      "bob-result",
	}
	catalog := NewUnattendedCatalog(&catalogRegistry{snapshots: map[string]*catalogMCPSnapshot{
		testUnattendedUsername: alice,
		"bob":                  bob,
	}}, []FITRoute{
		{ServerName: "GARMIN", ServerScope: "USER_alice", DownloadTool: testFITGenericTool, BridgeURL: "http://alice"},
		{ServerName: "GARMIN", ServerScope: "USER_bob", DownloadTool: testFITGenericTool, BridgeURL: "http://bob"},
	})

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
	if !slices.Equal(aliceTools, []string{testSharedPrivateTool, analyzeGarminFITToolName}) {
		t.Fatalf("alice tools = %v", aliceTools)
	}
	if !slices.Equal(bobTools, []string{testSharedPrivateTool, analyzeGarminFITToolName}) {
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
	if _, err := aliceSnapshot.Call(t.Context(), analyzeGarminFITToolName, `{"activity_id":1,"unexpected":true}`); err == nil {
		t.Fatal("native FIT accepted unknown arguments")
	}
	if !slices.Equal(alice.calls, []string{testSharedPrivateTool}) {
		t.Fatalf("invalid FIT reached MCP: %v", alice.calls)
	}
}

func TestUnattendedCatalogFailsClosed(t *testing.T) {
	catalog := NewUnattendedCatalog(nil, nil)
	snapshot, err := catalog.SnapshotFor(t.Context(), testUnattendedUsername)
	if err != nil {
		t.Fatal(err)
	}
	if tools, err := snapshot.ToolsFor(t.Context()); err != nil || len(tools) != 0 {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	if _, err := snapshot.Call(t.Context(), "missing", `{}`); err == nil {
		t.Fatal("missing tool call succeeded")
	}

	want := errors.New("list failed")
	broken := NewUnattendedCatalog(&catalogRegistry{snapshots: map[string]*catalogMCPSnapshot{
		testUnattendedUsername: {err: want},
	}}, nil)
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
