package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime"
	"net/http"
	"strings"

	"golang.org/x/image/webp"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

// ErrUnsupportedAttachment reports a file type or image feature that chat
// attachments do not support.
var ErrUnsupportedAttachment = errors.New("unsupported attachment")

// ErrInvalidAttachment reports bytes that do not form a valid file of the
// declared attachment type.
var ErrInvalidAttachment = errors.New("invalid attachment")

const (
	maxNativeImageAxis   = 8192
	maxNativeImagePixels = 8 << 20
)

// FileInput is one raw file supplied for the current chat turn.
type FileInput struct {
	Filename string
	MIME     string
	Data     []byte
}

// AttachmentProcessor validates raw files locally, then extracts document
// text only after the caller's egress guardrail allows the turn.
type AttachmentProcessor struct {
	extractors []ingest.Extractor
}

// NewAttachmentProcessor constructs a processor over the effective extractor
// set available in the running server.
func NewAttachmentProcessor(extractors []ingest.Extractor) *AttachmentProcessor {
	return &AttachmentProcessor{extractors: append([]ingest.Extractor(nil), extractors...)}
}

// Prepare validates file types and image dimensions without calling an
// extractor. The returned attachments retain their raw bytes for atomic
// persistence after guardrail classification.
func (p *AttachmentProcessor) Prepare(files []FileInput) ([]model.MessageAttachment, error) {
	if len(files) == 0 {
		return nil, nil
	}
	out := make([]model.MessageAttachment, 0, len(files))
	for _, file := range files {
		attachment, err := p.prepareOne(file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.Filename, err)
		}
		out = append(out, attachment)
	}
	return out, nil
}

func (p *AttachmentProcessor) prepareOne(file FileInput) (model.MessageAttachment, error) {
	if len(file.Data) == 0 {
		return model.MessageAttachment{}, ErrInvalidAttachment
	}
	normalized := ingest.NormalizeUploadMIME(file.Filename, file.MIME)
	mediaType, _, err := mime.ParseMediaType(normalized)
	if err != nil {
		return model.MessageAttachment{}, fmt.Errorf("%w: malformed MIME", ErrUnsupportedAttachment)
	}
	mediaType = strings.ToLower(mediaType)

	attachment := model.MessageAttachment{
		Filename:  file.Filename,
		MIME:      mediaType,
		SizeBytes: int64(len(file.Data)),
		RawBytes:  append([]byte(nil), file.Data...),
	}
	if isNativeImageMIME(mediaType) {
		width, height, err := validateNativeImage(mediaType, file.Data)
		if err != nil {
			return model.MessageAttachment{}, err
		}
		attachment.Kind = model.AttachmentKindImage
		attachment.ImageWidth = &width
		attachment.ImageHeight = &height
		return attachment, nil
	}
	if strings.HasPrefix(mediaType, "image/") {
		return model.MessageAttachment{}, fmt.Errorf("%w: %s", ErrUnsupportedAttachment, mediaType)
	}
	if _, err := ingest.Select(p.extractors, mediaType); err != nil {
		return model.MessageAttachment{}, fmt.Errorf("%w: %s", ErrUnsupportedAttachment, mediaType)
	}
	attachment.Kind = model.AttachmentKindDocument
	return attachment, nil
}

// ExtractDocuments extracts each prepared document with exactly the first
// matching effective extractor. Native images are copied through unchanged.
func (p *AttachmentProcessor) ExtractDocuments(
	ctx context.Context, prepared []model.MessageAttachment,
) ([]model.MessageAttachment, error) {
	if len(prepared) == 0 {
		return nil, nil
	}
	out := append([]model.MessageAttachment(nil), prepared...)
	for i := range out {
		if out[i].Kind != model.AttachmentKindDocument ||
			out[i].ExtractionComplete ||
			out[i].ExtractedMarkdown != "" {
			continue
		}
		extractor, err := ingest.Select(p.extractors, out[i].MIME)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", out[i].Filename, ErrUnsupportedAttachment)
		}
		result, err := extractor.Extract(ctx, out[i].RawBytes, out[i].MIME)
		if err != nil {
			return nil, fmt.Errorf("%s: %w: %w", out[i].Filename, ErrInvalidAttachment, err)
		}
		out[i].ExtractedMarkdown = result.Markdown
		out[i].ExtractionComplete = true
	}
	return out, nil
}

