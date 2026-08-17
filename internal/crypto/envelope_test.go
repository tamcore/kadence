package crypto

import (
	"bytes"
	"testing"
)

const (
	testServer      = "garmin"
	testOtherServer = "strava"
)

func envelopeCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestSealWithContextRoundTrips(t *testing.T) {
	c := envelopeCipher(t)
	bind := Context{UserID: 7, ServerID: testServer, Record: RecordRefreshToken}

	blob, err := c.SealWithContext("refresh-token-value", bind)
	if err != nil {
		t.Fatalf("SealWithContext: %v", err)
	}

	got, err := c.OpenWithContext(blob, bind)
	if err != nil {
		t.Fatalf("OpenWithContext: %v", err)
	}
	if got != "refresh-token-value" {
		t.Fatalf("got %q, want %q", got, "refresh-token-value")
	}
}

func TestOpenWithContextRejectsMovedCiphertext(t *testing.T) {
	c := envelopeCipher(t)
	blob, err := c.SealWithContext("refresh-token-value",
		Context{UserID: 7, ServerID: testServer, Record: RecordRefreshToken})
	if err != nil {
		t.Fatalf("SealWithContext: %v", err)
	}

	for name, bind := range map[string]Context{
		"other user":   {UserID: 8, ServerID: testServer, Record: RecordRefreshToken},
		"other server": {UserID: 7, ServerID: testOtherServer, Record: RecordRefreshToken},
		"other record": {UserID: 7, ServerID: testServer, Record: RecordAccessToken},
	} {
		if _, err := c.OpenWithContext(blob, bind); err == nil {
			t.Fatalf("%s: OpenWithContext succeeded, want failure", name)
		}
	}
}

func TestOpenWithContextRejectsForeignFormats(t *testing.T) {
	c := envelopeCipher(t)
	bind := Context{UserID: 7, ServerID: testServer, Record: RecordAccessToken}

	legacy, err := c.Encrypt("legacy-value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c.OpenWithContext(legacy, bind); err == nil {
		t.Fatal("OpenWithContext accepted a legacy blob, want failure")
	}

	sealed, err := c.SealWithContext("new-value", bind)
	if err != nil {
		t.Fatalf("SealWithContext: %v", err)
	}
	if _, err := c.Decrypt(sealed); err == nil {
		t.Fatal("Decrypt accepted a context-bound blob, want failure")
	}

	if _, err := c.OpenWithContext([]byte{envelopeVersion}, bind); err == nil {
		t.Fatal("OpenWithContext accepted a truncated blob, want failure")
	}
}

func TestSealWithContextRejectsIncompleteContext(t *testing.T) {
	c := envelopeCipher(t)
	for name, bind := range map[string]Context{
		"no user":   {ServerID: testServer, Record: RecordAccessToken},
		"no server": {UserID: 7, Record: RecordAccessToken},
		"no record": {UserID: 7, ServerID: testServer},
	} {
		if _, err := c.SealWithContext("v", bind); err == nil {
			t.Fatalf("%s: SealWithContext succeeded, want failure", name)
		}
	}
}
