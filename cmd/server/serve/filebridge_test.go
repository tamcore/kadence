package serve

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const bridgeTestMaxBytes int64 = 1024

func TestFileBridgeDefaultAddressIsPodReachable(t *testing.T) {
	if defaultFileBridgeAddr != ":8081" {
		t.Errorf("defaultFileBridgeAddr = %q, want :8081", defaultFileBridgeAddr)
	}
}

func TestFileBridgeHealthIsUnauthenticated(t *testing.T) {
	h := testFileBridgeHandler(t, t.TempDir(), bridgeTestMaxBytes)

	res := serveBridgeRequest(h, "/healthz", false)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
}

func TestFileBridgeRequiresBasicAuth(t *testing.T) {
	root := t.TempDir()
	path := writeBridgeFile(t, root, "activity.fit", []byte("FIT"))
	h := testFileBridgeHandler(t, root, bridgeTestMaxBytes)

	res := serveBridgeRequest(h, "/files/activity.fit", false)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unauthorized request removed file: %v", err)
	}
}

func TestFileBridgeFetchesAndDeletesFITAfterSuccessfulRead(t *testing.T) {
	root := t.TempDir()
	want := []byte("FIT bytes")
	path := writeBridgeFile(t, root, "activity.fit", want)
	h := testFileBridgeHandler(t, root, bridgeTestMaxBytes)

	res := serveBridgeRequest(h, "/files/activity.fit", true)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("body = %q, want %q", got, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file remains after successful read: %v", err)
	}
}

func TestFileBridgeRejectsTraversalOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := writeBridgeFile(t, outsideDir, "secret.fit", []byte("secret"))
	h := testFileBridgeHandler(t, root, bridgeTestMaxBytes)

	name := url.PathEscape("../" + filepath.Base(outsidePath))
	res := serveBridgeRequest(h, "/files/"+name, true)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Fatalf("traversal request affected outside file: %v", err)
	}
}

func TestFileBridgeRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := writeBridgeFile(t, t.TempDir(), "target.fit", []byte("secret"))
	if err := os.Symlink(target, filepath.Join(root, "linked.fit")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	h := testFileBridgeHandler(t, root, bridgeTestMaxBytes)

	res := serveBridgeRequest(h, "/files/linked.fit", true)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink request affected target: %v", err)
	}
}

func TestFileBridgeRejectsNonFITName(t *testing.T) {
	root := t.TempDir()
	path := writeBridgeFile(t, root, "activity.txt", []byte("not FIT"))
	h := testFileBridgeHandler(t, root, bridgeTestMaxBytes)

	res := serveBridgeRequest(h, "/files/activity.txt", true)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("non-FIT request removed file: %v", err)
	}
}

func TestFileBridgeRejectsOversizeFile(t *testing.T) {
	root := t.TempDir()
	path := writeBridgeFile(t, root, "activity.fit", []byte("too large"))
	h := testFileBridgeHandler(t, root, 4)

	res := serveBridgeRequest(h, "/files/activity.fit", true)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusRequestEntityTooLarge)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("oversize request removed file: %v", err)
	}
}

func TestFileBridgeHandlerClosesRoot(t *testing.T) {
	dir := t.TempDir()
	handler, err := NewFileBridgeHandler(FileBridgeConfig{
		Root: dir, Username: "u", Password: "p", MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	closer, ok := handler.(io.Closer)
	if !ok {
		t.Fatal("file bridge handler must implement io.Closer so its os.Root is released")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRunFileBridgeShutsDownGracefullyOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.fit"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		switch key {
		case "KADENCE_FILE_BRIDGE_ROOT":
			return dir
		case "KADENCE_FILE_BRIDGE_AUTH_USER":
			return "u"
		case "KADENCE_FILE_BRIDGE_AUTH_PASS":
			return "p"
		case "KADENCE_FILE_BRIDGE_ADDR":
			return "127.0.0.1:0"
		}
		return ""
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runFileBridgeContext(ctx, nil, getenv) }()

	time.Sleep(50 * time.Millisecond) // let the listener bind
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("graceful shutdown returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runFileBridgeContext did not return after ctx cancel")
	}
}

func testFileBridgeHandler(t *testing.T, root string, maxBytes int64) http.Handler {
	t.Helper()
	h, err := NewFileBridgeHandler(FileBridgeConfig{
		Root:     root,
		Username: "bridge-user",
		Password: "bridge-password",
		MaxBytes: maxBytes,
	})
	if err != nil {
		t.Fatalf("NewFileBridgeHandler() error = %v", err)
	}
	return h
}

func serveBridgeRequest(h http.Handler, target string, authorized bool) *http.Response {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if authorized {
		req.SetBasicAuth("bridge-user", "bridge-password")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func writeBridgeFile(t *testing.T, root, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	return path
}
