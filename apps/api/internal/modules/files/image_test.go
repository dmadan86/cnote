package files

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func testImg(w, h int) image.Image {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			m.Set(x, y, color.RGBA{uint8(x * 5), uint8(y * 5), 40, 255})
		}
	}
	return m
}

func jpegBytes(t *testing.T, w, h int) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, testImg(w, h), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withExif inserts an APP1 segment (orientation tag and a fake GPS blob) after the JPEG start marker.
func withExif(jpg []byte, orientation uint16) []byte {
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08")
	tiff = binary.BigEndian.AppendUint16(tiff, 1)
	tiff = append(tiff, 0x01, 0x12, 0x00, 0x03, 0, 0, 0, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	seg = append(seg, []byte("GPSLatitude=12.97")...)
	app1 := []byte{0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}
	return append(append(append([]byte{}, jpg[:2]...), append(app1, seg...)...), jpg[2:]...)
}

func TestProcessImageRoundTripAndExif(t *testing.T) {
	ctx := context.Background()
	in := withExif(jpegBytes(t, 40, 20), 6) // 40x20 stored sideways: shown 20x40
	if !bytes.Contains(in, []byte("GPSLatitude")) {
		t.Fatal("fixture has no GPS")
	}
	out, ct, err := ProcessImage(ctx, in)
	if err != nil || ct != "image/jpeg" {
		t.Fatalf("%v %s", err, ct)
	}
	if bytes.Contains(out, []byte("GPS")) || bytes.Contains(out, []byte("Exif")) {
		t.Fatal("EXIF survived")
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(out))
	if cfg.Width != 20 || cfg.Height != 40 {
		t.Fatalf("orientation not applied: %dx%d", cfg.Width, cfg.Height)
	}

	var p bytes.Buffer
	_ = png.Encode(&p, testImg(10, 10))
	p.WriteString("APPENDED-PAYLOAD")
	out, ct, err = ProcessImage(ctx, p.Bytes())
	if err != nil || ct != "image/png" || bytes.Contains(out, []byte("APPENDED")) {
		t.Fatalf("png: %v %s", err, ct)
	}
}

func TestProcessImageRefusals(t *testing.T) {
	ctx := context.Background()
	var g bytes.Buffer
	_ = gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White}), nil)
	var wide bytes.Buffer // 5000 x 4000 = 20 MP, mostly empty so it stays under 1 MB
	_ = png.Encode(&wide, image.NewGray(image.Rect(0, 0, 5000, 4000)))
	for name, data := range map[string][]byte{
		"empty": nil, "pdf": []byte("%PDF-1.4 x"), "gif": g.Bytes(), "text": []byte(strings.Repeat("a", 100)),
		"oversize": append([]byte("\xff\xd8\xff"), make([]byte, 1<<20)...), "16MP": wide.Bytes(), "truncated": jpegBytes(t, 40, 20)[:60],
	} {
		if _, _, err := ProcessImage(ctx, data); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
