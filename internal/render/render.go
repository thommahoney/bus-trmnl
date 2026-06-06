// Package render draws the arrivals board to a grayscale PNG for the device.
package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"time"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/thommahoney/bus-trmnl/internal/board"
)

// Default panel size for the TRMNL X in landscape. The device reports its own
// WIDTH/HEIGHT headers, which the server prefers when present.
const (
	DefaultWidth  = 1872
	DefaultHeight = 1404
)

var (
	regularFont *truetype.Font
	boldFont    *truetype.Font
)

func init() {
	var err error
	if regularFont, err = truetype.Parse(goregular.TTF); err != nil {
		panic(err)
	}
	if boldFont, err = truetype.Parse(gobold.TTF); err != nil {
		panic(err)
	}
}

func face(f *truetype.Font, size float64) font.Face {
	return truetype.NewFace(f, &truetype.Options{Size: size})
}

// Screen renders the boards to a grayscale PNG sized width x height.
func Screen(boards []board.Board, now time.Time, width, height int) ([]byte, error) {
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}

	dc := gg.NewContext(width, height)
	dc.SetColor(color.White)
	dc.Clear()
	dc.SetColor(color.Black)

	fw := float64(width)
	fh := float64(height)
	margin := fw * 0.035

	// Scale type to the panel height.
	titleSize := fh * 0.055
	clockSize := fh * 0.045
	boardSize := fh * 0.045
	lineSize := fh * 0.05
	destSize := fh * 0.038
	etaSize := fh * 0.05
	metaSize := fh * 0.022

	// Header.
	dc.SetFontFace(face(boldFont, titleSize))
	dc.DrawString("SF MUNI", margin, margin+titleSize)

	dc.SetFontFace(face(regularFont, clockSize))
	clock := now.Format("3:04 PM")
	cw, _ := dc.MeasureString(clock)
	dc.DrawString(clock, fw-margin-cw, margin+titleSize)

	// Divider under header.
	y := margin + titleSize + fh*0.02
	dc.SetLineWidth(3)
	dc.DrawLine(margin, y, fw-margin, y)
	dc.Stroke()

	// Body: each board gets an equal vertical slice.
	contentTop := y + fh*0.025
	contentBottom := fh - margin - metaSize*1.5
	if len(boards) == 0 {
		boards = nil
	}
	slices := len(boards)
	if slices == 0 {
		slices = 1
	}
	sliceH := (contentBottom - contentTop) / float64(slices)

	for i, b := range boards {
		top := contentTop + float64(i)*sliceH

		dc.SetFontFace(face(boldFont, boardSize))
		dc.DrawString(b.Title, margin, top+boardSize)

		rowTop := top + boardSize + fh*0.018
		rowH := lineSize * 1.55

		switch {
		case b.Err != nil:
			dc.SetFontFace(face(regularFont, destSize))
			dc.DrawString("data unavailable", margin, rowTop+destSize)
		case len(b.Arrivals) == 0:
			dc.SetFontFace(face(regularFont, destSize))
			dc.DrawString("no upcoming arrivals", margin, rowTop+destSize)
		default:
			for r, a := range b.Arrivals {
				ry := rowTop + float64(r)*rowH
				if ry+lineSize > top+sliceH {
					break
				}
				drawArrival(dc, a, now, margin, ry, fw, lineSize, destSize, etaSize, regularFont, boldFont)
			}
		}
	}

	// Footer meta line.
	dc.SetFontFace(face(regularFont, metaSize))
	meta := "updated " + now.Format("3:04:05 PM")
	dc.DrawString(meta, margin, fh-margin*0.4)

	return encodeGrayPNG(dc.Image())
}

func drawArrival(dc *gg.Context, a board.Arrival, now time.Time, margin, ry, fw, lineSize, destSize, etaSize float64, reg, bold *truetype.Font) {
	// Route badge / line name on the left.
	dc.SetFontFace(face(bold, lineSize))
	dc.DrawString(a.Line, margin, ry+lineSize)
	lineW, _ := dc.MeasureString(a.Line)

	// Destination after the line.
	dc.SetFontFace(face(reg, destSize))
	destX := margin + lineW + fw*0.03
	dc.DrawString(a.Destination, destX, ry+lineSize)

	// ETA on the right.
	eta := formatETA(a.MinutesUntil(now))
	dc.SetFontFace(face(bold, etaSize))
	ew, _ := dc.MeasureString(eta)
	dc.DrawString(eta, fw-margin-ew, ry+lineSize)
}

func formatETA(min int) string {
	if min <= 0 {
		return "Now"
	}
	if min == 1 {
		return "1 min"
	}
	return fmt.Sprintf("%d min", min)
}

// encodeGrayPNG converts the rendered image to 8-bit grayscale and PNG-encodes
// it, which suits the device's e-ink panel and keeps files small.
func encodeGrayPNG(src image.Image) ([]byte, error) {
	b := src.Bounds()
	gray := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			gray.Set(x, y, color.GrayModel.Convert(src.At(x, y)))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, gray); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
