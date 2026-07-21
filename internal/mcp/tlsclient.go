package mcp

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

// HTTPClientWithCA builds an *http.Client whose TLS trust store is the
// system root CA pool augmented with the PEM-encoded CA certificate(s) found
// in caFile. It is used to verify TLS certificates presented by in-cluster
// MCP server sidecars (nginx) that are signed by a private, cert-manager
// managed CA rather than a public one.
//
// An empty caFile means "no custom CA is configured" and returns (nil, nil):
// callers should fall back to mcp-go's default HTTP client (plaintext http
// or system-trusted https), preserving today's behavior unchanged.
func HTTPClientWithCA(caFile string) (*http.Client, error) {
	if caFile == "" {
		return nil, nil
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	// #nosec G304 -- caFile is an operator-controlled path from KADENCE_MCP_CA_FILE (env config), not user input.
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("mcp: read CA file %s: %w", caFile, err)
	}

	if ok := pool.AppendCertsFromPEM(pemBytes); !ok {
		return nil, fmt.Errorf("mcp: no valid certificates found in CA file %s", caFile)
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}, nil
}
