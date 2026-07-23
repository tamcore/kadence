// Package model holds Kadence domain types.
package model

import "time"

// Role values.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Unit system values.
const (
	UnitMetric   = "metric"
	UnitImperial = "imperial"
)

// User is an application account. Login is by Username; Email is the canonical
// backend identity (unique, used for future OIDC linking and notifications).
type User struct {
	ID             int64
	Username       string
	Email          string
	PasswordHash   string
	Role           string
	DisplayName    string
	UnitSystem     string
	Location       string
	AboutMe        string
	CreatedAt      time.Time
	WebAuthnHandle string
}

// IsAdmin reports whether the user has the admin role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }
