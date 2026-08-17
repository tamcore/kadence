package oauth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/store"
)

const (
	testUserID     = int64(42)
	testServerID   = "garmin"
	testWriteScope = "garmin:write"
)

// fakeLinkStore is an in-memory LinkStore modelling only what the service
// depends on: one link and a handful of transactions.
type fakeLinkStore struct {
	link *store.MCPOAuthLink
	txs  map[string]store.MCPOAuthTransaction
}

func newFakeLinkStore() *fakeLinkStore {
	return &fakeLinkStore{txs: map[string]store.MCPOAuthTransaction{}}
}

func (f *fakeLinkStore) Upsert(_ context.Context, link store.MCPOAuthLink) error {
	link.CASVersion++
	f.link = &link
	return nil
}

func (f *fakeLinkStore) Get(_ context.Context, _ int64, _ string) (store.MCPOAuthLink, error) {
	if f.link == nil {
		return store.MCPOAuthLink{}, store.ErrLinkNotFound
	}
	return *f.link, nil
}

func (f *fakeLinkStore) RotateUnderLock(
	ctx context.Context, _ int64, _ string, refresh store.RefreshFunc,
) (store.MCPOAuthLink, error) {
	if f.link == nil {
		return store.MCPOAuthLink{}, store.ErrLinkNotFound
	}
	if f.link.Status != store.LinkStatusLinked {
		return store.MCPOAuthLink{}, store.ErrLinkNotUsable
	}
	next, err := refresh(ctx, *f.link)
	if err != nil {
		return store.MCPOAuthLink{}, err
	}
	next.CASVersion = f.link.CASVersion + 1
	f.link = &next
	return next, nil
}

func (f *fakeLinkStore) SetStatusIfVersion(_ context.Context, _ int64, _, status string, casVersion int64) error {
	if f.link == nil {
		return store.ErrLinkNotFound
	}
	if f.link.CASVersion != casVersion {
		return store.ErrCASConflict
	}
	f.link.Status = status
	f.link.CASVersion++
	return nil
}

func (f *fakeLinkStore) Delete(_ context.Context, _ int64, _ string) error {
	f.link = nil
	return nil
}

func (f *fakeLinkStore) SetStatus(_ context.Context, _ int64, _, status string) error {
	if f.link == nil {
		return store.ErrLinkNotFound
	}
	f.link.Status = status
	f.link.CASVersion++
	return nil
}

func (f *fakeLinkStore) CreateTransaction(_ context.Context, t store.MCPOAuthTransaction) error {
	f.txs[string(t.StateHash)] = t
	return nil
}

func (f *fakeLinkStore) ConsumeTransaction(
	_ context.Context, stateHash []byte, userID int64, bindingHash []byte, now time.Time,
) (store.MCPOAuthTransaction, error) {
	t, ok := f.txs[string(stateHash)]
	if !ok || t.UserID != userID || string(t.BindingHash) != string(bindingHash) || !t.ExpiresAt.After(now) {
		return store.MCPOAuthTransaction{}, store.ErrTransactionNotFound
	}
	delete(f.txs, string(stateHash))
	return t, nil
}

func (f *fakeLinkStore) DeleteExpiredTransactions(_ context.Context, _ int64, now time.Time) error {
	for k, t := range f.txs {
		if !t.ExpiresAt.After(now) {
			delete(f.txs, k)
		}
	}
	return nil
}

func (f *fakeLinkStore) CountTransactions(_ context.Context, _ int64, now time.Time) (int, error) {
	n := 0
	for _, t := range f.txs {
		if t.ExpiresAt.After(now) {
			n++
		}
	}
	return n, nil
}

func newTestService(t *testing.T, ts *tokenServer, now func() time.Time) (*Service, *fakeLinkStore) {
	t.Helper()
	st := newFakeLinkStore()
	svc := NewService(st, map[string]*Client{testServerID: clientFor(ts, "")},
		testRedirect, map[string][]string{testServerID: {testScope}}, now)
	return svc, st
}

func fixedNow(at time.Time) func() time.Time { return func() time.Time { return at } }

func linkedAt(now time.Time, expiresIn time.Duration) *store.MCPOAuthLink {
	return &store.MCPOAuthLink{
		UserID: testUserID, ServerID: testServerID, AccessToken: testAccessV1,
		AccessExpiresAt: now.Add(expiresIn), RefreshToken: testRefreshV1,
		Scope: testScope, Status: store.LinkStatusLinked, CASVersion: 1,
	}
}

