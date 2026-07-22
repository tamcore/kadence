// Package auth provides password hashing, session-id generation, and request-context helpers.
package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword returns a bcrypt hash of the plaintext password.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword reports whether pw matches the bcrypt hash.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyHash is a bcrypt hash (DefaultCost) of a fixed, unrelated string. It
// has no corresponding real user account; it exists purely so
// CheckPasswordDummy pays the same bcrypt cost as a real comparison.
// Generated with:
//
//	bcrypt.GenerateFromPassword([]byte("kadence-dummy-compare-fixed-string"), bcrypt.DefaultCost)
const dummyHash = "$2a$10$yY0y1q.I5WT1VlwflrX65OGFKxUVaBuoCeiR.oHFfoLveq1dCa7sS"

// CheckPasswordDummy runs a bcrypt comparison against the fixed, non-user
// dummyHash and always returns false. Callers use it on the unknown-user
// login path so that path takes the same amount of time as a real password
// check, closing the username-enumeration timing oracle where an unknown
// username short-circuits before bcrypt ever runs.
func CheckPasswordDummy(pw string) bool {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(pw))
	return false
}
