package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/tamcore/kadence/internal/model"
)

// maxPushFailures is the number of consecutive send failures a subscription
// may accrue before it is pruned as unreachable.
const maxPushFailures = 5

// pushTTLSeconds is the TTL (in seconds) the push service is asked to hold a
// notification for if the user's device is offline. 24h so a locked phone or a
// briefly-disconnected browser still receives the digest when it next reconnects
// — a short TTL silently drops notifications for any device not reachable at the
// instant of send.
const pushTTLSeconds = 86400

// SubscriptionStore persists and queries browser Web Push subscriptions.
// Satisfied in production by *store.PushSubscriptionRepository.
type SubscriptionStore interface {
	ListByUser(ctx context.Context, userID int64) ([]model.PushSubscription, error)
	DeleteByID(ctx context.Context, id string) error
	IncrementFailure(ctx context.Context, id string) (int, error)
	MarkSuccess(ctx context.Context, id string) error
}

// Service sends Web Push notifications to a user's registered subscriptions,
// pruning endpoints that report they are dead (404/410) or that have failed
// too many times in a row.
type Service struct {
	store        SubscriptionStore
	vapidPublic  string
	vapidPrivate string
	vapidSubject string
	log          *slog.Logger
}

// NewService constructs a Service backed by store, using the given VAPID
// keypair/subject to authenticate outgoing push requests.
func NewService(store SubscriptionStore, vapidPublic, vapidPrivate, vapidSubject string, log *slog.Logger) *Service {
	return &Service{
		store:        store,
		vapidPublic:  vapidPublic,
		vapidPrivate: vapidPrivate,
		vapidSubject: normalizeVAPIDSubject(vapidSubject),
		log:          log,
	}
}

// normalizeVAPIDSubject strips a leading "mailto:" from a bare-email subject.
// The webpush library re-adds "mailto:" to any non-"https:" subject, so a
// configured "mailto:admin@example.com" would otherwise become the malformed
// "mailto:mailto:admin@example.com" — which Apple rejects with BadJwtToken
// (Mozilla tolerates it). An "https:" subject is left untouched.
func normalizeVAPIDSubject(subject string) string {
	if strings.HasPrefix(subject, "https:") {
		return subject
	}
	return strings.TrimPrefix(subject, "mailto:")
}

// SendToUser sends p to every subscription the user has registered. Dead
// endpoints (404/410) are pruned immediately; other failures increment a
// per-subscription failure counter and prune once it reaches maxPushFailures.
// Returns an error only if the user had subscriptions and every send failed.
func (s *Service) SendToUser(ctx context.Context, userID int64, p Payload) error {
	subs, err := s.store.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	msg, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	var sent, failed int
	for _, sub := range subs {
		if s.sendOne(ctx, sub, msg) {
			sent++
		} else {
			failed++
		}
	}
	if sent == 0 && failed > 0 {
		return errors.New("all push sends failed")
	}
	return nil
}

// sendOne sends msg to a single subscription and reports whether it succeeded.
func (s *Service) sendOne(ctx context.Context, sub model.PushSubscription, msg []byte) bool {
	resp, err := webpush.SendNotificationWithContext(ctx, msg, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		Subscriber:      s.vapidSubject,
		VAPIDPublicKey:  s.vapidPublic,
		VAPIDPrivateKey: s.vapidPrivate,
		TTL:             pushTTLSeconds,
	})
	if err != nil {
		s.afterFailure(ctx, sub.ID)
		s.log.Warn("push send error", "subscription", sub.ID, "err", err)
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound:
		if derr := s.store.DeleteByID(ctx, sub.ID); derr != nil {
			s.log.Warn("prune dead subscription failed", "subscription", sub.ID, "err", derr)
		}
		return false
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		if merr := s.store.MarkSuccess(ctx, sub.ID); merr != nil {
			s.log.Warn("mark push success failed", "subscription", sub.ID, "err", merr)
		}
		return true
	default:
		s.afterFailure(ctx, sub.ID)
		s.log.Warn("push send non-2xx",
			"subscription", sub.ID,
			"status", resp.StatusCode,
			"endpoint_host", endpointHost(sub.Endpoint),
			"body", responseSnippet(resp.Body))
		return false
	}
}

// maxResponseSnippet bounds how much of a push service's error response body
// is read for diagnostic logging.
const maxResponseSnippet = 512

// responseSnippet returns up to maxResponseSnippet bytes of a push service's
// response body (the gateway's error reason, e.g. Apple's JSON) for logging.
func responseSnippet(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, maxResponseSnippet))
	if err != nil {
		return ""
	}
	return string(b)
}

// endpointHost returns just the host of a push endpoint (e.g.
// web.push.apple.com) so logs identify the gateway without the token path.
func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Host
}

// afterFailure increments the subscription's failure counter and prunes it
// once the count reaches maxPushFailures.
func (s *Service) afterFailure(ctx context.Context, id string) {
	n, err := s.store.IncrementFailure(ctx, id)
	if err != nil {
		s.log.Warn("increment push failure count failed", "subscription", id, "err", err)
		return
	}
	if n >= maxPushFailures {
		if derr := s.store.DeleteByID(ctx, id); derr != nil {
			s.log.Warn("prune subscription after failure cap failed", "subscription", id, "err", derr)
		}
	}
}