func TestStartThenCompleteLinksTheAccount(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, map[string]any{
		fieldAccessToken: testAccessV1, fieldExpiresIn: 900, paramRefreshToken: testRefreshV1,
		paramScope: testScope,
	})
	svc, st := newTestService(t, ts, fixedNow(now))
	ctx := context.Background()

	authorizeURL, state, browserToken, err := svc.Start(ctx, testUserID, testServerID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if authorizeURL == "" || state == "" || browserToken == "" {
		t.Fatalf("Start returned an empty field: url=%q state=%q token=%q", authorizeURL, state, browserToken)
	}

	serverID, err := svc.Complete(ctx, testUserID, "code-1", state, browserToken)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if serverID != testServerID {
		t.Fatalf("server = %q, want %q", serverID, testServerID)
	}
	if st.link == nil || st.link.AccessToken != testAccessV1 || st.link.RefreshToken != testRefreshV1 {
		t.Fatalf("link stored wrong: %+v", st.link)
	}
	if st.link.Scope != testScope {
		t.Fatalf("scope = %q, want the granted %q", st.link.Scope, testScope)
	}
	if want := now.Add(900 * time.Second); !st.link.AccessExpiresAt.Equal(want) {
		t.Fatalf("expiry = %s, want %s", st.link.AccessExpiresAt, want)
	}
}

func TestCompleteRefusesEveryMismatch(t *testing.T) {
	now := time.Now().UTC()
	ctx := context.Background()

	for name, mangle := range map[string]func(state, token string) (int64, string, string){
		"unknown state":  func(_, token string) (int64, string, string) { return testUserID, "never-issued", token },
		"wrong browser":  func(state, _ string) (int64, string, string) { return testUserID, state, "other-token" },
		"different user": func(state, token string) (int64, string, string) { return testUserID + 1, state, token },
	} {
		t.Run(name, func(t *testing.T) {
			ts := newTokenServer(t, http.StatusOK, map[string]any{
				fieldAccessToken: "at", fieldExpiresIn: 900, paramRefreshToken: "rt",
			})
			svc, st := newTestService(t, ts, fixedNow(now))
			_, state, browserToken, err := svc.Start(ctx, testUserID, testServerID)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}

			userID, gotState, gotToken := mangle(state, browserToken)
			if _, err := svc.Complete(ctx, userID, "code", gotState, gotToken); !errors.Is(err, ErrBadTransaction) {
				t.Fatalf("Complete: %v, want ErrBadTransaction", err)
			}
			if st.link != nil {
				t.Fatal("a refused completion still linked the account")
			}
			if ts.calls != 0 {
				t.Fatalf("a refused completion still called the token endpoint %d times", ts.calls)
			}
		})
	}
}

func TestCompleteIsSingleUse(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, map[string]any{
		fieldAccessToken: "at", fieldExpiresIn: 900, paramRefreshToken: "rt",
	})
	svc, _ := newTestService(t, ts, fixedNow(now))
	ctx := context.Background()

	_, state, browserToken, err := svc.Start(ctx, testUserID, testServerID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := svc.Complete(ctx, testUserID, "code", state, browserToken); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if _, err := svc.Complete(ctx, testUserID, "code", state, browserToken); !errors.Is(err, ErrBadTransaction) {
		t.Fatalf("second Complete: %v, want ErrBadTransaction", err)
	}
}

func TestStartBoundsConcurrentTransactions(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, nil)
	svc, _ := newTestService(t, ts, fixedNow(now))
	ctx := context.Background()

	for i := range maxOpenTransactions {
		if _, _, _, err := svc.Start(ctx, testUserID, testServerID); err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
	}
	if _, _, _, err := svc.Start(ctx, testUserID, testServerID); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("Start beyond the bound: %v, want ErrTooManyAttempts", err)
	}
}

func TestTokenForUsesAValidTokenWithoutRefreshing(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, nil)
	svc, st := newTestService(t, ts, fixedNow(now))
	st.link = linkedAt(now, 10*time.Minute)
	st.link.AccessToken = "at-live"

	got, err := svc.TokenFor(context.Background(), testUserID, testServerID)
	if err != nil {
		t.Fatalf("TokenFor: %v", err)
	}
	if got != "at-live" {
		t.Fatalf("token = %q, want the stored one", got)
	}
	if ts.calls != 0 {
		t.Fatalf("a live token still triggered %d refreshes", ts.calls)
	}
}

func TestTokenForRefreshesWhenTheTokenIsAboutToExpire(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, map[string]any{
		fieldAccessToken: testAccessV2, fieldExpiresIn: 900, paramRefreshToken: testRefreshV2, paramScope: testScope,
	})
	svc, st := newTestService(t, ts, fixedNow(now))
	st.link = linkedAt(now, 30*time.Second)

	got, err := svc.TokenFor(context.Background(), testUserID, testServerID)
	if err != nil {
		t.Fatalf("TokenFor: %v", err)
	}
	if got != testAccessV2 {
		t.Fatalf("token = %q, want the rotated one", got)
	}
	if st.link.RefreshToken != testRefreshV2 {
		t.Fatalf("stored refresh token = %q, want the rotated one", st.link.RefreshToken)
	}
}

