package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const testUsername = "alice"

func TestLoginThenCurrentUser(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)

	hash, _ := auth.HashPassword("pw123")
	if _, err := users.Create(context.Background(), model.User{
		Username: testUsername, Email: "a@x.io", PasswordHash: hash, Role: model.RoleUser,
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(api.NewRouter(api.Deps{Users: users, Sessions: sessions, Config: config.Config{}}))
	defer srv.Close()
	jar := &cookieJar{}

	resp, err := http.Post(srv.URL+"/api/session", "application/json",
		strings.NewReader(`{"username":"`+testUsername+`","password":"pw123","remember":false}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v code=%v", err, resp.StatusCode)
	}
	jar.capture(resp)
	_ = resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/session", nil)
	jar.apply(req)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil || resp2.StatusCode != http.StatusOK {
		t.Fatalf("current user: %v code=%v", err, resp2.StatusCode)
	}
	var env struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&env)
	_ = resp2.Body.Close()
	if env.Data.Username != testUsername {
		t.Fatalf("expected %s, got %q", testUsername, env.Data.Username)
	}
}

func TestCSRFRejectsUnsafeRequestWithoutToken(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)

	hash, _ := auth.HashPassword("pw123")
	if _, err := users.Create(context.Background(), model.User{
		Username: testUsername, Email: "a@x.io", PasswordHash: hash, Role: model.RoleUser,
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Users:    users,
		Sessions: sessions,
		Config:   config.Config{CSRFSecret: "0123456789abcdef0123456789abcdef"},
	}))
	defer srv.Close()
	jar := &cookieJar{}

	resp, err := http.Post(srv.URL+"/api/session", "application/json",
		strings.NewReader(`{"username":"`+testUsername+`","password":"pw123","remember":false}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v code=%v", err, resp.StatusCode)
	}
	jar.capture(resp)
	_ = resp.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/session", nil)
	jar.apply(req)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete session: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for DELETE without CSRF token, got %d", resp2.StatusCode)
	}
}

type cookieJar struct{ cookies []*http.Cookie }

func (j *cookieJar) capture(resp *http.Response) { j.cookies = append(j.cookies, resp.Cookies()...) }
func (j *cookieJar) apply(req *http.Request) {
	for _, c := range j.cookies {
		req.AddCookie(c)
	}
}
