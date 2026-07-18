package model

import "time"

// Chunk scopes and source kinds.
const (
	ScopePrivate        = "private"
	ScopePublic         = "public"
	ChunkSourceMessage  = "message"
	ChunkSourceDocument = "document"
)

// Chunk is an embedded piece of retrievable content.
type Chunk struct {
	ID             int64
	UserID         int64
	ConversationID *int64
	Scope          string
	SourceKind     string
	SourceID       *int64
	Content        string
	CreatedAt      time.Time
}
