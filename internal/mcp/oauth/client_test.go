package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testClientID  = "kadence-test"
	testRedirect  = "https://kadence.example.invalid/api/mcp/oauth/callback"
	testScope     = "garmin:read"
	testRefreshV1 = "rt-1"
	testRefreshV2 = "rt-2"

	fieldAccessToken = "access_token"
	fieldExpiresIn   = "expires_in"
	fieldError       = "error"

	testAccessV1   = "at-1"
	testAccessV2   = "at-2"
	codeBadGrant   = "invalid_grant"
	codeServerFail = "server_error"
)

// metadataServer serves both discovery documents for its own origin, so the
// issuer check has a real origin to compare against.
func metadataServer(t *testing.T, tweak func(prm, asm map[string]any)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	prm := map[string]any{
		paramResource:              srv.URL + "/mcp",
		"authorization_servers":    []string{srv.URL},
		"scopes_supported":         []string{testScope},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Garmin Connect MCP",
	}
	asm := map[string]any{
		"issuer":                                srv.URL,
		"authorization_endpoint":                srv.URL + "/authorize",
		"token_endpoint":                        srv.URL + "/token",
		"revocation_endpoint":                   srv.URL + "/revoke",
		"scopes_supported":                      []string{testScope},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic"},
		"code_challenge_methods_supported":      []string{"S256"},
	}
	if tweak != nil {
		tweak(prm, asm)
	}

	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(prm)
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(asm)
	})
	return srv
}

func TestDiscoverReadsBothDocuments(t *testing.T) {
	srv := metadataServer(t, nil)
	md, err := Discover(context.Background(), srv.Client(), srv.URL+"/mcp")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if md.Issuer != srv.URL || md.Resource != srv.URL+"/mcp" {
		t.Fatalf("issuer/resource = %q/%q", md.Issuer, md.Resource)
	}
	if md.AuthorizationEndpoint != srv.URL+"/authorize" || md.TokenEndpoint != srv.URL+"/token" {
		t.Fatalf("endpoints = %q/%q", md.AuthorizationEndpoint, md.TokenEndpoint)
	}
	if md.RevocationEndpoint != srv.URL+"/revoke" {
		t.Fatalf("revocation endpoint = %q", md.RevocationEndpoint)
	}
	if len(md.ScopesSupported) != 1 || md.ScopesSupported[0] != testScope {
		t.Fatalf("scopes = %v", md.ScopesSupported)
	}
}

func TestDiscoverRefusesABadDeployment(t *testing.T) {
	t.Run("cleartext authorization server", func(t *testing.T) {
		srv := metadataServer(t, func(prm, _ map[string]any) {
			prm["authorization_servers"] = []string{"http://insecure.example.invalid"}
		})
		if _, err := Discover(context.Background(), srv.Client(), srv.URL+"/mcp"); err == nil {
			t.Fatal("Discover accepted a cleartext authorization server")
		}
	})

	t.Run("issuer is not the origin it was served from", func(t *testing.T) {
		srv := metadataServer(t, func(_, asm map[string]any) {
			asm["issuer"] = "https://elsewhere.example.invalid"
		})
		if _, err := Discover(context.Background(), srv.Client(), srv.URL+"/mcp"); err == nil {
			t.Fatal("Discover accepted an issuer that is not its own origin")
		}
	})

	t.Run("document missing", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.NotFoundHandler())
		t.Cleanup(srv.Close)
		if _, err := Discover(context.Background(), srv.Client(), srv.URL+"/mcp"); err == nil {
			t.Fatal("Discover accepted a deployment with no metadata")
		}
	})
}

// tokenServer records the last form it received and answers with body/status.
type tokenServer struct {
	*httptest.Server
	lastForm url.Values
	lastAuth string
	calls    int
}

