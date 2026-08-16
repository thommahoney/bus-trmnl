package render

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
)

// grayFrame encodes a blank gray panel, standing in for a MUNI design.
func grayFrame(t *testing.T, w, h int) []byte {
	t.Helper()
	g := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(g, g.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, g); err != nil {
		t.Fatalf("encode gray frame: %v", err)
	}
	return buf.Bytes()
}

// palettedFrame encodes a 16-level dithered panel, standing in for a cat photo.
func palettedFrame(t *testing.T, w, h int) []byte {
	t.Helper()
	pal := make(color.Palette, 16)
	for i := range pal {
		pal[i] = color.Gray{Y: uint8(i * 0xFF / 15)}
	}
	p := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	// A smooth gradient: compresses like a real photo without being pure noise.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p.SetColorIndex(x, y, uint8((x/60+y/60)%16))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, p); err != nil {
		t.Fatalf("encode paletted frame: %v", err)
	}
	return buf.Bytes()
}

func TestLowBatteryOverlayDrawsOnGray(t *testing.T) {
	orig := grayFrame(t, DefaultWidth, DefaultHeight)
	out, err := LowBatteryOverlay(orig, Battery{Percent: 2, Present: true, Volts: 3.42, Low: true})
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}
	if bytes.Equal(orig, out) {
		t.Fatal("overlay did not change the frame")
	}

	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode overlaid frame: %v", err)
	}
	if got, want := img.Bounds(), image.Rect(0, 0, DefaultWidth, DefaultHeight); got != want {
		t.Fatalf("bounds = %v, want %v", got, want)
	}

	// The badge is bottom-center; the frame started all white, so it must now
	// contain black pixels there and remain untouched at the top.
	if !hasDark(img, image.Rect(DefaultWidth/3, DefaultHeight*3/4, DefaultWidth*2/3, DefaultHeight)) {
		t.Error("expected the badge to darken the bottom-center of the panel")
	}
	if hasDark(img, image.Rect(0, 0, DefaultWidth, DefaultHeight/2)) {
		t.Error("badge must not intrude on the top half of the panel")
	}
}

// TestLowBatteryOverlayKeepsPaletted guards the cat path: a dithered frame must
// stay paletted (and so stay small) after the overlay, or the device rejects
// the download for exceeding its image-size cap.
func TestLowBatteryOverlayKeepsPaletted(t *testing.T) {
	orig := palettedFrame(t, DefaultWidth, DefaultHeight)
	out, err := LowBatteryOverlay(orig, Battery{Percent: 1, Present: true, Volts: 3.10, Low: true})
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode overlaid frame: %v", err)
	}
	if _, ok := img.(*image.Paletted); !ok {
		t.Fatalf("overlaid frame is %T, want *image.Paletted (a gray re-encode would blow the size cap)", img)
	}
	// Adding a flat badge must not meaningfully inflate the encoding.
	if len(out) > len(orig)+64<<10 {
		t.Errorf("overlaid frame grew from %d to %d bytes", len(orig), len(out))
	}
	if !hasDark(img, image.Rect(DefaultWidth/3, DefaultHeight*3/4, DefaultWidth*2/3, DefaultHeight)) {
		t.Error("expected the badge to be drawn on the paletted frame")
	}
}

func TestLowBatteryOverlayRejectsGarbage(t *testing.T) {
	if _, err := LowBatteryOverlay([]byte("not a png"), Battery{Low: true}); err == nil {
		t.Fatal("expected an error decoding a non-PNG frame")
	}
}

// hasDark reports whether any pixel in r is near-black.
func hasDark(img image.Image, r image.Rectangle) bool {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if c := color.GrayModel.Convert(img.At(x, y)).(color.Gray); c.Y < 40 {
				return true
			}
		}
	}
	return false
}
