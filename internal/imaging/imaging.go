// Package imaging turns any accepted upload (JPEG, PNG, HEIC/HEIF) into one normalized JPEG:
// EXIF orientation applied, long edge <= MaxEdge, quality Quality, and no metadata at all
// (re-encoding with image/jpeg writes no EXIF/GPS/XMP). That JPEG is what is stored, sent to
// the vision model and shown in the UI.
package imaging

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
)

const (
	MaxEdge   = 2000
	Quality   = 80
	MaxPixels = 50_000_000 // decompression-bomb guard on the source image
	// MaxDecodeBytes bounds the decoded source bitmap (pixels x bytes per pixel), so a 16-bit
	// PNG or HEIC gets half the pixel budget of an 8-bit one.
	MaxDecodeBytes = 200 << 20
	// MaxConcurrent normalizations at once (each can hold a large decoded bitmap).
	MaxConcurrent = 2
)

var sem = make(chan struct{}, MaxConcurrent)

// bytesPerPixel is the in-memory size of one decoded pixel for a color model.
func bytesPerPixel(m color.Model) int64 {
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	case color.YCbCrModel, color.NYCbCrAModel:
		return 4 // planes plus the RGBA copy made when flattening
	default:
		return 4
	}
}

// checkSize rejects images whose decoded size would exceed the pixel or byte budget.
func checkSize(w, h int, bpp int64) error {
	px := int64(w) * int64(h)
	if w <= 0 || h <= 0 || px > MaxPixels || px*bpp > MaxDecodeBytes {
		return ErrTooLarge
	}
	return nil
}

// Kind of an upload, sniffed from magic bytes.
type Kind string

const (
	JPEG Kind = "jpeg"
	PNG  Kind = "png"
	HEIC Kind = "heic"
)

var (
	ErrUnsupported   = errors.New("unsupported image type (use JPEG, PNG or HEIC)")
	ErrCorrupt       = errors.New("corrupt or truncated image")
	ErrTooLarge      = errors.New("image dimensions out of range")
	ErrNoHEICDecoder = errors.New("HEIC support is not available on this server")
)

// Detect sniffs JPEG/PNG signatures and the ISO-BMFF ftyp box of HEIC/HEIF.
func Detect(b []byte) Kind {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return JPEG
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return PNG
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		switch string(b[8:12]) {
		case "heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1", "heif":
			return HEIC
		}
	}
	return ""
}

// HEICDecoder decodes HEIC/HEIF to an image with its orientation transforms applied.
type HEICDecoder interface {
	Decode(ctx context.Context, data []byte) (image.Image, error)
	Name() string
}

// Result is the normalized JPEG.
type Result struct {
	JPEG          []byte
	Width, Height int
	Source        Kind
}

// Normalize converts data into the stored JPEG. heic may be nil (HEIC then fails with ErrNoHEICDecoder).
func Normalize(ctx context.Context, data []byte, heic HEICDecoder) (*Result, error) {
	kind := Detect(data)
	if kind == "" {
		return nil, ErrUnsupported
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var img image.Image
	switch kind {
	case JPEG, PNG:
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, ErrCorrupt
		}
		if err := checkSize(cfg.Width, cfg.Height, bytesPerPixel(cfg.ColorModel)); err != nil {
			return nil, err
		}
		if img, _, err = image.Decode(bytes.NewReader(data)); err != nil {
			return nil, ErrCorrupt
		}
		if kind == JPEG {
			img = applyOrientation(img, exifOrientation(data))
		}
	case HEIC:
		if heic == nil {
			return nil, ErrNoHEICDecoder
		}
		// check the declared size (ispe) before spawning the decoder; 10/12-bit HEIC may decode
		// to 16-bit, so budget 8 bytes per pixel
		w, h, ok := heifDimensions(data)
		if !ok {
			return nil, ErrCorrupt
		}
		if err := checkSize(w, h, 8); err != nil {
			return nil, err
		}
		var err error
		if img, err = heic.Decode(ctx, data); err != nil {
			if errors.Is(err, ErrTooLarge) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		b := img.Bounds()
		if b.Dx() <= 0 || b.Dy() <= 0 || int64(b.Dx())*int64(b.Dy()) > MaxPixels {
			return nil, ErrTooLarge
		}
	default:
		return nil, ErrUnsupported
	}
	img = fit(img, MaxEdge)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, flatten(img), &jpeg.Options{Quality: Quality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	b := img.Bounds()
	return &Result{JPEG: out.Bytes(), Width: b.Dx(), Height: b.Dy(), Source: kind}, nil
}

// fit scales img down so its long edge is <= max (never up).
func fit(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := w
	if h > long {
		long = h
	}
	if long <= max {
		return img
	}
	nw, nh := w*max/long, h*max/long
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// flatten composites transparency onto white (JPEG has no alpha).
func flatten(img image.Image) image.Image {
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		return img
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// heifDimensions returns the largest image spatial extent ("ispe" property) declared in a
// HEIF/HEIC file: the primary image (or its grid) is the largest. ok=false if none is found.
func heifDimensions(b []byte) (w, h int, ok bool) {
	var best int64
	for i := 4; i+16 <= len(b); i++ {
		if b[i] != 'i' || string(b[i:i+4]) != "ispe" {
			continue
		}
		size := binary.BigEndian.Uint32(b[i-4 : i])
		if size < 20 {
			continue
		}
		bw, bh := binary.BigEndian.Uint32(b[i+8:i+12]), binary.BigEndian.Uint32(b[i+12:i+16])
		if bw > 1<<20 || bh > 1<<20 { // absurd: treat as too large rather than overflow
			return 1 << 20, 1 << 20, true
		}
		if px := int64(bw) * int64(bh); px > best {
			best, w, h, ok = px, int(bw), int(bh), true
		}
	}
	return
}
