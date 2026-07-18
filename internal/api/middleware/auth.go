package middleware

import (
	"context"
	"net/http"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
)

// SessionGetter loads a live session by id.
type SessionGetter interface {
	GetByID(ctx context.Context, id string) (model.Session, error)
}

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
	_, _ = w.Write([]byte(`{"data":null,"error":` + quote(msg) + `}`))
}

func quote(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			b = append(b, '\\')
		}
		b = append(b, byte(r))
	}
	b = append(b, '"')
	return string(b)
}
