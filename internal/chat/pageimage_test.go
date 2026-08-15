package chat

import (
	"os"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

func pdfPageImageFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../ingest/testdata/imagepage.pdf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func testPageImageOptions() ingest.PageImageOptions {
	return ingest.PageImageOptions{MinCoverage: 0.12, MaxPages: 20}
}

func pdfAttachment(t *testing.T) model.MessageAttachment {
	t.Helper()
	return model.MessageAttachment{
		Filename:           "plan.pdf",
		MIME:               "application/pdf",
		Kind:               model.AttachmentKindDocument,
		RawBytes:           pdfPageImageFixture(t),
		ExtractedMarkdown:  "PLAN TEXT LAYER",
		ExtractionComplete: true,
	}
}

func TestDerivePageImagesFromPDFAttachment(t *testing.T) {
	// Arrange
	attachment := pdfAttachment(t)

	// Act
	images := derivePageImages(attachment, testPageImageOptions())

	// Assert
	if len(images) != 1 {
		t.Fatalf("got %d derived images, want 1", len(images))
	}
	if images[0].MIMEType != mimeImagePNG {
		t.Errorf("MIMEType = %q, want %q", images[0].MIMEType, mimeImagePNG)
	}
	if images[0].Width != 1000 || images[0].Height != 1400 {
		t.Errorf("dimensions = %dx%d, want 1000x1400", images[0].Width, images[0].Height)
	}
	if len(images[0].Data) == 0 {
		t.Error("derived image carries no bytes")
	}
}

func TestDerivePageImagesIgnoresNonPDFAttachments(t *testing.T) {
	// Arrange
	attachment := model.MessageAttachment{
		Filename: "note.txt",
		MIME:     "text/plain",
		Kind:     model.AttachmentKindDocument,
		RawBytes: []byte("hello"),
	}

	// Act
	images := derivePageImages(attachment, testPageImageOptions())

	// Assert
	if len(images) != 0 {
		t.Fatalf("got %d derived images, want 0 for a non-PDF attachment", len(images))
	}
}

func TestDerivePageImagesIgnoresNativeImageAttachments(t *testing.T) {
	// Arrange
	attachment := model.MessageAttachment{
		Filename: "photo.png",
		MIME:     mimeImagePNG,
		Kind:     model.AttachmentKindImage,
		RawBytes: []byte("not really a png"),
	}

	// Act
	images := derivePageImages(attachment, testPageImageOptions())

	// Assert
	if len(images) != 0 {
		t.Fatalf("got %d derived images, want 0 for a native image attachment", len(images))
	}
}

func TestDerivePageImagesReturnsNothingWithoutRawBytes(t *testing.T) {
	// Arrange
	attachment := pdfAttachment(t)
	attachment.RawBytes = nil

	// Act
	images := derivePageImages(attachment, testPageImageOptions())

	// Assert
	if len(images) != 0 {
		t.Fatalf("got %d derived images, want 0 when RawBytes is absent", len(images))
	}
}

func TestDerivePageImagesDisabledByZeroMaxPages(t *testing.T) {
	// Arrange
	attachment := pdfAttachment(t)

	// Act
	images := derivePageImages(attachment, ingest.PageImageOptions{})

	// Assert
	if len(images) != 0 {
		t.Fatalf("got %d derived images, want 0 when the feature is disabled", len(images))
	}
}

func TestDerivePageImagesForAttachmentsCoversEveryDocument(t *testing.T) {
	// Arrange
	attachments := []model.MessageAttachment{
		pdfAttachment(t),
		{Filename: "note.txt", MIME: "text/plain", Kind: model.AttachmentKindDocument},
		pdfAttachment(t),
	}

	// Act
	images := derivePageImagesForAttachments(attachments, testPageImageOptions())

	// Assert
	if len(images) != 2 {
		t.Fatalf("got %d derived images, want 2 (one per PDF attachment)", len(images))
	}
}

func TestCurrentTurnProviderMessageAppendsSuppliedPageImages(t *testing.T) {
	// Arrange
	message := model.Message{
		Role:        model.MsgRoleUser,
		Content:     "check this",
		Attachments: []model.MessageAttachment{pdfAttachment(t)},
	}
	pageImages := derivePageImagesForAttachments(message.Attachments, testPageImageOptions())

	// Act
	got, err := currentTurnProviderMessageWithPageImages(message, nil, pageImages)

	// Assert
	if err != nil {
		t.Fatalf("currentTurnProviderMessageWithPageImages() error = %v, want nil", err)
	}
	if len(got.Images) != 1 {
		t.Fatalf("got %d images on the provider message, want 1", len(got.Images))
	}
	if !strings.Contains(got.Content, "PLAN TEXT LAYER") {
		t.Error("provider message lost the extracted text layer")
	}
}

func TestCurrentTurnProviderMessageWithoutPageImagesIsUnchanged(t *testing.T) {
	// Arrange
	message := model.Message{
		Role:        model.MsgRoleUser,
		Content:     "check this",
		Attachments: []model.MessageAttachment{pdfAttachment(t)},
	}

	// Act
	got, err := currentTurnProviderMessage(message, nil)

	// Assert
	if err != nil {
		t.Fatalf("currentTurnProviderMessage() error = %v, want nil", err)
	}
	if len(got.Images) != 0 {
		t.Fatalf("got %d images, want 0 when no page images are supplied", len(got.Images))
	}
}
