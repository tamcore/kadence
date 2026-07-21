package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
)

// SessionGetter loads a live session by id and records recent activity.
type SessionGetter interface {
	GetByID(ctx context.Context, id string) (model.Session, error)
	Touch(ctx context.Context, id string, ip string, at time.Time) error
}

// sessionTouchInterval throttles how often LoadUser updates a session's
// last-active timestamp, to avoid a write on every single request.
const sessionTouchInterval = 5 * time.Minute

// UserGetter loads a user by id.
type UserGetter interface {
	GetByID(ctx context.Context, id int64) (model.User, error)
}

const sessionCookie = "session_id"

// LoadUser resolves the session cookie into the authenticated user and stores
// it in the request context. Missing/invalid sessions proceed anonymously.
func LoadUser(sessions SessionGetter, users UserGetter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(sessionCookie)
			if err != nil || c.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			sess, err := sessions.GetByID(r.Context(), c.Value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			now := time.Now()
			if now.Sub(sess.LastSeenAt) > sessionTouchInterval {
				if err := sessions.Touch(r.Context(), sess.ID, auth.ClientIP(r), now); err != nil {
					slog.Warn("session touch failed", "err", err)
				}
			}
			u, err := users.GetByID(r.Context(), sess.UserID)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx := auth.ContextWithUser(r.Context(), &u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth rejects requests without an authenticated user (401).
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFromContext(r.Context()) == nil {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeJSONError writes the standard error envelope. Kept local to avoid an
// import cycle with the api package.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "error": msg})
}
