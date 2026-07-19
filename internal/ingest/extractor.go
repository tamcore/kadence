// Package ingest provides pluggable document extraction: turning raw uploaded
// bytes of a supported MIME type into plain/markdown text for chunking and
// embedding.
package ingest

import (
	"context"
	"errors"
	"fmt"
)

// Result is the output of an Extractor: the extracted text (as markdown, or
// plain text when no richer structure is available) and the source type the
// text came from (see model.DocSource* constants).
type Result struct {
	Markdown   string
	SourceType string
}

// Extractor turns raw document bytes of a supported MIME type into a Result.
type Extractor interface {
	// CanHandle reports whether this extractor supports the given MIME type.
	CanHandle(mime string) bool
	// Extract parses data (of the given MIME type) and returns the extracted text.
	Extract(ctx context.Context, data []byte, mime string) (Result, error)
}

// ErrUnsupportedType is returned by Select when no extractor handles the
// given MIME type. Callers (e.g. HTTP handlers) should map this to 415.
var ErrUnsupportedType = errors.New("unsupported document type")

// Select returns the first extractor in extractors that can handle mime, or
// ErrUnsupportedType if none match.
func Select(extractors []Extractor, mime string) (Extractor, error) {
	for _, e := range extractors {
		if e.CanHandle(mime) {
			return e, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedType, mime)
}
