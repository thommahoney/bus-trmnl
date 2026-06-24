package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/fogleman/gg"
)

// RecipeIn is the input to the recipe card render. It mirrors recipe.Recipe but
// is declared here so the render package stays a leaf (no recipe import).
type RecipeIn struct {
	Title       string
	Servings    string
	Time        string
	Source      string
	Ingredients []string
	Steps       []string
	Now         time.Time
	Width       int
	Height      int
}

// Recipe renders a pinned recipe as a clean two-column card: title and a
// metadata strip across the top, ingredients down the left, numbered steps down
// the right. The body font auto-scales to the largest size that fits; if even
// the smallest size overflows, trailing steps are dropped with a "+N more"
// note (the uploader's phone holds the full recipe). Unlike the MUNI designs it
// is deliberately static — you read it while cooking, so it must not move.
func Recipe(in RecipeIn) ([]byte, error) {
	dc, fw, fh := designCanvas(in.Width, in.Height)

	marginX := fw * 0.043
	marginTop := fh * 0.05
	rightEdge := fw - marginX
	contentW := rightEdge - marginX

	// ── Title (up to two lines) ──
	titleSize := fh * 0.072
	dc.SetFontFace(newFace(bigShouldersBold, titleSize))
	titleLines := dc.WordWrap(strings.ToUpper(normalizeText(in.Title)), contentW)
	if len(titleLines) > 2 {
		titleLines = titleLines[:2]
	}
	titleLineH := titleSize * 1.10
	y := marginTop
	dc.SetColor(gray(15))
	for _, line := range titleLines {
		y += titleLineH
		dc.DrawString(line, marginX, y)
	}

	// ── Metadata strip ──
	if meta := joinMeta(in.Servings, in.Time, in.Source); meta != "" {
		metaSize := fh * 0.026
		dc.SetFontFace(newFace(ibmPlexMonoReg, metaSize))
		dc.SetColor(gray(95))
		y += metaSize * 1.7
		dc.DrawString(meta, marginX, y)
	}

	// ── Divider ──
	divY := y + fh*0.024
	dc.SetColor(gray(40))
	dc.SetLineWidth(3)
	dc.DrawLine(marginX, divY, rightEdge, divY)
	dc.Stroke()

	// ── Two columns ──
	contentTop := divY + fh*0.034
	availH := (fh - marginTop*0.6) - contentTop
	gap := fw * 0.035
	leftW := contentW * 0.36
	rightW := contentW - leftW - gap
	rightX := marginX + leftW + gap

	// Auto-fit: largest body size whose taller column fits; else smallest +
	// truncate steps.
	sizes := []float64{fh * 0.030, fh * 0.027, fh * 0.024, fh * 0.021, fh * 0.018}
	var size float64
	var ing, steps []wrapped
	for i, sz := range sizes {
		size = sz
		ing = wrapColumn(dc, sz, in.Ingredients, leftW, "•  ")
		steps = wrapColumnNumbered(dc, sz, in.Steps, rightW)
		leftH := columnHeight(ing, sz, sz*0.45)
		rightH := columnHeight(steps, sz, sz*0.70)
		fits := leftH <= availH && rightH <= availH
		if fits || i == len(sizes)-1 {
			if !fits {
				// Smallest legible size still overflows: truncate whichever
				// column is too tall (the phone holds the full recipe).
				if leftH > availH {
					ing = truncateToFit(ing, size, size*0.45, availH)
				}
				if rightH > availH {
					steps = truncateToFit(steps, size, size*0.70, availH)
				}
			}
			break
		}
	}

	drawHeading(dc, "INGREDIENTS", marginX, contentTop, size)
	drawColumn(dc, ing, marginX, contentTop+headingGap(size), size, size*0.45)

	drawHeading(dc, "STEPS", rightX, contentTop, size)
	drawColumn(dc, steps, rightX, contentTop+headingGap(size), size, size*0.70)

	return encodeGrayPNG(dc.Image())
}

// wrapped is one column entry: either a bulleted/numbered body item (a marker
// drawn at the left with the text wrapped to a hanging indent) or a section
// heading (no marker, drawn bold, with a little extra space above it).
type wrapped struct {
	marker  string
	indent  float64
	lines   []string
	heading bool
}

func lineHeight(size float64) float64 { return size * 1.34 }
func headingGap(size float64) float64 { return size*0.82*1.3 + size*0.7 }

// headPad is the extra space above a section heading (but not the first item).
func headPad(size float64) float64 { return size * 0.55 }

// wrapColumn wraps ingredient lines behind a bullet. Lines that look like a
// section header (e.g. "**Polenta**" or "For the sauce:") are rendered as a
// bold sub-heading with no bullet instead.
func wrapColumn(dc *gg.Context, size float64, items []string, width float64, marker string) []wrapped {
	dc.SetFontFace(newFace(ibmPlexMonoBold, size))
	indent, _ := dc.MeasureString(marker)
	out := make([]wrapped, 0, len(items))
	for _, it := range items {
		if isHeadingLine(it, false) {
			dc.SetFontFace(newFace(instrumentSansBold, size))
			out = append(out, wrapped{heading: true, lines: dc.WordWrap(normalizeText(it), width)})
			continue
		}
		dc.SetFontFace(newFace(instrumentSansReg, size))
		out = append(out, wrapped{marker: marker, indent: indent, lines: dc.WordWrap(normalizeText(it), width-indent)})
	}
	return out
}

