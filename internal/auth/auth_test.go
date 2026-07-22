package auth_test

import (
	"context"
	"testing"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
)

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "hunter2" || len(hash) == 0 {
		t.Fatal("hash not produced")
	}
	if !auth.CheckPassword(hash, "hunter2") {
		t.Fatal("CheckPassword returned false for correct password")
	}
	if auth.CheckPassword(hash, "wrong") {
		t.Fatal("CheckPassword returned true for wrong password")
	}
}

func TestCheckPasswordDummyAlwaysFalse(t *testing.T) {
	if auth.CheckPasswordDummy("anything") {
		t.Fatal("CheckPasswordDummy should always return false")
	}
	if auth.CheckPasswordDummy("") {
		t.Fatal("CheckPasswordDummy should always return false, even for empty input")
	}
}

func TestNewSessionIDIsUniqueHex(t *testing.T) {
	a, err := auth.NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	b, _ := auth.NewSessionID()
	if len(a) != 64 || a == b {
		t.Fatalf("bad session id: len=%d equal=%v", len(a), a == b)
	}
}

func TestUserContextRoundTrip(t *testing.T) {
	ctx := auth.ContextWithUser(context.Background(), &model.User{ID: 7, Username: "z"})
	got := auth.UserFromContext(ctx)
	if got == nil || got.ID != 7 {
		t.Fatalf("UserFromContext = %+v", got)
	}
	if auth.UserFromContext(context.Background()) != nil {
		t.Fatal("expected nil user for empty context")
	}
}