func TestTokenForCondemnsTheLinkOnADeadFamily(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusBadRequest, map[string]any{fieldError: codeBadGrant})
	svc, st := newTestService(t, ts, fixedNow(now))
	st.link = linkedAt(now, 10*time.Second)

	if _, err := svc.TokenFor(context.Background(), testUserID, testServerID); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("TokenFor: %v, want ErrReauthRequired", err)
	}
	if st.link.Status != store.LinkStatusReauthRequired {
		t.Fatalf("status = %q, want reauth_required", st.link.Status)
	}

	before := ts.calls
	if _, err := svc.TokenFor(context.Background(), testUserID, testServerID); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("second TokenFor: %v, want ErrReauthRequired", err)
	}
	if ts.calls != before {
		t.Fatal("a condemned link still called the token endpoint")
	}
}

func TestTokenForKeepsTheLinkOnAServerFault(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusInternalServerError, map[string]any{fieldError: codeServerFail})
	svc, st := newTestService(t, ts, fixedNow(now))
	st.link = linkedAt(now, 10*time.Second)

	_, err := svc.TokenFor(context.Background(), testUserID, testServerID)
	if err == nil {
		t.Fatal("TokenFor accepted a 500")
	}
	if errors.Is(err, ErrReauthRequired) {
		t.Fatal("a server fault unlinked the user")
	}
	if st.link.Status != store.LinkStatusLinked {
		t.Fatalf("status = %q, want linked", st.link.Status)
	}
}

func TestTokenForReportsAnUnlinkedUser(t *testing.T) {
	ts := newTokenServer(t, http.StatusOK, nil)
	svc, _ := newTestService(t, ts, fixedNow(time.Now().UTC()))

	if _, err := svc.TokenFor(context.Background(), testUserID, testServerID); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("TokenFor: %v, want ErrNotLinked", err)
	}
}

func TestUnlinkRevokesThenDeletes(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, nil)
	svc, st := newTestService(t, ts, fixedNow(now))
	st.link = linkedAt(now, 10*time.Minute)

	if err := svc.Unlink(context.Background(), testUserID, testServerID); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if ts.calls == 0 {
		t.Fatal("Unlink deleted the link without revoking upstream")
	}
	if st.link != nil {
		t.Fatal("Unlink left the link behind")
	}
}

func TestUnlinkStillDeletesWhenRevocationFails(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusInternalServerError, map[string]any{fieldError: codeServerFail})
	svc, st := newTestService(t, ts, fixedNow(now))
	st.link = linkedAt(now, 10*time.Minute)

	if err := svc.Unlink(context.Background(), testUserID, testServerID); err != nil {
		t.Fatalf("Unlink reported an error the user cannot act on: %v", err)
	}
	if st.link != nil {
		t.Fatal("the local link survived a failed revocation")
	}
}

func TestIntegrationsReportsAScopeShortfall(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, nil)
	st := newFakeLinkStore()
	svc := NewService(st, map[string]*Client{testServerID: clientFor(ts, "")},
		testRedirect, map[string][]string{testServerID: {testScope, testWriteScope}}, fixedNow(now))
	ctx := context.Background()

	// The deployment now asks for the write tier; this grant carries read only.
	st.link = linkedAt(now, 10*time.Minute)
	states, err := svc.Integrations(ctx, testUserID)
	if err != nil {
		t.Fatalf("Integrations: %v", err)
	}
	if len(states[0].ScopeShortfall) != 1 || states[0].ScopeShortfall[0] != testWriteScope {
		t.Fatalf("shortfall = %v, want [garmin:write]", states[0].ScopeShortfall)
	}

	// Once the user has authorized again, the grant covers it.
	st.link.Scope = testScope + " garmin:write"
	states, err = svc.Integrations(ctx, testUserID)
	if err != nil {
		t.Fatalf("Integrations: %v", err)
	}
	if len(states[0].ScopeShortfall) != 0 {
		t.Fatalf("shortfall = %v, want none", states[0].ScopeShortfall)
	}
}

func TestIntegrationsReportsNoShortfallForAnUnlinkedServer(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, nil)
	st := newFakeLinkStore()
	svc := NewService(st, map[string]*Client{testServerID: clientFor(ts, "")},
		testRedirect, map[string][]string{testServerID: {testScope, testWriteScope}}, fixedNow(now))

	states, err := svc.Integrations(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("Integrations: %v", err)
	}
	// Nothing is granted yet, so naming a shortfall would be noise: Connect
	// already asks for everything the deployment wants.
	if len(states[0].ScopeShortfall) != 0 {
		t.Fatalf("shortfall = %v for an unlinked server, want none", states[0].ScopeShortfall)
	}
}

func TestIntegrationsReportsEveryConfiguredServer(t *testing.T) {
	now := time.Now().UTC()
	ts := newTokenServer(t, http.StatusOK, nil)
	svc, st := newTestService(t, ts, fixedNow(now))

	states, err := svc.Integrations(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("Integrations: %v", err)
	}
	if len(states) != 1 || states[0].ServerID != testServerID || states[0].Linked {
		t.Fatalf("unlinked report = %+v", states)
	}

	st.link = linkedAt(now, 10*time.Minute)
	states, err = svc.Integrations(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("Integrations: %v", err)
	}
	if !states[0].Linked || states[0].Status != store.LinkStatusLinked || states[0].Scope != testScope {
		t.Fatalf("linked report = %+v", states[0])
	}
}
