package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api"
	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const testUsername = "alice"
const testEmail = "a@x.io"

func TestLoginThenCurrentUser(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)

	hash, _ := auth.HashPassword("pw123")
	if _, err := users.Create(context.Background(), model.User{
		Username: testUsername, Email: testEmail, PasswordHash: hash, Role: model.RoleUser,
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
		Username: testUsername, Email: testEmail, PasswordHash: hash, Role: model.RoleUser,
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

func TestChatEndToEnd(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	convs := store.NewConversationRepository(pool)
	msgs := store.NewMessageRepository(pool)

	hash, _ := auth.HashPassword("pw123")
	u, _ := users.Create(context.Background(), model.User{Username: testUsername, Email: testEmail, PasswordHash: hash, Role: model.RoleUser})

	chatSvc := chat.NewService(chatFakeProvider{reply: "Hi!"},
		chat.ServiceConfig{Model: "m", MaxTokens: 32, Temperature: 0.2}, convs, msgs, nil, nil)
	chatH := handlers.NewChat(chatSvc, convs, msgs)

	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Users: users, Sessions: sessions, Config: config.Config{CSRFSecret: "0123456789abcdef0123456789abcdef"}, Chat: chatH,
	}))
	defer srv.Close()
	jar := &cookieJar{}

	resp, _ := http.Post(srv.URL+"/api/session", "application/json",
		strings.NewReader(`{"username":"`+testUsername+`","password":"pw123","remember":false}`))
	jar.capture(resp)
	_ = resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/session", nil)
	jar.apply(req)
	sresp, _ := http.DefaultClient.Do(req)
	jar.capture(sresp)
	token := sresp.Header.Get("X-CSRF-Token")
	_ = sresp.Body.Close()

	creq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/chat", strings.NewReader(`{"message":"hi"}`))
	creq.Header.Set("Content-Type", "application/json")
	creq.Header.Set("X-CSRF-Token", token)
	jar.apply(creq)
	cresp, err := http.DefaultClient.Do(creq)
	if err != nil || cresp.StatusCode != http.StatusOK {
		t.Fatalf("chat: %v code=%v", err, cresp.StatusCode)
	}
	buf := new(strings.Builder)
	_, _ = io.Copy(buf, cresp.Body)
	_ = cresp.Body.Close()
	if !strings.Contains(buf.String(), `"type":"done"`) {
		t.Fatalf("no done event: %s", buf.String())
	}

	list, _ := convs.ListByUser(context.Background(), u.ID)
	if len(list) != 1 {
		t.Fatalf("conversations = %d, want 1", len(list))
	}
	stored, _ := msgs.ListByConversation(context.Background(), list[0].ID)
	if len(stored) != 2 {
		t.Fatalf("messages = %d, want 2", len(stored))
	}
}

type chatFakeProvider struct{ reply string }

func (f chatFakeProvider) StreamChat(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	_ = onToken(f.reply)
	return f.reply, nil
}

func (f chatFakeProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := f.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}
