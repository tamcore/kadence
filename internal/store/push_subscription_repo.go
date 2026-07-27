package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/model"
)

// PushSubscriptionRepository persists browser Web Push subscriptions.
type PushSubscriptionRepository struct{ pool *pgxpool.Pool }

// NewPushSubscriptionRepository constructs a PushSubscriptionRepository backed by pool.
func NewPushSubscriptionRepository(pool *pgxpool.Pool) *PushSubscriptionRepository {
	return &PushSubscriptionRepository{pool: pool}
}

const pushSubCols = `id::text, user_id, endpoint, p256dh, auth, user_agent, created_at, last_success_at, failure_count`

func scanPushSub(row pgx.Row) (model.PushSubscription, error) {
	var s model.PushSubscription
	err := row.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &s.CreatedAt, &s.LastSuccessAt, &s.FailureCount)
	return s, err
}

// Upsert inserts a new subscription or, on (user_id, endpoint) conflict,
// updates the stored keys and resets the failure count.
func (r *PushSubscriptionRepository) Upsert(ctx context.Context, s model.PushSubscription) (model.PushSubscription, error) {
	out, err := scanPushSub(r.pool.QueryRow(ctx,
		`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (user_id, endpoint)
		 DO UPDATE SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, user_agent = EXCLUDED.user_agent, failure_count = 0
		 RETURNING `+pushSubCols,
		s.UserID, s.Endpoint, s.P256dh, s.Auth, s.UserAgent))
	if err != nil {
		return model.PushSubscription{}, fmt.Errorf("upsert push subscription: %w", err)
	}
	return out, nil
}

// DeleteByEndpoint removes the subscription for the given user and endpoint,
// scoped to userID so one user cannot delete another's subscription.
func (r *PushSubscriptionRepository) DeleteByEndpoint(ctx context.Context, userID int64, endpoint string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`, userID, endpoint); err != nil {
		return fmt.Errorf("delete push subscription: %w", err)
	}
	return nil
}

// DeleteByID removes the subscription with the given id.
func (r *PushSubscriptionRepository) DeleteByID(ctx context.Context, id string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("delete push subscription by id: %w", err)
	}
	return nil
}

// ListByUser returns all subscriptions owned by userID, oldest first.
func (r *PushSubscriptionRepository) ListByUser(ctx context.Context, userID int64) ([]model.PushSubscription, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+pushSubCols+` FROM push_subscriptions WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	defer rows.Close()
	var out []model.PushSubscription
	for rows.Next() {
		s, err := scanPushSub(rows)
		if err != nil {
			return nil, fmt.Errorf("scan push subscription: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// IncrementFailure bumps the failure count for id and returns the new value.
func (r *PushSubscriptionRepository) IncrementFailure(ctx context.Context, id string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `UPDATE push_subscriptions SET failure_count = failure_count + 1 WHERE id = $1::uuid RETURNING failure_count`, id).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("increment push failure: %w", err)
	}
	return n, nil
}

// MarkSuccess resets the failure count to zero and stamps last_success_at.
func (r *PushSubscriptionRepository) MarkSuccess(ctx context.Context, id string) error {
	if _, err := r.pool.Exec(ctx, `UPDATE push_subscriptions SET failure_count = 0, last_success_at = NOW() WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("mark push success: %w", err)
	}
	return nil
}

// ClaimUndispatchedDeliveries atomically stamps push_dispatched_at = NOW() on
// up to limit delivered-but-undispatched scheduled runs and returns them.
// FOR UPDATE SKIP LOCKED guarantees concurrent callers never claim the same run.
func (r *PushSubscriptionRepository) ClaimUndispatchedDeliveries(ctx context.Context, limit int) ([]model.PendingPushDelivery, error) {
	rows, err := r.pool.Query(ctx,
		`UPDATE scheduled_task_runs r
		    SET push_dispatched_at = NOW()
		   FROM scheduled_tasks t
		  WHERE r.task_id = t.id
		    AND r.id IN (
		        SELECT id FROM scheduled_task_runs
		         WHERE state = 'delivered' AND push_dispatched_at IS NULL
		         ORDER BY id
		         FOR UPDATE SKIP LOCKED
		         LIMIT $1)
		  RETURNING r.id, t.user_id, t.id::text, COALESCE(t.name, ''),
		            COALESCE(t.delivery_conversation_id, t.conversation_id)::text,
		            r.delivery_message_id, COALESCE(r.result, '')`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim undispatched deliveries: %w", err)
	}
	defer rows.Close()
	var out []model.PendingPushDelivery
	for rows.Next() {
		var d model.PendingPushDelivery
		if err := rows.Scan(&d.RunID, &d.UserID, &d.TaskID, &d.TaskTitle, &d.ConversationID, &d.MessageID, &d.Result); err != nil {
			return nil, fmt.Errorf("scan pending push delivery: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
