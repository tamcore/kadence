package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func newTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<html><head><title>Kadence</title></head><body>app</body></html>"),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('kadence');"),
		},
	}
}

func TestStaticHandlerServesExistingFile(t *testing.T) {
	mapfs := newTestFS()
	srv := httptest.NewServer(staticHandler(mapfs))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	want := "console.log('kadence');"
	if string(body) != want {
		t.Fatalf("body = %q, want %q", string(body), want)
	}
}

func TestStaticHandlerFallsBackToIndexForUnknownRoute(t *testing.T) {
	mapfs := newTestFS()
	srv := httptest.NewServer(staticHandler(mapfs))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/some/spa/route")
	if err != nil {
		t.Fatalf("GET /some/spa/route: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "<title>Kadence</title>") {
		t.Fatalf("body = %q, want SPA fallback containing index.html marker", string(body))
	}
}

func TestFileExists(t *testing.T) {
	mapfs := newTestFS()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"existing nested file", "/assets/app.js", true},
		{"missing file", "/nope", false},
		{"root path", "/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fileExists(mapfs, tt.path); got != tt.want {
				t.Fatalf("fileExists(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFileExistsWithNilFS(t *testing.T) {
	if fileExists(nil, "/assets/app.js") {
		t.Fatal("fileExists(nil, ...) = true, want false")
	}
}
