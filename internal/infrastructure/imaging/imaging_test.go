package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/disintegration/imaging"
	xwebp "golang.org/x/image/webp"
)

// specMax maps a spec name to its 1x fit target, mirroring DefaultSpecs.
func specMax(name string) int {
	for _, s := range DefaultSpecs {
		if s.Name == name {
			return s.MaxSize
		}
	}
	return 0
}

// newFixtureRGBA builds an image with nonzero, non-uniform content so encoder
// round-trips are meaningfully verified.
func newFixtureRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), uint8((x + y) % 256), 255})
		}
	}
	return img
}

func encodeFixture(t *testing.T, format string, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatalf("encode jpeg fixture: %v", err)
		}
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("encode png fixture: %v", err)
		}
	case "webp":
		data, err := encodeWebP(img)
		if err != nil {
			t.Skipf("webp encoder unavailable: %v", err)
		}
		return data
	default:
		t.Fatalf("unknown fixture format %q", format)
	}
	return buf.Bytes()
}

// decodeVariant decodes encoded variant data using the decoder matching the
// declared format, mirroring what the media pipeline would serve.
func decodeVariant(t *testing.T, v Variant) image.Image {
	t.Helper()
	r := bytes.NewReader(v.Data)
	var (
		img image.Image
		err error
	)
	if v.Format == FormatWebP {
		img, err = xwebp.Decode(r)
	} else {
		img, err = imaging.Decode(r)
	}
	if err != nil {
		t.Fatalf("decode %s %dx variant: %v", v.Spec, v.Density, err)
	}
	return img
}

func TestDefaultSpecsContract(t *testing.T) {
	if len(DefaultSpecs) != 3 {
		t.Fatalf("expected 3 default specs, got %d", len(DefaultSpecs))
	}
	for _, s := range DefaultSpecs {
		if s.Name == "" || s.MaxSize <= 0 {
			t.Fatalf("invalid spec %+v", s)
		}
	}
}

// TestProcessJPEGProducesFullVariantSet drives a 400x300 JPEG through Process
// and checks the shape, bounds, aspect ratio, and decodability of every
// variant across the 3 specs × 3 densities grid.
func TestProcessJPEGProducesFullVariantSet(t *testing.T) {
	fixture := encodeFixture(t, "jpeg", newFixtureRGBA(400, 300))

	res, err := Process(bytes.NewReader(fixture), "image/jpeg")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.Width != 400 || res.Height != 300 {
		t.Fatalf("result dims = %dx%d, want 400x300", res.Width, res.Height)
	}
	if len(res.Variants) != len(DefaultSpecs)*3 {
		t.Fatalf("expected %d variants, got %d", len(DefaultSpecs)*3, len(res.Variants))
	}
	if res.Format != FormatWebP && res.Format != FormatJPEG {
		t.Fatalf("result format %q must be webp or jpeg", res.Format)
	}

	for _, v := range res.Variants {
		target := specMax(v.Spec) * v.Density
		if !(v.Spec == "thumb" || v.Spec == "card" || v.Spec == "detail") {
			t.Fatalf("unexpected spec %q", v.Spec)
		}
		if v.Density < 1 || v.Density > 3 {
			t.Fatalf("unexpected density %d", v.Density)
		}
		if v.Width <= 0 || v.Height <= 0 {
			t.Fatalf("%s %dx: empty bounds %dx%d", v.Spec, v.Density, v.Width, v.Height)
		}
		if v.Width > target || v.Height > target {
			t.Fatalf("%s %dx: %dx%d exceeds target %d", v.Spec, v.Density, v.Width, v.Height, target)
		}
		// Fit preserves aspect ratio (source 4:3); allow 1px rounding.
		if a, b := float64(v.Width)/400, float64(v.Height)/300; a > b+0.01 || b > a+0.01 {
			t.Fatalf("%s %dx: aspect ratio distorted: %dx%d", v.Spec, v.Density, v.Width, v.Height)
		}
		if len(v.Data) == 0 {
			t.Fatalf("%s %dx: empty encoded data", v.Spec, v.Density)
		}
		dec := decodeVariant(t, v)
		if b := dec.Bounds(); b.Dx() != v.Width || b.Dy() != v.Height {
			t.Fatalf("%s %dx: decoded %dx%d, want %dx%d", v.Spec, v.Density, b.Dx(), b.Dy(), v.Width, v.Height)
		}
	}
}

// TestProcessPNGSmallImageNeverUpscales verifies that a fixture smaller than
// every 1x spec target is returned unmagnified (Fit only scales down).
func TestProcessPNGSmallImageNeverUpscales(t *testing.T) {
	fixture := encodeFixture(t, "png", newFixtureRGBA(120, 80))

	res, err := Process(bytes.NewReader(fixture), "image/png")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(res.Variants) != len(DefaultSpecs)*3 {
		t.Fatalf("expected %d variants, got %d", len(DefaultSpecs)*3, len(res.Variants))
	}
	for _, v := range res.Variants {
		if v.Width != 120 || v.Height != 80 {
			t.Fatalf("%s %dx: upscaled to %dx%d, want 120x80", v.Spec, v.Density, v.Width, v.Height)
		}
	}
}

// TestProcessWebPInput exercises the x/image decode path for webp uploads.
func TestProcessWebPInput(t *testing.T) {
	fixture := encodeFixture(t, "webp", newFixtureRGBA(64, 48))

	res, err := Process(bytes.NewReader(fixture), "image/webp")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.Width != 64 || res.Height != 48 {
		t.Fatalf("result dims = %dx%d, want 64x48", res.Width, res.Height)
	}
	if len(res.Variants) == 0 {
		t.Fatal("expected at least one variant")
	}
}

func TestProcessRejectsUnsupportedContentType(t *testing.T) {
	_, err := Process(bytes.NewReader([]byte("hello")), "text/plain")
	if err == nil {
		t.Fatal("expected error for text/plain input")
	}
}

func TestProcessRejectsCorruptImage(t *testing.T) {
	_, err := Process(bytes.NewReader([]byte("this is definitely not a jpeg")), "image/jpeg")
	if err == nil {
		t.Fatal("expected error for corrupt jpeg input")
	}
}

func TestProcessRejectsEmptyInput(t *testing.T) {
	_, err := Process(bytes.NewReader(nil), "image/png")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}
