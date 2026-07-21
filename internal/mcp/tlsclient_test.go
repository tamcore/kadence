package mcp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTempCAPEM generates a self-signed CA certificate and writes its PEM
// encoding to a temp file, returning the file path and the parsed
// certificate (for RootCAs membership assertions).
func writeTempCAPEM(t *testing.T) (string, *x509.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kadence-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "ca.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}

	return path, cert
}

func TestHTTPClientWithCA_EmptyPathReturnsNilNil(t *testing.T) {
	client, err := HTTPClientWithCA("")
	if err != nil {
		t.Fatalf("want nil error for empty caFile, got: %v", err)
	}
	if client != nil {
		t.Fatalf("want nil client for empty caFile, got: %+v", client)
	}
}

func TestHTTPClientWithCA_ValidCAReturnsClientWithPool(t *testing.T) {
	path, cert := writeTempCAPEM(t)

	client, err := HTTPClientWithCA(path)
	if err != nil {
		t.Fatalf("HTTPClientWithCA: %v", err)
	}
	if client == nil {
		t.Fatal("want non-nil client")
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("want *http.Transport, got %T", client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("want non-nil TLSClientConfig")
	}
	pool := transport.TLSClientConfig.RootCAs
	if pool == nil {
		t.Fatal("want non-nil RootCAs pool")
	}

	// Verify the generated CA is trusted by the pool: verifying the
	// self-signed CA cert against itself as a root should succeed only if
	// the pool actually contains it.
	opts := x509.VerifyOptions{Roots: pool}
	if _, err := cert.Verify(opts); err != nil {
		t.Fatalf("want CA cert verifiable against pool (i.e. pool contains it), got: %v", err)
	}
}

func TestHTTPClientWithCA_NonexistentPathErrors(t *testing.T) {
	if _, err := HTTPClientWithCA("/nonexistent/path/ca.crt"); err == nil {
		t.Fatal("want error for nonexistent caFile path")
	}
}

func TestHTTPClientWithCA_GarbagePEMErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.crt")
	if err := os.WriteFile(path, []byte("not a valid PEM"), 0o600); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}

	if _, err := HTTPClientWithCA(path); err == nil {
		t.Fatal("want error for garbage PEM content")
	}
}
