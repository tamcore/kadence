package model

import "time"

// Document source types.
const (
	DocSourcePDF   = "pdf"
	DocSourceImage = "image"
	DocSourceText  = "text"
)

// Document image-extraction statuses. Uploads needing no page-image pass are
// created not_needed; PDFs start pending and are claimed by the pdfvision
// worker.
const (
	ExtractionStatusNotNeeded = "not_needed"
	ExtractionStatusPending   = "pending"
	ExtractionStatusRunning   = "running"
	ExtractionStatusComplete  = "complete"
	ExtractionStatusFailed    = "failed"
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
	// ExtractionStatus tracks the page-image pass over this document.
	ExtractionStatus string
	// RawBytes holds the original upload only while that pass is pending or
	// running; FinishExtraction clears it. Loaded only by
	// ClaimPendingExtraction, never by the list queries.
	RawBytes []byte
}
