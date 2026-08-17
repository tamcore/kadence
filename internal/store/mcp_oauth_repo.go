package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/crypto"
)

// Link statuses. A link is either usable, needs a fresh browser authorization,
// or still holds tokens an unlink failed to revoke upstream.
const (
	LinkStatusLinked            = "linked"
	LinkStatusReauthRequired    = "reauth_required"
	LinkStatusDisconnectPending = "disconnect_pending"
)

var (
	// ErrLinkNotFound means the user has not linked that server.
	ErrLinkNotFound = errors.New("store: mcp oauth link not found")
	// ErrCASConflict means another writer rotated the link first. The caller
	// must not retry with the refresh token it holds: the peer already
	// consumed it, and presenting it again revokes the whole family.
	ErrCASConflict = errors.New("store: mcp oauth link changed concurrently")
	// ErrTransactionNotFound means the authorization transaction is unknown,
	// already consumed, or expired. The three are deliberately one error: a
	// caller learning which would learn whether a state value ever existed.
	ErrTransactionNotFound = errors.New("store: mcp oauth transaction not found")
)

// MCPOAuthLink is one user's authorization with one MCP server. Token values
// are plaintext in memory and sealed at rest.
type MCPOAuthLink struct {
	UserID          int64
	ServerID        string
	AccessToken     string
	AccessExpiresAt time.Time
	RefreshToken    string
	Scope           string
	Resource        string
	Status          string
	CASVersion      int64
}

// MCPOAuthTransaction is a pending authorization. The state and the browser
// binding are digests; only the PKCE verifier is a sealed secret.
type MCPOAuthTransaction struct {
	StateHash    []byte
	UserID       int64
	ServerID     string
	PKCEVerifier string
	RedirectURI  string
	BindingHash  []byte
	ExpiresAt    time.Time
}

// MCPOAuthRepo stores per-user MCP OAuth links and pending authorizations.
type MCPOAuthRepo struct {
	pool   *pgxpool.Pool
	cipher *crypto.Cipher
}

func NewMCPOAuthRepo(pool *pgxpool.Pool, cipher *crypto.Cipher) *MCPOAuthRepo {
	return &MCPOAuthRepo{pool: pool, cipher: cipher}
}

func bindTo(userID int64, serverID, record string) crypto.Context {
	return crypto.Context{UserID: userID, ServerID: serverID, Record: record}
}

func (r *MCPOAuthRepo) sealTokens(link MCPOAuthLink) (access, refresh []byte, err error) {
	access, err = r.cipher.SealWithContext(link.AccessToken,
		bindTo(link.UserID, link.ServerID, crypto.RecordAccessToken))
	if err != nil {
		return nil, nil, fmt.Errorf("store: seal access token: %w", err)
	}
	refresh, err = r.cipher.SealWithContext(link.RefreshToken,
		bindTo(link.UserID, link.ServerID, crypto.RecordRefreshToken))
	if err != nil {
		return nil, nil, fmt.Errorf("store: seal refresh token: %w", err)
	}
	return access, refresh, nil
}

