package secret_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/secret"
)

const testUserA int64 = 1
const testUserB int64 = 2
const fieldAPIKey = "apiKey"

func testFields(names ...string) []secret.Field {
	fields := make([]secret.Field, 0, len(names))
	for _, n := range names {
		fields = append(fields, secret.Field{Name: n, Label: n, Secret: true})
	}
	return fields
}

func TestNewRequestTokenFormatAndUniqueness(t *testing.T) {
	// Arrange
	b := secret.NewBroker()

	// Act
	_, tokens, err := b.NewRequest(testUserA, testFields(fieldAPIKey, "apiSecret"))

	// Assert
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	seen := make(map[string]bool)
	for name, tok := range tokens {
		if !strings.HasPrefix(tok, "kadence_secret_") {
			t.Fatalf("token for %s missing prefix: %s", name, tok)
		}
		if len(tok) <= len("kadence_secret_") {
			t.Fatalf("token for %s has no random suffix", name)
		}
		if seen[tok] {
			t.Fatalf("duplicate token generated: %s", tok)
		}
		seen[tok] = true
	}
}

func TestNewRequestTwoTokensDiffer(t *testing.T) {
	// Arrange
	b := secret.NewBroker()

	// Act
	_, tokens1, err1 := b.NewRequest(testUserA, testFields(fieldAPIKey))
	_, tokens2, err2 := b.NewRequest(testUserA, testFields(fieldAPIKey))

	// Assert
	if err1 != nil || err2 != nil {
		t.Fatalf("NewRequest errors: %v %v", err1, err2)
	}
	if tokens1[fieldAPIKey] == tokens2[fieldAPIKey] {
		t.Fatal("expected different tokens across requests")
	}
}

func TestNewRequestValidatesFieldCount(t *testing.T) {
	// Arrange
	b := secret.NewBroker()

	// Act & Assert: zero fields
	if _, _, err := b.NewRequest(testUserA, nil); err == nil {
		t.Fatal("expected error for zero fields")
	}

	// Act & Assert: nine fields (max is 8)
	nine := testFields("f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9")
	if _, _, err := b.NewRequest(testUserA, nine); err == nil {
		t.Fatal("expected error for 9 fields")
	}
}

func TestNewRequestValidatesDuplicateNames(t *testing.T) {
	// Arrange
	b := secret.NewBroker()

	// Act
	_, _, err := b.NewRequest(testUserA, testFields(fieldAPIKey, fieldAPIKey))

	// Assert
	if err == nil {
		t.Fatal("expected error for duplicate field names")
	}
}

func TestNewRequestValidatesLongName(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	longName := strings.Repeat("a", 65)

	// Act
	_, _, err := b.NewRequest(testUserA, testFields(longName))

	// Assert
	if err == nil {
		t.Fatal("expected error for field name > 64 chars")
	}
}

func TestNewRequestValidatesEmptyName(t *testing.T) {
	// Arrange
	b := secret.NewBroker()

	// Act
	_, _, err := b.NewRequest(testUserA, testFields(""))

	// Assert
	if err == nil {
		t.Fatal("expected error for empty field name")
	}
}

func TestSubmitThenAwaitReturnsNil(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	reqID, tokens, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var awaitErr error
	go func() {
		defer wg.Done()
		awaitErr = b.Await(context.Background(), reqID)
	}()

	// Act
	time.Sleep(10 * time.Millisecond)
	err = b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "secretvalue"})
	wg.Wait()

	// Assert
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if awaitErr != nil {
		t.Fatalf("Await: expected nil, got %v", awaitErr)
	}
	_ = tokens
}

func TestAwaitContextCancel(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	reqID, _, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Act
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	awaitErr := b.Await(ctx, reqID)

	// Assert
	if !errors.Is(awaitErr, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", awaitErr)
	}
}

