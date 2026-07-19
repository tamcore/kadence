package ingest

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"

	"github.com/tamcore/kadence/internal/model"
)

const pdfMimeType = "application/pdf"

// PDFExtractor extracts the text layer from PDF documents using a pure-Go
// parser. It does not perform OCR: image-only PDFs yield empty text.
type PDFExtractor struct{}

// NewPDFExtractor returns an Extractor for application/pdf documents.
func NewPDFExtractor() *PDFExtractor {
	return &PDFExtractor{}
}

// CanHandle reports whether mime is application/pdf.
func (e *PDFExtractor) CanHandle(mime string) bool {
	return mime == pdfMimeType
}

// Extract parses data as a PDF and returns its text layer, joining pages with
// a blank line. Malformed input is reported as an error rather than a panic.
// The mime parameter is ignored: this extractor only ever handles
// application/pdf (see CanHandle).
func (e *PDFExtractor) Extract(_ context.Context, data []byte, _ string) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pdf parse panic: %v", r)
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Result{}, fmt.Errorf("open pdf: %w", err)
	}

	var pages []string
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, pageErr := page.GetPlainText(nil)
		if pageErr != nil {
			return Result{}, fmt.Errorf("extract page %d: %w", i, pageErr)
		}
		pages = append(pages, text)
	}

	return Result{
		Markdown:   strings.Join(pages, "\n\n"),
		SourceType: model.DocSourcePDF,
	}, nil
}
