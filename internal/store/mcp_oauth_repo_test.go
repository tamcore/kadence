package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/crypto"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const (
	oauthTestServerID = "garmin"
	oauthRefreshTwo   = "refresh-2"
)

func newOAuthRepo(t *testing.T) (*store.MCPOAuthRepo, *pgxpool.Pool, int64) {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	pool := testutil.SetupTestDB(t)
	cipher, err := crypto.NewCipher(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	var userID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, email, password_hash)
		 VALUES ('oauthrepo','oauthrepo@example.invalid','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return store.NewMCPOAuthRepo(pool, cipher), pool, userID
}

func oauthTestLink(userID int64) store.MCPOAuthLink {
	return store.MCPOAuthLink{
		UserID:          userID,
		ServerID:        oauthTestServerID,
		AccessToken:     "access-1",
		AccessExpiresAt: time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second),
		RefreshToken:    "refresh-1",
		Scope:           "garmin:read",
		Resource:        "https://garmin.example.invalid/mcp",
		Status:          store.LinkStatusLinked,
		CASVersion:      1,
	}
}

func TestMCPOAuthUpsertAndGetRoundTripTokens(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	want := oauthTestLink(userID)

	if err := repo.Upsert(ctx, want); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(ctx, userID, oauthTestServerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Fatalf("tokens did not round-trip: access=%q refresh=%q", got.AccessToken, got.RefreshToken)
	}
	if got.Status != store.LinkStatusLinked || got.CASVersion != 1 {
		t.Fatalf("status/cas = %q/%d, want linked/1", got.Status, got.CASVersion)
	}
	if !got.AccessExpiresAt.Equal(want.AccessExpiresAt) {
		t.Fatalf("expiry = %s, want %s", got.AccessExpiresAt, want.AccessExpiresAt)
	}
}

func TestMCPOAuthGetMissingLinkIsErrLinkNotFound(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	if _, err := repo.Get(context.Background(), userID, oauthTestServerID); !errors.Is(err, store.ErrLinkNotFound) {
		t.Fatalf("Get: %v, want ErrLinkNotFound", err)
	}
}

