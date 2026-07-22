// Package api wires HTTP routes for the Kadence backend.
package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/csrf"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/api/middleware"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/web"
)

// Deps carries the dependencies the router needs.
type Deps struct {
	Users       *store.UserRepository
	Sessions    *store.SessionRepository
	Config      config.Config
	Chat        *handlers.Chat
	Documents   *handlers.Documents
	Context     *handlers.Context
	Credentials *handlers.Credentials
	MCP         *handlers.MCP
	Profile     *handlers.Profile
	SessionsAPI *handlers.Sessions
	WebAuthn    *handlers.WebAuthn
}

// NewRouter returns the public HTTP handler. API routes live under /api; the
// embedded SvelteKit frontend (when built with -tags prodfrontend) is served
// at root with SPA fallback.
func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP) //nolint:staticcheck // trusted proxy sets X-Forwarded-For/X-Real-IP; used only for access logging, not auth decisions
	r.Use(middleware.AccessLog)
	r.Use(chimw.Recoverer)
	r.Use(middleware.SecurityHeaders)

	r.Get("/api/healthz", healthz)

	if deps.Users != nil && deps.Sessions != nil {
		// Global per-IP cap on all other /api routes; healthz and the static
		// frontend are registered outside this group and stay unlimited.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(deps.Config.RateLimitGlobal))
			mountAuth(r, deps)
		})
	}

	mountFrontend(r)
	return r
}

