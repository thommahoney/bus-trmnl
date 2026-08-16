package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"github.com/fogleman/gg"
)

// LowBatteryOverlay stamps a high-contrast "LOW BATTERY" badge onto an
// already-encoded PNG frame. It runs as a post-process on the finished bytes,
// after the screen has drawn itself, so every screen — the moving MUNI designs,
// a cat photo, a pinned recipe — gets the identical warning without each one
// having to know about it.
//
// The badge is deliberately loud: a black pill with a thick white border, so it
// reads against both the white MUNI boards and the busy midtones of a dithered
// photo. It sits bottom-center, clear of the MUNI clock in the top-right.
//
// The source's pixel format is preserved — a paletted (dithered) frame stays
// paletted, so a cat keeps its compact <=4-bpp encoding and stays under the
// firmware's image-size cap. The badge is drawn in pure black and white, both
// of which are exact entries in every grayscale palette the cat encoder uses.
func LowBatteryOverlay(frame []byte, b Battery) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(frame))
	if err != nil {
		return nil, fmt.Errorf("decode frame for low-battery overlay: %w", err)
	}

	// Draw into the decoded image when it is already a mutable type we want to
	// keep (paletted photos), otherwise fall back to 8-bit gray like the rest of
	// the render package.
	var dst draw.Image
	switch img := src.(type) {
	case *image.Paletted:
		dst = img
	case *image.Gray:
		dst = img
	default:
		g := image.NewGray(src.Bounds())
		draw.Draw(g, g.Bounds(), src, src.Bounds().Min, draw.Src)
		dst = g
	}

	// Composite through the badge's own alpha so only the rounded pill lands on
	// the frame — a plain Src draw would paint its transparent corners black.
	badge, at := lowBatteryBadge(dst.Bounds(), b)
	draw.DrawMask(dst, badge.Bounds().Add(at), grayscale(badge), image.Point{}, badge, image.Point{}, draw.Over)

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// lowBatteryBadge renders the warning pill for a panel of the given bounds and
// returns it along with the top-left point it should be composited at.
func lowBatteryBadge(bounds image.Rectangle, b Battery) (image.Image, image.Point) {
	fw := float64(bounds.Dx())
	fh := float64(bounds.Dy())

	bw := fw * 0.46
	bh := fh * 0.115
	border := bh * 0.10
	radius := bh * 0.30

	dc := gg.NewContext(int(bw), int(bh))

	// White border, then the black body inset inside it: the pale outline keeps
	// the pill legible where the frame behind it is dark, and the black body
	// does the same where it is light.
	dc.SetColor(color.White)
	dc.DrawRoundedRectangle(0, 0, bw, bh, radius)
	dc.Fill()
	dc.SetColor(color.Black)
	dc.DrawRoundedRectangle(border, border, bw-2*border, bh-2*border, radius*0.8)
	dc.Fill()

	// ── Battery glyph (drawn nearly empty) ──
	glyphH := bh * 0.42
	glyphW := glyphH * 1.9
	glyphX := bh * 0.42
	glyphY := (bh - glyphH) / 2
	stroke := glyphH * 0.14

	dc.SetColor(color.White)
	dc.SetLineWidth(stroke)
	dc.DrawRoundedRectangle(glyphX, glyphY, glyphW, glyphH, stroke)
	dc.Stroke()
	// Terminal nub on the right.
	nubH := glyphH * 0.40
	dc.DrawRoundedRectangle(glyphX+glyphW+stroke*0.5, glyphY+(glyphH-nubH)/2, stroke*1.8, nubH, stroke*0.5)
	dc.Fill()
	// A sliver of charge left inside.
	inset := stroke * 1.6
	dc.DrawRectangle(glyphX+inset, glyphY+inset, (glyphW-2*inset)*0.16, glyphH-2*inset)
	dc.Fill()

	// ── Text ──
	textX := glyphX + glyphW + stroke*2 + bh*0.34
	title := "LOW BATTERY"
	titleSize := bh * 0.34

	dc.SetFontFace(newFace(instrumentSansBold, titleSize))
	_, th := dc.MeasureString(title)

	sub := "CHARGE ME"
	if b.Volts > 0 {
		sub = fmt.Sprintf("CHARGE ME  ·  %.2fV", b.Volts)
	}
	if b.Present {
		sub = fmt.Sprintf("CHARGE ME  ·  %d%%", b.Percent)
		if b.Volts > 0 {
			sub = fmt.Sprintf("CHARGE ME  ·  %d%%  ·  %.2fV", b.Percent, b.Volts)
		}
	}
	subSize := bh * 0.20

	// Stack title over subtitle, centered as a block on the pill's midline.
	gap := bh * 0.10
	dc.SetFontFace(newFace(instrumentSansReg, subSize))
	_, sh := dc.MeasureString(sub)
	blockH := th + gap + sh
	top := (bh - blockH) / 2

	dc.SetColor(color.White)
	dc.SetFontFace(newFace(instrumentSansBold, titleSize))
	dc.DrawString(title, textX, top+th)
	dc.SetFontFace(newFace(instrumentSansReg, subSize))
	dc.DrawString(sub, textX, top+th+gap+sh)

	at := image.Point{
		X: bounds.Min.X + (bounds.Dx()-int(bw))/2,
		Y: bounds.Min.Y + bounds.Dy() - int(bh) - int(fh*0.045),
	}
	return dc.Image(), at
}

// grayscale flattens the badge to 8-bit gray so it composites cleanly into
// either a gray or a paletted destination.
func grayscale(src image.Image) *image.Gray {
	b := src.Bounds()
	g := image.NewGray(b)
	draw.Draw(g, b, src, b.Min, draw.Src)
	return g
}
