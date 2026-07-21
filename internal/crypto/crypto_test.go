package crypto_test

import (
	"bytes"
	"testing"

	"github.com/tamcore/kadence/internal/crypto"
)

func key32() []byte { return bytes.Repeat([]byte{7}, 32) }

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c, err := crypto.NewCipher(key32())
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	blob, err := c.Encrypt("hunter2")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(blob, []byte("hunter2")) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := c.Decrypt(blob)
	if err != nil || got != "hunter2" {
		t.Fatalf("Decrypt = %q,%v want hunter2,nil", got, err)
	}
}

func TestDistinctNonces(t *testing.T) {
	c, _ := crypto.NewCipher(key32())
	a, _ := c.Encrypt("x")
	b, _ := c.Encrypt("x")
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of same plaintext are identical (nonce reuse)")
	}
}

func TestWrongKeyAndTamperFail(t *testing.T) {
	c, _ := crypto.NewCipher(key32())
	blob, _ := c.Encrypt("secret")
	other, _ := crypto.NewCipher(bytes.Repeat([]byte{9}, 32))
	if _, err := other.Decrypt(blob); err == nil {
		t.Fatal("decrypt with wrong key succeeded")
	}
	blob[len(blob)-1] ^= 0xFF
	if _, err := c.Decrypt(blob); err == nil {
		t.Fatal("decrypt of tampered blob succeeded")
	}
}

func TestNewCipherRejectsBadKey(t *testing.T) {
	if _, err := crypto.NewCipher(make([]byte, 16)); err == nil {
		t.Fatal("NewCipher accepted a 16-byte key")
	}
}

func TestDecryptFailsOnTooShortBlob(t *testing.T) {
	c, err := crypto.NewCipher(key32())
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if _, err := c.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Fatal("decrypt of too-short blob (3 bytes < 12-byte nonce) succeeded")
	}
}
