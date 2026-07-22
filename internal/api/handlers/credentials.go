package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/secret"
)

// credentialSubmitter records the real values a user submits for a pending
// credential request. Satisfied by *secret.Broker.
type credentialSubmitter interface {
	Submit(userID int64, requestID string, values map[string]string) error
}

// Credentials handles the secure credential-submission HTTP endpoint. It
// never logs the request body or submitted values.
type Credentials struct {
	broker credentialSubmitter
}

// NewCredentials constructs the Credentials handler.
func NewCredentials(b credentialSubmitter) *Credentials {
	return &Credentials{broker: b}
}

// credentialSubmitBody is the expected JSON request body for Submit.
type credentialSubmitBody struct {
	Values map[string]string `json:"values"`
}

// Submit handles POST /api/credentials/{requestId}, handing the caller's
// submitted credential values to the broker. The body and values are never
// logged anywhere in this handler.
func (c *Credentials) Submit(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())

	requestID := chi.URLParam(r, "requestId")
	if requestID == "" {
		RespondError(w, http.StatusBadRequest, "requestId is required")
		return
	}

	var body credentialSubmitBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := c.broker.Submit(u.ID, requestID, body.Values); err != nil {
		respondSubmitError(w, err)
		return
	}

	RespondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// respondSubmitError maps a secret.Broker.Submit error to an HTTP status
// using the package's sentinel errors.
func respondSubmitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secret.ErrNotOwner):
		RespondError(w, http.StatusForbidden, "not the owner of this request")
	case errors.Is(err, secret.ErrAlreadySubmitted):
		RespondError(w, http.StatusConflict, "request already submitted")
	case errors.Is(err, secret.ErrUnknownRequest), errors.Is(err, secret.ErrFieldMismatch):
		RespondError(w, http.StatusBadRequest, "invalid credential request")
	default:
		RespondError(w, http.StatusBadRequest, "could not submit credentials")
	}
}
