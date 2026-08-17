// Package chat orchestrates streaming LLM conversations.
package chat

import (
	"github.com/tamcore/kadence/internal/conversationdto"
	"github.com/tamcore/kadence/internal/scheduled"
)

// EventAttachment is safe attachment metadata for optimistic client
// reconciliation. It never contains raw bytes or extracted document text.
type EventAttachment struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	MIME        string `json:"mime"`
	Kind        string `json:"kind"`
	SizeBytes   int64  `json:"sizeBytes"`
	ImageWidth  *int   `json:"imageWidth,omitempty"`
	ImageHeight *int   `json:"imageHeight,omitempty"`
	Ordinal     int    `json:"ordinal"`
}

// EventDocumentReference is the persisted selected-document snapshot exposed
// in meta events for optimistic client reconciliation.
type EventDocumentReference struct {
	ID         int64  `json:"id"`
	DocumentID *int64 `json:"documentId,omitempty"`
	Filename   string `json:"filename"`
	Scope      string `json:"scope"`
	Ordinal    int    `json:"ordinal"`
	Available  bool   `json:"available"`
}

// Event types emitted over SSE.
const (
	EventMeta              = "meta"
	EventToken             = "token"
	EventTitle             = "title"
	EventDone              = "done"
	EventError             = "error"
	EventUpload            = "upload"
	EventTool              = "tool"
	EventCredentials       = "credentials_request"
	EventScheduledArtifact = "scheduled_artifact"
	EventConfirm           = "confirm_request"
)

const (
	UploadStatusProcessing = "processing"
	UploadStatusDone       = "done"
	UploadStatusError      = "error"
)

// EventConversation is the sidebar-safe conversation state in a title event.
type EventConversation = conversationdto.Conversation

// ChatEvent is a single server-sent event in a chat stream.
type ChatEvent struct {
	Type               string             `json:"type"`
	Delta              string             `json:"delta,omitempty"`
	ConversationID     string             `json:"conversationId,omitempty"`
	UserMessageID      int64              `json:"userMessageId,omitempty"`
	AssistantMessageID int64              `json:"assistantMessageId,omitempty"`
	AssistantContent   *string            `json:"assistantContent,omitempty"`
	Message            string             `json:"message,omitempty"`
	FileOrdinal        *int               `json:"fileOrdinal,omitempty"`
	Filename           string             `json:"filename,omitempty"`
	Tool               string             `json:"tool,omitempty"`
	Status             string             `json:"status,omitempty"`
	Arguments          string             `json:"arguments,omitempty"`
	RequestID          string             `json:"requestId,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	Fields             []CredentialField  `json:"fields,omitempty"`
	Conversation       *EventConversation `json:"conversation,omitempty"`
	// Pointer-to-slice keeps these fields absent on non-meta events while meta
	// events encode an empty collection as [] rather than null/omitted.
	Attachments        *[]EventAttachment        `json:"attachments,omitempty"`
	DocumentReferences *[]EventDocumentReference `json:"documentReferences,omitempty"`
	ScheduledArtifact  *scheduled.ChatArtifact   `json:"scheduledArtifact,omitempty"`
}

// CredentialField describes one credential field being requested from the
// user via the credentials_request SSE event (name + display metadata only —
// never a value or token).
type CredentialField struct {
	Name   string `json:"name"`
	Label  string `json:"label,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// EventSink receives chat events (implemented by the SSE handler).
type EventSink interface {
	Send(ChatEvent) error
	Flush() error
}
