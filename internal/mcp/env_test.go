package mcp

import "testing"

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
