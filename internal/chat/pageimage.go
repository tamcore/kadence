package chat

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// mimePDF is the only document type page images are derived from.
const mimePDF = "application/pdf"

// defaultTurnImageBytes bounds the combined derived-image payload of one turn.
// The page cap alone does not bound bytes, and the budget must be shared across
// attachments so several PDFs cannot each spend it in full.
const defaultTurnImageBytes = 24 << 20

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
// MaxPages is a per-message allowance, not a per-attachment one: the remaining
// allowance shrinks as each attachment contributes, so two PDFs in one turn
// cannot together exceed the configured cap.
func derivePageImagesForAttachments(
	attachments []model.MessageAttachment, opts ingest.PageImageOptions,
) []provider.ImageContent {
	if opts.MaxPages <= 0 || len(attachments) == 0 {
		return nil
	}
	var out []provider.ImageContent
	remaining := opts.MaxPages
	remainingBytes := opts.MaxTotalBytes
	if remainingBytes <= 0 {
		remainingBytes = defaultTurnImageBytes
	}
	for _, attachment := range attachments {
		if remaining <= 0 || remainingBytes <= 0 {
			break
		}
		scoped := opts
		scoped.MaxPages = remaining
		scoped.MaxTotalBytes = remainingBytes
		images := derivePageImages(attachment, scoped)
		out = append(out, images...)
		remaining -= len(images)
		for _, image := range images {
			remainingBytes -= len(image.Data)
		}
	}
	return out
}

// stripDerivedImages returns messages with the derived page images removed and
// every user-attached image kept. Derived images are always appended last, so
// dropping them is a suffix truncation. The input slice is not mutated.
func stripDerivedImages(
	messages []provider.Message, assembly turnContextAssembly,
) []provider.Message {
	out := append([]provider.Message(nil), messages...)
	// The request is [system..., history..., current], so locate the current
	// turn and the history block by walking back from the end.
	current := len(out) - 1
	if current >= 0 && assembly.currentDerivedImages > 0 {
		out[current] = withoutTrailingImages(out[current], assembly.currentDerivedImages)
	}
	historyStart := current - len(assembly.historyMessages)
	for index, count := range assembly.derivedImages {
		position := historyStart + index
		if position < 0 || position >= current {
			continue
		}
		out[position] = withoutTrailingImages(out[position], count)
	}
	return out
}

// withoutTrailingImages drops the last count images from a message.
func withoutTrailingImages(message provider.Message, count int) provider.Message {
	if count <= 0 || len(message.Images) == 0 {
		return message
	}
	keep := max(len(message.Images)-count, 0)
	message.Images = append([]provider.ImageContent(nil), message.Images[:keep]...)
	return message
}

// visionUnsupported reports whether err is a provider refusal to accept image
// input for a turn that produced no content and no scheduling handoff, the only
// case where retrying without derived images is safe.
func visionUnsupported(err error, turnState toolTurnState) bool {
	var failure *providerStreamFailure
	if !errors.As(err, &failure) {
		return false
	}
	return failure.content == "" &&
		errors.Is(failure.err, provider.ErrVisionUnsupported) &&
		len(turnState.Handoffs) == 0
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

// droppedPageImagesNotice tells the model that page images were left out, so it
// reports the gap instead of answering from the text layer as though the
// document were complete.
func droppedPageImagesNotice(count int) string {
	return fmt.Sprintf(
		"[%d page image(s) from the attached PDF(s) were omitted to fit the "+
			"context budget. Any table rendered as an image is therefore NOT "+
			"visible to you. Do not infer or guess their contents: say which "+
			"information you cannot see and ask for a smaller selection.]",
		count,
	)
}