func newTokenServer(t *testing.T, status int, body map[string]any) *tokenServer {
	t.Helper()
	ts := &tokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		ts.calls++
		ts.lastForm = r.PostForm
		ts.lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func clientFor(ts *tokenServer, secret string) *Client {
	md := Metadata{
		Issuer:                ts.URL,
		AuthorizationEndpoint: ts.URL + "/authorize",
		TokenEndpoint:         ts.URL,
		RevocationEndpoint:    ts.URL,
		Resource:              ts.URL + "/mcp",
	}
	return NewClient(nil, md, testClientID, secret)
}

func TestExchangeSendsThePKCEAndResourceAndParsesTheTokens(t *testing.T) {
	ts := newTokenServer(t, http.StatusOK, map[string]any{
		fieldAccessToken:  testAccessV1,
		"token_type":      "Bearer",
		fieldExpiresIn:    900,
		paramRefreshToken: testRefreshV1,
		paramScope:        testScope,
	})
	c := clientFor(ts, "")

	got, err := c.Exchange(context.Background(), "code-1", "verifier-1", testRedirect)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if got.AccessToken != testAccessV1 || got.RefreshToken != testRefreshV1 || got.Scope != testScope {
		t.Fatalf("tokens parsed wrong: %+v", got)
	}
	if got.ExpiresIn != 900*time.Second {
		t.Fatalf("expires in %s, want 15m", got.ExpiresIn)
	}

	form := ts.lastForm
	for k, want := range map[string]string{
		"grant_type":    "authorization_code",
		paramCode:       "code-1",
		"code_verifier": "verifier-1",
		"redirect_uri":  testRedirect,
		paramResource:   ts.URL + "/mcp",
		"client_id":     testClientID,
	} {
		if form.Get(k) != want {
			t.Fatalf("form[%s] = %q, want %q", k, form.Get(k), want)
		}
	}
	if ts.lastAuth != "" {
		t.Fatalf("a public client sent an Authorization header: %q", ts.lastAuth)
	}
}

func TestExchangeAuthenticatesAConfidentialClient(t *testing.T) {
	ts := newTokenServer(t, http.StatusOK, map[string]any{fieldAccessToken: "at", fieldExpiresIn: 900, paramRefreshToken: "rt"})
	c := clientFor(ts, "s3cret")

	if _, err := c.Exchange(context.Background(), "code", "verifier", testRedirect); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if !strings.HasPrefix(ts.lastAuth, "Basic ") {
		t.Fatalf("Authorization = %q, want client_secret_basic", ts.lastAuth)
	}
}

func TestRefreshRotatesAndReportsADeadFamily(t *testing.T) {
	ok := newTokenServer(t, http.StatusOK, map[string]any{
		fieldAccessToken: testAccessV2, fieldExpiresIn: 900, paramRefreshToken: testRefreshV2, paramScope: testScope,
	})
	got, err := clientFor(ok, "").Refresh(context.Background(), testRefreshV1)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got.AccessToken != testAccessV2 || got.RefreshToken != testRefreshV2 {
		t.Fatalf("rotation parsed wrong: %+v", got)
	}
	if ok.lastForm.Get("grant_type") != "refresh_token" || ok.lastForm.Get("refresh_token") != testRefreshV1 {
		t.Fatalf("form = %v", ok.lastForm)
	}

	dead := newTokenServer(t, http.StatusBadRequest, map[string]any{
		fieldError: codeBadGrant, "error_description": "The refresh token is no longer valid.",
	})
	if _, err := clientFor(dead, "").Refresh(context.Background(), testRefreshV1); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("Refresh on a dead family: %v, want ErrInvalidGrant", err)
	}
}

func TestTokenEndpointFaultIsNotADeadFamily(t *testing.T) {
	broken := newTokenServer(t, http.StatusInternalServerError, map[string]any{fieldError: codeServerFail})
	_, err := clientFor(broken, "").Refresh(context.Background(), testRefreshV1)
	if err == nil {
		t.Fatal("Refresh accepted a 500")
	}
	if errors.Is(err, ErrInvalidGrant) {
		t.Fatal("a 500 was reported as invalid_grant; a server fault must not unlink a user")
	}
}

func TestErrorDescriptionIsNeverEchoed(t *testing.T) {
	const leak = "rt-secret-value-should-not-appear"
	ts := newTokenServer(t, http.StatusBadRequest, map[string]any{
		fieldError: "invalid_request", "error_description": leak,
	})
	_, err := clientFor(ts, "").Exchange(context.Background(), "code", "verifier", testRedirect)
	if err == nil {
		t.Fatal("Exchange accepted a 400")
	}
	if strings.Contains(err.Error(), leak) {
		t.Fatalf("error echoed the server's description: %v", err)
	}
}

func TestAuthorizeURLCarriesEverythingTheServerRequires(t *testing.T) {
	ts := newTokenServer(t, http.StatusOK, nil)
	c := clientFor(ts, "")

	raw := c.AuthorizeURL(testRedirect, "state-1", "challenge-1", []string{testScope})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("AuthorizeURL produced an unparsable URL: %v", err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"response_type":         "code",
		"client_id":             testClientID,
		"redirect_uri":          testRedirect,
		"state":                 "state-1",
		"code_challenge":        "challenge-1",
		"code_challenge_method": "S256",
		paramResource:           ts.URL + "/mcp",
		paramScope:              testScope,
	} {
		if q.Get(k) != want {
			t.Fatalf("query[%s] = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestRevokeTreatsAnAlreadyDeadTokenAsSuccess(t *testing.T) {
	for name, status := range map[string]int{"ok": http.StatusOK, "already dead": http.StatusBadRequest} {
		body := map[string]any{}
		if status == http.StatusBadRequest {
			body["error"] = "invalid_token"
		}
		ts := newTokenServer(t, status, body)
		if err := clientFor(ts, "").Revoke(context.Background(), testRefreshV1); err != nil {
			t.Fatalf("%s: Revoke: %v", name, err)
		}
	}
}

func TestNilHTTPClientIsNormalized(t *testing.T) {
	ts := newTokenServer(t, http.StatusOK, map[string]any{fieldAccessToken: "at", fieldExpiresIn: 900, paramRefreshToken: "rt"})
	// clientFor passes nil, which is what mcp.HTTPClientWithCA returns when no
	// custom CA is configured — the default deployment.
	if _, err := clientFor(ts, "").Exchange(context.Background(), "code", "verifier", testRedirect); err != nil {
		t.Fatalf("Exchange with a nil http client: %v", err)
	}
}
