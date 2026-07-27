package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/model"
)

const pushTestValidSubscribeBody = `{"endpoint":"https://push.example/z","keys":{"p256dh":"p","auth":"a"}}`

type fakeSubStore struct {
	upserted     model.PushSubscription
	upsertErr    error
	deletedUser  int64
	deletedEndp  string
	deleteCalled bool
	deleteErr    error
}

func (f *fakeSubStore) Upsert(_ context.Context, s model.PushSubscription) (model.PushSubscription, error) {
	f.upserted = s
	if f.upsertErr != nil {
		return model.PushSubscription{}, f.upsertErr
	}
	return s, nil
}

func (f *fakeSubStore) DeleteByEndpoint(_ context.Context, userID int64, endpoint string) error {
	f.deleteCalled = true
	f.deletedUser = userID
	f.deletedEndp = endpoint
	return f.deleteErr
}

func TestSubscribeRejectsMissingKeys(t *testing.T) {
	h := handlers.NewPush("pub", &fakeSubStore{})
	body := `{"endpoint":"https://x","keys":{"p256dh":"","auth":""}}`
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(body)), 1)
	rec := httptest.NewRecorder()

	h.Subscribe(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSubscribeRejectsMissingEndpoint(t *testing.T) {
	h := handlers.NewPush("pub", &fakeSubStore{})
	body := `{"endpoint":"","keys":{"p256dh":"p","auth":"a"}}`
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(body)), 1)
	rec := httptest.NewRecorder()

	h.Subscribe(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSubscribeRejectsAnonymous(t *testing.T) {
	h := handlers.NewPush("pub", &fakeSubStore{})
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(pushTestValidSubscribeBody))
	rec := httptest.NewRecorder()

	h.Subscribe(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestSubscribeUpsertsForSessionUser(t *testing.T) {
	store := &fakeSubStore{}
	h := handlers.NewPush("pub", store)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(pushTestValidSubscribeBody)), 99)
	req.Header.Set("User-Agent", "UA/1.0")
	rec := httptest.NewRecorder()

	h.Subscribe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	if store.upserted.UserID != 99 || store.upserted.Endpoint != "https://push.example/z" {
		t.Fatalf("unexpected upsert: %+v", store.upserted)
	}
	if store.upserted.P256dh != "p" || store.upserted.Auth != "a" {
		t.Fatalf("unexpected keys in upsert: %+v", store.upserted)
	}
	if store.upserted.UserAgent != "UA/1.0" {
		t.Fatalf("ua = %q", store.upserted.UserAgent)
	}
}

func TestSubscribeReturns500OnStoreError(t *testing.T) {
	store := &fakeSubStore{upsertErr: assertErr("boom")}
	h := handlers.NewPush("pub", store)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(pushTestValidSubscribeBody)), 1)
	rec := httptest.NewRecorder()

	h.Subscribe(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestConfigReturnsPublicKeyOnly(t *testing.T) {
	h := handlers.NewPush("the-public-key", &fakeSubStore{})
	req := httptest.NewRequest(http.MethodGet, "/api/push/config", nil)
	rec := httptest.NewRecorder()

	h.Config(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "the-public-key") {
		t.Fatal("expected public key in body")
	}
	if strings.Contains(rec.Body.String(), "private") {
		t.Fatal("response must never contain a private key field")
	}
}

func TestUnsubscribeDeletesForSessionUser(t *testing.T) {
	store := &fakeSubStore{}
	h := handlers.NewPush("pub", store)
	body := `{"endpoint":"https://push.example/z"}`
	req := withUser(httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions", strings.NewReader(body)), 42)
	rec := httptest.NewRecorder()

	h.Unsubscribe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	if !store.deleteCalled || store.deletedUser != 42 || store.deletedEndp != "https://push.example/z" {
		t.Fatalf("unexpected delete call: called=%v user=%d endpoint=%q", store.deleteCalled, store.deletedUser, store.deletedEndp)
	}
}

func TestUnsubscribeRejectsMissingEndpoint(t *testing.T) {
	h := handlers.NewPush("pub", &fakeSubStore{})
	req := withUser(httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions", strings.NewReader(`{"endpoint":""}`)), 1)
	rec := httptest.NewRecorder()

	h.Unsubscribe(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestUnsubscribeRejectsAnonymous(t *testing.T) {
	h := handlers.NewPush("pub", &fakeSubStore{})
	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions", strings.NewReader(`{"endpoint":"https://x"}`))
	rec := httptest.NewRecorder()

	h.Unsubscribe(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
