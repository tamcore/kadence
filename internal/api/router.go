// Package api wires HTTP routes for the Kadence backend.
package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/api/middleware"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/web"
)

// Deps carries the dependencies the router needs.
type Deps struct {
	Users    *store.UserRepository
	Sessions *store.SessionRepository
	Config   config.Config
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
		mountAuth(r, deps)
	}

	mountFrontend(r)
	return r
}

func mountAuth(r chi.Router, deps Deps) {
	authH := handlers.NewAuth(deps.Config, deps.Users, deps.Sessions)
	usersH := handlers.NewUsers(deps.Users)

	secret := []byte(deps.Config.CSRFSecret)
	if len(secret) == 0 {
		secret = randomSecret()
	}
	csrf := middleware.CSRF(secret, deps.Config.IsProd())
	loadUser := middleware.LoadUser(deps.Sessions, deps.Users)

	// Login: reachable without a prior CSRF token (no session yet), but behind LoadUser.
	r.With(loadUser).Post("/api/session", authH.Login)

	// All other auth/admin routes: LoadUser + CSRF protection.
	r.Group(func(r chi.Router) {
		r.Use(loadUser)
		r.Use(csrf)

		r.Delete("/api/session", authH.Logout)
		r.Get("/api/session", authH.CurrentUser)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth)
			r.Use(middleware.RequireAdmin)
			r.Get("/api/users", usersH.List)
			r.Post("/api/users", usersH.Create)
			r.Delete("/api/users/{id}", usersH.Delete)
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
