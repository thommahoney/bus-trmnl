// Package render draws the arrivals board to a grayscale PNG for the device.
package render

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"time"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"

	"github.com/thommahoney/bus-trmnl/internal/board"
)

// Default panel size for the TRMNL X in landscape.
const (
	DefaultWidth  = 1872
	DefaultHeight = 1404
)

//go:embed fonts/BigShoulders-Bold.ttf
var bigShouldersBoldTTF []byte

//go:embed fonts/InstrumentSans-Bold.ttf
var instrumentSansBoldTTF []byte

//go:embed fonts/InstrumentSans-Regular.ttf
var instrumentSansRegularTTF []byte

//go:embed fonts/IBMPlexMono-Bold.ttf
var ibmPlexMonoBoldTTF []byte

//go:embed fonts/IBMPlexMono-Regular.ttf
var ibmPlexMonoRegularTTF []byte

var (
	bigShouldersBold   *opentype.Font
	instrumentSansBold *opentype.Font
	instrumentSansReg  *opentype.Font
	ibmPlexMonoBold    *opentype.Font
	ibmPlexMonoReg     *opentype.Font
)

func init() {
	mustParse := func(data []byte) *opentype.Font {
		f, err := opentype.Parse(data)
		if err != nil {
			panic(err)
		}
		return f
	}
	bigShouldersBold = mustParse(bigShouldersBoldTTF)
	instrumentSansBold = mustParse(instrumentSansBoldTTF)
	instrumentSansReg = mustParse(instrumentSansRegularTTF)
	ibmPlexMonoBold = mustParse(ibmPlexMonoBoldTTF)
	ibmPlexMonoReg = mustParse(ibmPlexMonoRegularTTF)
}

func newFace(f *opentype.Font, size float64) font.Face {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		panic(err)
	}
	return face
}

// gray returns a color.Gray at the given 0–255 level.
func gray(level uint8) color.Gray {
	return color.Gray{Y: level}
}

// Metadata holds timing information displayed in the footer.
type Metadata struct {
	FetchStats    board.FetchStats
	RefreshRate   time.Duration // device wake interval
}

