package store_test

import (
	"context"
	"testing"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestPushSubscriptionUpsertAndList(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	u := createScheduledUser(t, ctx, users, "push-upsert", "push-upsert@example.com")

	repo := store.NewPushSubscriptionRepository(pool)
	sub := model.PushSubscription{UserID: u.ID, Endpoint: "https://push.example/abc", P256dh: "p", Auth: "a", UserAgent: "test-agent"}

	saved, err := repo.Upsert(ctx, sub)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated id")
	}

	// upsert same (user, endpoint) updates keys, does not duplicate
	sub.P256dh = "p2"
	if _, err := repo.Upsert(ctx, sub); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	list, err := repo.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(list))
	}
	if list[0].P256dh != "p2" {
		t.Fatalf("expected updated key, got %q", list[0].P256dh)
	}
}

func TestPushSubscriptionDeleteByEndpointScopedToUser(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	u := createScheduledUser(t, ctx, users, "push-delete", "push-delete@example.com")
	repo := store.NewPushSubscriptionRepository(pool)
	if _, err := repo.Upsert(ctx, model.PushSubscription{UserID: u.ID, Endpoint: "e", P256dh: "p", Auth: "a"}); err != nil {
		t.Fatal(err)
	}

	// wrong user cannot delete
	if err := repo.DeleteByEndpoint(ctx, u.ID+999, "e"); err != nil {
		t.Fatalf("delete wrong user: %v", err)
	}
	if list, _ := repo.ListByUser(ctx, u.ID); len(list) != 1 {
		t.Fatal("must not delete other user's sub")
	}

	if err := repo.DeleteByEndpoint(ctx, u.ID, "e"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list, _ := repo.ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatal("expected deleted")
	}
}

func TestPushSubscriptionDeleteByIDAndFailureLifecycle(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()
	users := store.NewUserRepository(pool)
	u := createScheduledUser(t, ctx, users, "push-lifecycle", "push-lifecycle@example.com")
	repo := store.NewPushSubscriptionRepository(pool)

	saved, err := repo.Upsert(ctx, model.PushSubscription{UserID: u.ID, Endpoint: "life", P256dh: "p", Auth: "a"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if saved.FailureCount != 0 {
		t.Fatalf("expected initial failure count 0, got %d", saved.FailureCount)
	}

	n, err := repo.IncrementFailure(ctx, saved.ID)
	if err != nil {
		t.Fatalf("increment failure: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected failure count 1, got %d", n)
	}

	if err := repo.MarkSuccess(ctx, saved.ID); err != nil {
		t.Fatalf("mark success: %v", err)
	}
	list, err := repo.ListByUser(ctx, u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list after success: %v %+v", err, list)
	}
	if list[0].FailureCount != 0 {
		t.Fatalf("expected failure count reset to 0, got %d", list[0].FailureCount)
	}
	if list[0].LastSuccessAt == nil {
		t.Fatal("expected LastSuccessAt to be set")
	}

	if err := repo.DeleteByID(ctx, saved.ID); err != nil {
		t.Fatalf("delete by id: %v", err)
	}
	if list, _ := repo.ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatal("expected subscription removed after DeleteByID")
	}
}
