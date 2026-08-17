package mcp

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
)

const (
	testOAuthClientID  = "kadence"
	testOAuthResource  = "https://garmin.example.invalid/mcp"
	envGarminURL       = "MCP_GARMIN_GLOBAL_URL=" + testOAuthResource
	envGarminTransport = "MCP_GARMIN_GLOBAL_TRANSPORT=streamable-http"
	envGarminClientID  = "MCP_GARMIN_GLOBAL_OAUTH_CLIENT_ID=" + testOAuthClientID
	envGarminResource  = "MCP_GARMIN_GLOBAL_OAUTH_RESOURCE=" + testOAuthResource
	envGarminScopes    = "MCP_GARMIN_GLOBAL_OAUTH_SCOPES=garmin:read"
)

// stubPrincipals resolves usernames to fixed ids.
type stubPrincipals map[string]int64

func (s stubPrincipals) UserIDFor(_ context.Context, username string) (int64, error) {
	id, ok := s[username]
	if !ok {
		return 0, errors.New("stub: unknown user")
	}
	return id, nil
}

func TestServersFromEnvParsesAuthMode(t *testing.T) {
	env := []string{
		envGarminURL,
		envGarminTransport,
		"MCP_GARMIN_GLOBAL_AUTH_MODE=OAuth",
		envGarminClientID,
		envGarminResource,
		envGarminScopes,
	}
	got, err := ServersFromEnv(env)
	if err != nil {
		t.Fatalf("ServersFromEnv: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 server, got %d", len(got))
	}
	if got[0].AuthMode != authModeOAuth {
		t.Fatalf("AuthMode = %q, want %q", got[0].AuthMode, authModeOAuth)
	}
	if !got[0].PerPrincipal() {
		t.Fatal("PerPrincipal = false for an oauth server, want true")
	}
}

func TestServersFromEnvParsesOAuthClient(t *testing.T) {
	env := []string{
		envGarminURL,
		envGarminTransport,
		"MCP_GARMIN_GLOBAL_AUTH_MODE=oauth",
		envGarminClientID,
		"MCP_GARMIN_GLOBAL_OAUTH_CLIENT_SECRET=s3cret",
		"MCP_GARMIN_GLOBAL_OAUTH_SCOPES=garmin:read, garmin:read ,",
		envGarminResource,
	}
	got, err := ServersFromEnv(env)
	if err != nil {
		t.Fatalf("ServersFromEnv: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 server, got %d", len(got))
	}
	s := got[0]
	if s.OAuthClientID != testOAuthClientID || s.OAuthClientSecret != "s3cret" {
		t.Fatalf("client identity parsed wrong: id=%q secret set=%v", s.OAuthClientID, s.OAuthClientSecret != "")
	}
	if s.OAuthResource != testOAuthResource {
		t.Fatalf("resource = %q", s.OAuthResource)
	}
	if len(s.OAuthScopes) != 1 || s.OAuthScopes[0] != "garmin:read" {
		t.Fatalf("scopes = %v, want one deduplicated garmin:read", s.OAuthScopes)
	}
}

func TestServersFromEnvDropsOAuthServerMissingClientOrResource(t *testing.T) {
	base := []string{
		envGarminURL,
		envGarminTransport,
		"MCP_GARMIN_GLOBAL_AUTH_MODE=oauth",
	}
	for name, extra := range map[string][]string{
		"no client id": {envGarminResource, envGarminScopes},
		"no resource":  {envGarminClientID, envGarminScopes},
		"no scopes":    {envGarminClientID, envGarminResource},
		"ungrantable":  {envGarminClientID, envGarminResource, "MCP_GARMIN_GLOBAL_OAUTH_SCOPES=garmin:write"},
		"resource mismatch": {envGarminClientID, envGarminScopes,
			"MCP_GARMIN_GLOBAL_OAUTH_RESOURCE=https://other.example.invalid/mcp"},
		"neither": {},
	} {
		got, err := ServersFromEnv(append(append([]string{}, base...), extra...))
		if err != nil {
			t.Fatalf("%s: ServersFromEnv: %v", name, err)
		}
		if len(got) != 0 {
			t.Fatalf("%s: server survived validation: %+v", name, got)
		}
	}
}

