package handlers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/secret"
)

const credsUserID = int64(7)

// fakeSubmitter is a configurable fake for the credentialSubmitter interface,
// used to drive the handler's sentinel-error-to-HTTP-status mapping without
// depending on secret.Broker's internal state machine.
type fakeSubmitter struct {
	err       error
	gotUserID int64
	gotReqID  string
	gotValues map[string]string
	wasCalled bool
}

func (f *fakeSubmitter) Submit(userID int64, requestID string, values map[string]string) error {
	f.wasCalled = true
	f.gotUserID = userID
	f.gotReqID = requestID
	f.gotValues = values
	return f.err
}

func newSubmitRequest(userID int64, requestID, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/credentials/"+requestID, strings.NewReader(body))
	if userID != 0 {
		r = withUser(r, userID)
	}
	return withChiParam(r, "requestId", requestID)
}

func TestCredentialsSubmitSuccess(t *testing.T) {
	// Arrange
	fs := &fakeSubmitter{}
	h := handlers.NewCredentials(fs)
	req := newSubmitRequest(credsUserID, "req-1", `{"values":{"apiKey":"secretvalue"}}`)
	rec := httptest.NewRecorder()

	// Act
	h.Submit(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !fs.wasCalled {
		t.Fatal("expected broker.Submit to be called")
	}
	if fs.gotUserID != credsUserID || fs.gotReqID != "req-1" {
		t.Fatalf("unexpected call args: userID=%d reqID=%s", fs.gotUserID, fs.gotReqID)
	}
	if fs.gotValues["apiKey"] != "secretvalue" {
		t.Fatalf("expected values passed through, got %v", fs.gotValues)
	}
}

func TestCredentialsSubmitUnauthenticated(t *testing.T) {
	// Arrange
	fs := &fakeSubmitter{}
	h := handlers.NewCredentials(fs)
	req := newSubmitRequest(0, "req-1", `{"values":{"apiKey":"x"}}`)
	rec := httptest.NewRecorder()

	// Act
	h.Submit(rec, req)

	// Assert
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if fs.wasCalled {
		t.Fatal("expected broker.Submit not to be called")
	}
}

func TestCredentialsSubmitEmptyRequestID(t *testing.T) {
	// Arrange
	fs := &fakeSubmitter{}
	h := handlers.NewCredentials(fs)
	req := newSubmitRequest(credsUserID, "", `{"values":{"apiKey":"x"}}`)
	rec := httptest.NewRecorder()

	// Act
	h.Submit(rec, req)

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if fs.wasCalled {
		t.Fatal("expected broker.Submit not to be called")
	}
}

func TestCredentialsSubmitMalformedJSON(t *testing.T) {
	// Arrange
	fs := &fakeSubmitter{}
	h := handlers.NewCredentials(fs)
	req := newSubmitRequest(credsUserID, "req-1", `{not valid json`)
	rec := httptest.NewRecorder()

	// Act
	h.Submit(rec, req)

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if fs.wasCalled {
		t.Fatal("expected broker.Submit not to be called")
	}
}

func TestCredentialsSubmitErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not owner", secret.ErrNotOwner, http.StatusForbidden},
		{"already submitted", secret.ErrAlreadySubmitted, http.StatusConflict},
		{"unknown request", secret.ErrUnknownRequest, http.StatusBadRequest},
		{"field mismatch", secret.ErrFieldMismatch, http.StatusBadRequest},
		{"other error", errors.New("boom"), http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			fs := &fakeSubmitter{err: tc.err}
			h := handlers.NewCredentials(fs)
			req := newSubmitRequest(credsUserID, "req-1", `{"values":{"apiKey":"x"}}`)
			rec := httptest.NewRecorder()

			// Act
			h.Submit(rec, req)

			// Assert
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}
