// Package model holds Kadence domain types.
package model

import "time"

// Role values.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User is an application account. Login is by Username; Email is the canonical
// backend identity (unique, used for future OIDC linking and notifications).
type User struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

// IsAdmin reports whether the user has the admin role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }
