// Package push sends browser Web Push notifications via VAPID-authenticated
// requests, pruning subscriptions whose endpoints have gone dead.
package push

import "unicode/utf8"

// Payload is the JSON body delivered to the browser's push service worker.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag,omitempty"`
}

// BuildSnippet returns s truncated to max runes, appending an ellipsis when cut.
// Truncation is rune-safe so multi-byte UTF-8 characters are never split.
func BuildSnippet(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}
