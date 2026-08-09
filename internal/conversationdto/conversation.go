// Package conversationdto defines the canonical conversation wire format.
package conversationdto

import (
	"time"

	"github.com/tamcore/kadence/internal/model"
)

const timestampLayout = "2006-01-02T15:04:05.000000Z"

// Conversation is the sidebar-safe conversation state sent by REST and SSE.
type Conversation struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	PinnedAt       *string `json:"pinnedAt"`
	LastActivityAt string  `json:"lastActivityAt"`
	CreatedAt      string  `json:"createdAt"`
}

// FromModel converts a stored conversation to its wire representation.
func FromModel(c model.Conversation) Conversation {
	var pinnedAt *string
	if c.PinnedAt != nil {
		formatted := formatTimestamp(*c.PinnedAt)
		pinnedAt = &formatted
	}
	return Conversation{
		ID: c.ID, Title: c.Title, PinnedAt: pinnedAt,
		LastActivityAt: formatTimestamp(c.LastActivityAt),
		CreatedAt:      formatTimestamp(c.CreatedAt),
	}
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(timestampLayout)
}
