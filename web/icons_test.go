package web

import (
	"encoding/json"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	identityColor = "#021c46"
	pngMIME       = "image/png"
)

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

type webManifest struct {
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	Description     string         `json:"description"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	ThemeColor      string         `json:"theme_color"`
	BackgroundColor string         `json:"background_color"`
	Icons           []manifestIcon `json:"icons"`
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	img, format, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if format != "png" {
		t.Fatalf("%s format = %q, want png", path, format)
	}
	return img
}

func assertDimensions(t *testing.T, img image.Image, width, height int) {
	t.Helper()
	if got := img.Bounds().Size(); got.X != width || got.Y != height {
		t.Fatalf("dimensions = %dx%d, want %dx%d", got.X, got.Y, width, height)
	}
}

func assertOpaque(t *testing.T, img image.Image) {
	t.Helper()
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			if alpha != 0xffff {
				t.Fatalf("pixel (%d,%d) alpha = %d, want opaque", x, y, alpha)
			}
		}
	}
}

func assertTransparentCornersPreserveWhiteArtwork(t *testing.T, img image.Image) {
	t.Helper()
	b := img.Bounds()
	for _, point := range []image.Point{
		b.Min,
		{X: b.Max.X - 1, Y: b.Min.Y},
		{X: b.Min.X, Y: b.Max.Y - 1},
		{X: b.Max.X - 1, Y: b.Max.Y - 1},
	} {
		_, _, _, alpha := img.At(point.X, point.Y).RGBA()
		if alpha != 0 {
			t.Fatalf("corner (%d,%d) alpha = %d, want transparent", point.X, point.Y, alpha)
		}
	}

	opaqueWhite := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			red, green, blue, alpha := img.At(x, y).RGBA()
			if alpha == 0xffff && red > 0xe000 && green > 0xe000 && blue > 0xe000 {
				opaqueWhite++
			}
		}
	}
	if opaqueWhite == 0 {
		t.Fatal("regular icon removed internal white artwork")
	}
}

func assertRoadStripePreserved(t *testing.T, img image.Image) {
	t.Helper()
	size := img.Bounds().Dx()
	x := int(float64(size) * 600.0 / 1254.0)
	y := int(float64(size) * 1200.0 / 1254.0)
	assertNearWhitePixel(t, img, x, y)
}

func assertMaskableRoadStripePreserved(t *testing.T, img image.Image) {
	t.Helper()
	size := img.Bounds().Dx()
	artworkSize := int(float64(size)*0.8 + 0.5)
	offset := (size - artworkSize) / 2
	x := offset + int(float64(artworkSize)*600.0/1254.0)
	y := offset + int(float64(artworkSize)*1200.0/1254.0)
	assertNearWhitePixel(t, img, x, y)
}

func assertNearWhitePixel(t *testing.T, img image.Image, x, y int) {
	t.Helper()
	red, green, blue, alpha := img.At(x, y).RGBA()
	if alpha != 0xffff || red < 0xe000 || green < 0xe000 || blue < 0xe000 {
		t.Fatalf("road stripe pixel (%d,%d) = %04x/%04x/%04x/%04x, want opaque near-white",
			x, y, red, green, blue, alpha)
	}
}

func assertMaskableBackground(t *testing.T, img image.Image) {
	t.Helper()
	wantRed, wantGreen, wantBlue := uint32(0x02*0x101), uint32(0x1c*0x101), uint32(0x46*0x101)
	b := img.Bounds()
	padding := b.Dx() / 10
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if x >= b.Min.X+padding && x < b.Max.X-padding &&
				y >= b.Min.Y+padding && y < b.Max.Y-padding {
				continue
			}
			red, green, blue, alpha := img.At(x, y).RGBA()
			if red != wantRed || green != wantGreen || blue != wantBlue || alpha != 0xffff {
				t.Fatalf("padding pixel (%d,%d) = %04x/%04x/%04x/%04x, want %04x/%04x/%04x/ffff",
					x, y, red, green, blue, alpha, wantRed, wantGreen, wantBlue)
			}
		}
	}
}

func TestCommittedIconAssets(t *testing.T) {
	tests := []struct {
		path         string
		width        int
		height       int
		regular      bool
		maskable     bool
		mustBeOpaque bool
	}{
		{path: "static/icons/kadence-master.png", width: 1254, height: 1254, mustBeOpaque: true},
		{path: "static/favicon.png", width: 32, height: 32, regular: true},
		{path: "static/apple-touch-icon.png", width: 180, height: 180, mustBeOpaque: true},
		{path: "static/icons/icon-192.png", width: 192, height: 192, regular: true},
		{path: "static/icons/icon-512.png", width: 512, height: 512, regular: true},
		{path: "static/icons/icon-maskable-192.png", width: 192, height: 192, maskable: true, mustBeOpaque: true},
		{path: "static/icons/icon-maskable-512.png", width: 512, height: 512, maskable: true, mustBeOpaque: true},
	}

	for _, tt := range tests {
		t.Run(filepath.Base(tt.path), func(t *testing.T) {
			img := decodePNG(t, tt.path)
			assertDimensions(t, img, tt.width, tt.height)
			if tt.regular {
				assertTransparentCornersPreserveWhiteArtwork(t, img)
				if tt.width >= 192 {
					assertRoadStripePreserved(t, img)
				}
			}
			if tt.mustBeOpaque {
				assertOpaque(t, img)
			}
			if tt.maskable {
				assertMaskableBackground(t, img)
				assertMaskableRoadStripePreserved(t, img)
			}
		})
	}
}

func TestManifestDefinesKadenceIdentity(t *testing.T) {
	data, err := os.ReadFile("static/manifest.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var manifest webManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	if manifest.Name != "Kadence" || manifest.ShortName != "Kadence" {
		t.Fatalf("names = %q/%q, want Kadence/Kadence", manifest.Name, manifest.ShortName)
	}
	if manifest.Description != "Self-hostable, multi-user AI coach." {
		t.Fatalf("description = %q", manifest.Description)
	}
	if manifest.StartURL != "/" || manifest.Scope != "/" || manifest.Display != "standalone" {
		t.Fatalf("navigation/display = %q/%q/%q, want / / standalone",
			manifest.StartURL, manifest.Scope, manifest.Display)
	}
	if manifest.ThemeColor != identityColor || manifest.BackgroundColor != identityColor {
		t.Fatalf("colors = %q/%q, want %s", manifest.ThemeColor, manifest.BackgroundColor, identityColor)
	}

	wantIcons := []manifestIcon{
		{Src: "/icons/icon-192.png", Sizes: "192x192", Type: pngMIME, Purpose: "any"},
		{Src: "/icons/icon-512.png", Sizes: "512x512", Type: pngMIME, Purpose: "any"},
		{Src: "/icons/icon-maskable-192.png", Sizes: "192x192", Type: pngMIME, Purpose: "maskable"},
		{Src: "/icons/icon-maskable-512.png", Sizes: "512x512", Type: pngMIME, Purpose: "maskable"},
	}
	if len(manifest.Icons) != len(wantIcons) {
		t.Fatalf("icons = %d, want %d", len(manifest.Icons), len(wantIcons))
	}
	for i, want := range wantIcons {
		if manifest.Icons[i] != want {
			t.Fatalf("icons[%d] = %+v, want %+v", i, manifest.Icons[i], want)
		}
	}
}

func TestDocumentHeadReferencesIdentityAssets(t *testing.T) {
	data, err := os.ReadFile("src/app.html")
	if err != nil {
		t.Fatalf("read app.html: %v", err)
	}
	head := string(data)

	for _, reference := range []string{
		`<link rel="icon" type="image/png" sizes="32x32" href="/favicon.png" />`,
		`<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png" />`,
		`<link rel="manifest" href="/manifest.json" />`,
		`<meta name="theme-color" content="#021c46" />`,
	} {
		if !strings.Contains(head, reference) {
			t.Errorf("app.html missing %s", reference)
		}
	}
}