func TestAwaitTimeoutPastTTL(t *testing.T) {
	// Arrange: inject a clock we control
	base := time.Now()
	current := base
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	b := secret.NewBrokerWithClock(clock)
	reqID, _, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	// Act: advance clock past TTL, then Await with a short real-time budget
	mu.Lock()
	current = base.Add(200 * time.Second)
	mu.Unlock()

	awaitErr := b.Await(context.Background(), reqID)

	// Assert
	if !errors.Is(awaitErr, secret.ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", awaitErr)
	}
}

func TestSubmitUnknownRequest(t *testing.T) {
	// Arrange
	b := secret.NewBroker()

	// Act
	err := b.Submit(testUserA, "kadence_secret_doesnotexist", map[string]string{fieldAPIKey: "v"})

	// Assert
	if !errors.Is(err, secret.ErrUnknownRequest) {
		t.Fatalf("expected ErrUnknownRequest, got %v", err)
	}
}

func TestSubmitWrongOwner(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	reqID, _, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	// Act
	err = b.Submit(testUserB, reqID, map[string]string{fieldAPIKey: "v"})

	// Assert
	if !errors.Is(err, secret.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner, got %v", err)
	}
}

func TestSubmitAlreadySubmitted(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	reqID, _, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "v1"}); err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	// Act
	err = b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "v2"})

	// Assert
	if !errors.Is(err, secret.ErrAlreadySubmitted) {
		t.Fatalf("expected ErrAlreadySubmitted, got %v", err)
	}
}

func TestSubmitMismatchedKeys(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	reqID, _, err := b.NewRequest(testUserA, testFields(fieldAPIKey, "apiSecret"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	// Act: missing apiSecret, has extra bogus field
	err = b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "v1", "bogus": "v2"})

	// Assert
	if !errors.Is(err, secret.ErrFieldMismatch) {
		t.Fatalf("expected ErrFieldMismatch, got %v", err)
	}
}

func TestSubstituteOnceThenConsumed(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	reqID, tokens, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "s3cr3t"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	tok := tokens[fieldAPIKey]
	argsJSON := `{"key":"` + tok + `"}`

	// Act: first substitution
	realJSON, used := b.Substitute(testUserA, argsJSON)

	// Assert: value substituted in, token reported used
	if !strings.Contains(realJSON, "s3cr3t") {
		t.Fatalf("expected substituted value in JSON, got %s", realJSON)
	}
	if strings.Contains(realJSON, tok) {
		t.Fatalf("expected token to be replaced, still present: %s", realJSON)
	}
	if len(used) != 1 || used[0] != tok {
		t.Fatalf("expected used=[%s], got %v", tok, used)
	}

	// Act: second substitution attempt (token consumed)
	realJSON2, used2 := b.Substitute(testUserA, argsJSON)

	// Assert: token remains literally in output (not substituted again), not reported used
	if !strings.Contains(realJSON2, tok) {
		t.Fatalf("expected consumed token to remain unsubstituted, got %s", realJSON2)
	}
	if len(used2) != 0 {
		t.Fatalf("expected no tokens used on second call, got %v", used2)
	}
}

func TestSubstituteIsUserScoped(t *testing.T) {
	// Arrange: user A's token must not be substituted when called for user B, and vice versa
	b := secret.NewBroker()
	reqID, tokens, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "a-secret"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	tokA := tokens[fieldAPIKey]
	argsJSON := `{"key":"` + tokA + `"}`

	// Act: attempt substitution scoped to user B
	realJSON, used := b.Substitute(testUserB, argsJSON)

	// Assert: user A's token is untouched, not reported used, and not consumed
	if strings.Contains(realJSON, "a-secret") {
		t.Fatal("user B's Substitute must not resolve user A's token")
	}
	if !strings.Contains(realJSON, tokA) {
		t.Fatal("token should remain unsubstituted for wrong user")
	}
	if len(used) != 0 {
		t.Fatalf("expected no tokens used for wrong-user Substitute, got %v", used)
	}

	// Act: now call for correct user A, should still work (not consumed by the failed attempt)
	realJSON2, used2 := b.Substitute(testUserA, argsJSON)

	// Assert
	if !strings.Contains(realJSON2, "a-secret") {
		t.Fatalf("expected value substituted for correct user, got %s", realJSON2)
	}
	if len(used2) != 1 {
		t.Fatalf("expected token used for correct user, got %v", used2)
	}
}

