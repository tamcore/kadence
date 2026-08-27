package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/tamcore/kadence/internal/model"
)

const testPayloadURL = "/chat/x"

// testKeys generates a fresh, cryptographically valid VAPID keypair and a
// valid P-256 client subscription keypair so webpush-go's encryption
// actually succeeds and the request reaches the test HTTP server.
func testKeys(t *testing.T) (vapidPub, vapidPriv, p256dh, auth string) {
	t.Helper()

	vapidPriv, vapidPub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	p256dh, auth = testClientKeys(t)

	return vapidPub, vapidPriv, p256dh, auth
}

// testClientKeys generates a valid P-256 client subscription keypair so
// webpush-go's encryption actually succeeds and the request reaches the test
// HTTP server. Shared by testKeys and testSubscription.
func testClientKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()

	clientKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	p256dh = base64.RawURLEncoding.EncodeToString(clientKey.PublicKey().Bytes())

	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	auth = base64.RawURLEncoding.EncodeToString(authSecret)

	return p256dh, auth
}

// testSubscription builds a model.PushSubscription with a fresh, valid P-256
// client keypair pointing at endpoint, for tests exercising the real webpush
// transport against an httptest server.
func testSubscription(t *testing.T, endpoint string) model.PushSubscription {
	t.Helper()

	p256dh, auth := testClientKeys(t)
	return model.PushSubscription{ID: "sub-1", Endpoint: endpoint, P256dh: p256dh, Auth: auth}
}

type fakeStore struct {
	subs    []model.PushSubscription
	deleted map[string]bool
	failed  map[string]int
	success map[string]bool
	listErr error
}

func (f *fakeStore) ListByUser(_ context.Context, _ int64) ([]model.PushSubscription, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.subs, nil
}

func (f *fakeStore) DeleteByID(_ context.Context, id string) error {
	if f.deleted == nil {
		f.deleted = map[string]bool{}
	}
	f.deleted[id] = true
	return nil
}

func (f *fakeStore) IncrementFailure(_ context.Context, id string) (int, error) {
	if f.failed == nil {
		f.failed = map[string]int{}
	}
	f.failed[id]++
	return f.failed[id], nil
}

func (f *fakeStore) MarkSuccess(_ context.Context, id string) error {
	if f.success == nil {
		f.success = map[string]bool{}
	}
	f.success[id] = true
	return nil
}

func TestSendToUserPrunesGoneEndpoints(t *testing.T) {
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusGone) }))
	defer gone.Close()

	vapidPub, vapidPriv, p256dh, auth := testKeys(t)

	fake := &fakeStore{subs: []model.PushSubscription{{ID: "s1", Endpoint: gone.URL, P256dh: p256dh, Auth: auth}}}
	svc := NewService(fake, vapidPub, vapidPriv, "mailto:a@b.c", slog.Default())

	_ = svc.SendToUser(t.Context(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL})
	if !fake.deleted["s1"] {
		t.Fatal("expected 410 Gone endpoint to be deleted")
	}
}

func TestSendToUserPrunesNotFoundEndpoints(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer notFound.Close()

	vapidPub, vapidPriv, p256dh, auth := testKeys(t)

	fake := &fakeStore{subs: []model.PushSubscription{{ID: "s1", Endpoint: notFound.URL, P256dh: p256dh, Auth: auth}}}
	svc := NewService(fake, vapidPub, vapidPriv, "mailto:a@b.c", slog.Default())

	_ = svc.SendToUser(t.Context(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL})
	if !fake.deleted["s1"] {
		t.Fatal("expected 404 endpoint to be deleted")
	}
}

