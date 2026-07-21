package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/tamcore/kadence/internal/crypto"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

const (
	testServerName   = "mine"
	testServerPass   = "sekret"
	testServerURL    = "https://a.example.io/mcp"
	testTransportSSE = "sse"
	testBobUsername  = "bob"
)

func TestUserServerRepo_CRUDAndEncryption(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	testutil.CleanTables(t, pool)
	ctx := context.Background()

	users := store.NewUserRepository(pool)
	alice, err := users.Create(ctx, model.User{Username: testAliceUsername, Email: testEmailA, PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := users.Create(ctx, model.User{Username: testBobUsername, Email: "bob@x.io", PasswordHash: "h", Role: model.RoleUser})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	cipher, err := crypto.NewCipher(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	repo := store.NewUserServerRepo(pool, cipher)

	id, err := repo.Create(ctx, alice.ID, store.UserMCPInput{
		Name: testServerName, URL: testServerURL, Transport: "streamable-http",
		AuthUser: "u", AuthPass: testServerPass,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// stored password is ciphertext, not plaintext
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT auth_pass_enc FROM user_mcp_servers WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatalf("select auth_pass_enc: %v", err)
	}
	if bytes.Contains(raw, []byte(testServerPass)) {
		t.Fatal("stored auth_pass_enc contains plaintext")
	}

	recs, err := repo.ListForOwner(ctx, alice.ID)
	if err != nil || len(recs) != 1 || recs[0].Name != testServerName {
		t.Fatalf("ListForOwner=%#v err=%v", recs, err)
	}

	servers, err := repo.ServersForUser(ctx, testAliceUsername)
	if err != nil || len(servers) != 1 || servers[0].AuthPass != testServerPass || servers[0].Scope != "USER_"+testAliceUsername {
		t.Fatalf("ServersForUser=%#v err=%v", servers, err)
	}

	if _, err := repo.Create(ctx, alice.ID, store.UserMCPInput{Name: testServerName, URL: testServerURL, Transport: testTransportSSE, AuthPass: "x"}); !errors.Is(err, store.ErrDuplicateName) {
		t.Fatalf("duplicate create err=%v want ErrDuplicateName", err)
	}

	if err := repo.Delete(ctx, bob.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-owner delete err=%v want ErrNotFound", err)
	}

	// update with empty AuthPass keeps existing password
	if err := repo.Update(ctx, alice.ID, id, store.UserMCPInput{Name: testServerName, URL: "https://a.example.io/v2", Transport: testTransportSSE, AuthUser: "u2", AuthPass: ""}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	servers, err = repo.ServersForUser(ctx, testAliceUsername)
	if err != nil || len(servers) != 1 || servers[0].AuthPass != testServerPass || servers[0].URL != "https://a.example.io/v2" {
		t.Fatalf("after keep-password update: %#v err=%v", servers, err)
	}

	if _, err := repo.Create(ctx, bob.ID, store.UserMCPInput{Name: "bobsrv", URL: "https://x.foo.io/mcp", Transport: testTransportSSE, AuthPass: "p"}); err != nil {
		t.Fatalf("create bob server: %v", err)
	}
	all, err := repo.AllServers(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("AllServers=%d err=%v want 2", len(all), err)
	}
}
