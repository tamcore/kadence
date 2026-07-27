package push

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tamcore/kadence/internal/model"
)

const (
	defaultPollInterval = 5 * time.Second
	defaultBatchLimit   = 50
	defaultSnippetLen   = 120
)

// DeliveryClaimer atomically claims scheduled-run deliveries that are ready
// for a push notification. Satisfied in production by
// *store.PushSubscriptionRepository.
type DeliveryClaimer interface {
	ClaimUndispatchedDeliveries(ctx context.Context, limit int) ([]model.PendingPushDelivery, error)
}

// Sender delivers a push payload to a user's registered subscriptions.
// Satisfied in production by *Service.
type Sender interface {
	SendToUser(ctx context.Context, userID int64, p Payload) error
}

// DispatcherConfig controls the Dispatcher's polling cadence and batch/body
// size. Zero values are filled with sane defaults by NewDispatcher.
type DispatcherConfig struct {
	PollInterval time.Duration
	BatchLimit   int
	SnippetLen   int
}

// Dispatcher polls for scheduled-run deliveries awaiting a push notification
// and sends them via a Sender.
type Dispatcher struct {
	claimer DeliveryClaimer
	sender  Sender
	cfg     DispatcherConfig
	log     *slog.Logger
}

// NewDispatcher constructs a Dispatcher, filling any unset DispatcherConfig
// fields with defaults.
func NewDispatcher(claimer DeliveryClaimer, sender Sender, cfg DispatcherConfig, log *slog.Logger) *Dispatcher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.BatchLimit <= 0 {
		cfg.BatchLimit = defaultBatchLimit
	}
	if cfg.SnippetLen <= 0 {
		cfg.SnippetLen = defaultSnippetLen
	}
	return &Dispatcher{claimer: claimer, sender: sender, cfg: cfg, log: log}
}

// Run polls on cfg.PollInterval, dispatching claimed deliveries, until ctx is
// done.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := d.dispatchOnce(ctx); err != nil {
				d.log.Warn("push dispatch batch failed", "err", err)
			}
		}
	}
}

// dispatchOnce claims a single batch of deliveries and sends a push
// notification for each. It returns the number of deliveries claimed;
// per-user send failures are logged and do not abort the batch.
func (d *Dispatcher) dispatchOnce(ctx context.Context) (int, error) {
	items, err := d.claimer.ClaimUndispatchedDeliveries(ctx, d.cfg.BatchLimit)
	if err != nil {
		return 0, fmt.Errorf("claim deliveries: %w", err)
	}
	for _, it := range items {
		p := Payload{
			Title: "Scheduled digest: " + it.TaskTitle,
			Body:  BuildSnippet(it.Result, d.cfg.SnippetLen),
			URL:   deliveryURL(it.ConversationID, it.MessageID),
			Tag:   "scheduled-" + it.TaskID,
		}
		if err := d.sender.SendToUser(ctx, it.UserID, p); err != nil {
			// Best-effort: log and continue. The run is already stamped
			// dispatched; the in-app unread badge remains the reliable
			// surface even if the push send itself fails.
			d.log.Warn("push send to user failed", "user", it.UserID, "run", it.RunID, "err", err)
		}
	}
	return len(items), nil
}

// deliveryURL builds the in-app chat deep link for a delivery, anchoring to
// the specific message when one is known.
func deliveryURL(conversationID string, messageID *int64) string {
	if messageID == nil {
		return "/chat/" + conversationID
	}
	return fmt.Sprintf("/chat/%s#msg=%d", conversationID, *messageID)
}
