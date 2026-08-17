package store_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
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

func TestMCPOAuthUpsertNeverLowersCASVersion(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthTestLink(userID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	rotated := oauthTestLink(userID)
	rotated.RefreshToken = oauthRefreshTwo
	if err := repo.RotateCAS(ctx, rotated); err != nil {
		t.Fatalf("RotateCAS: %v", err)
	}

	relinked := oauthTestLink(userID)
	relinked.RefreshToken = "refresh-fresh"
	if err := repo.Upsert(ctx, relinked); err != nil {
		t.Fatalf("Upsert(relink): %v", err)
	}

	// A rotation that was in flight across the relink still holds version 2.
	// It must lose: its tokens were minted from a grant the user replaced.
	stale := oauthTestLink(userID)
	stale.RefreshToken = "refresh-stale"
	stale.CASVersion = 2
	if err := repo.RotateCAS(ctx, stale); !errors.Is(err, store.ErrCASConflict) {
		t.Fatalf("stale RotateCAS after relink: %v, want ErrCASConflict", err)
	}
	got, err := repo.Get(ctx, userID, oauthTestServerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RefreshToken != "refresh-fresh" {
		t.Fatalf("stale rotation overwrote the relink: refresh=%q", got.RefreshToken)
	}
}

func TestMCPOAuthSetStatusBeatsAnInFlightRotation(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthTestLink(userID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	observed, err := repo.Get(ctx, userID, oauthTestServerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The user disconnects while a rotation is in flight.
	if err := repo.SetStatus(ctx, userID, oauthTestServerID, store.LinkStatusDisconnectPending); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	late := oauthTestLink(userID)
	late.RefreshToken = oauthRefreshTwo
	late.CASVersion = observed.CASVersion
	if err := repo.RotateCAS(ctx, late); !errors.Is(err, store.ErrCASConflict) {
		t.Fatalf("late RotateCAS: %v, want ErrCASConflict", err)
	}
	got, err := repo.Get(ctx, userID, oauthTestServerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != store.LinkStatusDisconnectPending {
		t.Fatalf("status = %q, want disconnect_pending (the rotation resurrected the link)", got.Status)
	}
}

func TestMCPOAuthRotateUnderLockSerializesTwoWorkers(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthTestLink(userID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var (
		mu       sync.Mutex
		observed []string
		inFirst  = make(chan struct{})
		holdOff  = make(chan struct{})
	)
	rotate := func(hold bool) error {
		_, err := repo.RotateUnderLock(ctx, userID, oauthTestServerID,
			func(_ context.Context, current store.MCPOAuthLink) (store.MCPOAuthLink, error) {
				mu.Lock()
				observed = append(observed, current.RefreshToken)
				mu.Unlock()
				if hold {
					close(inFirst)
					<-holdOff
				}
				next := current
				next.AccessToken = "access-" + current.RefreshToken
				next.RefreshToken = "next-of-" + current.RefreshToken
				return next, nil
			})
		return err
	}

	first := make(chan error, 1)
	go func() { first <- rotate(true) }()
	<-inFirst

	second := make(chan error, 1)
	go func() { second <- rotate(false) }()

	// The second worker must be blocked on the row lock: if it were not, it
	// would already have observed the same refresh token as the first.
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	seenWhileHeld := len(observed)
	mu.Unlock()
	if seenWhileHeld != 1 {
		t.Fatalf("%d workers read the link while it was locked, want 1", seenWhileHeld)
	}

	close(holdOff)
	if err := <-first; err != nil {
		t.Fatalf("first RotateUnderLock: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second RotateUnderLock: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 2 {
		t.Fatalf("observed %d reads, want 2", len(observed))
	}
	if observed[0] == observed[1] {
		t.Fatalf("both workers presented the same refresh token %q", observed[0])
	}
	if observed[1] != "next-of-"+observed[0] {
		t.Fatalf("second worker read %q, want the token the first one wrote", observed[1])
	}
}

func TestMCPOAuthRotateUnderLockLeavesTokensOnRefreshFailure(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, oauthTestLink(userID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	wantErr := errors.New("upstream refused")
	if _, err := repo.RotateUnderLock(ctx, userID, oauthTestServerID,
		func(context.Context, store.MCPOAuthLink) (store.MCPOAuthLink, error) {
			return store.MCPOAuthLink{}, wantErr
		}); !errors.Is(err, wantErr) {
		t.Fatalf("RotateUnderLock: %v, want the refresh error", err)
	}

	got, err := repo.Get(ctx, userID, oauthTestServerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RefreshToken != "refresh-1" {
		t.Fatalf("a failed rotation changed the stored token: %q", got.RefreshToken)
	}
}

func TestMCPOAuthRotateUnderLockMissingLink(t *testing.T) {
	repo, _, userID := newOAuthRepo(t)
	if _, err := repo.RotateUnderLock(context.Background(), userID, oauthTestServerID,
		func(context.Context, store.MCPOAuthLink) (store.MCPOAuthLink, error) {
			t.Fatal("refresh ran for a link that does not exist")
			return store.MCPOAuthLink{}, nil
		}); !errors.Is(err, store.ErrLinkNotFound) {
		t.Fatalf("RotateUnderLock: %v, want ErrLinkNotFound", err)
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
