package ingest

import (
	"path/filepath"
	"strings"
)

// UploadCapabilities describes the upload limits and browser file filter
// backed by the extractors that are actually active.
type UploadCapabilities struct {
	MaxBytes       int    `json:"max_bytes"`
	RichExtraction bool   `json:"rich_extraction"`
	Accept         string `json:"accept"`
}

type uploadFormat struct {
	mime       string
	extensions []string
}

var uploadFormats = []uploadFormat{
	{mime: "application/pdf", extensions: []string{".pdf"}},
	{mime: "image/png", extensions: []string{".png"}},
	{mime: "image/jpeg", extensions: []string{".jpg", ".jpeg"}},
	{mime: "image/webp", extensions: []string{".webp"}},
	{mime: "image/gif", extensions: []string{".gif"}},
	{mime: "text/plain", extensions: []string{".txt"}},
	{mime: "text/markdown", extensions: []string{".md"}},
	{mime: "text/html", extensions: []string{".html", ".htm"}},
	{mime: "text/csv", extensions: []string{".csv"}},
	{mime: "application/msword", extensions: []string{".doc"}},
	{mime: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", extensions: []string{".docx"}},
	{mime: "application/vnd.ms-excel", extensions: []string{".xls"}},
	{mime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", extensions: []string{".xlsx"}},
	{mime: "application/vnd.ms-powerpoint", extensions: []string{".ppt"}},
	{mime: "application/vnd.openxmlformats-officedocument.presentationml.presentation", extensions: []string{".pptx"}},
	{mime: "application/rtf", extensions: []string{".rtf"}},
	{mime: "application/epub+zip", extensions: []string{".epub"}},
}

// BuildUploadCapabilities derives the browser-facing profile from the
// effective extractor set. Configured-but-unavailable extractors therefore
// never advertise formats the running server cannot ingest.
func BuildUploadCapabilities(extractors []Extractor, maxBytes int) UploadCapabilities {
	accept := make([]string, 0, len(uploadFormats)*2)
	rich := false
	for _, format := range uploadFormats {
		if !canAnyHandle(extractors, format.mime) {
			continue
		}
		accept = append(accept, format.mime)
		accept = append(accept, format.extensions...)
		if format.mime != pdfMimeType {
			rich = true
		}
	}
	return UploadCapabilities{
		MaxBytes:       maxBytes,
		RichExtraction: rich,
		Accept:         strings.Join(accept, ","),
	}
}

func canAnyHandle(extractors []Extractor, mime string) bool {
	for _, extractor := range extractors {
		if extractor.CanHandle(mime) {
			return true
		}
	}
	return false
}

// NormalizeUploadMIME maps absent or generic browser MIME values to the MIME
// associated with a known filename extension. Meaningful declared types are
// preserved so the extractor remains authoritative.
func NormalizeUploadMIME(filename, declaredMIME string) string {
	switch strings.ToLower(strings.TrimSpace(declaredMIME)) {
	case "", "application/octet-stream", "binary/octet-stream", "application/zip", "application/x-zip-compressed":
	default:
		return declaredMIME
	}

	extension := strings.ToLower(filepath.Ext(filename))
	for _, format := range uploadFormats {
		for _, candidate := range format.extensions {
			if extension == candidate {
				return format.mime
			}
		}
	}
	return declaredMIME
}