func TestBasicAuthServerIsNotPerPrincipal(t *testing.T) {
	env := []string{
		"MCP_MARKITDOWN_GLOBAL_URL=http://markitdown:8080/mcp/",
		"MCP_MARKITDOWN_GLOBAL_TRANSPORT=streamable-http",
	}
	got, err := ServersFromEnv(env)
	if err != nil {
		t.Fatalf("ServersFromEnv: %v", err)
	}
	if got[0].PerPrincipal() {
		t.Fatal("PerPrincipal = true for a basic-auth server, want false")
	}
}

func TestClientCacheKeyIncludesPrincipalOnlyForOAuth(t *testing.T) {
	shared := Server{Name: "MARKITDOWN", Scope: scopeGlobal}
	perUser := Server{Name: "GARMIN", Scope: scopeGlobal, AuthMode: authModeOAuth}

	if got, want := clientCacheKey(shared, "7"), "MARKITDOWN/GLOBAL"; got != want {
		t.Fatalf("shared key = %q, want %q", got, want)
	}
	if got, want := clientCacheKey(perUser, "7"), "GARMIN/GLOBAL/7"; got != want {
		t.Fatalf("per-principal key = %q, want %q", got, want)
	}
	if clientCacheKey(perUser, "7") == clientCacheKey(perUser, "8") {
		t.Fatal("two principals produced the same cache key")
	}
}

func TestClientForIsolatesPrincipalsOnOAuthServer(t *testing.T) {
	ts := newFakeGarminServer(t)
	s := Server{
		Name: testGarminName, Scope: scopeGlobal, URL: ts.URL,
		Transport: transportStreamableHTTP, AuthMode: authModeOAuth, FromEnv: true,
	}
	reg := NewRegistry([]Server{s}, nil, nil)
	t.Cleanup(func() { _ = reg.Close() })

	a, releaseA, err := reg.clientFor(context.Background(), s, "7")
	if err != nil {
		t.Fatalf("clientFor(principal 7): %v", err)
	}
	defer releaseA()

	b, releaseB, err := reg.clientFor(context.Background(), s, "8")
	if err != nil {
		t.Fatalf("clientFor(principal 8): %v", err)
	}
	defer releaseB()

	if a == b {
		t.Fatal("two principals share one client on an oauth server")
	}
	if n := len(reg.clients); n != 2 {
		t.Fatalf("cached clients = %d, want 2 (one per principal)", n)
	}
}

func TestClientForSharesOneClientOnBasicServer(t *testing.T) {
	ts := newFakeGarminServer(t)
	s := Server{
		Name: testGarminName, Scope: scopeGlobal, URL: ts.URL,
		Transport: transportStreamableHTTP, FromEnv: true,
	}
	reg := NewRegistry([]Server{s}, nil, nil)
	t.Cleanup(func() { _ = reg.Close() })

	a, releaseA, err := reg.clientFor(context.Background(), s, "7")
	if err != nil {
		t.Fatalf("clientFor(principal 7): %v", err)
	}
	defer releaseA()

	b, releaseB, err := reg.clientFor(context.Background(), s, "8")
	if err != nil {
		t.Fatalf("clientFor(principal 8): %v", err)
	}
	defer releaseB()

	if a != b {
		t.Fatal("a basic-auth server was dialed twice for two users")
	}
}