func TestActiveValuesLongestFirst(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	reqID, _, err := b.NewRequest(testUserA, testFields("short", "longer", "longest"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	err = b.Submit(testUserA, reqID, map[string]string{
		"short":   "ab",
		"longer":  "abcdef",
		"longest": "abcdefghij",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Act
	values := b.ActiveValues(testUserA)

	// Assert
	if len(values) != 3 {
		t.Fatalf("expected 3 active values, got %d", len(values))
	}
	for i := 0; i < len(values)-1; i++ {
		if len(values[i]) < len(values[i+1]) {
			t.Fatalf("expected longest-first ordering, got %v", values)
		}
	}
	if values[0] != "abcdefghij" {
		t.Fatalf("expected longest value first, got %s", values[0])
	}
}

func TestActiveValuesExcludesExpired(t *testing.T) {
	// Arrange
	base := time.Now()
	current := base
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	b := secret.NewBrokerWithClock(clock)
	reqID, _, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "v"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Act: advance clock past the 120s TTL
	mu.Lock()
	current = base.Add(200 * time.Second)
	mu.Unlock()
	values := b.ActiveValues(testUserA)

	// Assert
	if len(values) != 0 {
		t.Fatalf("expected expired values excluded, got %v", values)
	}
}

func TestRedactAllOccurrencesIncludingOverlap(t *testing.T) {
	// Arrange
	s := "secret=abc123 and again abc123 done"

	// Act
	redacted := secret.Redact(s, []string{"abc123"})

	// Assert
	if strings.Contains(redacted, "abc123") {
		t.Fatalf("expected all occurrences redacted, got %s", redacted)
	}
	if strings.Count(redacted, "[redacted]") != 2 {
		t.Fatalf("expected 2 redactions, got %s", redacted)
	}
}

func TestRedactOverlappingValuesLongestFirst(t *testing.T) {
	// Arrange: "ab" is a substring of "abcdef" -- longest-first ordering must
	// ensure the longer value is redacted whole, not fragmented by the shorter one.
	s := "value is abcdef here"

	// Act
	redacted := secret.Redact(s, []string{"abcdef", "ab"})

	// Assert
	if strings.Contains(redacted, "abcdef") {
		t.Fatalf("expected full value redacted, got %s", redacted)
	}
	if !strings.Contains(redacted, "[redacted]") {
		t.Fatalf("expected redaction marker present, got %s", redacted)
	}
}

func TestRedactEmptyValueNoop(t *testing.T) {
	// Arrange
	s := "nothing to see here"

	// Act
	redacted := secret.Redact(s, []string{""})

	// Assert
	if redacted != s {
		t.Fatalf("expected unchanged string for empty value, got %s", redacted)
	}
}

func TestRedactNoActiveValuesUnchanged(t *testing.T) {
	// Arrange
	s := "nothing to redact here"

	// Act
	redacted := secret.Redact(s, nil)

	// Assert
	if redacted != s {
		t.Fatalf("expected unchanged string, got %s", redacted)
	}
}

func TestPurgeUserClearsRequestsAndValues(t *testing.T) {
	// Arrange
	b := secret.NewBroker()
	reqID, _, err := b.NewRequest(testUserA, testFields(fieldAPIKey))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "v"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Act
	b.PurgeUser(testUserA)

	// Assert: active values gone
	if values := b.ActiveValues(testUserA); len(values) != 0 {
		t.Fatalf("expected no active values after purge, got %v", values)
	}
	// Assert: request itself no longer known (Submit again -> unknown, not already-submitted)
	err = b.Submit(testUserA, reqID, map[string]string{fieldAPIKey: "v2"})
	if !errors.Is(err, secret.ErrUnknownRequest) {
		t.Fatalf("expected ErrUnknownRequest after purge, got %v", err)
	}
}
