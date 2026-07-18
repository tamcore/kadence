// Package api wires HTTP routes for the Kadence backend.
package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/tamcore/kadence/internal/api/middleware"
	"github.com/tamcore/kadence/web"
)

// NewRouter returns the public HTTP handler. API routes live under /api; the
// embedded SvelteKit frontend (when built with -tags prodfrontend) is served
// at root with SPA fallback.
func NewRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP) //nolint:staticcheck // trusted proxy sets X-Forwarded-For/X-Real-IP; used only for access logging, not auth decisions
	r.Use(middleware.AccessLog)
	r.Use(chimw.Recoverer)
	r.Use(middleware.SecurityHeaders)

	r.Get("/api/healthz", healthz)

	if web.Available() {
		fs := http.FileServer(http.FS(web.FS))
		r.NotFound(func(w http.ResponseWriter, req *http.Request) {
			if hasEmbeddedFile(req.URL.Path) {
				fs.ServeHTTP(w, req)
				return
			}
			req2 := req.Clone(req.Context())
			req2.URL.Path = "/"
			req2.URL.RawPath = ""
			fs.ServeHTTP(w, req2)
		})
	} else {
		r.Get("/", placeholder)
	}
	return r
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

func hasEmbeddedFile(urlPath string) bool {
	if web.FS == nil {
		return false
	}
	p := strings.TrimPrefix(urlPath, "/")
	if p == "" {
		return false
	}
	f, err := web.FS.Open(p)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
