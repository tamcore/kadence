package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// HashSessionID returns the sha256 hex digest used as the sessions.id lookup
// key. The raw session id (the cookie value) is never stored; only its hash
// is. Exported so callers that need to compare a raw cookie value against a
// previously-fetched Session.IDHash (e.g. detecting the caller's own session
// in a list) can hash consistently without duplicating the algorithm.
//
// The distinct return type is deliberate: every method below takes the RAW id
// and hashes it itself, so feeding a hash back in would hash twice and match
// no row. model.SessionIDHash makes that a compile error instead of a silent
// no-op.
func HashSessionID(raw string) model.SessionIDHash {
	sum := sha256.Sum256([]byte(raw))
	return model.SessionIDHash(hex.EncodeToString(sum[:]))
}

// hashedID is HashSessionID narrowed back to the string pgx binds as the
// sessions.id parameter. Keeping the conversion in one place documents that
// every id parameter below is the hash, never the raw value.
func hashedID(raw string) string { return string(HashSessionID(raw)) }

// SessionRepository provides access to the sessions table.
type SessionRepository struct {
	pool *pgxpool.Pool
}

// NewSessionRepository returns a SessionRepository backed by pool.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create inserts a session row. s.ID must be the raw session id (as minted
// by auth.NewSessionID); only its hash is persisted.
func (r *SessionRepository) Create(ctx context.Context, s model.Session) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, remember_me, expires_at, user_agent, ip)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		hashedID(s.ID), s.UserID, s.RememberMe, s.ExpiresAt, s.UserAgent, s.IP)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetByID returns a live (non-expired) session or ErrNotFound. id is the raw
// session id (the cookie value); it is hashed before the lookup. The
// returned Session.ID is set back to the raw id passed in, not the stored
// hash, so callers keep working with the value they already hold.
func (r *SessionRepository) GetByID(ctx context.Context, id string) (model.Session, error) {
	var s model.Session
	err := r.pool.QueryRow(ctx,
		`SELECT public_id, user_id, remember_me, user_agent, ip, created_at, last_seen_at, expires_at
		 FROM sessions WHERE id = $1 AND expires_at > NOW()`, hashedID(id)).
		Scan(&s.PublicID, &s.UserID, &s.RememberMe, &s.UserAgent, &s.IP, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Session{}, ErrNotFound
	}
	if err != nil {
		return model.Session{}, fmt.Errorf("scan session: %w", err)
	}
	s.ID = id
	return s, nil
}

// ListByUser returns a user's live sessions, most-recently-active first.
// Since the raw session id is never stored, each result carries only the
// hashed id, in Session.IDHash; Session.ID is left EMPTY because this query
// cannot recover the raw value. To find the caller's own session in the list,
// compare HashSessionID(rawCookieValue) against IDHash. The distinct type stops
// a listed session's id from being passed to Touch/Delete/UpdateExpiry, which
// hash the raw id themselves and would otherwise match no row and report
// success.
func (r *SessionRepository) ListByUser(ctx context.Context, userID int64) ([]model.Session, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, public_id, user_id, remember_me, user_agent, ip, created_at, last_seen_at, expires_at
		 FROM sessions WHERE user_id = $1 AND expires_at > NOW() ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		var s model.Session
		var idHash string
		if err := rows.Scan(&idHash, &s.PublicID, &s.UserID, &s.RememberMe, &s.UserAgent, &s.IP, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.IDHash = model.SessionIDHash(idHash)
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteByPublicIDForUser revokes one session by its public id, owner-scoped.
func (r *SessionRepository) DeleteByPublicIDForUser(ctx context.Context, publicID string, userID int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE public_id = $1 AND user_id = $2`, publicID, userID)
	if err != nil {
		return fmt.Errorf("delete session by public id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Touch updates a session's last-active time + IP (best-effort recency
// tracking). id is the raw session id; it is hashed before the lookup.
func (r *SessionRepository) Touch(ctx context.Context, id string, ip string, at time.Time) error {
	if _, err := r.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = $1, ip = $2 WHERE id = $3`, at, ip, hashedID(id)); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// UpdateExpiry extends a session's expiry. id is the raw session id; it is
// hashed before the lookup.
func (r *SessionRepository) UpdateExpiry(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET expires_at = $2 WHERE id = $1`, hashedID(id), expiresAt)
	if err != nil {
		return fmt.Errorf("update session expiry: %w", err)
	}
	return nil
}

// Delete removes a single session. id is the raw session id; it is hashed
// before the lookup.
func (r *SessionRepository) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, hashedID(id))
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteAllByUser removes every session for a user.
func (r *SessionRepository) DeleteAllByUser(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete sessions by user: %w", err)
	}
	return nil
}

// DeleteExpired removes every session whose expiry has passed and returns how
// many rows were deleted. Intended to be called periodically by a background
// reaper; relies on idx_sessions_expires_at for the scan.
func (r *SessionRepository) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteOthersByUser removes every session for userID except exceptID,
// leaving the caller's current session untouched. exceptID is the raw
// session id; it is hashed before comparison.
func (r *SessionRepository) DeleteOthersByUser(ctx context.Context, userID int64, exceptID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id <> $2`, userID, hashedID(exceptID))
	if err != nil {
		return fmt.Errorf("delete other sessions by user: %w", err)
	}
	return nil
}
