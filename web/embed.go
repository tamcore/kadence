// Package web exposes the embedded SvelteKit frontend build, when available.
//
// Default builds compile with FS == nil so `go test ./...` and dev workflows
// don't require a prior `npm run build`. Production binaries are built with
// `-tags prodfrontend` to embed the actual build/ directory (see embed_prod.go).
package web

import "io/fs"

// FS is the embedded SvelteKit build (rooted at the build/ directory) or nil
// when built without the prodfrontend tag.
var FS fs.FS

// Available reports whether an embedded frontend is bundled in this binary.
func Available() bool { return FS != nil }
