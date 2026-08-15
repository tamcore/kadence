package chat

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
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

func TestDerivePageImagesForAttachmentsCapsPerMessageNotPerAttachment(t *testing.T) {
	// Arrange: two PDFs that would each yield an image, but a cap of one.
	attachments := []model.MessageAttachment{pdfAttachment(t), pdfAttachment(t)}
	opts := ingest.PageImageOptions{MinCoverage: 0.12, MaxPages: 1}

	// Act
	images := derivePageImagesForAttachments(attachments, opts)

	// Assert
	if len(images) != 1 {
		t.Fatalf("got %d derived images, want 1 (the cap is per message, not per attachment)", len(images))
	}
}

func TestStripDerivedImagesKeepsUserAttachedImages(t *testing.T) {
	// Arrange: one user image followed by two derived ones.
	userImage := provider.ImageContent{MIMEType: mimeImagePNG, Data: []byte("user")}
	derivedA := provider.ImageContent{MIMEType: mimeImagePNG, Data: []byte("derived-a")}
	derivedB := provider.ImageContent{MIMEType: mimeImagePNG, Data: []byte("derived-b")}
	messages := []provider.Message{
		{Role: model.MsgRoleSystem, Content: "system"},
		{Role: model.MsgRoleUser, Content: "current",
			Images: []provider.ImageContent{userImage, derivedA, derivedB}},
	}
	assembly := turnContextAssembly{currentDerivedImages: 2}

	// Act
	got := stripDerivedImages(messages, assembly)

	// Assert
	if len(got[1].Images) != 1 {
		t.Fatalf("got %d images after stripping, want 1", len(got[1].Images))
	}
	if string(got[1].Images[0].Data) != "user" {
		t.Errorf("kept image = %q, want the user-attached one", got[1].Images[0].Data)
	}
	if len(messages[1].Images) != 3 {
		t.Error("stripDerivedImages mutated its input")
	}
}

func TestStripDerivedImagesClearsRehydratedHistory(t *testing.T) {
	// Arrange
	derived := provider.ImageContent{MIMEType: mimeImagePNG, Data: []byte("derived")}
	messages := []provider.Message{
		{Role: model.MsgRoleSystem, Content: "system"},
		{Role: model.MsgRoleUser, Content: "older", Images: []provider.ImageContent{derived}},
		{Role: model.MsgRoleAssistant, Content: "reply"},
		{Role: model.MsgRoleUser, Content: "current"},
	}
	assembly := turnContextAssembly{
		historyMessages: make([]provider.Message, 2),
		derivedImages:   map[int]int{0: 1},
	}

	// Act
	got := stripDerivedImages(messages, assembly)

	// Assert
	if len(got[1].Images) != 0 {
		t.Fatalf("got %d images on the rehydrated history message, want 0", len(got[1].Images))
	}
}

func TestVisionUnsupportedOnlyMatchesEmptyContentRefusals(t *testing.T) {
	// Arrange
	refusal := &providerStreamFailure{err: provider.ErrVisionUnsupported}
	partial := &providerStreamFailure{err: provider.ErrVisionUnsupported, content: "partial"}

	// Act / Assert
	if !visionUnsupported(refusal, toolTurnState{}) {
		t.Error("visionUnsupported() = false for an empty-content vision refusal, want true")
	}
	if visionUnsupported(partial, toolTurnState{}) {
		t.Error("visionUnsupported() = true when content already streamed, want false")
	}
	if visionUnsupported(errors.New("other"), toolTurnState{}) {
		t.Error("visionUnsupported() = true for an unrelated error, want false")
	}
}
