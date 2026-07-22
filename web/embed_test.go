package web

import (
	"testing"
	"testing/fstest"
)

func TestCSPScriptHashesNilFS(t *testing.T) {
	old := FS
	FS = nil
	defer func() { FS = old }()

	if got := CSPScriptHashes(); got != nil {
		t.Fatalf("CSPScriptHashes() = %v, want nil when FS is nil", got)
	}
	if Available() {
		t.Fatal("Available() = true, want false when FS is nil")
	}
}

func TestCSPScriptHashesMissingFile(t *testing.T) {
	old := FS
	FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	defer func() { FS = old }()

	if got := CSPScriptHashes(); got != nil {
		t.Fatalf("CSPScriptHashes() = %v, want nil when csp-hashes.json is absent", got)
	}
}

func TestCSPScriptHashesMalformedJSON(t *testing.T) {
	old := FS
	FS = fstest.MapFS{cspHashesFile: &fstest.MapFile{Data: []byte("not json")}}
	defer func() { FS = old }()

	if got := CSPScriptHashes(); got != nil {
		t.Fatalf("CSPScriptHashes() = %v, want nil on malformed JSON", got)
	}
}

func TestCSPScriptHashesReturnsHashes(t *testing.T) {
	old := FS
	FS = fstest.MapFS{
		cspHashesFile: &fstest.MapFile{Data: []byte(`["sha256-AAA=","sha256-BBB="]`)},
	}
	defer func() { FS = old }()

	got := CSPScriptHashes()
	want := []string{"sha256-AAA=", "sha256-BBB="}
	if len(got) != len(want) {
		t.Fatalf("CSPScriptHashes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CSPScriptHashes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !Available() {
		t.Fatal("Available() = false, want true when FS is set")
	}
}