// Upsert writes the link, replacing any existing one for the same user and
// server and resetting cas_version to 1: a fresh authorization supersedes
// whatever rotation state the old grant was in.
func (r *MCPOAuthRepo) Upsert(ctx context.Context, link MCPOAuthLink) error {
	access, refresh, err := r.sealTokens(link)
	if err != nil {
		return err
	}
	status := link.Status
	if status == "" {
		status = LinkStatusLinked
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO mcp_oauth_tokens
		    (user_id, server_id, access_token, access_expires_at, refresh_token,
		     scope, resource, status, cas_version, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1,NOW())
		ON CONFLICT (user_id, server_id) DO UPDATE SET
		    access_token = EXCLUDED.access_token,
		    access_expires_at = EXCLUDED.access_expires_at,
		    refresh_token = EXCLUDED.refresh_token,
		    scope = EXCLUDED.scope,
		    resource = EXCLUDED.resource,
		    status = EXCLUDED.status,
		    cas_version = 1,
		    updated_at = NOW()`,
		link.UserID, link.ServerID, access, link.AccessExpiresAt, refresh,
		link.Scope, link.Resource, status); err != nil {
		return fmt.Errorf("store: upsert mcp oauth link: %w", err)
	}
	return nil
}

// Get returns the link with its tokens opened.
func (r *MCPOAuthRepo) Get(ctx context.Context, userID int64, serverID string) (MCPOAuthLink, error) {
	link := MCPOAuthLink{UserID: userID, ServerID: serverID}
	var access, refresh []byte
	err := r.pool.QueryRow(ctx, `
		SELECT access_token, access_expires_at, refresh_token, scope, resource,
		       status, cas_version
		  FROM mcp_oauth_tokens
		 WHERE user_id = $1 AND server_id = $2`, userID, serverID).
		Scan(&access, &link.AccessExpiresAt, &refresh, &link.Scope, &link.Resource,
			&link.Status, &link.CASVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPOAuthLink{}, ErrLinkNotFound
	}
	if err != nil {
		return MCPOAuthLink{}, fmt.Errorf("store: get mcp oauth link: %w", err)
	}

	link.AccessToken, err = r.cipher.OpenWithContext(access,
		bindTo(userID, serverID, crypto.RecordAccessToken))
	if err != nil {
		return MCPOAuthLink{}, fmt.Errorf("store: open access token: %w", err)
	}
	link.RefreshToken, err = r.cipher.OpenWithContext(refresh,
		bindTo(userID, serverID, crypto.RecordRefreshToken))
	if err != nil {
		return MCPOAuthLink{}, fmt.Errorf("store: open refresh token: %w", err)
	}
	return link, nil
}

// RotateCAS writes rotated tokens only if nobody else rotated first.
func (r *MCPOAuthRepo) RotateCAS(ctx context.Context, link MCPOAuthLink) error {
	access, refresh, err := r.sealTokens(link)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE mcp_oauth_tokens
		   SET access_token = $1, access_expires_at = $2, refresh_token = $3,
		       status = $4, cas_version = cas_version + 1, updated_at = NOW()
		 WHERE user_id = $5 AND server_id = $6 AND cas_version = $7`,
		access, link.AccessExpiresAt, refresh, LinkStatusLinked,
		link.UserID, link.ServerID, link.CASVersion)
	if err != nil {
		return fmt.Errorf("store: rotate mcp oauth link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCASConflict
	}
	return nil
}

// SetStatus moves a link between usable, needs-reauth, and revoke-pending.
func (r *MCPOAuthRepo) SetStatus(ctx context.Context, userID int64, serverID, status string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE mcp_oauth_tokens SET status = $1, updated_at = NOW()
		 WHERE user_id = $2 AND server_id = $3`, status, userID, serverID)
	if err != nil {
		return fmt.Errorf("store: set mcp oauth link status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLinkNotFound
	}
	return nil
}

// Delete removes the link. It is idempotent: an absent link is not an error,
// because unlinking twice is a user double-clicking, not a fault.
func (r *MCPOAuthRepo) Delete(ctx context.Context, userID int64, serverID string) error {
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM mcp_oauth_tokens WHERE user_id = $1 AND server_id = $2`,
		userID, serverID); err != nil {
		return fmt.Errorf("store: delete mcp oauth link: %w", err)
	}
	return nil
}

// CreateTransaction records a pending authorization.
func (r *MCPOAuthRepo) CreateTransaction(ctx context.Context, t MCPOAuthTransaction) error {
	verifier, err := r.cipher.SealWithContext(t.PKCEVerifier,
		bindTo(t.UserID, t.ServerID, crypto.RecordPKCEVerifier))
	if err != nil {
		return fmt.Errorf("store: seal pkce verifier: %w", err)
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO mcp_oauth_transactions
		    (state_hash, user_id, server_id, pkce_verifier, redirect_uri,
		     binding_hash, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		t.StateHash, t.UserID, t.ServerID, verifier, t.RedirectURI,
		t.BindingHash, t.ExpiresAt); err != nil {
		return fmt.Errorf("store: create mcp oauth transaction: %w", err)
	}
	return nil
}

// ConsumeTransaction deletes and returns the transaction in one statement, so
// two callbacks racing on one state cannot both proceed. An expired row is
// treated as absent, and the same call has already removed it.
func (r *MCPOAuthRepo) ConsumeTransaction(ctx context.Context, stateHash []byte, now time.Time) (MCPOAuthTransaction, error) {
	t := MCPOAuthTransaction{StateHash: stateHash}
	var verifier []byte
	err := r.pool.QueryRow(ctx, `
		DELETE FROM mcp_oauth_transactions
		 WHERE state_hash = $1
	   RETURNING user_id, server_id, pkce_verifier, redirect_uri, binding_hash, expires_at`,
		stateHash).
		Scan(&t.UserID, &t.ServerID, &verifier, &t.RedirectURI, &t.BindingHash, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPOAuthTransaction{}, ErrTransactionNotFound
	}
	if err != nil {
		return MCPOAuthTransaction{}, fmt.Errorf("store: consume mcp oauth transaction: %w", err)
	}
	if !t.ExpiresAt.After(now) {
		return MCPOAuthTransaction{}, ErrTransactionNotFound
	}

	t.PKCEVerifier, err = r.cipher.OpenWithContext(verifier,
		bindTo(t.UserID, t.ServerID, crypto.RecordPKCEVerifier))
	if err != nil {
		return MCPOAuthTransaction{}, fmt.Errorf("store: open pkce verifier: %w", err)
	}
	return t, nil
}
