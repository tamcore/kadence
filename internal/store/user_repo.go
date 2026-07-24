package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// ErrNotFound is returned when a repository lookup matches no row.
var ErrNotFound = errors.New("not found")

// ErrEmailTaken is returned when a profile update's email collides with
// another user's email (unique constraint violation).
var ErrEmailTaken = errors.New("store: email already in use")

// ErrUsernameTaken is returned when an update's username collides with
// another user's username (unique constraint violation).
var ErrUsernameTaken = errors.New("store: username already in use")

// UserRepository provides access to the users table.
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository returns a UserRepository backed by pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func scanUser(row pgx.Row) (model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.DisplayName, &u.UnitSystem,
		&u.Location, &u.AboutMe, &u.Timezone, &u.CreatedAt, &u.WebAuthnHandle)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

const userCols = "id, username, email, password_hash, role, display_name, unit_system, " +
	"COALESCE(location, '') AS location, COALESCE(about_me, '') AS about_me, timezone, created_at, webauthn_user_handle"

// Create inserts a new user and returns it with ID and CreatedAt populated.
func (r *UserRepository) Create(ctx context.Context, u model.User) (model.User, error) {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+userCols,
		u.Username, u.Email, u.PasswordHash, u.Role)
	return scanUser(row)
}

// GetByUsername looks up a user by username.
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (model.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE username = $1`, username))
}

// GetByID looks up a user by id.
func (r *UserRepository) GetByID(ctx context.Context, id int64) (model.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// ListAll returns all users ordered by id.
func (r *UserRepository) ListAll(ctx context.Context) ([]model.User, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Delete removes a user by id.
func (r *UserRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// Count returns the number of users.
func (r *UserRepository) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// CountAdmins returns the number of users with the admin role.
func (r *UserRepository) CountAdmins(ctx context.Context) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = $1`, model.RoleAdmin).Scan(&n); err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return n, nil
}

// UpdateUser updates a user's username, email, and role, returning the updated
// row. Returns ErrUsernameTaken or ErrEmailTaken on a unique-constraint
// collision, and ErrNotFound if no user has the given id.
func (r *UserRepository) UpdateUser(ctx context.Context, id int64, username, email, role string) (model.User, error) {
	row := r.pool.QueryRow(ctx,
		`UPDATE users SET username = $1, email = $2, role = $3 WHERE id = $4
		 RETURNING `+userCols,
		username, email, role, id)
	u, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "users_username_key" {
				return model.User{}, ErrUsernameTaken
			}
			return model.User{}, ErrEmailTaken
		}
		return model.User{}, err
	}
	return u, nil
}

// UpdateProfile updates a user's display name, email, unit system preference,
// location, and about-me text. location and aboutMe are stored as NULL when
// empty (so a cleared field round-trips to ""); non-empty values overwrite
// any prior one. Returns ErrEmailTaken if email collides with another user.
func (r *UserRepository) UpdateProfile(ctx context.Context, id int64, displayName, email, unitSystem, location, aboutMe, timezone string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET display_name = $1, email = $2, unit_system = $3, location = NULLIF($4, ''), about_me = NULLIF($5, ''), timezone = $6
		 WHERE id = $7`,
		displayName, email, unitSystem, location, aboutMe, timezone, id)
	if isUniqueViolation(err) {
		return ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	return nil
}

// UpdateTimezone stores a user's IANA timezone preference. Validation belongs
// to the caller because this repository is deliberately persistence-only.
func (r *UserRepository) UpdateTimezone(ctx context.Context, id int64, timezone string) error {
	command, err := r.pool.Exec(ctx, `UPDATE users SET timezone = $1 WHERE id = $2`, timezone, id)
	if err != nil {
		return fmt.Errorf("update timezone: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePassword updates a user's password hash.
func (r *UserRepository) UpdatePassword(ctx context.Context, id int64, passwordHash string) error {
	if _, err := r.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, passwordHash, id); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// GetByWebAuthnHandle looks up a user by their opaque WebAuthn handle.
func (r *UserRepository) GetByWebAuthnHandle(ctx context.Context, handle string) (model.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE webauthn_user_handle=$1`, handle))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return u, err
}
