package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
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
const testCSRFSecret = "0123456789abcdef0123456789abcdef"

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

	srv := httptest.NewServer(api.NewRouter(api.Deps{Users: users, Sessions: sessions, Config: config.Config{ScheduledEnabled: true}}))
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
			Username         string `json:"username"`
			Timezone         string `json:"timezone"`
			ScheduledEnabled bool   `json:"scheduledEnabled"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&env)
	_ = resp2.Body.Close()
	if env.Data.Username != testUsername {
		t.Fatalf("expected %s, got %q", testUsername, env.Data.Username)
	}
	if env.Data.Timezone != "UTC" || !env.Data.ScheduledEnabled {
		t.Fatalf("session omitted scheduled preferences: %+v", env.Data)
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
		Config:   config.Config{CSRFSecret: testCSRFSecret},
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
		chat.ServiceConfig{Model: "m", MaxTokens: 32, Temperature: 0.2}, chat.Deps{Convs: convs, Msgs: msgs})
	chatH := handlers.NewChat(chatSvc, convs, msgs)

	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Users: users, Sessions: sessions, Config: config.Config{CSRFSecret: testCSRFSecret}, Chat: chatH,
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

// TestBodyLimit_GlobalCapAppliesButUploadOverrides covers WAVE2-hardening.md
// §11: the global KADENCE_MAX_BODY_BYTES cap applies to ordinary JSON routes
// (oversized body -> 400, normal body -> 200) but does not block
// /api/documents uploads, which re-wrap r.Body with the larger
// cfg.UploadMaxBytes at the route level.
func TestBodyLimit_GlobalCapAppliesButUploadOverrides(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)
	docs := store.NewDocumentRepository(pool)

	hash, _ := auth.HashPassword("pw123")
	if _, err := users.Create(context.Background(), model.User{
		Username: testUsername, Email: testEmail, PasswordHash: hash, Role: model.RoleUser,
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// bytes: must fit the login POST (~60B) and a normal profile PATCH, but not a padded one.
	const globalCap = 128
	cfg := config.Config{
		CSRFSecret:     testCSRFSecret,
		MaxBodyBytes:   globalCap,
		UploadMaxBytes: 1 << 20, // 1 MiB: comfortably larger than globalCap.
	}
	documentsH := handlers.NewDocuments(bodyLimitFakeIngester{}, docs, cfg.UploadMaxBytes)
	profileH := handlers.NewProfile(users, sessions, cfg)

	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Users: users, Sessions: sessions, Config: cfg, Documents: documentsH, Profile: profileH,
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

	// Oversized JSON body on an ordinary route: rejected by the global cap.
	oversized := `{"displayName":"` + strings.Repeat("x", 2*globalCap) + `","email":"a@x.io","unitSystem":"metric"}`
	oreq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/profile", strings.NewReader(oversized))
	oreq.Header.Set("Content-Type", "application/json")
	oreq.Header.Set("X-CSRF-Token", token)
	jar.apply(oreq)
	oresp, err := http.DefaultClient.Do(oreq)
	if err != nil {
		t.Fatalf("oversized profile patch: %v", err)
	}
	_ = oresp.Body.Close()
	if oresp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized profile patch status = %d, want 400", oresp.StatusCode)
	}

	// Normal-sized request on the same route: passes through fine.
	normal := `{"displayName":"Alice","email":"a@x.io","unitSystem":"metric"}`
	nreq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/profile", strings.NewReader(normal))
	nreq.Header.Set("Content-Type", "application/json")
	nreq.Header.Set("X-CSRF-Token", token)
	jar.apply(nreq)
	nresp, err := http.DefaultClient.Do(nreq)
	if err != nil {
		t.Fatalf("normal profile patch: %v", err)
	}
	_ = nresp.Body.Close()
	if nresp.StatusCode != http.StatusOK {
		t.Fatalf("normal profile patch status = %d, want 200", nresp.StatusCode)
	}

	// Upload larger than the global cap, but under UploadMaxBytes: unaffected.
	uploadBody, contentType := multipartFile(t, "sample.pdf", bytes.Repeat([]byte("a"), globalCap*4))
	ureq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/documents", uploadBody)
	ureq.Header.Set("Content-Type", contentType)
	ureq.Header.Set("X-CSRF-Token", token)
	jar.apply(ureq)
	uresp, err := http.DefaultClient.Do(ureq)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer func() { _ = uresp.Body.Close() }()
	if uresp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(uresp.Body)
		t.Fatalf("upload status = %d, want 200, body=%s", uresp.StatusCode, body)
	}
}

// bodyLimitFakeIngester satisfies the Documents handler's ingest dependency
// without running the real extract/chunk/embed pipeline.
type bodyLimitFakeIngester struct{}

func (bodyLimitFakeIngester) Ingest(_ context.Context, ownerUserID *int64, scope, filename, mime string, _ []byte) (model.Document, error) {
	return model.Document{ID: 1, OwnerUserID: ownerUserID, Scope: scope, Filename: filename, Mime: mime, SourceType: model.DocSourcePDF}, nil
}

// multipartFile builds a multipart/form-data body with a single "file" field,
// returning the body reader and its Content-Type header value.
func multipartFile(t *testing.T, filename string, data []byte) (io.Reader, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf, mw.FormDataContentType()
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

func TestRouter_WebAuthnEnabledEndpoint(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)

	waH := handlers.NewWebAuthn(nil, nil, nil, nil, nil, config.Config{})
	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Users: users, Sessions: sessions, Config: config.Config{}, WebAuthn: waH,
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/webauthn/enabled")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("body = %s", body)
	}

	resp2, err := http.Post(srv.URL+"/api/webauthn/register/begin", "application/json", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("register/begin code = %d, want 401 (RequireAuth rejects the anonymous request before CSRF is checked)", resp2.StatusCode)
	}
}
