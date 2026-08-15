package chat

import (
	"log/slog"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// mimePDF is the only document type page images are derived from.
const mimePDF = "application/pdf"

// derivePageImages returns page-content images derived from one PDF document
// attachment.
//
// Derived images are transient: they are handed to the provider for this
// request only and are never persisted. That keeps them off the storage quota,
// out of the conversation UI, and out of historicalPayloadTokenCost, which
// charges persisted images at bytes/3 on every later turn.
//
// Extraction failure yields no images rather than an error: the attachment's
// text layer still reaches the model, so a broken PDF degrades the turn instead
// of failing it.
func derivePageImages(
	attachment model.MessageAttachment, opts ingest.PageImageOptions,
) []provider.ImageContent {
	if attachment.Kind != model.AttachmentKindDocument ||
		attachment.MIME != mimePDF ||
		len(attachment.RawBytes) == 0 ||
		opts.MaxPages <= 0 {
		return nil
	}
	pageImages, err := ingest.ExtractPageImages(attachment.RawBytes, opts)
	if err != nil {
		slog.Warn("pdf page-image extraction failed",
			"filename", attachment.Filename, "err", err)
		return nil
	}
	out := make([]provider.ImageContent, 0, len(pageImages))
	for _, image := range pageImages {
		out = append(out, provider.ImageContent{
			Data:     image.Data,
			MIMEType: image.MIME,
			Width:    image.Width,
			Height:   image.Height,
		})
	}
	return out
}

// derivePageImagesForAttachments derives page images across every attachment of
// one message.
//
// Callers must invoke this exactly once per message and pass the result down,
// never deriving inside currentTurnProviderMessage: that function runs many
// times inside the context-fitting loop, and PDF extraction is expensive enough
// (seconds on a large document) that repeating it there would dominate turn
// latency.
func derivePageImagesForAttachments(
	attachments []model.MessageAttachment, opts ingest.PageImageOptions,
) []provider.ImageContent {
	if opts.MaxPages <= 0 || len(attachments) == 0 {
		return nil
	}
	var out []provider.ImageContent
	for _, attachment := range attachments {
		out = append(out, derivePageImages(attachment, opts)...)
	}
	return out
}

// estimatePageImageTokens estimates the context cost of derived page images, so
// the turn budget accounts for them the same way it accounts for native image
// attachments.
func estimatePageImageTokens(images []provider.ImageContent) int {
	tokens := 0
	for _, image := range images {
		tokens += estimateImageTokens(image)
	}
	return tokens
}