func mountAuth(r chi.Router, deps Deps) {
	authH := handlers.NewAuth(deps.Config, deps.Users, deps.Sessions)
	usersH := handlers.NewUsers(deps.Users, deps.Sessions)

	secret := []byte(deps.Config.CSRFSecret)
	if len(secret) == 0 {
		secret = randomSecret()
	}
	csrfProtect := middleware.CSRF(secret, deps.Config.IsProd(), deps.Config.TrustedOrigins)
	loadUser := middleware.LoadUser(deps.Sessions, deps.Users)
	// Shared per-IP limiter for the auth-sensitive endpoints (login, passkey
	// login, credential submission): brute-forcing credentials is the primary
	// abuse case, so these get a stricter cap than the global one.
	authLimit := middleware.RateLimit(deps.Config.RateLimitAuth)

	// Login: reachable without a prior CSRF token (no session yet), but behind LoadUser.
	r.With(loadUser, authLimit).Post("/api/session", authH.Login)

	if deps.WebAuthn != nil {
		// Passkey login: no prior session/CSRF token; the origin-bound WebAuthn
		// assertion is the CSRF defense, mirroring password Login.
		r.With(loadUser, authLimit).Post("/api/webauthn/login/begin", deps.WebAuthn.LoginBegin)
		r.With(loadUser, authLimit).Post("/api/webauthn/login/finish", deps.WebAuthn.LoginFinish)
		r.Get("/api/webauthn/enabled", deps.WebAuthn.Enabled)
	}

	// All other auth/admin routes: LoadUser + RequireAuth + CSRF protection.
	// RequireAuth runs before CSRF so an anonymous request always gets a
	// uniform 401, regardless of HTTP method; CSRF then guards authenticated
	// unsafe-method requests that are missing a valid token.
	r.Group(func(r chi.Router) {
		r.Use(loadUser)
		r.Use(middleware.RequireAuth)
		if !deps.Config.IsProd() {
			// gorilla/csrf enforces a same-origin Referer check on unsafe methods
			// and rejects cleartext HTTP referers unless the request is marked
			// plaintext. Outside production we always serve over plain HTTP
			// (dev server, tests), so mark requests accordingly; production
			// requests are unaffected and remain subject to the full check.
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					next.ServeHTTP(w, csrf.PlaintextHTTPRequest(req))
				})
			})
		}
		r.Use(csrfProtect)

		r.Delete("/api/session", authH.Logout)
		r.Get("/api/session", authH.CurrentUser)

		if deps.Chat != nil {
			r.Post("/api/chat", deps.Chat.Send)
			r.Get("/api/conversations", deps.Chat.ListConversations)
			r.Get("/api/conversations/{id}/messages", deps.Chat.Messages)
			r.Patch("/api/conversations/{id}", deps.Chat.PatchConversation)
			r.Delete("/api/conversations/{id}", deps.Chat.DeleteConversation)
		}

		if deps.Context != nil {
			r.Get("/api/context/overview", deps.Context.Overview)
			r.Get("/api/context/search", deps.Context.Search)
		}

		if deps.MCP != nil {
			r.Get("/api/mcp", deps.MCP.List)
			r.Get("/api/mcp/{name}/tools", deps.MCP.Tools)
			r.Post("/api/mcp", deps.MCP.Create)
			r.Put("/api/mcp/{id}", deps.MCP.Update)
			r.Delete("/api/mcp/{id}", deps.MCP.Delete)
		}

		if deps.Documents != nil {
			r.Post("/api/documents", deps.Documents.Upload)
			r.Get("/api/documents", deps.Documents.List)
			r.Delete("/api/documents/{id}", deps.Documents.Delete)
		}

		if deps.Credentials != nil {
			r.With(authLimit).Post("/api/credentials/{requestId}", deps.Credentials.Submit)
		}

		if deps.Profile != nil {
			r.Patch("/api/profile", deps.Profile.Update)
			r.Post("/api/profile/password", deps.Profile.ChangePassword)
		}

		if deps.SessionsAPI != nil {
			r.Get("/api/sessions", deps.SessionsAPI.List)
			r.Delete("/api/sessions/{publicId}", deps.SessionsAPI.Revoke)
			r.Post("/api/sessions/revoke-others", deps.SessionsAPI.RevokeOthers)
		}

		if deps.WebAuthn != nil {
			r.Post("/api/webauthn/register/begin", deps.WebAuthn.RegisterBegin)
			r.Post("/api/webauthn/register/finish", deps.WebAuthn.RegisterFinish)
			r.Get("/api/webauthn/credentials", deps.WebAuthn.ListCredentials)
			r.Patch("/api/webauthn/credentials/{publicId}", deps.WebAuthn.RenameCredential)
			r.Delete("/api/webauthn/credentials/{publicId}", deps.WebAuthn.DeleteCredential)
		}

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth)
			r.Use(middleware.RequireAdmin)
			r.Get("/api/users", usersH.List)
			r.Post("/api/users", usersH.Create)
			r.Patch("/api/users/{id}", usersH.Update)
			r.Delete("/api/users/{id}", usersH.Delete)

			if deps.Documents != nil {
				r.Post("/api/admin/documents", deps.Documents.UploadPublic)
				r.Get("/api/admin/documents", deps.Documents.ListPublic)
				r.Delete("/api/admin/documents/{id}", deps.Documents.DeletePublic)
			}
		})
	})
}

func mountFrontend(r chi.Router) {
	if web.Available() {
		r.NotFound(staticHandler(web.FS).ServeHTTP)
		return
	}
	r.Get("/", placeholder)
}

// staticHandler serves files from fsys, falling back to the root document
// (SPA entry point) for any path not present in fsys.
func staticHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fileExists(fsys, req.URL.Path) {
			fileServer.ServeHTTP(w, req)
			return
		}
		req2 := req.Clone(req.Context())
		req2.URL.Path = "/"
		req2.URL.RawPath = ""
		fileServer.ServeHTTP(w, req2)
	})
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func placeholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><title>kadence</title>` +
		`<p>Backend running. Frontend not embedded (build without -tags prodfrontend).</p>`))
}

// fileExists reports whether urlPath resolves to a regular file within fsys.
func fileExists(fsys fs.FS, urlPath string) bool {
	if fsys == nil {
		return false
	}
	p := strings.TrimPrefix(urlPath, "/")
	if p == "" {
		return false
	}
	f, err := fsys.Open(p)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
