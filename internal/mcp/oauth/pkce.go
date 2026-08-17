// Package oauth speaks the OAuth 2.1 wire protocol an MCP server publishes:
// discovery, PKCE, the authorization URL, the token endpoint, and revocation.
// It holds no state and knows nothing about Kadence's storage or HTTP layer.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// verifierLen is the PKCE verifier length. RFC 7636 allows 43 to 128
// characters; 64 is comfortably inside that and still short enough for a URL.
const verifierLen = 64

// stateLen is how many random bytes a state value carries.
const stateLen = 32

// verifierAlphabet is the unreserved set RFC 7636 permits in a verifier.
const verifierAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

// PKCE is one authorization's proof key: the verifier stays with the server
// that started the flow, and only its challenge travels through the browser.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE mints a verifier and its S256 challenge.
func NewPKCE() (PKCE, error) {
	buf := make([]byte, verifierLen)
	if _, err := rand.Read(buf); err != nil {
		return PKCE{}, fmt.Errorf("oauth: read random: %w", err)
	}
	out := make([]byte, verifierLen)
	for i, b := range buf {
		out[i] = verifierAlphabet[int(b)%len(verifierAlphabet)]
	}
	verifier := string(out)

	sum := sha256.Sum256([]byte(verifier))
	return PKCE{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// NewState mints an authorization state value.
func NewState() (string, error) {
	buf := make([]byte, stateLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth: read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
