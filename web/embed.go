// Package web exposes the embedded SvelteKit frontend build, when available.
//
// Default builds compile with FS == nil so `go test ./...` and dev workflows
// don't require a prior `npm run build`. Production binaries are built with
// `-tags prodfrontend` to embed the actual build/ directory (see embed_prod.go).
package web

import (
	"encoding/json"
	"io/fs"
)

// FS is the embedded SvelteKit build (rooted at the build/ directory) or nil
// when built without the prodfrontend tag.
var FS fs.FS

// Available reports whether an embedded frontend is bundled in this binary.
func Available() bool { return FS != nil }

// cspHashesFile is the build artifact written by web/scripts/gen-csp-hashes.mjs
// (wired into `npm run build`), containing a JSON array of "sha256-<base64>"
// CSP hash-source tokens for every inline <script> in the built SPA's HTML.
const cspHashesFile = "csp-hashes.json"

// CSPScriptHashes returns the sha256 CSP hash-source tokens for the embedded
// frontend's inline scripts, or nil if no embedded frontend is available or
// the hashes file is missing/unreadable/malformed. Callers must treat a nil
// or empty result as "hashes unavailable" and degrade to a permissive policy
// rather than emitting a strict script-src with no hashes (which would break
// the SPA's own bootstrap script).
func CSPScriptHashes() []string {
	if FS == nil {
		return nil
	}
	data, err := fs.ReadFile(FS, cspHashesFile)
	if err != nil {
		return nil
	}
	var hashes []string
	if err := json.Unmarshal(data, &hashes); err != nil {
		return nil
	}
	return hashes
}
