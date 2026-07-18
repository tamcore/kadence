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

func TestLoginThenCurrentUser(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)

	hash, _ := auth.HashPassword("pw123")
	if _, err := users.Create(context.Background(), model.User{
		Username: "alice", Email: "a@x.io", PasswordHash: hash, Role: model.RoleUser,
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(api.NewRouter(api.Deps{Users: users, Sessions: sessions, Config: config.Config{}}))
	defer srv.Close()
	jar := &cookieJar{}

	resp, err := http.Post(srv.URL+"/api/session", "application/json",
		strings.NewReader(`{"username":"alice","password":"pw123","remember":false}`))
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
	if env.Data.Username != "alice" {
		t.Fatalf("expected alice, got %q", env.Data.Username)
	}
}

type cookieJar struct{ cookies []*http.Cookie }

func (j *cookieJar) capture(resp *http.Response) { j.cookies = append(j.cookies, resp.Cookies()...) }
func (j *cookieJar) apply(req *http.Request) {
	for _, c := range j.cookies {
		req.AddCookie(c)
	}
}
