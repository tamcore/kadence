package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
)

// confirmSubmitter records one user's answer to a pending confirmation.
// Satisfied by *confirm.Broker.
type confirmSubmitter interface {
	Submit(userID int64, id string, allowed bool) error
}

// Confirmations handles answers to mid-turn confirmation prompts.
type Confirmations struct {
	broker confirmSubmitter
}

// NewConfirmations constructs the Confirmations handler.
func NewConfirmations(b confirmSubmitter) *Confirmations {
	return &Confirmations{broker: b}
}

// confirmSubmitBody is the expected JSON request body for Submit.
type confirmSubmitBody struct {
	Confirm bool `json:"confirm"`
}

// Submit handles POST /api/confirmations/{id}.
//
// Every rejection the broker reports — unknown id, expired, already answered,
// another user's — is answered with the same bare 404. The distinctions are
// exactly what an attacker guessing ids would want, and none of them is useful
// to the legitimate caller, who either answered in time or did not.
func (c *Confirmations) Submit(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())

	id := chi.URLParam(r, "id")
	if id == "" {
		RespondError(w, http.StatusBadRequest, "id is required")
		return
	}

	var body confirmSubmitBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := c.broker.Submit(u.ID, id, body.Confirm); err != nil {
		RespondError(w, http.StatusNotFound, "unknown confirmation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
