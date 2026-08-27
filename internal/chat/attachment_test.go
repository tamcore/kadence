package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

// mimeTextMarkdown is the MIME type used by attachment tests that exercise
// document extraction.
const mimeTextMarkdown = "text/markdown"

func TestAttachmentProcessorPrepareAcceptsNativeStaticImages(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		mime     string
		data     []byte
		width    int
		height   int
	}{
		{name: "PNG", filename: "chart.png", mime: mimeImagePNG, data: encodedPNG(t, 3, 2), width: 3, height: 2},
		{name: "JPEG", filename: "photo.jpg", mime: mimeImageJPEG, data: encodedJPEG(t, 4, 3), width: 4, height: 3},
		{
			name: "WebP", filename: "route.webp", mime: mimeImageWebP,
			data:  mustBase64(t, "UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA"),
			width: 1, height: 1,
		},
		{name: "GIF", filename: "badge.gif", mime: mimeImageGIF, data: encodedGIF(t, 5, 4, 1), width: 5, height: 4},
		{
			name: "generic MIME normalized by filename", filename: "generic.png",
			mime: "application/octet-stream", data: encodedPNG(t, 6, 5), width: 6, height: 5,
		},
	}

	processor := NewAttachmentProcessor(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := processor.Prepare([]FileInput{{
				Filename: tt.filename,
				MIME:     tt.mime,
				Data:     tt.data,
			}})
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("attachments = %d, want 1", len(got))
			}
			attachment := got[0]
			if attachment.Kind != model.AttachmentKindImage ||
				attachment.ImageWidth == nil || *attachment.ImageWidth != tt.width ||
				attachment.ImageHeight == nil || *attachment.ImageHeight != tt.height {
				t.Fatalf("prepared image = %+v", attachment)
			}
			if !bytes.Equal(attachment.RawBytes, tt.data) {
				t.Fatal("prepared image did not preserve raw bytes")
			}
		})
	}
}

func TestAttachmentProcessorPrepareRejectsMIMEConfusionAndMalformedImages(t *testing.T) {
	processor := NewAttachmentProcessor(nil)
	tests := []struct {
		name string
		file FileInput
		err  error
	}{
		{
			name: "declared image differs from magic",
			file: FileInput{Filename: "disguised.jpg", MIME: mimeImageJPEG, Data: encodedPNG(t, 2, 2)},
			err:  ErrUnsupportedAttachment,
		},
		{
			name: "malformed native image",
			file: FileInput{Filename: "broken.png", MIME: mimeImagePNG, Data: []byte("\x89PNG\r\n\x1a\ntruncated")},
			err:  ErrInvalidAttachment,
		},
		{
			name: "unsupported image type",
			file: FileInput{Filename: "bitmap.bmp", MIME: "image/bmp", Data: []byte("BMfake")},
			err:  ErrUnsupportedAttachment,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := processor.Prepare([]FileInput{tt.file}); !errors.Is(err, tt.err) {
				t.Fatalf("Prepare error = %v, want errors.Is(_, %v)", err, tt.err)
			}
		})
	}
}

func TestAttachmentProcessorPrepareRejectsAnimatedAndTruncatedGIFWithoutDecodeAll(t *testing.T) {
	processor := NewAttachmentProcessor(nil)

	if _, err := processor.Prepare([]FileInput{{
		Filename: "animated.gif",
		MIME:     mimeImageGIF,
		Data:     encodedGIF(t, 2, 2, 2),
	}}); !errors.Is(err, ErrUnsupportedAttachment) {
		t.Fatalf("animated GIF error = %v, want ErrUnsupportedAttachment", err)
	}

	static := encodedGIF(t, 2, 2, 1)
	if _, err := processor.Prepare([]FileInput{{
		Filename: "truncated.gif",
		MIME:     mimeImageGIF,
		Data:     static[:len(static)-1],
	}}); !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("truncated GIF error = %v, want ErrInvalidAttachment", err)
	}
}

func TestAttachmentProcessorPrepareRejectsOversizedImageDimensions(t *testing.T) {
	processor := NewAttachmentProcessor(nil)
	tests := []struct {
		name string
		data []byte
	}{
		{name: "axis", data: pngHeaderOnly(8193, 1)},
		{name: "pixels", data: pngHeaderOnly(4097, 2048)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := processor.Prepare([]FileInput{{
				Filename: tt.name + ".png", MIME: mimeImagePNG, Data: tt.data,
			}})
			if !errors.Is(err, ErrUnsupportedAttachment) {
				t.Fatalf("Prepare error = %v, want ErrUnsupportedAttachment", err)
			}
		})
	}
}

