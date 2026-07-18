package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// SessionRepository provides access to the sessions table.
type SessionRepository struct {
	pool *pgxpool.Pool
}

// NewSessionRepository returns a SessionRepository backed by pool.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create inserts a session row.
func (r *SessionRepository) Create(ctx context.Context, s model.Session) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, remember_me, expires_at) VALUES ($1, $2, $3, $4)`,
		s.ID, s.UserID, s.RememberMe, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetByID returns a live (non-expired) session or ErrNotFound.
func (r *SessionRepository) GetByID(ctx context.Context, id string) (model.Session, error) {
	var s model.Session
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, remember_me, created_at, expires_at
		 FROM sessions WHERE id = $1 AND expires_at > NOW()`, id).
		Scan(&s.ID, &s.UserID, &s.RememberMe, &s.CreatedAt, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Session{}, ErrNotFound
	}
	if err != nil {
		return model.Session{}, fmt.Errorf("scan session: %w", err)
	}
	return s, nil
}

// UpdateExpiry extends a session's expiry.
func (r *SessionRepository) UpdateExpiry(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET expires_at = $2 WHERE id = $1`, id, expiresAt)
	if err != nil {
		return fmt.Errorf("update session expiry: %w", err)
	}
	return nil
}

// Delete removes a single session.
func (r *SessionRepository) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
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