// Screen renders the boards to a grayscale PNG sized width x height.
func Screen(boards []board.Board, now time.Time, width, height int, meta *Metadata) ([]byte, error) {
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}

	dc := gg.NewContext(width, height)
	dc.SetColor(color.White)
	dc.Clear()

	fw := float64(width)
	fh := float64(height)

	// Layout constants scaled to panel size.
	marginX := fw * 0.043
	marginTop := fh * 0.05
	rightEdge := fw - marginX

	// Font sizes scaled to panel height.
	clockSize := fh * 0.040
	sectionSize := fh * 0.046
	badgeFontSize := fh * 0.056
	lineSize := fh * 0.050
	destSize := fh * 0.033
	etaSize := fh * 0.049
	metaSize := fh * 0.020

	// Fixed column positions (proportional to width).
	badgeH := fh * 0.060
	badgeW := fh * 0.075 // uniform width for all badges
	badgeColX := marginX
	lineColX := marginX + badgeW + fw*0.020
	destColX := marginX + fw*0.44
	rowH := fh * 0.086

	// ── Header: clock + divider (no title) ──
	y := marginTop

	dc.SetFontFace(newFace(ibmPlexMonoReg, clockSize))
	dc.SetColor(gray(80))
	clock := now.Format("3:04:05 PM")
	cw, _ := dc.MeasureString(clock)
	dc.DrawString(clock, rightEdge-cw, y+clockSize)

	divY := y + clockSize + fh*0.018
	dc.SetColor(color.Black)
	dc.SetLineWidth(3)
	dc.DrawLine(marginX, divY, rightEdge, divY)
	dc.Stroke()

	// ── Board sections ──
	contentTop := divY + fh*0.030
	contentBottom := fh - marginTop - metaSize*2
	slices := len(boards)
	if slices == 0 {
		slices = 1
	}
	sliceH := (contentBottom - contentTop) / float64(slices)

	for i, b := range boards {
		top := contentTop + float64(i)*sliceH

		// Section title.
		dc.SetFontFace(newFace(instrumentSansBold, sectionSize))
		dc.SetColor(gray(60))
		dc.DrawString(b.Title, marginX, top+sectionSize)

		rowTop := top + sectionSize + fh*0.025

		switch {
		case b.Err != nil:
			dc.SetFontFace(newFace(instrumentSansReg, destSize))
			dc.SetColor(gray(120))
			dc.DrawString("data unavailable", marginX, rowTop+destSize)
		case len(b.Arrivals) == 0:
			dc.SetFontFace(newFace(instrumentSansReg, destSize))
			dc.SetColor(gray(120))
			dc.DrawString("no upcoming arrivals", marginX, rowTop+destSize)
		default:
			for r, a := range b.Arrivals {
				ry := rowTop + float64(r)*rowH
				if ry+badgeH > top+sliceH {
					break
				}
				drawArrival(dc, a, now, ry, fw, marginX, rightEdge,
					badgeColX, lineColX, destColX,
					badgeW, badgeH, badgeFontSize,
					lineSize, destSize, etaSize)

				// Subtle row separator.
				if r < len(b.Arrivals)-1 {
					sepY := ry + rowH - 2
					dc.SetColor(gray(220))
					dc.SetLineWidth(1)
					dc.DrawLine(lineColX, sepY, rightEdge, sepY)
					dc.Stroke()
				}
			}
		}

		// Section divider between boards.
		if i < len(boards)-1 {
			sdY := top + sliceH - fh*0.015
			dc.SetColor(gray(180))
			dc.SetLineWidth(1)
			dc.DrawLine(marginX, sdY, rightEdge, sdY)
			dc.Stroke()
		}
	}

	// ── Footer ──
	dc.SetFontFace(newFace(ibmPlexMonoReg, metaSize))
	dc.SetColor(gray(100))

	renderDur := time.Since(now)
	footerLeft := "updated " + now.Format("3:04:05 PM")
	if meta != nil && !meta.FetchStats.At.IsZero() {
		footerLeft += fmt.Sprintf("  fetch %dms  render %dms",
			meta.FetchStats.Duration.Milliseconds(),
			renderDur.Milliseconds())
	}
	dc.DrawString(footerLeft, marginX, fh-marginTop*0.5)

	if meta != nil && meta.RefreshRate > 0 {
		nextUpdate := now.Add(meta.RefreshRate).Format("3:04:05 PM")
		footerRight := "next " + nextUpdate
		rw, _ := dc.MeasureString(footerRight)
		dc.DrawString(footerRight, rightEdge-rw, fh-marginTop*0.5)
	}

	return encodeGrayPNG(dc.Image())
}

func drawArrival(dc *gg.Context, a board.Arrival, now time.Time,
	ry, fw, marginX, rightEdge float64,
	badgeColX, lineColX, destColX float64,
	badgeW, badgeH, badgeFontSize float64,
	lineSize, destSize, etaSize float64,
) {
	// All elements center on the row midline.
	rowMidY := ry + badgeH*0.65

	// ── Badge: fixed-width dark rounded rect ──
	badgeText := a.LineRef
	if badgeText == "" {
		badgeText = a.Line
	}

	badgeY := rowMidY - badgeH/2
	radius := badgeH * 0.18
	dc.SetColor(gray(30))
	dc.DrawRoundedRectangle(badgeColX, badgeY, badgeW, badgeH, radius)
	dc.Fill()

	// Badge text: centered with upward nudge for caps-only visual centering.
	dc.SetFontFace(newFace(bigShouldersBold, badgeFontSize))
	dc.SetColor(color.White)
	dc.DrawStringAnchored(badgeText, badgeColX+badgeW/2, rowMidY-badgeH*0.10, 0.5, 0.5)

	// ── Line name: vertically centered on row midline ──
	dc.SetFontFace(newFace(instrumentSansBold, lineSize))
	dc.SetColor(color.Black)
	dc.DrawStringAnchored(a.Line, lineColX, rowMidY, 0.0, 0.35)

	// ── Destination: vertically centered on row midline ──
	dc.SetFontFace(newFace(instrumentSansReg, destSize))
	dc.SetColor(gray(120))
	dc.DrawStringAnchored(a.Destination, destColX, rowMidY, 0.0, 0.35)

	// ── ETA: right-aligned, vertically centered on row midline ──
	eta := formatETA(a.MinutesUntil(now))
	dc.SetFontFace(newFace(ibmPlexMonoBold, etaSize))
	dc.SetColor(color.Black)
	dc.DrawStringAnchored(eta, rightEdge, rowMidY, 1.0, 0.35)
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
	g := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			g.Set(x, y, color.GrayModel.Convert(src.At(x, y)))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