func TestSendToUserMarksSuccessOn2xx(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	defer ok.Close()

	vapidPub, vapidPriv, p256dh, auth := testKeys(t)

	fake := &fakeStore{subs: []model.PushSubscription{{ID: "s1", Endpoint: ok.URL, P256dh: p256dh, Auth: auth}}}
	svc := NewService(fake, vapidPub, vapidPriv, "mailto:a@b.c", slog.Default())

	if err := svc.SendToUser(t.Context(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !fake.success["s1"] {
		t.Fatal("expected MarkSuccess to be called")
	}
	if fake.deleted["s1"] {
		t.Fatal("did not expect deletion on success")
	}
}

func TestSendToUserPrunesAfterFailureCap(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer failing.Close()

	vapidPub, vapidPriv, p256dh, auth := testKeys(t)

	fake := &fakeStore{
		subs:   []model.PushSubscription{{ID: "s1", Endpoint: failing.URL, P256dh: p256dh, Auth: auth}},
		failed: map[string]int{"s1": maxPushFailures - 1},
	}
	svc := NewService(fake, vapidPub, vapidPriv, "mailto:a@b.c", slog.Default())

	_ = svc.SendToUser(t.Context(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL})
	if fake.failed["s1"] != maxPushFailures {
		t.Fatalf("expected failure count %d, got %d", maxPushFailures, fake.failed["s1"])
	}
	if !fake.deleted["s1"] {
		t.Fatal("expected subscription to be pruned once failure cap reached")
	}
}

func TestSendToUserNoSubscriptionsIsNoop(t *testing.T) {
	fake := &fakeStore{}
	svc := NewService(fake, "pub", "priv", "mailto:a@b.c", slog.Default())

	if err := svc.SendToUser(t.Context(), 1, Payload{Title: "t"}); err != nil {
		t.Fatalf("expected nil error for no subscriptions, got %v", err)
	}
}

func TestSendToUserReturnsErrorWhenAllSendsFail(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer failing.Close()

	vapidPub, vapidPriv, p256dh, auth := testKeys(t)

	fake := &fakeStore{subs: []model.PushSubscription{{ID: "s1", Endpoint: failing.URL, P256dh: p256dh, Auth: auth}}}
	svc := NewService(fake, vapidPub, vapidPriv, "mailto:a@b.c", slog.Default())

	err := svc.SendToUser(t.Context(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL})
	if err == nil {
		t.Fatal("expected error when every send fails, got nil")
	}
	if fake.deleted["s1"] {
		t.Fatal("did not expect a non-410/404 failure to prune below the failure cap")
	}
}

func TestSendToUserPropagatesListError(t *testing.T) {
	sentinel := errors.New("boom from store")
	fake := &fakeStore{listErr: sentinel}
	svc := NewService(fake, "pub", "priv", "mailto:a@b.c", slog.Default())

	err := svc.SendToUser(t.Context(), 1, Payload{Title: "t"})
	if err == nil {
		t.Fatal("expected error propagated from ListByUser, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected error to wrap sentinel, got %v", err)
	}
}

func TestNormalizeVAPIDSubject(t *testing.T) {
	const email = "admin@example.com"
	cases := map[string]string{
		"mailto:" + email:       email,                   // strip so webpush re-adds exactly one mailto:
		email:                   email,                   // bare left as-is (webpush adds mailto:)
		"https://example.com/x": "https://example.com/x", // https left untouched
	}
	for in, want := range cases {
		if got := normalizeVAPIDSubject(in); got != want {
			t.Errorf("normalizeVAPIDSubject(%q) = %q, want %q", in, got, want)
		}
	}
	// NewService applies the normalization to the stored subject.
	svc := NewService(&fakeStore{}, "pubkey", "privkey", "mailto:"+email, slog.Default())
	if svc.vapidSubject != email {
		t.Errorf("NewService vapidSubject = %q, want %q", svc.vapidSubject, email)
	}
}

func TestSendOneTimesOutOnHangingGateway(t *testing.T) {
	restore := pushSendTimeoutForTest(50 * time.Millisecond)
	t.Cleanup(restore)

	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-blocked // never responds within the deadline
	}))
	t.Cleanup(func() {
		close(blocked)
		srv.Close()
	})

	pub, priv, _, _ := testKeys(t)
	store := &fakeStore{subs: []model.PushSubscription{testSubscription(t, srv.URL)}}
	s := NewService(store, pub, priv, "mailto:ops@example.test", slog.Default())

	start := time.Now()
	err := s.SendToUser(t.Context(), 7, Payload{Title: "t", Body: "b"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when every send times out")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("send took %s; a hung gateway must not stall the batch", elapsed)
	}
}

func TestSendToUserContainsAPanickingSend(t *testing.T) {
	var logs bytes.Buffer
	pub, priv, _, _ := testKeys(t)
	store := &fakeStore{subs: []model.PushSubscription{{
		ID: "sub-1", Endpoint: "http://127.0.0.1:1/push",
		P256dh: "!!not-base64!!", Auth: "!!not-base64!!",
	}}}
	s := NewService(store, pub, priv, "mailto:ops@example.test",
		slog.New(slog.NewTextHandler(&logs, nil)))

	// Must return an error rather than taking the process down, whatever the
	// transport does with malformed subscription keys.
	if err := s.SendToUser(t.Context(), 7, Payload{Title: "t"}); err == nil {
		t.Fatal("expected an error for an unusable subscription")
	}
}
