package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/mcp/oauth"
)

// linkService is the OAuth link flow this handler drives. Satisfied by
// *oauth.Service.
type linkService interface {
	Start(ctx context.Context, userID int64, serverID string) (authorizeURL, state, browserToken string, err error)
	Complete(ctx context.Context, userID int64, code, state, browserToken string) (string, error)
	Unlink(ctx context.Context, userID int64, serverID string) error
	Integrations(ctx context.Context, userID int64) ([]oauth.LinkState, error)
}

// bindingCookieTTL bounds how long a started authorization may be completed. It
// matches the authorization server's own transaction window.
const bindingCookieTTL = 10 * time.Minute

// integrationsPath is where the callback sends the browser when it is done.
const integrationsPath = "/profile"

// MCPOAuthCookieName is the binding cookie's name. The __Host- prefix requires
// Secure, which a browser refuses over plain HTTP, so a development build uses
// the unprefixed name rather than silently losing the cookie.
func MCPOAuthCookieName(secure bool) string {
	if secure {
		return "__Host-kadence_mcp_oauth"
	}
	return "kadence_mcp_oauth"
}

// MCPOAuth serves the per-user OAuth link flow for MCP servers.
type MCPOAuth struct {
	svc    linkService
	secure bool
}

// NewMCPOAuth constructs the handler. secure selects the cookie shape and must
// be true wherever the app is served over https.
func NewMCPOAuth(svc linkService, secure bool) *MCPOAuth {
	return &MCPOAuth{svc: svc, secure: secure}
}

// Start handles POST /api/mcp/oauth/{server}/start. It returns the URL the
// browser must visit and sets the cookie binding the callback to this browser.
func (h *MCPOAuth) Start(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	server := chi.URLParam(r, "server")

	authorizeURL, state, browserToken, err := h.svc.Start(r.Context(), u.ID, server)
	switch {
	case errors.Is(err, oauth.ErrUnknownServer):
		RespondError(w, http.StatusNotFound, "unknown integration")
		return
	case errors.Is(err, oauth.ErrTooManyAttempts):
		RespondError(w, http.StatusTooManyRequests, "too many link attempts in flight; finish or abandon one first")
		return
	case err != nil:
		slog.Error("mcp oauth: start", "server", server, "err", err)
		RespondError(w, http.StatusInternalServerError, "could not start the authorization")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     MCPOAuthCookieName(h.secure),
		Value:    state + "." + browserToken,
		Path:     "/",
		MaxAge:   int(bindingCookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})
	RespondJSON(w, http.StatusOK, map[string]string{"authorize_url": authorizeURL})
}

// Callback handles GET /api/mcp/oauth/callback, the redirect the authorization
// server sends the browser to.
//
// It always answers with a redirect carrying only an outcome, never the code or
// the state: neither may reach the SPA, a Referer, or a proxy log.
func (h *MCPOAuth) Callback(w http.ResponseWriter, r *http.Request) {
	// Set before anything can fail: this response must never be cached, and the
	// browser must not send this URL as the Referer of the next navigation.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	u := auth.UserFromContext(r.Context())
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	cookie, cookieErr := r.Cookie(MCPOAuthCookieName(h.secure))
	h.clearBindingCookie(w)

	if code == "" || state == "" || cookieErr != nil {
		h.redirect(w, r, "", false)
		return
	}
	cookieState, browserToken, ok := strings.Cut(cookie.Value, ".")
	if !ok || cookieState != state {
		// The cookie belongs to a different authorization: another tab, or a
		// callback this browser never started.
		h.redirect(w, r, "", false)
		return
	}

	server, err := h.svc.Complete(r.Context(), u.ID, code, state, browserToken)
	if err != nil {
		if !errors.Is(err, oauth.ErrBadTransaction) {
			slog.Error("mcp oauth: complete", "err", err)
		}
		h.redirect(w, r, server, false)
		return
	}
	h.redirect(w, r, server, true)
}

func (h *MCPOAuth) clearBindingCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     MCPOAuthCookieName(h.secure),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *MCPOAuth) redirect(w http.ResponseWriter, r *http.Request, server string, linked bool) {
	status := "failed"
	if linked {
		status = "linked"
	}
	q := url.Values{"status": {status}}
	if server != "" {
		q.Set("integration", server)
	}
	http.Redirect(w, r, integrationsPath+"?"+q.Encode(), http.StatusSeeOther)
}

// Unlink handles DELETE /api/mcp/oauth/{server}.
func (h *MCPOAuth) Unlink(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	server := chi.URLParam(r, "server")

	if err := h.svc.Unlink(r.Context(), u.ID, server); err != nil {
		slog.Error("mcp oauth: unlink", "server", server, "err", err)
		RespondError(w, http.StatusInternalServerError, "could not disconnect the integration")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type integrationDTO struct {
	Server          string `json:"server"`
	Linked          bool   `json:"linked"`
	Status          string `json:"status,omitempty"`
	Scope           string `json:"scope,omitempty"`
	AccessExpiresAt string `json:"access_expires_at,omitempty"`
	// ScopeShortfall names the configured scopes this link was not granted.
	// It is non-empty when a tier was enabled after the user linked, which a
	// refresh cannot repair — only authorizing again can.
	ScopeShortfall []string `json:"scope_shortfall,omitempty"`
}

// List handles GET /api/mcp/integrations.
func (h *MCPOAuth) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())

	states, err := h.svc.Integrations(r.Context(), u.ID)
	if err != nil {
		slog.Error("mcp oauth: list integrations", "err", err)
		RespondError(w, http.StatusInternalServerError, "could not read the integrations")
		return
	}

	out := make([]integrationDTO, 0, len(states))
	for _, st := range states {
		dto := integrationDTO{
			Server: st.ServerID, Linked: st.Linked, Status: st.Status,
			Scope: st.Scope, ScopeShortfall: st.ScopeShortfall,
		}
		if !st.AccessExpiresAt.IsZero() {
			dto.AccessExpiresAt = st.AccessExpiresAt.UTC().Format(time.RFC3339)
		}
		out = append(out, dto)
	}
	RespondJSON(w, http.StatusOK, out)
}
