package model

import "time"

// Document source types.
const (
	DocSourcePDF   = "pdf"
	DocSourceImage = "image"
	DocSourceText  = "text"
)

// Document is an uploaded file whose extracted text feeds RAG.
type Document struct {
	ID                int64
	OwnerUserID       *int64 // nil for public/admin-published documents
	Scope             string // private | public
	Filename          string
	Mime              string
	SourceType        string
	ExtractedMarkdown string
	CreatedAt         time.Time
}
