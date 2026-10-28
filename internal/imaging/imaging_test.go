package imaging

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
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
