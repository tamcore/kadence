package model

import "time"

// Chat message roles (distinct from user account roles).
const (
	MsgRoleSystem    = "system"
	MsgRoleUser      = "user"
	MsgRoleAssistant = "assistant"
)

// Message attachment kinds.
const (
	AttachmentKindImage    = "image"
	AttachmentKindDocument = "document"
)

// MessageToolCall is an audit record of one tool invocation the assistant made
// while producing a message: the tool name and the (redacted) JSON arguments.
type MessageToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// MessageAttachment is a file owned by one user chat message. Message list/get
// operations hydrate metadata only; RawBytes and ExtractedMarkdown remain empty
// until a payload-specific repository operation requests them.
type MessageAttachment struct {
	ID                int64  `json:"id"`
	MessageID         int64  `json:"message_id"`
	Filename          string `json:"filename"`
	MIME              string `json:"mime"`
	Kind              string `json:"kind"`
	SizeBytes         int64  `json:"size_bytes"`
	RawBytes          []byte `json:"-"`
	ExtractedMarkdown string `json:"-"`
	ImageWidth        *int   `json:"image_width,omitempty"`
	ImageHeight       *int   `json:"image_height,omitempty"`
	Ordinal           int    `json:"ordinal"`
}

// MessageDocumentReference is a snapshot of a document selected for one user
// chat message. DocumentID becomes nil and Available false when the source
// document is later deleted; the filename and scope snapshots remain visible.
type MessageDocumentReference struct {
	ID         int64  `json:"id"`
	MessageID  int64  `json:"message_id"`
	DocumentID *int64 `json:"document_id,omitempty"`
	Filename   string `json:"filename"`
	Scope      string `json:"scope"`
	Ordinal    int    `json:"ordinal"`
	Available  bool   `json:"available"`
}

// Message is a single chat message within a conversation. ToolCalls,
// Attachments, and DocumentReferences are nil when the message has none.
type Message struct {
	ID                 int64
	ConversationID     string
	Role               string
	Content            string
	ToolCalls          []MessageToolCall
	Attachments        []MessageAttachment
	DocumentReferences []MessageDocumentReference
	CreatedAt          time.Time
}
