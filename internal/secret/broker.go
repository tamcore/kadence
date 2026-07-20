// Package secret implements an in-memory broker that mints unguessable,
// one-time placeholder tokens for credential fields collected from a user,
// and substitutes those tokens with the real values inside tool-call JSON.
//
// The broker never logs secret values. Callers that need to redact secrets
// out of logs or transcripts should use ActiveValues + Redact.
package secret

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// tokenPrefix is prepended to every minted placeholder token and requestID
// so both are easy to recognize (and grep-exclude from logs) without leaking
// any information about the underlying random bytes.
const tokenPrefix = "kadence_secret_"

// tokenRandomBytes is the amount of crypto/rand entropy used per token and
// per requestID, making both practically unguessable.
const tokenRandomBytes = 32

// valueTTL is how long a submitted value remains substitutable/active after
// Submit, after which it is treated as expired and swept on next access.
const valueTTL = 120 * time.Second

// minFields / maxFields bound how many credential fields a single request
// may ask for.
const (
	minFields    = 1
	maxFields    = 8
	maxFieldName = 64
)

// Sentinel errors returned by Broker methods. Callers should use errors.Is.
var (
	// ErrTimeout is returned by Await when the request's value TTL elapses
	// before the user submits values.
	ErrTimeout = errors.New("secret: request timed out waiting for submission")
	// ErrUnknownRequest is returned when a requestID is not recognized (never
	// existed, or was purged/expired).
	ErrUnknownRequest = errors.New("secret: unknown request")
	// ErrNotOwner is returned when a caller's userID does not match the
	// request's owning user.
	ErrNotOwner = errors.New("secret: caller does not own this request")
	// ErrAlreadySubmitted is returned when Submit is called more than once
	// for the same request.
	ErrAlreadySubmitted = errors.New("secret: request already submitted")
	// ErrFieldMismatch is returned when the set of keys in Submit's values
	// does not exactly match the request's field names.
	ErrFieldMismatch = errors.New("secret: submitted values do not match requested fields")
)

// Field describes one credential value being requested from a user.
type Field struct {
	// Name identifies the field programmatically (e.g. "apiKey"). Must be
	// non-empty, unique within a request, and at most 64 characters.
	Name string
	// Label is a human-readable prompt shown to the user.
	Label string
	// Secret indicates the field's value should be masked in any UI that
	// renders it (kept for callers; the broker itself always treats values
	// as sensitive regardless of this flag).
	Secret bool
}

// request tracks one in-flight (or completed) credential request.
type request struct {
	userID    int64
	fields    map[string]string // field name -> token
	done      chan struct{}
	submitted bool
	createdAt time.Time
}

// storedValue is a real secret value bound to a single-use token and scoped
// to the user who owns it.
type storedValue struct {
	userID  int64
	value   string
	expires time.Time
}

// Broker mints one-time placeholder tokens for credential fields, blocks
// callers until a user submits real values, and substitutes tokens for real
// values inside tool-call JSON. All state is in-memory and protected by a
// single mutex; there is no background goroutine, so expiry is enforced by
// sweeping expired entries on access.
type Broker struct {
	mu       sync.Mutex
	requests map[string]*request     // requestID -> request
	values   map[string]*storedValue // token -> stored value
	now      func() time.Time
}

// NewBroker returns a Broker using the real wall clock.
func NewBroker() *Broker {
	return NewBrokerWithClock(time.Now)
}

// NewBrokerWithClock returns a Broker using the supplied clock function
// instead of time.Now, enabling deterministic TTL tests. now must not be nil.
func NewBrokerWithClock(now func() time.Time) *Broker {
	if now == nil {
		now = time.Now
	}
	return &Broker{
		requests: make(map[string]*request),
		values:   make(map[string]*storedValue),
		now:      now,
	}
}

