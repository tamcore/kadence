package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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

	return vapidPub, vapidPriv, p256dh, auth
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

	_ = svc.SendToUser(context.Background(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL})
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

	_ = svc.SendToUser(context.Background(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL})
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

	if err := svc.SendToUser(context.Background(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL}); err != nil {
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

	_ = svc.SendToUser(context.Background(), 1, Payload{Title: "t", Body: "b", URL: testPayloadURL})
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

	if err := svc.SendToUser(context.Background(), 1, Payload{Title: "t"}); err != nil {
		t.Fatalf("expected nil error for no subscriptions, got %v", err)
	}
}