func TestEvictClientEvictsOnlyThatPrincipal(t *testing.T) {
	ts := newFakeGarminServer(t)
	s := Server{
		Name: testGarminName, Scope: scopeGlobal, URL: ts.URL,
		Transport: transportStreamableHTTP, AuthMode: authModeOAuth, FromEnv: true,
	}
	reg := NewRegistry([]Server{s}, nil, nil)
	t.Cleanup(func() { _ = reg.Close() })

	_, releaseA, err := reg.clientFor(context.Background(), s, "7")
	if err != nil {
		t.Fatalf("clientFor(principal 7): %v", err)
	}
	releaseA()
	b, releaseB, err := reg.clientFor(context.Background(), s, "8")
	if err != nil {
		t.Fatalf("clientFor(principal 8): %v", err)
	}
	releaseB()

	reg.evictClient(s, "7")

	if _, ok := reg.clients[clientCacheKey(s, "7")]; ok {
		t.Fatal("principal 7's client survived its own eviction")
	}
	again, release, err := reg.clientFor(context.Background(), s, "8")
	if err != nil {
		t.Fatalf("clientFor(principal 8, after evict): %v", err)
	}
	defer release()
	if again != b {
		t.Fatal("evicting principal 7 also evicted principal 8")
	}
}

func TestConcurrentPrincipalsNeverShareAClient(t *testing.T) {
	ts := newFakeGarminServer(t)
	s := Server{
		Name: testGarminName, Scope: scopeGlobal, URL: ts.URL,
		Transport: transportStreamableHTTP, AuthMode: authModeOAuth, FromEnv: true,
	}
	reg := NewRegistry([]Server{s}, nil, nil)
	t.Cleanup(func() { _ = reg.Close() })

	const principals = 8
	var wg sync.WaitGroup
	seen := make([]mcpClient, principals)
	errs := make([]error, principals)
	for i := range principals {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, release, err := reg.clientFor(context.Background(), s, strconv.Itoa(i+1))
			if err != nil {
				errs[i] = err
				return
			}
			defer release()
			seen[i] = c
		}(i)
	}
	wg.Wait()

	unique := make(map[mcpClient]bool, principals)
	for i, c := range seen {
		if errs[i] != nil {
			t.Fatalf("principal %d: %v", i+1, errs[i])
		}
		if c == nil {
			t.Fatalf("principal %d got no client", i+1)
		}
		if unique[c] {
			t.Fatalf("principal %d shares a client with an earlier principal", i+1)
		}
		unique[c] = true
	}
}

func TestToolsForSkipsPerPrincipalServerWithoutAPrincipalSource(t *testing.T) {
	ts := newFakeGarminServer(t)
	s := Server{
		Name: testGarminName, Scope: scopeGlobal, URL: ts.URL,
		Transport: transportStreamableHTTP, AuthMode: authModeOAuth, FromEnv: true,
	}
	reg := NewRegistry([]Server{s}, nil, nil)
	t.Cleanup(func() { _ = reg.Close() })

	defs, err := reg.ToolsFor(context.Background(), testUsername)
	if err != nil {
		t.Fatalf("ToolsFor: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("got %d tool definitions with no principal source, want 0", len(defs))
	}
}

func TestToolsForUsesResolvedPrincipal(t *testing.T) {
	ts := newFakeGarminServer(t)
	s := Server{
		Name: testGarminName, Scope: scopeGlobal, URL: ts.URL,
		Transport: transportStreamableHTTP, AuthMode: authModeOAuth, FromEnv: true,
	}
	reg := NewRegistry([]Server{s}, nil, nil)
	reg.SetPrincipalSource(stubPrincipals{testUsername: 42})
	t.Cleanup(func() { _ = reg.Close() })

	defs, err := reg.ToolsFor(context.Background(), testUsername)
	if err != nil {
		t.Fatalf("ToolsFor: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("got no tool definitions for a resolvable principal")
	}
	if _, ok := reg.clients[clientCacheKey(s, "42")]; !ok {
		t.Fatalf("no client cached under the resolved principal; cache holds %d entries", len(reg.clients))
	}
}
