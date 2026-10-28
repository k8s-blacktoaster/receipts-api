package imaging

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func solid(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{40, 40, 40, 255}
			if x < w/4 {
				c = color.RGBA{230, 20, 20, 255} // red marker on the left edge
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// exifJPEG returns a JPEG with an APP1 Exif segment carrying Orientation and a GPS IFD with a
// recognizable latitude ref, like a phone photo.
func exifJPEG(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, solid(w, h), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	tiff := []byte("II*\x00")
	tiff = le.AppendUint32(tiff, 8)
	// IFD0: 2 entries (Orientation, GPSInfo pointer)
	tiff = le.AppendUint16(tiff, 2)
	tiff = append(tiff, ifdEntry(le, 0x0112, 3, 1, uint32(orientation))...)
	gpsOff := uint32(8 + 2 + 2*12 + 4)
	tiff = append(tiff, ifdEntry(le, 0x8825, 4, 1, gpsOff)...)
	tiff = le.AppendUint32(tiff, 0)
	// GPS IFD: GPSLatitudeRef = "N" plus a marker string in the maker area
	tiff = le.AppendUint16(tiff, 1)
	tiff = append(tiff, ifdEntry(le, 0x0001, 2, 2, uint32('N'))...)
	tiff = le.AppendUint32(tiff, 0)
	tiff = append(tiff, []byte("GPS-SECRET-LOCATION")...)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1}
	seg = binary.BigEndian.AppendUint16(seg, uint16(len(app1)+2))
	seg = append(seg, app1...)
	b := enc.Bytes()
	out := append([]byte{}, b[:2]...)
	out = append(out, seg...)
	return append(out, b[2:]...)
}

func ifdEntry(bo binary.AppendByteOrder, tag, typ uint16, count, val uint32) []byte {
	e := bo.AppendUint16(nil, tag)
	e = bo.AppendUint16(e, typ)
	e = bo.AppendUint32(e, count)
	return bo.AppendUint32(e, val)
}

func TestNormalizeStripsEXIFAndGPSAndAppliesOrientation(t *testing.T) {
	src := exifJPEG(t, 400, 200, 6) // rotate 90° CW to display
	if exifOrientation(src) != 6 || !bytes.Contains(src, []byte("GPS-SECRET-LOCATION")) {
		t.Fatal("fixture broken")
	}
	res, err := Normalize(context.Background(), src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(res.JPEG, []byte("Exif")) || bytes.Contains(res.JPEG, []byte("GPS-SECRET-LOCATION")) || hasMarker(res.JPEG, 0xE1) {
		t.Fatal("EXIF/GPS metadata survived")
	}
	if res.Width != 200 || res.Height != 400 {
		t.Fatalf("orientation not applied: %dx%d", res.Width, res.Height)
	}
	img, err := jpeg.Decode(bytes.NewReader(res.JPEG))
	if err != nil {
		t.Fatal(err)
	}
	// left edge red marker is now on top after a 90° CW rotation
	if r, _, _, _ := img.At(100, 5).RGBA(); r>>8 < 150 {
		t.Fatalf("rotation wrong: top pixel r=%d", r>>8)
	}
}

func hasMarker(b []byte, m byte) bool {
	i := 2
	for i+4 <= len(b) && b[i] == 0xFF {
		if b[i+1] == m {
			return true
		}
		if b[i+1] == 0xDA {
			return false
		}
		i += 2 + int(binary.BigEndian.Uint16(b[i+2:]))
	}
	return false
}

func TestNormalizeResizeBound(t *testing.T) {
	var b bytes.Buffer
	_ = png.Encode(&b, solid(3000, 1200))
	res, err := Normalize(context.Background(), b.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 2000 || res.Height != 800 || Detect(res.JPEG) != JPEG || res.Source != PNG {
		t.Fatalf("got %dx%d %s", res.Width, res.Height, Detect(res.JPEG))
	}
	small := exifJPEG(t, 640, 480, 1)
	res, _ = Normalize(context.Background(), small, nil)
	if res.Width != 640 || res.Height != 480 {
		t.Fatalf("small image resized: %dx%d", res.Width, res.Height)
	}
}

func TestNormalizeRejects(t *testing.T) {
	var b bytes.Buffer
	_ = png.Encode(&b, solid(50, 50))
	for name, data := range map[string][]byte{"truncated": b.Bytes()[:b.Len()-20], "gif": []byte("GIF89a......")} {
		if _, err := Normalize(context.Background(), data, nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	heic, _ := os.ReadFile("testdata/synthetic.heic")
	if Detect(heic) != HEIC {
		t.Fatal("HEIC fixture not detected")
	}
	if _, err := Normalize(context.Background(), heic, nil); err != ErrNoHEICDecoder {
		t.Fatalf("no decoder: %v", err)
	}
}

// Synthetic HEIC fixture (300x120, generated from a test pattern; never a real photo).
func TestNormalizeHEIC(t *testing.T) {
	dec := FindHEICDecoder(os.Getenv("HEIC_DECODER"))
	if dec == nil {
		t.Skip("SKIP: no HEIC decoder (install libheif: heif-dec/heif-convert; the Docker test stage has it)")
	}
	heic, err := os.ReadFile("testdata/synthetic.heic")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Normalize(context.Background(), heic, dec)
	if err != nil {
		t.Fatalf("%s: %v", dec.Name(), err)
	}
	if res.Width != 300 || res.Height != 120 || res.Source != HEIC || Detect(res.JPEG) != JPEG || hasMarker(res.JPEG, 0xE1) {
		t.Fatalf("heic -> %dx%d via %s", res.Width, res.Height, dec.Name())
	}
	t.Logf("HEIC decoded with %s", dec.Name())
}

func ispeHEIC(w, h uint32) []byte {
	b := append([]byte{0, 0, 0, 0x18}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...)
	b = append(b, 0, 0, 0, 20, 'i', 's', 'p', 'e', 0, 0, 0, 0)
	b = binary.BigEndian.AppendUint32(b, w)
	b = binary.BigEndian.AppendUint32(b, h)
	return append(b, make([]byte, 32)...)
}

type countingDecoder struct{ calls int }

func (c *countingDecoder) Name() string { return "count" }
func (c *countingDecoder) Decode(context.Context, []byte) (image.Image, error) {
	c.calls++
	return image.NewRGBA(image.Rect(0, 0, 10, 10)), nil
}

// M2: HEIC dimensions are checked from ispe BEFORE the decoder runs; missing ispe is corrupt.
func TestHEICSizeCheckedBeforeDecode(t *testing.T) {
	dec := &countingDecoder{}
	if _, err := Normalize(context.Background(), ispeHEIC(10000, 10000), dec); !errors.Is(err, ErrTooLarge) || dec.calls != 0 {
		t.Fatalf("100MP HEIC: %v calls=%d", err, dec.calls)
	}
	// 16-bit budget: 5000x5000 = 25MP x 8 B = 200MB > budget? exactly 200,000,000 < 209,715,200 -> ok
	if _, err := Normalize(context.Background(), ispeHEIC(5000, 5000), dec); err != nil || dec.calls != 1 {
		t.Fatalf("25MP HEIC: %v calls=%d", err, dec.calls)
	}
	if _, err := Normalize(context.Background(), ispeHEIC(6000, 6000), dec); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("36MP HEIC at 8 B/px must exceed the byte budget: %v", err)
	}
	noIspe := append([]byte{0, 0, 0, 0x18}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...)
	if _, err := Normalize(context.Background(), append(noIspe, make([]byte, 64)...), dec); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("no ispe: %v", err)
	}
}

// M2: the pixel budget accounts for bit depth (a 16-bit PNG gets half the pixels of 8-bit).
func TestByteBudgetByBitDepth(t *testing.T) {
	if checkSize(7000, 7000, 4) != nil { // 49MP x 4 = 196MB
		t.Fatal("49MP 8-bit should fit")
	}
	if checkSize(7000, 7000, 8) == nil { // 49MP x 8 = 392MB
		t.Fatal("49MP 16-bit must be refused")
	}
	if bytesPerPixel(color.NRGBA64Model) != 8 || bytesPerPixel(color.GrayModel) != 1 {
		t.Fatal("bytesPerPixel")
	}
	// a real 16-bit PNG header is read as 8 B/px by DecodeConfig
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewNRGBA64(image.Rect(0, 0, 4, 4)))
	cfg, _ := png.DecodeConfig(bytes.NewReader(buf.Bytes()))
	if bytesPerPixel(cfg.ColorModel) != 8 {
		t.Fatalf("16-bit png model %T", cfg.ColorModel)
	}
}

// M2: at most MaxConcurrent normalizations run at once.
func TestNormalizeConcurrencyLimit(t *testing.T) {
	var cur, peak int32
	block := make(chan struct{})
	dec := decoderFunc(func() {
		n := atomic.AddInt32(&cur, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		<-block
		atomic.AddInt32(&cur, -1)
	})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = Normalize(context.Background(), ispeHEIC(100, 100), dec) }()
	}
	time.Sleep(100 * time.Millisecond)
	close(block)
	wg.Wait()
	if peak != MaxConcurrent {
		t.Fatalf("peak concurrency %d, want %d", peak, MaxConcurrent)
	}
	// a cancelled context does not wait for a slot
	for i := 0; i < MaxConcurrent; i++ {
		sem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Normalize(ctx, ispeHEIC(100, 100), dec)
	for i := 0; i < MaxConcurrent; i++ {
		<-sem
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

type decoderFunc func()

func (f decoderFunc) Name() string { return "func" }
func (f decoderFunc) Decode(context.Context, []byte) (image.Image, error) {
	f()
	return image.NewRGBA(image.Rect(0, 0, 100, 100)), nil
}
