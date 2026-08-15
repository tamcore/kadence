package ingest

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"sort"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"golang.org/x/image/tiff"
)

// minPageImageAxis is the smallest axis (in pixels) an embedded image may have
// and still count as page content rather than an icon or a logo.
const minPageImageAxis = 400

// Upper bounds mirroring the chat attachment limits, so a selected page image
// is always something the provider image path can carry.
const (
	maxPageImageAxis   = 8192
	maxPageImagePixels = 8 << 20
)

// maxPageImageBytes bounds a single image read, so a malformed or hostile
// stream cannot drive an unbounded allocation.
const maxPageImageBytes = 64 << 20

// defaultMaxTotalBytes bounds the combined payload of one extraction when the
// caller supplies no budget.
const defaultMaxTotalBytes = 24 << 20

// nominalRenderDPI is the resolution a page is notionally rendered at when
// judging how much of it an embedded image covers.
const nominalRenderDPI = 150

// pointsPerInch converts PDF user-space units to inches.
const pointsPerInch = 72

// PageImage is one embedded image selected as page content.
type PageImage struct {
	Page   int
	Data   []byte
	MIME   string
	Width  int
	Height int
}

// PageImageOptions tunes which embedded images qualify as page content.
type PageImageOptions struct {
	// MinCoverage is the minimum share of a nominal 150dpi page render an
	// image's pixel count must reach.
	MinCoverage float64
	// MaxPages caps how many images are returned, lowest page number first.
	MaxPages int
	// MaxTotalBytes caps the combined payload returned. Zero means
	// defaultMaxTotalBytes.
	MaxTotalBytes int
}

// imageStub is a candidate identified from pdfcpu's stub listing, which carries
// dimensions but no payload.
type imageStub struct {
	page, objNr, width, height int
	coverage                   float64
}

type imagePayload struct {
	data []byte
	mime string
}

// ExtractPageImages returns the largest qualifying embedded image for each page
// of the PDF in data, ordered by page number and capped at opts.MaxPages and
// opts.MaxTotalBytes.
//
// Selection keeps at most one image per page because a coverage threshold alone
// cannot separate content tables from photographs: on the reference training
// plan a pace-chart raster covers 0.205 of its page while decorative exercise
// photos cover 0.217.
//
// pdfcpu reports dimensions and payloads through two different calls, so this
// runs two passes: api.Images yields stubs with dimensions but no bytes, and
// api.ExtractImagesRaw yields bytes but leaves dimensions at zero. Results are
// matched on the object number. The second pass runs one page at a time so peak
// memory stays bounded by a single page's images rather than the whole
// selection.
//
// pdfcpu can panic on malformed input, so the body is panic-guarded and the
// panic is reported as an error, matching PDFExtractor.Extract.
func ExtractPageImages(data []byte, opts PageImageOptions) (out []PageImage, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("pdf page-image panic: %v", r)
		}
	}()

	if opts.MaxPages <= 0 {
		return nil, nil
	}
	if opts.MaxTotalBytes <= 0 {
		opts.MaxTotalBytes = defaultMaxTotalBytes
	}

	dims, err := api.PageDims(bytes.NewReader(data), nil)
	if err != nil {
		return nil, fmt.Errorf("page dims: %w", err)
	}

	stubPages, err := api.Images(bytes.NewReader(data), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}

	selected := selectLargestPerPage(stubPages, dims, opts)
	if len(selected) == 0 {
		return nil, nil
	}
	if len(selected) > opts.MaxPages {
		slog.Info("pdf page-images truncated to the page cap",
			"selected", len(selected), "cap", opts.MaxPages)
		selected = selected[:opts.MaxPages]
	}

	return collectPayloads(data, selected, opts.MaxTotalBytes)
}

// collectPayloads fetches bytes for each selected image, stopping once the
// combined payload would exceed budget.
func collectPayloads(data []byte, selected []imageStub, budget int) ([]PageImage, error) {
	out := make([]PageImage, 0, len(selected))
	total := 0
	for _, stub := range selected {
		payload, found, err := extractPayload(data, stub)
		if err != nil {
			return nil, err
		}
		if !found {
			// Never silent: a selected image that yields no usable payload is
			// page content the model will not get to see.
			slog.Warn("pdf page-image selected but not extractable",
				"page", stub.page, "object", stub.objNr)
			continue
		}
		if total+len(payload.data) > budget {
			slog.Info("pdf page-images truncated to the byte budget",
				"page", stub.page, "budget", budget, "kept", len(out))
			break
		}
		total += len(payload.data)
		out = append(out, PageImage{
			Page:   stub.page,
			Data:   payload.data,
			MIME:   payload.mime,
			Width:  stub.width,
			Height: stub.height,
		})
	}
	return out, nil
}

