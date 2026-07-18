// Package migrations holds embedded goose SQL migrations for Kadence.
package migrations

import "embed"

// FS holds the embedded *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
