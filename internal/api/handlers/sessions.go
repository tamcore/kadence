package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const timeFormatRFC3339 = "2006-01-02T15:04:05Z07:00"

// sessionStore is the persistence seam Sessions depends on.
type sessionStore interface {
	ListByUser(ctx context.Context, userID int64) ([]model.Session, error)
	DeleteByPublicIDForUser(ctx context.Context, publicID string, userID int64) error
	DeleteOthersByUser(ctx context.Context, userID int64, exceptID string) error
}

// Sessions serves the authenticated user's active-session list + revocation.
type Sessions struct{ store sessionStore }

// NewSessions builds a Sessions handler backed by the given store.
func NewSessions(s sessionStore) *Sessions { return &Sessions{store: s} }

// sessionDTO is the wire representation of a session. It intentionally omits
// the raw session id: only the opaque publicId is ever exposed. "current" is
// computed server-side by comparing each row's raw id against the caller's
// session cookie before the raw id is discarded.
type sessionDTO struct {
	PublicID   string `json:"publicId"`
	Device     string `json:"device"`
	IP         string `json:"ip"`
	CreatedAt  string `json:"createdAt"`
	LastSeenAt string `json:"lastSeenAt"`
	Current    bool   `json:"current"`
}

// List handles GET /api/sessions.
func (h *Sessions) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sessions, err := h.store.ListByUser(r.Context(), u.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list sessions")
		return
	}

	currentID := ""
	if c, cErr := r.Cookie(sessionCookie); cErr == nil {
		currentID = c.Value
	}

	out := make([]sessionDTO, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionDTO{
			PublicID:   s.PublicID,
			Device:     parseUserAgent(s.UserAgent),
			IP:         s.IP,
			CreatedAt:  s.CreatedAt.Format(timeFormatRFC3339),
			LastSeenAt: s.LastSeenAt.Format(timeFormatRFC3339),
			Current:    s.ID == currentID,
		})
	}
	RespondJSON(w, http.StatusOK, out)
}

// Revoke handles DELETE /api/sessions/{publicId}.
func (h *Sessions) Revoke(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	publicID := chi.URLParam(r, "publicId")
	if err := h.store.DeleteByPublicIDForUser(r.Context(), publicID, u.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			RespondError(w, http.StatusNotFound, "session not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, "could not revoke session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeOthers handles POST /api/sessions/revoke-others.
func (h *Sessions) RevokeOthers(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		RespondError(w, http.StatusBadRequest, "no current session")
		return
	}

	if err := h.store.DeleteOthersByUser(r.Context(), u.ID, c.Value); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not revoke sessions")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type uaPair struct{ needle, label string }

const maxRawUALen = 60

// parseUserAgent maps a User-Agent string to a friendly "Browser on OS"
// label, falling back to the raw UA (truncated) or "Unknown device" when no
// known browser/OS token is found.
//
// Chrome (and Edge) User-Agent strings also contain the substring "Safari",
// so Edge/Chrome must be checked before Safari in the browser list below —
// otherwise every Chrome UA would be misidentified as Safari.
func parseUserAgent(ua string) string {
	if strings.TrimSpace(ua) == "" {
		return "Unknown device"
	}

	browser := firstMatch(ua, []uaPair{
		{"Edg", "Edge"},
		{"Chrome", "Chrome"},
		{"Firefox", "Firefox"},
		{"Safari", "Safari"},
	})
	os := firstMatch(ua, []uaPair{
		{"Mac OS X", "macOS"},
		{"Windows", "Windows"},
		{"Android", "Android"},
		{"iPhone", "iOS"},
		{"iPad", "iOS"},
		{"Linux", "Linux"},
	})

	switch {
	case browser != "" && os != "":
		return browser + " on " + os
	case browser != "":
		return browser
	default:
		if len(ua) > maxRawUALen {
			return ua[:maxRawUALen]
		}
		return ua
	}
}

func firstMatch(s string, pairs []uaPair) string {
	for _, p := range pairs {
		if strings.Contains(s, p.needle) {
			return p.label
		}
	}
	return ""
}
