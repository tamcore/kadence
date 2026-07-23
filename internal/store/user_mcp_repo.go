package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/crypto"
	"github.com/tamcore/kadence/internal/mcp"
)

// ErrDuplicateName is returned when an owner already has a server with the name.
var ErrDuplicateName = errors.New("store: duplicate user MCP server name")

// UserMCPInput is the create/update payload (plaintext password).
type UserMCPInput struct {
	Name, URL, Transport, AuthUser, AuthPass string
	// Alias, if set, replaces Name as this server's tool-name prefix (see
	// mcp.Server.Alias). Hint is a free-text "when to use this" line
	// injected into the chat system prompt (see mcp.Server.Hint). Both
	// optional; empty means neither is set.
	Alias, Hint string
}

// UserMCPRecord is a password-free projection for API responses.
type UserMCPRecord struct {
	ID        int64
	Name      string
	URL       string
	Transport string
	AuthUser  string
	Alias     string
	Hint      string
	CreatedAt time.Time
}

// UserServerRepo stores per-user MCP server definitions with the basic-auth
// password encrypted at rest.
type UserServerRepo struct {
	pool   *pgxpool.Pool
	cipher *crypto.Cipher
}

// NewUserServerRepo constructs the repo.
func NewUserServerRepo(pool *pgxpool.Pool, cipher *crypto.Cipher) *UserServerRepo {
	return &UserServerRepo{pool: pool, cipher: cipher}
}

// Create inserts a new server for ownerUserID, encrypting the password.
func (r *UserServerRepo) Create(ctx context.Context, ownerUserID int64, in UserMCPInput) (int64, error) {
	enc, err := r.cipher.Encrypt(in.AuthPass)
	if err != nil {
		return 0, fmt.Errorf("store: encrypt: %w", err)
	}
	var id int64
	err = r.pool.QueryRow(ctx,
		`INSERT INTO user_mcp_servers (owner_user_id, name, url, transport, auth_user, auth_pass_enc, alias, hint)
		 VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,'')) RETURNING id`,
		ownerUserID, in.Name, in.URL, in.Transport, in.AuthUser, enc, in.Alias, in.Hint).Scan(&id)
	if isUniqueViolation(err) {
		return 0, ErrDuplicateName
	}
	if err != nil {
		return 0, fmt.Errorf("store: create user mcp server: %w", err)
	}
	return id, nil
}

// Update modifies an owner's server. An empty AuthPass keeps the existing one.
func (r *UserServerRepo) Update(ctx context.Context, ownerUserID, id int64, in UserMCPInput) error {
	var (
		tag pgconn.CommandTag
		err error
	)
	if in.AuthPass == "" {
		tag, err = r.pool.Exec(ctx,
			`UPDATE user_mcp_servers SET name=$1, url=$2, transport=$3, auth_user=$4, alias=NULLIF($5,''), hint=NULLIF($6,'')
			 WHERE id=$7 AND owner_user_id=$8`,
			in.Name, in.URL, in.Transport, in.AuthUser, in.Alias, in.Hint, id, ownerUserID)
	} else {
		var enc []byte
		enc, err = r.cipher.Encrypt(in.AuthPass)
		if err != nil {
			return fmt.Errorf("store: encrypt: %w", err)
		}
		tag, err = r.pool.Exec(ctx,
			`UPDATE user_mcp_servers SET name=$1, url=$2, transport=$3, auth_user=$4, auth_pass_enc=$5, alias=NULLIF($6,''), hint=NULLIF($7,'')
			 WHERE id=$8 AND owner_user_id=$9`,
			in.Name, in.URL, in.Transport, in.AuthUser, enc, in.Alias, in.Hint, id, ownerUserID)
	}
	if isUniqueViolation(err) {
		return ErrDuplicateName
	}
	if err != nil {
		return fmt.Errorf("store: update user mcp server: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes an owner's server.
func (r *UserServerRepo) Delete(ctx context.Context, ownerUserID, id int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM user_mcp_servers WHERE id=$1 AND owner_user_id=$2`, id, ownerUserID)
	if err != nil {
		return fmt.Errorf("store: delete user mcp server: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListForOwner returns password-free records for an owner.
func (r *UserServerRepo) ListForOwner(ctx context.Context, ownerUserID int64) ([]UserMCPRecord, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, url, transport, auth_user, COALESCE(alias,''), COALESCE(hint,''), created_at
		 FROM user_mcp_servers WHERE owner_user_id=$1 ORDER BY name`, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("store: list user mcp servers: %w", err)
	}
	defer rows.Close()
	var out []UserMCPRecord
	for rows.Next() {
		var rec UserMCPRecord
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.URL, &rec.Transport, &rec.AuthUser, &rec.Alias, &rec.Hint, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan user mcp record: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ServersForUser returns decrypted mcp.Server values for one user (by username).
func (r *UserServerRepo) ServersForUser(ctx context.Context, username string) ([]mcp.Server, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT s.name, s.url, s.transport, s.auth_user, s.auth_pass_enc, COALESCE(s.alias,''), COALESCE(s.hint,'')
		 FROM user_mcp_servers s JOIN users u ON u.id = s.owner_user_id
		 WHERE u.username = $1 ORDER BY s.name`, username)
	if err != nil {
		return nil, fmt.Errorf("store: servers for user: %w", err)
	}
	return r.scanServers(rows, username)
}

// AllServers returns decrypted mcp.Server values for every user (for the poller).
func (r *UserServerRepo) AllServers(ctx context.Context) ([]mcp.Server, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT s.name, s.url, s.transport, s.auth_user, s.auth_pass_enc, COALESCE(s.alias,''), COALESCE(s.hint,''), u.username
		 FROM user_mcp_servers s JOIN users u ON u.id = s.owner_user_id
		 ORDER BY u.username, s.name`)
	if err != nil {
		return nil, fmt.Errorf("store: all user servers: %w", err)
	}
	return r.scanServers(rows, "")
}

// scanServers builds mcp.Server rows, decrypting the password. When
// usernameOverride is non-empty it is used for every Scope; otherwise the query
// must select username as the final column.
func (r *UserServerRepo) scanServers(rows pgx.Rows, usernameOverride string) ([]mcp.Server, error) {
	defer rows.Close()
	var out []mcp.Server
	for rows.Next() {
		var name, u, transport, authUser, alias, hint string
		var enc []byte
		username := usernameOverride
		var scanErr error
		if usernameOverride == "" {
			scanErr = rows.Scan(&name, &u, &transport, &authUser, &enc, &alias, &hint, &username)
		} else {
			scanErr = rows.Scan(&name, &u, &transport, &authUser, &enc, &alias, &hint)
		}
		if scanErr != nil {
			return nil, fmt.Errorf("store: scan user server: %w", scanErr)
		}
		pass, err := r.cipher.Decrypt(enc)
		if err != nil {
			return nil, fmt.Errorf("store: decrypt %s: %w", name, err)
		}
		out = append(out, mcp.Server{
			Name: name, Scope: "USER_" + username, URL: u,
			AuthUser: authUser, AuthPass: pass, Transport: transport,
			Alias: alias, Hint: hint,
		})
	}
	return out, rows.Err()
}

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