func TestMCPOAuthRotateCASRejectsAStaleVersion(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthTestLink(userID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	first := oauthTestLink(userID)
	first.AccessToken = "access-2"
	first.RefreshToken = oauthRefreshTwo
	first.CASVersion = 1
	if err := repo.RotateCAS(ctx, first); err != nil {
		t.Fatalf("RotateCAS(first): %v", err)
	}

	second := oauthTestLink(userID)
	second.AccessToken = "access-3"
	second.RefreshToken = "refresh-3"
	second.CASVersion = 1 // stale: the first rotation already moved it
	if err := repo.RotateCAS(ctx, second); !errors.Is(err, store.ErrCASConflict) {
		t.Fatalf("RotateCAS(second): %v, want ErrCASConflict", err)
	}

	got, err := repo.Get(ctx, userID, oauthTestServerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RefreshToken != oauthRefreshTwo || got.CASVersion != 2 {
		t.Fatalf("stale rotation won: refresh=%q cas=%d", got.RefreshToken, got.CASVersion)
	}
}

func TestMCPOAuthSetStatusAndDelete(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthTestLink(userID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.SetStatus(ctx, userID, oauthTestServerID, store.LinkStatusReauthRequired); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, err := repo.Get(ctx, userID, oauthTestServerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != store.LinkStatusReauthRequired {
		t.Fatalf("status = %q, want reauth_required", got.Status)
	}

	if err := repo.SetStatus(ctx, userID, "absent", store.LinkStatusReauthRequired); !errors.Is(err, store.ErrLinkNotFound) {
		t.Fatalf("SetStatus(absent): %v, want ErrLinkNotFound", err)
	}

	if err := repo.Delete(ctx, userID, oauthTestServerID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, userID, oauthTestServerID); !errors.Is(err, store.ErrLinkNotFound) {
		t.Fatalf("Get after Delete: %v, want ErrLinkNotFound", err)
	}
	if err := repo.Delete(ctx, userID, oauthTestServerID); err != nil {
		t.Fatalf("Delete is not idempotent: %v", err)
	}
}

func TestMCPOAuthUpsertReplacesAndResetsCAS(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthTestLink(userID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	rotated := oauthTestLink(userID)
	rotated.AccessToken = "access-2"
	rotated.RefreshToken = oauthRefreshTwo
	if err := repo.RotateCAS(ctx, rotated); err != nil {
		t.Fatalf("RotateCAS: %v", err)
	}

	relinked := oauthTestLink(userID)
	relinked.AccessToken = "access-fresh"
	relinked.RefreshToken = "refresh-fresh"
	if err := repo.Upsert(ctx, relinked); err != nil {
		t.Fatalf("Upsert(relink): %v", err)
	}

	got, err := repo.Get(ctx, userID, oauthTestServerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RefreshToken != "refresh-fresh" || got.CASVersion != 1 {
		t.Fatalf("relink left refresh=%q cas=%d, want refresh-fresh/1", got.RefreshToken, got.CASVersion)
	}
}

func TestMCPOAuthConsumeTransactionIsSingleUseAndExpires(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()

	tx := store.MCPOAuthTransaction{
		StateHash:    []byte("state-hash-1"),
		UserID:       userID,
		ServerID:     oauthTestServerID,
		PKCEVerifier: "verifier-1",
		RedirectURI:  "https://kadence.example.invalid/api/mcp/oauth/callback",
		BindingHash:  []byte("binding-hash-1"),
		ExpiresAt:    now.Add(10 * time.Minute),
	}
	if err := repo.CreateTransaction(ctx, tx); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	got, err := repo.ConsumeTransaction(ctx, tx.StateHash, now)
	if err != nil {
		t.Fatalf("ConsumeTransaction: %v", err)
	}
	if got.PKCEVerifier != "verifier-1" || got.UserID != userID {
		t.Fatalf("transaction did not round-trip: verifier=%q user=%d", got.PKCEVerifier, got.UserID)
	}
	if !bytes.Equal(got.BindingHash, tx.BindingHash) || got.RedirectURI != tx.RedirectURI {
		t.Fatalf("binding or redirect did not round-trip: %x %q", got.BindingHash, got.RedirectURI)
	}
	if _, err := repo.ConsumeTransaction(ctx, tx.StateHash, now); !errors.Is(err, store.ErrTransactionNotFound) {
		t.Fatalf("second consume: %v, want ErrTransactionNotFound", err)
	}

	expired := tx
	expired.StateHash = []byte("state-hash-2")
	expired.ExpiresAt = now.Add(-time.Second)
	if err := repo.CreateTransaction(ctx, expired); err != nil {
		t.Fatalf("CreateTransaction(expired): %v", err)
	}
	if _, err := repo.ConsumeTransaction(ctx, expired.StateHash, now); !errors.Is(err, store.ErrTransactionNotFound) {
		t.Fatalf("expired consume: %v, want ErrTransactionNotFound", err)
	}
}

func TestMCPOAuthStoredTokensAreContextBound(t *testing.T) {
	repo, pool, userID := newOAuthRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthTestLink(userID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Copy the sealed columns verbatim into another server's row. The envelope
	// binds the server id, so the moved ciphertext must refuse to open.
	if _, err := pool.Exec(ctx, `
		INSERT INTO mcp_oauth_tokens
		    (user_id, server_id, access_token, access_expires_at, refresh_token, scope, resource)
		SELECT user_id, 'strava', access_token, access_expires_at, refresh_token, scope, resource
		  FROM mcp_oauth_tokens WHERE user_id = $1 AND server_id = $2`,
		userID, oauthTestServerID); err != nil {
		t.Fatalf("copy row: %v", err)
	}

	if _, err := repo.Get(ctx, userID, "strava"); err == nil {
		t.Fatal("a ciphertext moved to another server's row decrypted, want failure")
	}
}
