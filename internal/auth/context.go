package auth

import (
	"context"

	"github.com/tamcore/kadence/internal/model"
)

type ctxKey int

const userKey ctxKey = 0

// ContextWithUser returns a context carrying the authenticated user.
func ContextWithUser(ctx context.Context, u *model.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFromContext returns the authenticated user, or nil if none.
func UserFromContext(ctx context.Context) *model.User {
	u, _ := ctx.Value(userKey).(*model.User)
	return u
}