func isNativeImageMIME(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func validateNativeImage(mediaType string, data []byte) (int, int, error) {
	detected, _, err := mime.ParseMediaType(http.DetectContentType(data))
	if err != nil || detected != mediaType {
		return 0, 0, fmt.Errorf(
			"%w: declared %s does not match file bytes", ErrUnsupportedAttachment, mediaType,
		)
	}

	var config image.Config
	switch mediaType {
	case "image/png":
		config, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/jpeg":
		config, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case "image/webp":
		config, err = webp.DecodeConfig(bytes.NewReader(data))
	case "image/gif":
		if err = validateStaticGIF(data); err == nil {
			config, err = gif.DecodeConfig(bytes.NewReader(data))
		}
	}
	if err != nil {
		if errors.Is(err, ErrUnsupportedAttachment) || errors.Is(err, ErrInvalidAttachment) {
			return 0, 0, err
		}
		return 0, 0, fmt.Errorf("%w: %w", ErrInvalidAttachment, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return 0, 0, fmt.Errorf("%w: image has invalid dimensions", ErrInvalidAttachment)
	}
	if config.Width > maxNativeImageAxis || config.Height > maxNativeImageAxis {
		return 0, 0, fmt.Errorf(
			"%w: image axis exceeds %d pixels",
			ErrUnsupportedAttachment, maxNativeImageAxis,
		)
	}
	if config.Width > maxNativeImagePixels/config.Height {
		return 0, 0, fmt.Errorf(
			"%w: image exceeds %d pixels",
			ErrUnsupportedAttachment, maxNativeImagePixels,
		)
	}
	if err := decodeNativeImage(mediaType, data); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrInvalidAttachment, err)
	}
	return config.Width, config.Height, nil
}

func decodeNativeImage(mediaType string, data []byte) error {
	reader := bytes.NewReader(data)
	switch mediaType {
	case "image/png":
		_, err := png.Decode(reader)
		return err
	case "image/jpeg":
		_, err := jpeg.Decode(reader)
		return err
	case "image/webp":
		_, err := webp.Decode(reader)
		return err
	case "image/gif":
		_, err := gif.Decode(reader)
		return err
	default:
		return fmt.Errorf("unsupported native image MIME %q", mediaType)
	}
}

// validateStaticGIF walks the GIF block stream without decoding pixel data.
// It rejects a second image descriptor (animation) and requires a complete
// trailer, avoiding gif.DecodeAll's frame allocation amplification.
func validateStaticGIF(data []byte) error {
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return fmt.Errorf("%w: malformed GIF header", ErrInvalidAttachment)
	}
	offset := 13
	packed := data[10]
	if packed&0x80 != 0 {
		tableBytes := 3 * (1 << ((packed & 0x07) + 1))
		if !advanceGIF(&offset, tableBytes, len(data)) {
			return fmt.Errorf("%w: truncated GIF color table", ErrInvalidAttachment)
		}
	}

	images := 0
	for offset < len(data) {
		blockType := data[offset]
		offset++
		switch blockType {
		case 0x3b:
			if images != 1 || offset != len(data) {
				return fmt.Errorf("%w: malformed GIF trailer", ErrInvalidAttachment)
			}
			return nil
		case 0x21:
			if !advanceGIF(&offset, 1, len(data)) || !skipGIFSubBlocks(data, &offset) {
				return fmt.Errorf("%w: truncated GIF extension", ErrInvalidAttachment)
			}
		case 0x2c:
			images++
			if images > 1 {
				return fmt.Errorf("%w: animated GIF", ErrUnsupportedAttachment)
			}
			if !advanceGIF(&offset, 9, len(data)) {
				return fmt.Errorf("%w: truncated GIF image descriptor", ErrInvalidAttachment)
			}
			imagePacked := data[offset-1]
			if imagePacked&0x80 != 0 {
				tableBytes := 3 * (1 << ((imagePacked & 0x07) + 1))
				if !advanceGIF(&offset, tableBytes, len(data)) {
					return fmt.Errorf("%w: truncated GIF local color table", ErrInvalidAttachment)
				}
			}
			if !advanceGIF(&offset, 1, len(data)) || !skipGIFSubBlocks(data, &offset) {
				return fmt.Errorf("%w: truncated GIF image data", ErrInvalidAttachment)
			}
		default:
			return fmt.Errorf("%w: unknown GIF block", ErrInvalidAttachment)
		}
	}
	return fmt.Errorf("%w: GIF has no trailer", ErrInvalidAttachment)
}

func advanceGIF(offset *int, amount, length int) bool {
	if amount < 0 || *offset > length-amount {
		return false
	}
	*offset += amount
	return true
}

func skipGIFSubBlocks(data []byte, offset *int) bool {
	for {
		if *offset >= len(data) {
			return false
		}
		size := int(data[*offset])
		(*offset)++
		if size == 0 {
			return true
		}
		if !advanceGIF(offset, size, len(data)) {
			return false
		}
	}
}