func TestAttachmentProcessorPrepareFullyDecodesConfigValidImageBodies(t *testing.T) {
	jpegBody := encodedJPEG(t, 3, 2)
	tests := []FileInput{
		{
			Filename: "header-only.png", MIME: mimeImagePNG,
			Data: pngHeaderOnly(3, 2),
		},
		{
			Filename: "truncated.jpg", MIME: mimeImageJPEG,
			Data: jpegBody[:len(jpegBody)-2],
		},
	}
	processor := NewAttachmentProcessor(nil)

	for _, file := range tests {
		t.Run(file.Filename, func(t *testing.T) {
			if _, err := processor.Prepare([]FileInput{file}); !errors.Is(err, ErrInvalidAttachment) {
				t.Fatalf("Prepare error = %v, want ErrInvalidAttachment", err)
			}
		})
	}
}

func TestAttachmentProcessorExtractDocumentsUsesFirstEffectiveExtractorOnce(t *testing.T) {
	first := &recordingAttachmentExtractor{
		mime: mimeTextMarkdown,
		result: ingest.Result{
			Markdown:   "# Extracted",
			SourceType: model.DocSourceText,
		},
	}
	second := &recordingAttachmentExtractor{
		mime: mimeTextMarkdown,
		result: ingest.Result{
			Markdown:   "# Wrong extractor",
			SourceType: model.DocSourceText,
		},
	}
	processor := NewAttachmentProcessor([]ingest.Extractor{first, second})

	prepared, err := processor.Prepare([]FileInput{{
		Filename: "notes.md",
		MIME:     "application/octet-stream",
		Data:     []byte("raw notes"),
	}})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if first.calls != 0 || second.calls != 0 {
		t.Fatalf("Prepare performed external extraction: first=%d second=%d", first.calls, second.calls)
	}
	if len(prepared) != 1 || prepared[0].MIME != mimeTextMarkdown ||
		prepared[0].Kind != model.AttachmentKindDocument {
		t.Fatalf("prepared document = %+v", prepared)
	}

	extracted, err := processor.ExtractDocuments(t.Context(), prepared)
	if err != nil {
		t.Fatalf("ExtractDocuments: %v", err)
	}
	if first.calls != 1 || second.calls != 0 {
		t.Fatalf("extractor calls: first=%d second=%d, want 1 and 0", first.calls, second.calls)
	}
	if len(extracted) != 1 || extracted[0].ExtractedMarkdown != "# Extracted" ||
		!bytes.Equal(extracted[0].RawBytes, []byte("raw notes")) {
		t.Fatalf("extracted document = %+v", extracted)
	}
}

type recordingAttachmentExtractor struct {
	mime   string
	result ingest.Result
	calls  int
}

func (e *recordingAttachmentExtractor) CanHandle(mime string) bool {
	return mime == e.mime
}

func (e *recordingAttachmentExtractor) Extract(
	_ context.Context, _ []byte, _ string,
) (ingest.Result, error) {
	e.calls++
	return e.result, nil
}

func encodedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return out.Bytes()
}

func encodedJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewNRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return out.Bytes()
}

func encodedGIF(t *testing.T, width, height, frames int) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	g := &gif.GIF{Config: image.Config{ColorModel: palette, Width: width, Height: height}}
	for i := range frames {
		frame := image.NewPaletted(image.Rect(0, 0, width, height), palette)
		frame.Pix[0] = uint8(i % len(palette))
		g.Image = append(g.Image, frame)
		g.Delay = append(g.Delay, 0)
	}
	var out bytes.Buffer
	if err := gif.EncodeAll(&out, g); err != nil {
		t.Fatalf("encode GIF: %v", err)
	}
	return out.Bytes()
}

func mustBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return decoded
}

func pngHeaderOnly(width, height uint32) []byte {
	out := make([]byte, 33)
	copy(out, "\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(out[8:12], 13)
	copy(out[12:16], "IHDR")
	binary.BigEndian.PutUint32(out[16:20], width)
	binary.BigEndian.PutUint32(out[20:24], height)
	out[24] = 8
	out[25] = 6
	binary.BigEndian.PutUint32(out[29:33], crc32.ChecksumIEEE(out[12:29]))
	return out
}
