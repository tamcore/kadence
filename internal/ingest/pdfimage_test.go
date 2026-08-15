package ingest

import (
	"os"
	"testing"
)

func loadPageImageFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/imagepage.pdf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

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
		t.Fatalf("got %d images, want 1 (page 1 only; the 100x100 images are below the axis floor)", len(images))
	}
	got := images[0]
	if got.Page != 1 {
		t.Errorf("Page = %d, want 1", got.Page)
	}
	if got.Width != 1000 || got.Height != 1400 {
		t.Errorf("dimensions = %dx%d, want 1000x1400", got.Width, got.Height)
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