// extractPayload fetches the bytes for one selected image. It extracts a single
// page at a time so a page carrying dozens of images cannot inflate peak memory
// beyond that one page.
func extractPayload(data []byte, stub imageStub) (imagePayload, bool, error) {
	rawPages, err := api.ExtractImagesRaw(
		bytes.NewReader(data), []string{strconv.Itoa(stub.page)}, nil,
	)
	if err != nil {
		return imagePayload{}, false, fmt.Errorf("extract images on page %d: %w", stub.page, err)
	}
	for _, page := range rawPages {
		img, ok := page[stub.objNr]
		if !ok || img.Reader == nil {
			continue
		}
		payload, readErr := io.ReadAll(io.LimitReader(img, maxPageImageBytes))
		if readErr != nil {
			return imagePayload{}, false, fmt.Errorf(
				"read image object %d: %w", stub.objNr, readErr)
		}
		if len(payload) == 0 {
			return imagePayload{}, false, nil
		}
		return normalizePayload(payload, img.FileType, stub)
	}
	return imagePayload{}, false, nil
}

// normalizePayload maps a pdfcpu payload to something the provider image path
// accepts, transcoding TIFF (which pdfcpu emits for CCITT-encoded scans) to PNG
// rather than dropping it.
func normalizePayload(
	payload []byte, fileType string, stub imageStub,
) (imagePayload, bool, error) {
	switch fileType {
	case "png":
		return imagePayload{data: payload, mime: mimeImagePNG}, true, nil
	case "jpg", "jpeg":
		return imagePayload{data: payload, mime: mimeImageJPEG}, true, nil
	case "tif", "tiff":
		decoded, decodeErr := tiff.Decode(bytes.NewReader(payload))
		if decodeErr != nil {
			slog.Warn("pdf page-image tiff decode failed",
				"page", stub.page, "object", stub.objNr, "err", decodeErr)
			return imagePayload{}, false, nil
		}
		var buf bytes.Buffer
		if encodeErr := png.Encode(&buf, decoded); encodeErr != nil {
			return imagePayload{}, false, fmt.Errorf(
				"re-encode tiff image object %d: %w", stub.objNr, encodeErr)
		}
		return imagePayload{data: buf.Bytes(), mime: mimeImagePNG}, true, nil
	default:
		slog.Warn("pdf page-image has an unsupported encoding",
			"page", stub.page, "object", stub.objNr, "type", fileType)
		return imagePayload{}, false, nil
	}
}

// selectLargestPerPage keeps the single highest-coverage qualifying image per
// page, ordered by page number.
func selectLargestPerPage(
	stubPages []map[int]pdfmodel.Image, dims []types.Dim, opts PageImageOptions,
) []imageStub {
	best := map[int]imageStub{}
	for _, page := range stubPages {
		for objNr, img := range page {
			stub, ok := qualifyImage(objNr, img, dims, opts)
			if !ok {
				continue
			}
			if current, exists := best[stub.page]; exists && current.coverage >= stub.coverage {
				continue
			}
			best[stub.page] = stub
		}
	}

	out := make([]imageStub, 0, len(best))
	for _, stub := range best {
		out = append(out, stub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].page < out[j].page })
	return out
}

// qualifyImage reports whether one embedded image is large enough to be page
// content and small enough for the provider image path, returning its candidate
// record when it is.
func qualifyImage(
	objNr int, img pdfmodel.Image, dims []types.Dim, opts PageImageOptions,
) (imageStub, bool) {
	if img.IsImgMask || img.PageNr < 1 || img.PageNr > len(dims) {
		return imageStub{}, false
	}
	if img.Width < minPageImageAxis || img.Height < minPageImageAxis {
		return imageStub{}, false
	}
	if img.Width > maxPageImageAxis || img.Height > maxPageImageAxis {
		return imageStub{}, false
	}
	if img.Width > maxPageImagePixels/img.Height {
		return imageStub{}, false
	}
	dim := dims[img.PageNr-1]
	nominalW := dim.Width / pointsPerInch * nominalRenderDPI
	nominalH := dim.Height / pointsPerInch * nominalRenderDPI
	if nominalW <= 0 || nominalH <= 0 {
		return imageStub{}, false
	}
	coverage := (float64(img.Width) * float64(img.Height)) / (nominalW * nominalH)
	if coverage < opts.MinCoverage {
		return imageStub{}, false
	}
	return imageStub{
		page: img.PageNr, objNr: objNr,
		width: img.Width, height: img.Height, coverage: coverage,
	}, true
}