// newToken generates an unguessable token/requestID of the form
// "kadence_secret_" + base64.RawURLEncoding(32 crypto-random bytes).
func newToken() (string, error) {
	buf := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewRequest validates fields, mints one unguessable token per field plus a
// requestID, and registers a pending request for userID. It returns the
// requestID and a map of field name -> token. No secret values exist yet;
// nothing here is ever logged because none is captured.
func (b *Broker) NewRequest(userID int64, fields []Field) (string, map[string]string, error) {
	if err := validateFields(fields); err != nil {
		return "", nil, err
	}

	requestID, err := newToken()
	if err != nil {
		return "", nil, err
	}

	tokens := make(map[string]string, len(fields))
	fieldTokens := make(map[string]string, len(fields))
	for _, f := range fields {
		tok, err := newToken()
		if err != nil {
			return "", nil, err
		}
		tokens[f.Name] = tok
		fieldTokens[f.Name] = tok
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.requests[requestID] = &request{
		userID:    userID,
		fields:    fieldTokens,
		done:      make(chan struct{}),
		createdAt: b.now(),
	}
	return requestID, tokens, nil
}

// validateFields enforces the field-count bounds and per-field name rules
// (non-empty, <= maxFieldName chars, unique within the request).
func validateFields(fields []Field) error {
	if len(fields) < minFields || len(fields) > maxFields {
		return errors.New("secret: field count must be between 1 and 8")
	}
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if f.Name == "" {
			return errors.New("secret: field name must not be empty")
		}
		if len(f.Name) > maxFieldName {
			return errors.New("secret: field name exceeds 64 characters")
		}
		if seen[f.Name] {
			return errors.New("secret: duplicate field name: " + f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// Submit records the real values for a pending request, keyed by user and
// requestID. The set of keys in values must exactly match the request's
// field names. Values are never logged. On success each value is stored
// against its field's token with an expiry of now+120s, and any Await
// callers are released.
func (b *Broker) Submit(userID int64, requestID string, values map[string]string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sweepExpiredLocked()

	req, ok := b.requests[requestID]
	if !ok {
		return ErrUnknownRequest
	}
	if req.userID != userID {
		return ErrNotOwner
	}
	if req.submitted {
		return ErrAlreadySubmitted
	}
	if !fieldsMatch(req.fields, values) {
		return ErrFieldMismatch
	}

	expires := b.now().Add(valueTTL)
	for name, tok := range req.fields {
		b.values[tok] = &storedValue{
			userID:  userID,
			value:   values[name],
			expires: expires,
		}
	}
	req.submitted = true
	close(req.done)
	return nil
}

// fieldsMatch reports whether values has exactly the same set of keys as
// fields (a map of field name -> token).
func fieldsMatch(fields map[string]string, values map[string]string) bool {
	if len(fields) != len(values) {
		return false
	}
	for name := range fields {
		if _, ok := values[name]; !ok {
			return false
		}
	}
	return true
}

// Await blocks until requestID's values are submitted, the context is
// cancelled, or the request's TTL elapses, whichever comes first. It
// returns nil on submission, ctx.Err() on cancellation, or ErrTimeout on
// TTL expiry. Returns ErrUnknownRequest if requestID is not recognized.
func (b *Broker) Await(ctx context.Context, requestID string) error {
	b.mu.Lock()
	b.sweepExpiredLocked()
	req, ok := b.requests[requestID]
	if !ok {
		b.mu.Unlock()
		return ErrUnknownRequest
	}
	done := req.done
	remaining := req.createdAt.Add(valueTTL).Sub(b.now())
	b.mu.Unlock()

	if remaining <= 0 {
		return ErrTimeout
	}

	timer := time.NewTimer(remaining)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrTimeout
	}
}

// Substitute scans argsJSON for any not-yet-consumed token that belongs to
// userID, replaces each occurrence with its real value, marks the token
// consumed (single-use), and returns the resulting JSON along with the list
// of tokens that were substituted. Tokens belonging to a different user, or
// already consumed/expired, are left untouched in the output and are not
// reported as used.
func (b *Broker) Substitute(userID int64, argsJSON string) (string, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sweepExpiredLocked()

	result := argsJSON
	var used []string
	for tok, sv := range b.values {
		if sv.userID != userID {
			continue
		}
		if !strings.Contains(result, tok) {
			continue
		}
		result = strings.ReplaceAll(result, tok, sv.value)
		used = append(used, tok)
		delete(b.values, tok) // single-use: consume immediately
	}
	return result, used
}

// ActiveValues returns the non-expired real values currently stored for
// userID, ordered longest-first so that overlapping values (one a substring
// of another) can be redacted safely by Redact.
func (b *Broker) ActiveValues(userID int64) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sweepExpiredLocked()

	values := make([]string, 0)
	for _, sv := range b.values {
		if sv.userID == userID {
			values = append(values, sv.value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		return len(values[i]) > len(values[j])
	})
	return values
}

// PurgeUser deletes all pending/submitted requests and stored values owned
// by userID.
func (b *Broker) PurgeUser(userID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for id, req := range b.requests {
		if req.userID == userID {
			delete(b.requests, id)
		}
	}
	for tok, sv := range b.values {
		if sv.userID == userID {
			delete(b.values, tok)
		}
	}
}

// sweepExpiredLocked removes stored values whose TTL has elapsed. Callers
// must hold b.mu. Unsubmitted requests are intentionally left in place past
// their TTL so Await can still observe them and report ErrTimeout rather
// than ErrUnknownRequest; PurgeUser remains the only way to remove a
// request outright.
func (b *Broker) sweepExpiredLocked() {
	now := b.now()
	for tok, sv := range b.values {
		if !sv.expires.After(now) {
			delete(b.values, tok)
		}
	}
}

// Redact returns a copy of s with every occurrence of each non-empty value
// replaced by "[redacted]". Values should be supplied longest-first (as
// ActiveValues does) so that a value which is a substring of another longer
// value does not fragment the longer value's redaction. Empty values and an
// empty/nil values slice are no-ops.
func Redact(s string, values []string) string {
	result := s
	for _, v := range values {
		if v == "" {
			continue
		}
		result = strings.ReplaceAll(result, v, "[redacted]")
	}
	return result
}
