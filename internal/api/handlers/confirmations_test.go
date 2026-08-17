package handlers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/confirm"
	"github.com/tamcore/kadence/internal/model"
)

const (
	confirmTestUserID  = int64(7)
	confirmTestBase    = "/api/confirmations"
	confirmTestAllowed = `{"confirm":true}`
)

// fakeConfirmBroker records what the handler passed through.
type fakeConfirmBroker struct {
	calls   int
	userID  int64
	id      string
	allowed bool
	err     error
}

func (f *fakeConfirmBroker) Submit(userID int64, id string, allowed bool) error {
	f.calls++
	f.userID, f.id, f.allowed = userID, id, allowed
	return f.err
}

// serveConfirm routes one request through the handler with an authenticated
// user in the context, as LoadUser + RequireAuth would.
func serveConfirm(t *testing.T, b *fakeConfirmBroker, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := handlers.NewConfirmations(b)
	r := chi.NewRouter()
	r.Post(confirmTestBase+"/{id}", h.Submit)
	r.Post(confirmTestBase, h.Submit)

	req := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: confirmTestUserID}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSubmittingAConfirmationReachesTheBrokerAsThatUser(t *testing.T) {
	b := &fakeConfirmBroker{}
	rec := serveConfirm(t, b, confirmTestBase+"/req-1", confirmTestAllowed)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if b.calls != 1 {
		t.Fatalf("broker called %d times, want 1", b.calls)
	}
	if b.userID != confirmTestUserID {
		t.Fatalf("userID = %d, want the authenticated %d", b.userID, confirmTestUserID)
	}
	if b.id != "req-1" || !b.allowed {
		t.Fatalf("broker got (%q, %v), want (req-1, true)", b.id, b.allowed)
	}
}

func TestADeclineIsForwardedAsFalse(t *testing.T) {
	b := &fakeConfirmBroker{}
	if rec := serveConfirm(t, b, confirmTestBase+"/req-1", `{"confirm":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if b.allowed {
		t.Fatal("a decline reached the broker as an allow")
	}
}

func TestAnUnknownOrForeignConfirmationIsIndistinguishable(t *testing.T) {
	// One answer for "never existed", "already answered", "expired" and
	// "someone else's": a prober must learn nothing from the difference.
	b := &fakeConfirmBroker{err: confirm.ErrUnknownRequest}
	rec := serveConfirm(t, b, confirmTestBase+"/req-1", confirmTestAllowed)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "owner") {
		t.Fatalf("the response distinguishes the reason: %s", rec.Body.String())
	}
}

func TestAMalformedConfirmationBodyIsRejected(t *testing.T) {
	b := &fakeConfirmBroker{}
	rec := serveConfirm(t, b, confirmTestBase+"/req-1", `{"confirm":`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if b.calls != 0 {
		t.Fatal("a malformed body still reached the broker")
	}
}

func TestAConfirmationWithNoIDIsRejected(t *testing.T) {
	b := &fakeConfirmBroker{}
	rec := serveConfirm(t, b, confirmTestBase, confirmTestAllowed)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if b.calls != 0 {
		t.Fatal("an id-less request still reached the broker")
	}
}
