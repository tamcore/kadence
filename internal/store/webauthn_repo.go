package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// webauthnCredCols is the SQL column list for webauthn_credentials (not a credential value).
const webauthnCredCols = "id, public_id, user_id, credential_id, public_key, aaguid, sign_count, transports, name, backup_eligible, backup_state, created_at, last_used_at" // #nosec G101

// WebAuthnCredentialRepository stores registered passkeys.
type WebAuthnCredentialRepository struct{ pool *pgxpool.Pool }

// NewWebAuthnCredentialRepository builds the repo.
func NewWebAuthnCredentialRepository(pool *pgxpool.Pool) *WebAuthnCredentialRepository {
	return &WebAuthnCredentialRepository{pool: pool}
}

func scanWebAuthnCred(row rowScanner) (model.WebAuthnCredential, error) {
	var c model.WebAuthnCredential
	var signCount int64
	err := row.Scan(&c.ID, &c.PublicID, &c.UserID, &c.CredentialID, &c.PublicKey,
		&c.AAGUID, &signCount, &c.Transports, &c.Name, &c.BackupEligible, &c.BackupState,
		&c.CreatedAt, &c.LastUsedAt)
	c.SignCount = uint32(signCount) // #nosec G115 -- WebAuthn sign counter is uint32 by spec
	return c, err
}

// Create inserts a credential (public_id/created_at via column defaults).
func (r *WebAuthnCredentialRepository) Create(ctx context.Context, c model.WebAuthnCredential) error {
	// aaguid/transports are NOT NULL columns; a nil slice sends explicit SQL
	// NULL (column DEFAULT does not apply), so coalesce to empty non-nil
	// values without mutating the caller's struct.
	aaguid := c.AAGUID
	if aaguid == nil {
		aaguid = []byte{}
	}
	transports := c.Transports
	if transports == nil {
		transports = []string{}
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, aaguid, sign_count, transports, name, backup_eligible, backup_state)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		c.UserID, c.CredentialID, c.PublicKey, aaguid, c.SignCount, transports, c.Name, c.BackupEligible, c.BackupState)
	if err != nil {
		return fmt.Errorf("insert webauthn credential: %w", err)
	}
	return nil
}

// ListByUser returns the user's credentials, newest first.
func (r *WebAuthnCredentialRepository) ListByUser(ctx context.Context, userID int64) ([]model.WebAuthnCredential, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+webauthnCredCols+` FROM webauthn_credentials WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list webauthn credentials: %w", err)
	}
	return collectRows(rows, "list webauthn credentials", scanWebAuthnCred)
}

// Rename sets a credential's name, owner-scoped.
func (r *WebAuthnCredentialRepository) Rename(ctx context.Context, publicID string, userID int64, name string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE webauthn_credentials SET name=$1 WHERE public_id=$2 AND user_id=$3`, name, publicID, userID)
	if err != nil {
		return fmt.Errorf("rename webauthn credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteByPublicIDForUser removes a credential, owner-scoped.
func (r *WebAuthnCredentialRepository) DeleteByPublicIDForUser(ctx context.Context, publicID string, userID int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM webauthn_credentials WHERE public_id=$1 AND user_id=$2`, publicID, userID)
	if err != nil {
		return fmt.Errorf("delete webauthn credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateSignCount bumps the counter + last_used_at after a successful assertion.
func (r *WebAuthnCredentialRepository) UpdateSignCount(ctx context.Context, credID []byte, signCount uint32, lastUsed time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE webauthn_credentials SET sign_count=$1, last_used_at=$2 WHERE credential_id=$3`, signCount, lastUsed, credID)
	if err != nil {
		return fmt.Errorf("update webauthn sign count: %w", err)
	}
	return nil
}
