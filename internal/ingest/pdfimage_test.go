package ingest

import (
	"os"
	"testing"

	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func loadPageImageFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/imagepage.pdf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// The fixture has two qualifying images on page 1 (1000x1400 and 600x800) and a
// single 100x100 image on page 2, so it exercises both the per-page winner and
// the axis floor.
func TestExtractPageImagesKeepsLargestQualifyingImagePerPage(t *testing.T) {
	// Arrange
	data := loadPageImageFixture(t)

	// Act
	images, err := ExtractPageImages(data, PageImageOptions{MinCoverage: 0.12, MaxPages: 20})

	// Assert
	if err != nil {
		t.Fatalf("ExtractPageImages() error = %v, want nil", err)
	}
	if len(images) != 1 {
		t.Fatalf("got %d images, want 1 (page 1 winner only; page 2 is below the axis floor)", len(images))
	}
	got := images[0]
	if got.Page != 1 {
		t.Errorf("Page = %d, want 1", got.Page)
	}
	if got.Width != 1000 || got.Height != 1400 {
		t.Errorf("dimensions = %dx%d, want 1000x1400 (the larger of the two images on page 1)",
			got.Width, got.Height)
	}
	if got.MIME != mimeImagePNG {
		t.Errorf("MIME = %q, want %q", got.MIME, mimeImagePNG)
	}
	if len(got.Data) == 0 {
		t.Error("Data is empty, want decoded image bytes")
	}
}

func TestExtractPageImagesRespectsMaxPages(t *testing.T) {
	// Arrange
	data := loadPageImageFixture(t)

	// Act
	images, err := ExtractPageImages(data, PageImageOptions{MinCoverage: 0.12, MaxPages: 0})

	// Assert
	if err != nil {
		t.Fatalf("ExtractPageImages() error = %v, want nil", err)
	}
	if len(images) != 0 {
		t.Fatalf("got %d images, want 0 when MaxPages is 0", len(images))
	}
}

func TestExtractPageImagesRespectsByteBudget(t *testing.T) {
	// Arrange
	data := loadPageImageFixture(t)

	// Act
	images, err := ExtractPageImages(data, PageImageOptions{
		MinCoverage: 0.12, MaxPages: 20, MaxTotalBytes: 1,
	})

	// Assert
	if err != nil {
		t.Fatalf("ExtractPageImages() error = %v, want nil", err)
	}
	if len(images) != 0 {
		t.Fatalf("got %d images, want 0 when the byte budget admits nothing", len(images))
	}
}

func TestExtractPageImagesSkipsImagesBelowCoverage(t *testing.T) {
	// Arrange
	data := loadPageImageFixture(t)

	// Act
	images, err := ExtractPageImages(data, PageImageOptions{MinCoverage: 0.99, MaxPages: 20})

	// Assert
	if err != nil {
		t.Fatalf("ExtractPageImages() error = %v, want nil", err)
	}
	if len(images) != 0 {
		t.Fatalf("got %d images, want 0 when the coverage floor excludes every image", len(images))
	}
}

func TestExtractPageImagesRejectsMalformedInput(t *testing.T) {
	// Arrange
	data := []byte("this is not a pdf")

	// Act
	_, err := ExtractPageImages(data, PageImageOptions{MinCoverage: 0.12, MaxPages: 20})

	// Assert
	if err == nil {
		t.Fatal("ExtractPageImages() error = nil, want an error for malformed input")
	}
}

func TestQualifyImageRejectsOversizedImages(t *testing.T) {
	// Arrange
	dims := []types.Dim{{Width: 600, Height: 800}}
	oversized := pdfmodel.Image{PageNr: 1, Width: maxPageImageAxis + 1, Height: 500}

	// Act
	_, ok := qualifyImage(1, oversized, dims, PageImageOptions{MinCoverage: 0.0})

	// Assert
	if ok {
		t.Fatal("qualifyImage() accepted an image beyond the axis ceiling, want rejection")
	}
}

func TestQualifyImageRejectsImageMasks(t *testing.T) {
	// Arrange
	dims := []types.Dim{{Width: 600, Height: 800}}
	mask := pdfmodel.Image{PageNr: 1, Width: 1000, Height: 1400, IsImgMask: true}

	// Act
	_, ok := qualifyImage(1, mask, dims, PageImageOptions{MinCoverage: 0.0})

	// Assert
	if ok {
		t.Fatal("qualifyImage() accepted an image mask, want rejection")
	}
}

// Extraction must not depend on a writable HOME: the production container runs
// read-only as nonroot, where pdfcpu's config-dir creation fails and takes
// every extraction with it.
func TestPageImageExtractionDoesNotUseAConfigDir(t *testing.T) {
	// Arrange / Act: package init disables it.
	got := pdfmodel.ConfigPath

	// Assert
	if got != "disable" {
		t.Fatalf("pdfcpu ConfigPath = %q, want %q", got, "disable")
	}
}

func TestExtractPageImagesWorksWithoutWritableHome(t *testing.T) {
	// Arrange: point HOME at a path that cannot be created.
	t.Setenv("HOME", "/nonexistent/read-only")
	t.Setenv("XDG_CONFIG_HOME", "/nonexistent/read-only")
	data := loadPageImageFixture(t)

	// Act
	images, err := ExtractPageImages(data, PageImageOptions{MinCoverage: 0.12, MaxPages: 20})

	// Assert
	if err != nil {
		t.Fatalf("ExtractPageImages() error = %v, want nil with an unwritable HOME", err)
	}
	if len(images) != 1 {
		t.Fatalf("got %d images, want 1", len(images))
	}
}
