// Package imaging produces the variant set for uploaded media. Output is WebP
// via libwebp (cgo) with a JPEG fallback if the encoder is unavailable at
// runtime; decoding supports JPEG/PNG (stdlib) and WebP (x/image).
package imaging

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/kolesa-team/go-webp/encoder"
	"github.com/kolesa-team/go-webp/webp"
	xwebp "golang.org/x/image/webp"
)

const (
	FormatWebP = "webp"
	FormatJPEG = "jpeg"
)

// Spec is a named output size. MaxSize is the fit target in the longest
// dimension at 1x density; density scales it (2x, 3x).
type Spec struct {
	Name    string
	MaxSize int
}

var DefaultSpecs = []Spec{
	{Name: "thumb", MaxSize: 256},
	{Name: "card", MaxSize: 640},
	{Name: "detail", MaxSize: 1280},
}

// Variant is one encoded output.
type Variant struct {
	Spec    string
	Density int
	Format  string
	Width   int
	Height  int
	Data    []byte
}

// Result is the outcome of processing one upload.
type Result struct {
	Width    int
	Height   int
	Format   string
	Variants []Variant
}

// Process decodes src (contentType: image/jpeg, image/png, image/webp), fits
// each spec, and encodes the variant set. The returned Format is what was
// actually produced (webp, falling back to jpeg).
func Process(r io.Reader, contentType string) (*Result, error) {
	img, err := decode(r, contentType)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	b := img.Bounds()
	res := &Result{Width: b.Dx(), Height: b.Dy(), Format: FormatWebP}

	for _, spec := range DefaultSpecs {
		for _, density := range []int{1, 2, 3} {
			target := spec.MaxSize * density
			fit := imaging.Fit(img, target, target, imaging.Lanczos)
			data, format, err := encode(fit)
			if err != nil {
				return nil, fmt.Errorf("encode %s %dx: %w", spec.Name, density, err)
			}
			if format == FormatJPEG {
				res.Format = FormatJPEG
			}
			fb := fit.Bounds()
			res.Variants = append(res.Variants, Variant{
				Spec:    spec.Name,
				Density: density,
				Format:  format,
				Width:   fb.Dx(),
				Height:  fb.Dy(),
				Data:    data,
			})
		}
	}
	return res, nil
}

func decode(r io.Reader, contentType string) (image.Image, error) {
	if strings.HasPrefix(contentType, "image/webp") {
		return xwebp.Decode(r)
	}
	// imaging registers png/jpeg/gif decoders internally.
	return imaging.Decode(r, imaging.AutoOrientation(true))
}

// encode renders img to a lossy WebP at quality 80, falling back to JPEG if
// libwebp is unavailable at runtime.
func encode(img image.Image) ([]byte, string, error) {
	if data, err := encodeWebP(img); err == nil {
		return data, FormatWebP, nil
	}
	var buf bytes.Buffer
	if err := imaging.Encode(&buf, img, imaging.JPEG, imaging.JPEGQuality(80)); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), FormatJPEG, nil
}

func encodeWebP(img image.Image) ([]byte, error) {
	opts, err := encoder.NewLossyEncoderOptions(encoder.PresetPhoto, 80)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := webp.Encode(&buf, img, opts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
