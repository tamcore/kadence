package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
)

// PushSubStore persists browser Web Push subscriptions. Satisfied by
// *store.PushSubscriptionRepository.
type PushSubStore interface {
	Upsert(ctx context.Context, s model.PushSubscription) (model.PushSubscription, error)
	DeleteByEndpoint(ctx context.Context, userID int64, endpoint string) error
}

// Push serves the web-push config and subscription management endpoints.
type Push struct {
	vapidPublicKey string
	subs           PushSubStore
}

// NewPush constructs a Push handler. vapidPublicKey is the only VAPID value
// ever exposed to clients; the private key must never reach this type.
func NewPush(vapidPublicKey string, subs PushSubStore) *Push {
	return &Push{vapidPublicKey: vapidPublicKey, subs: subs}
}

// Config returns whether web push is enabled and the public VAPID key
// clients need to create a PushSubscription. Never returns the private key.
func (h *Push) Config(w http.ResponseWriter, _ *http.Request) {
	RespondJSON(w, http.StatusOK, map[string]any{
		"enabled":        true,
		"vapidPublicKey": h.vapidPublicKey,
	})
}

type subscribeKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type subscribeBody struct {
	Endpoint string        `json:"endpoint"`
	Keys     subscribeKeys `json:"keys"`
}

// Subscribe upserts a Web Push subscription for the authenticated session
// user, scoped to that user's ID.
func (h *Push) Subscribe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body subscribeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.Endpoint) == "" ||
		strings.TrimSpace(body.Keys.P256dh) == "" ||
		strings.TrimSpace(body.Keys.Auth) == "" {
		RespondError(w, http.StatusBadRequest, "endpoint and keys are required")
		return
	}

	_, err := h.subs.Upsert(r.Context(), model.PushSubscription{
		UserID:    u.ID,
		Endpoint:  body.Endpoint,
		P256dh:    body.Keys.P256dh,
		Auth:      body.Keys.Auth,
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not save subscription")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type unsubscribeBody struct {
	Endpoint string `json:"endpoint"`
}

// Unsubscribe removes a Web Push subscription owned by the authenticated
// session user, scoped to that user's ID so one user cannot delete another
// user's subscription.
func (h *Push) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body unsubscribeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Endpoint) == "" {
		RespondError(w, http.StatusBadRequest, "endpoint is required")
		return
	}

	if err := h.subs.DeleteByEndpoint(r.Context(), u.ID, body.Endpoint); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not remove subscription")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
