package mcp

import "testing"

const testGarminGlobalURLEnv = "MCP_GARMIN_GLOBAL_URL=http://x/mcp"

func TestServersFromEnv(t *testing.T) {
	env := []string{
		"MCP_GARMIN_GLOBAL_URL=http://garmin:8080",
		"MCP_GARMIN_GLOBAL_TRANSPORT=streamable-http",
		"MCP_GARMIN_GLOBAL_AUTH_USER=kadence",
		"MCP_GARMIN_GLOBAL_AUTH_PASS=secret",
		"MCP_STRAVA_USER_philipp_URL=http://strava-philipp:8080",
		"MCP_STRAVA_USER_philipp_TRANSPORT=sse",
		"UNRELATED=x",
	}
	got, err := ServersFromEnv(env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 servers, got %d: %+v", len(got), got)
	}
	byKey := map[string]Server{}
	for _, s := range got {
		byKey[s.Name+"/"+s.Scope] = s
	}
	g := byKey["GARMIN/GLOBAL"]
	if g.URL != "http://garmin:8080" || g.Transport != "streamable-http" || g.AuthUser != "kadence" || g.AuthPass != "secret" {
		t.Fatalf("garmin parsed wrong: %+v", g)
	}
	s := byKey["STRAVA/USER_philipp"]
	if s.URL != "http://strava-philipp:8080" || s.Transport != "sse" {
		t.Fatalf("strava parsed wrong: %+v", s)
	}
}

func TestServersFromEnvParsesTools(t *testing.T) {
	env := []string{
		testGarminGlobalURLEnv,
		"MCP_GARMIN_GLOBAL_TOOLS=get_activity*, *_workout ,get_exercise_types",
	}
	servers, err := ServersFromEnv(env)
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers=%d err=%v", len(servers), err)
	}
	got := servers[0].Tools
	want := []string{"get_activity*", "*_workout", "get_exercise_types"}
	if len(got) != len(want) {
		t.Fatalf("Tools=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Tools[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestServersFromEnvNoToolsMeansNil(t *testing.T) {
	servers, _ := ServersFromEnv([]string{testGarminGlobalURLEnv})
	if servers[0].Tools != nil {
		t.Fatalf("expected nil Tools, got %v", servers[0].Tools)
	}
}

func TestServersFromEnvParsesAliasAndHint(t *testing.T) {
	env := []string{
		"MCP_CLOAKBROWSER_GLOBAL_URL=http://x/mcp",
		"MCP_CLOAKBROWSER_GLOBAL_ALIAS=browser",
		"MCP_CLOAKBROWSER_GLOBAL_HINT=a full live web browser — use for current info: weather, news, prices",
	}
	servers, err := ServersFromEnv(env)
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers=%d err=%v", len(servers), err)
	}
	if servers[0].Alias != testBrowserAlias {
		t.Fatalf("Alias=%q want %q", servers[0].Alias, testBrowserAlias)
	}
	if servers[0].Hint != "a full live web browser — use for current info: weather, news, prices" {
		t.Fatalf("Hint=%q", servers[0].Hint)
	}
}

func TestServersFromEnvNoAliasOrHintMeansEmpty(t *testing.T) {
	servers, _ := ServersFromEnv([]string{testGarminGlobalURLEnv})
	if servers[0].Alias != "" || servers[0].Hint != "" {
		t.Fatalf("want empty Alias/Hint, got Alias=%q Hint=%q", servers[0].Alias, servers[0].Hint)
	}
}

func TestServersFromEnvInvalidAliasFallsBackToEmpty(t *testing.T) {
	env := []string{
		testGarminGlobalURLEnv,
		"MCP_GARMIN_GLOBAL_ALIAS=Not_Valid!",
	}
	servers, err := ServersFromEnv(env)
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers=%d err=%v", len(servers), err)
	}
	if servers[0].Alias != "" {
		t.Fatalf("want invalid alias dropped (empty), got %q", servers[0].Alias)
	}
}

func TestScopeAppliesToUser(t *testing.T) {
	if !(Server{Scope: "GLOBAL"}).AppliesTo("anyone") {
		t.Fatal("GLOBAL applies to everyone")
	}
	if !(Server{Scope: "USER_philipp"}).AppliesTo("philipp") {
		t.Fatal("USER_philipp applies to philipp")
	}
	if (Server{Scope: "USER_philipp"}).AppliesTo("bob") {
		t.Fatal("USER_philipp must NOT apply to bob")
	}
}
