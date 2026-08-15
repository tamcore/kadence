package ingest

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// minPageImageAxis is the smallest axis (in pixels) an embedded image may have
// and still count as page content rather than an icon or a logo.
const minPageImageAxis = 400

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
// of the PDF in data, ordered by page number and capped at opts.MaxPages.
//
// Selection keeps at most one image per page because a coverage threshold alone
// cannot separate content tables from photographs: on the reference training
// plan a pace-chart raster covers 0.205 of its page while decorative exercise
// photos cover 0.217.
//
// pdfcpu reports dimensions and payloads through two different calls, so this
// runs two passes: api.Images yields stubs with dimensions but no bytes, and
// api.ExtractImagesRaw yields bytes but leaves dimensions at zero. Results are
// matched on the object number.
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
		selected = selected[:opts.MaxPages]
	}

	payloads, err := extractPayloads(data, selected)
	if err != nil {
		return nil, err
	}

	out = make([]PageImage, 0, len(selected))
	for _, stub := range selected {
		payload, ok := payloads[stub.objNr]
		if !ok {
			continue
		}
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

// extractPayloads fetches raw bytes for the selected images, keyed by object
// number.
func extractPayloads(data []byte, selected []imageStub) (map[int]imagePayload, error) {
	pages := make([]string, 0, len(selected))
	seenPage := map[int]struct{}{}
	wanted := map[int]struct{}{}
	for _, stub := range selected {
		wanted[stub.objNr] = struct{}{}
		if _, ok := seenPage[stub.page]; ok {
			continue
		}
		seenPage[stub.page] = struct{}{}
		pages = append(pages, strconv.Itoa(stub.page))
	}

	rawPages, err := api.ExtractImagesRaw(bytes.NewReader(data), pages, nil)
	if err != nil {
		return nil, fmt.Errorf("extract images: %w", err)
	}

	out := map[int]imagePayload{}
	for _, page := range rawPages {
		for objNr, img := range page {
			if _, ok := wanted[objNr]; !ok || img.Reader == nil {
				continue
			}
			mime := mimeForPDFImageType(img.FileType)
			if mime == "" {
				continue
			}
			payload, readErr := io.ReadAll(img)
			if readErr != nil {
				return nil, fmt.Errorf("read image object %d: %w", objNr, readErr)
			}
			if len(payload) == 0 {
				continue
			}
			out[objNr] = imagePayload{data: payload, mime: mime}
		}
	}
	return out, nil
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
// content, returning its candidate record when it is.
func qualifyImage(
	objNr int, img pdfmodel.Image, dims []types.Dim, opts PageImageOptions,
) (imageStub, bool) {
	if img.IsImgMask || img.PageNr < 1 || img.PageNr > len(dims) {
		return imageStub{}, false
	}
	if img.Width < minPageImageAxis || img.Height < minPageImageAxis {
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

// mimeForPDFImageType maps pdfcpu's FileType to a MIME type, returning "" for
// types the chat attachment path cannot carry as a native image.
func mimeForPDFImageType(fileType string) string {
	switch fileType {
	case "png":
		return mimeImagePNG
	case "jpg", "jpeg":
		return mimeImageJPEG
	default:
		return ""
	}
}