// wrapColumnNumbered wraps each step behind its 1-based number, hanging-indented
// so multi-line steps align. Heading-like paragraphs ("**For the polenta**",
// "Make Dough") are rendered as bold sub-headings and do not consume a number.
func wrapColumnNumbered(dc *gg.Context, size float64, items []string, width float64) []wrapped {
	// Number width is sized to the count of real (non-heading) steps.
	realSteps := 0
	for _, it := range items {
		if !isHeadingLine(it, true) {
			realSteps++
		}
	}
	dc.SetFontFace(newFace(ibmPlexMonoBold, size))
	indent, _ := dc.MeasureString(fmt.Sprintf("%d.  ", realSteps))

	out := make([]wrapped, 0, len(items))
	n := 0
	for _, it := range items {
		if isHeadingLine(it, true) {
			dc.SetFontFace(newFace(instrumentSansBold, size))
			out = append(out, wrapped{heading: true, lines: dc.WordWrap(normalizeText(it), width)})
			continue
		}
		n++
		dc.SetFontFace(newFace(instrumentSansReg, size))
		out = append(out, wrapped{
			marker: fmt.Sprintf("%d.", n),
			indent: indent,
			lines:  dc.WordWrap(normalizeText(it), width-indent),
		})
	}
	return out
}

func itemLineCount(it wrapped) int {
	if len(it.lines) == 0 {
		return 1
	}
	return len(it.lines)
}

func columnHeight(items []wrapped, size, itemGap float64) float64 {
	lh := lineHeight(size)
	h := 0.0
	for i, it := range items {
		if i > 0 {
			h += itemGap
			if it.heading {
				h += headPad(size)
			}
		}
		h += float64(itemLineCount(it)) * lh
	}
	return h
}

// truncateToFit drops trailing items until the column fits availH, then appends
// a "+N more" note (which itself must fit).
func truncateToFit(items []wrapped, size, itemGap, availH float64) []wrapped {
	for n := len(items); n > 0; n-- {
		trial := items[:n]
		if n < len(items) {
			note := wrapped{lines: []string{fmt.Sprintf("+%d more on your phone", len(items)-n)}}
			trial = append(append([]wrapped{}, items[:n]...), note)
		}
		if columnHeight(trial, size, itemGap) <= availH {
			return trial
		}
	}
	return nil
}

func drawHeading(dc *gg.Context, text string, x, y, size float64) {
	dc.SetFontFace(newFace(instrumentSansBold, size*0.82))
	dc.SetColor(gray(110))
	dc.DrawString(text, x, y+size*0.82)
}

func drawColumn(dc *gg.Context, items []wrapped, x, y, size, itemGap float64) {
	lh := lineHeight(size)
	cur := y
	for i, it := range items {
		if i > 0 {
			cur += itemGap
			if it.heading {
				cur += headPad(size)
			}
		}
		if it.heading {
			dc.SetFontFace(newFace(instrumentSansBold, size))
			dc.SetColor(gray(70))
		} else {
			if it.marker != "" {
				dc.SetFontFace(newFace(ibmPlexMonoBold, size))
				dc.SetColor(gray(15))
				dc.DrawString(it.marker, x, cur+size)
			}
			dc.SetFontFace(newFace(instrumentSansReg, size))
			dc.SetColor(gray(25))
		}
		for j, line := range it.lines {
			dc.DrawString(line, x+it.indent, cur+size+float64(j)*lh)
		}
		cur += float64(itemLineCount(it)) * lh
	}
}

// vulgarFractions and friends map characters our embedded fonts lack to ASCII.
var inlineReplacer = strings.NewReplacer(
	"**", "", "__", "",
	"½", "1/2", "⅓", "1/3", "⅔", "2/3", "¼", "1/4", "¾", "3/4",
	"⅕", "1/5", "⅖", "2/5", "⅗", "3/5", "⅘", "4/5",
	"⅙", "1/6", "⅚", "5/6",
	"⅛", "1/8", "⅜", "3/8", "⅝", "5/8", "⅞", "7/8",
	"’", "'", "‘", "'", "“", "\"", "”", "\"", "…", "...",
)

// normalizeText strips Markdown emphasis and substitutes vulgar fractions and
// smart punctuation the panel fonts can't render, so e.g. "3 ½ cups" doesn't
// lose its "½".
func normalizeText(s string) string {
	return strings.TrimSpace(inlineReplacer.Replace(s))
}

// isHeadingLine reports whether a line is a section header rather than a real
// ingredient or step. Strong signals (Markdown bold wrap, trailing colon) apply
// everywhere; the short-title heuristic only applies to steps (allowShort),
// since ingredient lines are legitimately short.
func isHeadingLine(raw string, allowShort bool) bool {
	t := strings.TrimSpace(raw)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "**") && strings.HasSuffix(t, "**") {
		return true
	}
	if strings.HasSuffix(t, ":") {
		return true
	}
	if allowShort {
		c := normalizeText(t)
		// A short, title-like phrase with no terminal punctuation reads as a
		// heading — but not if it starts with a digit (that's a quantity, i.e.
		// real content like "2 eggs").
		if c != "" && c[0] >= '0' && c[0] <= '9' {
			return false
		}
		if c != "" && len([]rune(c)) <= 28 && len(strings.Fields(c)) <= 4 {
			if !strings.ContainsRune(".!?:;,", rune(c[len(c)-1])) {
				return true
			}
		}
	}
	return false
}

// joinMeta builds the "makes 12  ·  25 min  ·  Joy of Cooking" strip from the
// non-empty fields.
func joinMeta(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if n := normalizeText(p); n != "" {
			kept = append(kept, n)
		}
	}
	return strings.Join(kept, "  ·  ")
}
