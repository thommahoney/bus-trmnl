package render

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fogleman/gg"

	"github.com/thommahoney/bus-trmnl/internal/board"
)

// The "moving" MUNI designs. Each render places the same payload — up to three
// bus and three train arrivals, reduced to a glyph + minutes — in a different
// spot on the panel so no pixel carries the same value forever. That is the
// whole point: the static board burned the e-ink, these don't. Tick advances
// once per render (supplied by the screen) and drives the per-render offset.

// Item is one arrival reduced to what the moving designs draw: its kind and the
// whole minutes until it arrives (0 means "now").
type Item struct {
	Kind string // "bus" or "train"
	Min  int
}

// In is the input to a design render.
type In struct {
	Buses  []Item
	Trains []Item
	Now    time.Time
	Tick   int // monotonic per-render counter; drives anti-burn-in motion
	Width  int
	Height int
	Note   string // shown when there are no arrivals (e.g. "data unavailable")
}

// Ink tones, picked from the panel's 16-level grayscale.
var (
	inkD0 = gray(25)  // near-black: numbers, badges
	inkD1 = gray(80)  // strong gray: hub, accents
	inkD2 = gray(140) // mid gray: axis, ticks, clock
	inkD3 = gray(205) // faint gray: rings, separators
)

// Items flattens the configured boards into bus and train arrivals, classifies
// each by route, sorts soonest-first and keeps at most three of each. Board
// titles, destinations and route numbers are intentionally dropped — the moving
// designs speak only "bus" and "train".
func Items(boards []board.Board, now time.Time) (buses, trains []Item) {
	for _, b := range boards {
		for _, a := range b.Arrivals {
			it := Item{Kind: kindOf(a.LineRef, a.Line), Min: a.MinutesUntil(now)}
			if it.Kind == "train" {
				trains = append(trains, it)
			} else {
				buses = append(buses, it)
			}
		}
	}
	sort.SliceStable(buses, func(i, j int) bool { return buses[i].Min < buses[j].Min })
	sort.SliceStable(trains, func(i, j int) bool { return trains[i].Min < trains[j].Min })
	if len(buses) > 3 {
		buses = buses[:3]
	}
	if len(trains) > 3 {
		trains = trains[:3]
	}
	return buses, trains
}

// kindOf classifies a route as "bus" or "train". SF MUNI Metro lines are
// lettered (J K L M N T, plus the F/E streetcars); buses are numbered. So a
// leading letter means rail.
func kindOf(lineRef, line string) string {
	s := strings.TrimSpace(lineRef)
	if s == "" {
		s = strings.TrimSpace(line)
	}
	if s == "" {
		return "bus"
	}
	if unicode.IsLetter(rune(s[0])) {
		return "train"
	}
	return "bus"
}

// ── shared drawing helpers ───────────────────────────────────────────────

func designCanvas(width, height int) (*gg.Context, float64, float64) {
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}
	dc := gg.NewContext(width, height)
	dc.SetColor(color.White)
	dc.Clear()
	return dc, float64(width), float64(height)
}

// etaLabel renders minutes for the big numerals: "now" at zero, otherwise the
// bare number (the rings / axis carry the "minutes" meaning).
func etaLabel(min int) string {
	if min <= 0 {
		return "now"
	}
	return strconv.Itoa(min)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func pick2(a [][2]float64, t int) [2]float64 { return a[((t%len(a))+len(a))%len(a)] }
func pick1(a []float64, t int) float64       { return a[((t%len(a))+len(a))%len(a)] }

// rotate turns (px,py) by angle a about (ox,oy).
func rotate(px, py, ox, oy, a float64) (float64, float64) {
	dx, dy := px-ox, py-oy
	c, s := math.Cos(a), math.Sin(a)
	return ox + dx*c - dy*s, oy + dx*s + dy*c
}

// drawGlyph draws a simple outline bus or train centred at (cx,cy), sized to
// radius r, in colour col. Local coordinates span roughly ±50; r maps that onto
// the panel. Stroke width is in device pixels so it is never warped.
func drawGlyph(dc *gg.Context, kind string, cx, cy, r float64, col color.Color) {
	u := r / 50.0
	lw := math.Max(2, r*0.13)
	px := func(lx float64) float64 { return cx + lx*u }
	py := func(ly float64) float64 { return cy + ly*u }
	dc.SetColor(col)
	dc.SetLineWidth(lw)
	if kind == "train" {
		dc.DrawRoundedRectangle(px(-38), py(-34), 76*u, 60*u, 16*u)
		dc.Stroke()
		dc.DrawRoundedRectangle(px(-28), py(-22), 56*u, 22*u, 4*u)
		dc.Stroke()
		dc.DrawLine(px(0), py(-34), px(0), py(-48))
		dc.Stroke()
		dc.DrawLine(px(-12), py(-48), px(12), py(-48))
		dc.Stroke()
		dc.DrawCircle(px(-20), py(30), 8*u)
		dc.Fill()
		dc.DrawCircle(px(20), py(30), 8*u)
		dc.Fill()
		return
	}
	dc.DrawRoundedRectangle(px(-46), py(-30), 92*u, 56*u, 12*u)
	dc.Stroke()
	dc.DrawRoundedRectangle(px(-34), py(-18), 26*u, 17*u, 3*u)
	dc.Stroke()
	dc.DrawRoundedRectangle(px(8), py(-18), 26*u, 17*u, 3*u)
	dc.Stroke()
	dc.DrawLine(px(-46), py(9), px(46), py(9))
	dc.Stroke()
	dc.DrawCircle(px(-24), py(30), 8*u)
	dc.Fill()
	dc.DrawCircle(px(24), py(30), 8*u)
	dc.Fill()
}

// drawMarker draws the shared token: a solid disc for a bus, a hollow ring for
// a train, each carrying its glyph. This shape language is constant across all
// three designs so the eye learns it once.
func drawMarker(dc *gg.Context, kind string, cx, cy, r float64) {
	if kind == "train" {
		dc.SetColor(color.White)
		dc.DrawCircle(cx, cy, r)
		dc.Fill()
		dc.SetColor(inkD0)
		dc.SetLineWidth(math.Max(5, r*0.14))
		dc.DrawCircle(cx, cy, r)
		dc.Stroke()
		drawGlyph(dc, "train", cx, cy, r*0.9, inkD0)
		return
	}
	dc.SetColor(inkD0)
	dc.DrawCircle(cx, cy, r)
	dc.Fill()
	drawGlyph(dc, "bus", cx, cy, r*0.9, color.White)
}

func drawBigNum(dc *gg.Context, x, y float64, label string, size float64, col color.Color, ax, ay float64) {
	dc.SetFontFace(newFace(bigShouldersBold, size))
	dc.SetColor(col)
	dc.DrawStringAnchored(label, x, y, ax, ay)
}

// drawClock prints the time small, hopping between corners each render so even
// the clock never burns in.
func drawClock(dc *gg.Context, in In, fw, fh float64) {
	corners := [][3]float64{
		{fw * 0.93, fh * 0.06, 1},
		{fw * 0.07, fh * 0.06, 0},
		{fw * 0.93, fh * 0.95, 1},
		{fw * 0.07, fh * 0.95, 0},
	}
	c := corners[((in.Tick%4)+4)%4]
	dc.SetFontFace(newFace(ibmPlexMonoReg, fh*0.028))
	dc.SetColor(inkD2)
	dc.DrawStringAnchored(in.Now.Format("3:04 PM"), c[0], c[1], c[2], 0.5)
}

// drawNote centres a status line, nudged around by tick so it too keeps moving.
func drawNote(dc *gg.Context, in In, fw, fh float64) {
	note := in.Note
	if note == "" {
		note = "no arrivals"
	}
	spots := [][2]float64{{0.5, 0.46}, {0.42, 0.54}, {0.58, 0.5}, {0.48, 0.6}}
	p := spots[((in.Tick%len(spots))+len(spots))%len(spots)]
	dc.SetFontFace(newFace(instrumentSansReg, fh*0.04))
	dc.SetColor(inkD2)
	dc.DrawStringAnchored(note, fw*p[0], fh*p[1], 0.5, 0.5)
}

func empty(in In) bool { return len(in.Buses) == 0 && len(in.Trains) == 0 }

// dims resolves the panel size, applying the same defaults designCanvas does so
// the pure layout helpers can compute geometry without a drawing context.
func dims(width, height int) (float64, float64) {
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}
	return float64(width), float64(height)
}

// numberRadius returns the radius of the circle that encloses label rendered in
// BigShoulders-Bold at the given size. Placing a number this far plus the marker
// radius from a marker centre clears it in every direction, so the dial can spin
// to any rotation and a wide label ("88", "now") never touches its icon.
func numberRadius(size float64, label string) float64 {
	dc := gg.NewContext(1, 1)
	dc.SetFontFace(newFace(bigShouldersBold, size))
	w, h := dc.MeasureString(label)
	return math.Hypot(w, h) / 2
}

// maxRayDist returns how far a point can travel from (cx,cy) along angle ang
// before a circle of radius margin around it would leave the panel — i.e. the
// largest marker-centre distance that keeps the marker and its number on screen.
func maxRayDist(cx, cy, ang, fw, fh, margin float64) float64 {
	c, s := math.Cos(ang), math.Sin(ang)
	t := math.Max(fw, fh) * 2
	if c > 1e-9 {
		t = math.Min(t, (fw-margin-cx)/c)
	} else if c < -1e-9 {
		t = math.Min(t, (margin-cx)/c)
	}
	if s > 1e-9 {
		t = math.Min(t, (fh-margin-cy)/s)
	} else if s < -1e-9 {
		t = math.Min(t, (margin-cy)/s)
	}
	if t < 0 {
		t = 0
	}
	return t
}

// placed is one fully positioned mark: a marker (centre + radius) and its big
// number (centre + font size). The renderers draw these; the tests assert the
// number never overlaps its marker and both stay on the panel.
type placed struct {
	kind          string
	mx, my, mr    float64
	nx, ny, nsize float64
	label         string
}

// ── design 1: departure radar ────────────────────────────────────────────

var radarDrift = [][2]float64{{0, 0}, {70, -46}, {-80, 34}, {46, 64}, {-56, -58}, {80, 24}}

// radarFrame is the per-render dial geometry, shared by the renderer (rings,
// spokes, hub) and the layout helper (marker placement).
type radarFrame struct {
	cx, cy, R, rMin, base, s, sx, sy float64
}

func computeRadarFrame(in In) (fw, fh float64, f radarFrame) {
	fw, fh = dims(in.Width, in.Height)
	f.sx, f.sy = fw/DefaultWidth, fh/DefaultHeight
	f.s = math.Min(f.sx, f.sy)
	drift := pick2(radarDrift, in.Tick)
	f.cx = fw/2 + drift[0]*f.sx
	f.cy = fh/2 + drift[1]*f.sy
	f.R = math.Min(fw, fh) * 0.62 // large: range rings bleed past the edges by design
	f.rMin = f.R * 0.10           // inner clear zone around the hub
	f.base = float64(in.Tick)*47*math.Pi/180 + 0.35
	return fw, fh, f
}

// radarSpoke packs a lane's markers outward along one spoke, soonest nearest the
// hub, then sizes everything down (only if a far cluster would otherwise run off
// the panel) so the markers and their numbers always stay on screen and never
// overlap each other.
func radarSpoke(f radarFrame, fw, fh float64, items []Item, ang float64) []placed {
	n := len(items)
	if n == 0 {
		return nil
	}
	mr := make([]float64, n)
	nsize := make([]float64, n)
	nr := make([]float64, n)
	label := make([]string, n)
	maxReach := 0.0
	for i, it := range items {
		mr[i] = (78 - 12*float64(i)) * f.s
		label[i] = etaLabel(it.Min)
		nsize[i] = 120 * f.s
		if it.Min <= 0 {
			nsize[i] = 84 * f.s
		}
		nr[i] = numberRadius(nsize[i], label[i])
		if reach := mr[i] + 2*nr[i]; reach > maxReach {
			maxReach = reach
		}
	}
	pad := 10 * f.s
	gap := 22 * f.s
	// Centre distance the outermost marker needs, packed from the hub outward.
	centerNeed := f.rMin + mr[0]
	for i := 1; i < n; i++ {
		centerNeed += mr[i-1] + gap + mr[i]
	}
	avail := maxRayDist(f.cx, f.cy, ang, fw, fh, maxReach+pad)
	if centerNeed > avail && centerNeed > f.rMin {
		g := (avail - f.rMin) / (centerNeed - f.rMin)
		if g < 0 {
			g = 0
		}
		for i := range mr {
			mr[i] *= g
			nsize[i] *= g
			nr[i] *= g
		}
		gap *= g
		pad *= g
	}
	out := make([]placed, n)
	perp := ang + math.Pi/2
	r := f.rMin + mr[0]
	for i := 0; i < n; i++ {
		if i > 0 {
			r += mr[i-1] + gap + mr[i]
		}
		x := f.cx + math.Cos(ang)*r
		y := f.cy + math.Sin(ang)*r
		out[i] = placed{
			kind: items[i].Kind,
			mx:   x, my: y, mr: mr[i],
			nx:    x + math.Cos(perp)*(mr[i]+nr[i]+pad),
			ny:    y + math.Sin(perp)*(mr[i]+nr[i]+pad),
			nsize: nsize[i], label: label[i],
		}
	}
	return out
}

// radarLayout positions every mark for a radar render: buses on the base spoke,
// trains on the opposite one. Pure and deterministic so tests can check it.
func radarLayout(in In) []placed {
	fw, fh, f := computeRadarFrame(in)
	out := radarSpoke(f, fw, fh, in.Buses, f.base)
	return append(out, radarSpoke(f, fw, fh, in.Trains, f.base+math.Pi)...)
}

// Radar draws "you" at the hub with arrivals approaching from the rim: distance
// from centre is minutes away (rings at 10/20/30). Buses ride one spoke, trains
// the opposite spoke. The whole dial rotates ~47° per render and the centre
// drifts, so a given arrival never lands on the same pixels twice.
func Radar(in In) ([]byte, error) {
	dc, fw, fh := designCanvas(in.Width, in.Height)
	if empty(in) {
		drawNote(dc, in, fw, fh)
		return encodeGrayPNG(dc.Image())
	}
	_, _, f := computeRadarFrame(in)
	radiusFor := func(min float64) float64 { return f.rMin + clamp01(min/30)*(f.R-f.rMin) }

	// Range rings (10/20/30 min). The outer rings intentionally run off the
	// panel; their labels are drawn only where they'd land on screen.
	for _, m := range []float64{10, 20, 30} {
		rr := radiusFor(m)
		dc.SetColor(inkD3)
		dc.SetLineWidth(2 * f.s)
		dc.DrawCircle(f.cx, f.cy, rr)
		dc.Stroke()
		if ly := f.cy - rr - 4*f.s; ly > 24*f.s {
			dc.SetFontFace(newFace(instrumentSansReg, 32*f.s))
			dc.SetColor(inkD3)
			dc.DrawStringAnchored(fmt.Sprintf("%d", int(m)), f.cx, ly, 0.5, 1.0)
		}
	}

	dc.SetColor(inkD3)
	dc.SetLineWidth(3 * f.s)
	for _, a := range []float64{f.base, f.base + math.Pi} {
		dc.DrawLine(f.cx, f.cy, f.cx+math.Cos(a)*f.R, f.cy+math.Sin(a)*f.R)
		dc.Stroke()
	}

	// Hub dot marks the origin ("you" is implied, no label).
	dc.SetColor(inkD1)
	dc.DrawCircle(f.cx, f.cy, 16*f.s)
	dc.Fill()

	for _, p := range radarLayout(in) {
		drawPlaced(dc, p)
	}

	drawClock(dc, in, fw, fh)
	return encodeGrayPNG(dc.Image())
}

// drawPlaced renders one positioned mark: its bus/train marker and big number.
func drawPlaced(dc *gg.Context, p placed) {
	drawMarker(dc, p.kind, p.mx, p.my, p.mr)
	drawBigNum(dc, p.nx, p.ny, p.label, p.nsize, inkD0, 0.5, 0.5)
}

// ── design 2: reflowing board ────────────────────────────────────────────

var reflowShift = [][2]float64{{0, 0}, {-34, 28}, {37, -21}, {0, 21}, {-37, 28}, {41, -14}}

type chip struct {
	x, y, w, h float64
	kind       string
	min        int
	wide       bool
}

// Reflow keeps the legible departure-board feel — big numerals you can read
// across the room — but lays them out differently every render (two rows, two
// columns, swapped, one merged time-sorted column…) and nudges the whole field
// a few pixels, so the bright numbers never settle on fixed pixels.
func Reflow(in In) ([]byte, error) {
	dc, fw, fh := designCanvas(in.Width, in.Height)
	if empty(in) {
		drawNote(dc, in, fw, fh)
		return encodeGrayPNG(dc.Image())
	}
	for _, c := range reflowChips(in, fw, fh) {
		drawChip(dc, c)
	}
	drawClock(dc, in, fw, fh)
	return encodeGrayPNG(dc.Image())
}

func reflowChips(in In, fw, fh float64) []chip {
	m := fw * 0.06
	top := fh * 0.18
	bot := fh * 0.93
	usable := bot - top
	colGap := fw * 0.028
	sh := pick2(reflowShift, in.Tick)
	dx := sh[0] * fw / DefaultWidth
	dy := sh[1] * fh / DefaultHeight
	cw3 := (fw - 2*m - 2*colGap) / 3

	var chips []chip
	rows := func(busTop bool) {
		groups := [][]Item{in.Buses, in.Trains}
		if !busTop {
			groups = [][]Item{in.Trains, in.Buses}
		}
		rh := usable * 0.40
		rgap := usable * 0.20
		for gi, its := range groups {
			ry := top + float64(gi)*(rh+rgap)
			for i := 0; i < 3 && i < len(its); i++ {
				chips = append(chips, chip{x: m + float64(i)*(cw3+colGap) + dx, y: ry + dy, w: cw3, h: rh, kind: its[i].Kind, min: its[i].Min})
			}
		}
	}
	cols := func(busLeft bool) {
		groups := [][]Item{in.Buses, in.Trains}
		if !busLeft {
			groups = [][]Item{in.Trains, in.Buses}
		}
		cw := (fw - 2*m - colGap) / 2
		rh := usable * 0.26
		rgap := (usable - 3*rh) / 2
		for gi, its := range groups {
			cx := m + float64(gi)*(cw+colGap)
			for i := 0; i < 3 && i < len(its); i++ {
				chips = append(chips, chip{x: cx + dx, y: top + float64(i)*(rh+rgap) + dy, w: cw, h: rh, kind: its[i].Kind, min: its[i].Min})
			}
		}
	}
	merged := func() {
		all := append(append([]Item{}, in.Buses...), in.Trains...)
		sort.SliceStable(all, func(i, j int) bool { return all[i].Min < all[j].Min })
		step := usable / 6
		rh := step * 0.82
		for i, it := range all {
			chips = append(chips, chip{x: m + dx, y: top + float64(i)*step + dy, w: fw - 2*m, h: rh, kind: it.Kind, min: it.Min, wide: true})
		}
	}

	switch ((in.Tick % 6) + 6) % 6 {
	case 1:
		cols(true)
	case 2:
		rows(false)
	case 3:
		merged()
	case 4:
		cols(false)
	default:
		rows(true)
	}
	return chips
}

func drawChip(dc *gg.Context, c chip) {
	gy := c.y + c.h/2
	if c.wide {
		mr := c.h * 0.42
		drawMarker(dc, c.kind, c.x+mr*1.15, gy, mr)
		drawBigNum(dc, c.x+c.w*0.965, gy, etaLabel(c.min), c.h*0.8, inkD0, 1.0, 0.5)
		return
	}
	mr := math.Min(c.h*0.40, c.w*0.16)
	ns := math.Min(c.h*0.60, c.w*0.40)
	mx := c.x + c.w*0.12 + mr
	drawMarker(dc, c.kind, mx, gy, mr)
	label := etaLabel(c.min)
	nx := mx + mr + c.w*0.05
	dc.SetFontFace(newFace(bigShouldersBold, ns))
	dc.SetColor(inkD0)
	lw, _ := dc.MeasureString(label)
	dc.DrawStringAnchored(label, nx, gy, 0, 0.5)
	if c.min > 0 {
		dc.SetFontFace(newFace(instrumentSansReg, ns*0.32))
		dc.SetColor(inkD2)
		dc.DrawStringAnchored("min", nx+lw+ns*0.12, gy+ns*0.2, 0, 0.5)
	}
}

// ── design 3: time stream ────────────────────────────────────────────────

var streamTilt = []float64{-5, -2, 1, 4, 6, 2}

// Stream lays arrivals on one timeline that flows toward "now": as minutes tick
// down the tokens slide inward — the motion means something. On top of that the
// axis drifts vertically, tilts a few degrees and flips bus/train sides each
// render, so the band of ink keeps wandering the panel.
// streamFrame is the per-render timeline geometry shared by renderer and layout.
type streamFrame struct {
	axL, axR, axLen, midY, tilt, s float64
	gap                            float64
	busAbove                       bool
}

func computeStreamFrame(in In) (fw, fh float64, f streamFrame) {
	fw, fh = dims(in.Width, in.Height)
	f.s = math.Min(fw/DefaultWidth, fh/DefaultHeight)
	f.axL = fw * 0.08
	f.axR = fw * 0.92
	f.axLen = f.axR - f.axL
	f.tilt = pick1(streamTilt, in.Tick) * math.Pi / 180
	f.gap = 150 * f.s
	// Keep the whole band (token + number) on screen as the axis drifts/tilts.
	budget := f.gap + 70*f.s + 2*numberRadius(120*f.s, "88") + math.Abs(math.Sin(f.tilt))*f.axLen/2 + 30*f.s
	midY := fh*0.54 + math.Sin(float64(in.Tick)*1.05)*fh*0.07
	f.midY = math.Max(budget, math.Min(fh-budget, midY))
	f.busAbove = ((in.Tick%2)+2)%2 == 0
	return fw, fh, f
}

// streamTokens lays one band of tokens on the timeline, each at minutes→x, with
// its big number clear of the marker by construction.
func streamTokens(f streamFrame, items []Item, above bool) []placed {
	out := make([]placed, 0, len(items))
	pad := 10 * f.s
	mr := 70 * f.s
	// Position by minutes, but nudge same-band tokens apart so closely-timed
	// arrivals (e.g. 16 and 18 min) don't overlap as touching discs.
	prevBx := math.Inf(-1)
	for _, it := range items {
		bx := f.axL + clamp01(float64(it.Min)/30)*f.axLen
		if minBx := prevBx + 2*mr + 12*f.s; bx < minBx {
			bx = minBx
		}
		prevBx = bx
		by := f.midY - f.gap
		dir := -1.0
		if !above {
			by = f.midY + f.gap
			dir = 1.0
		}
		x, y := rotate(bx, by, f.axL, f.midY, f.tilt)
		label := etaLabel(it.Min)
		nsize := 120 * f.s
		if it.Min <= 0 {
			nsize = 84 * f.s
		}
		nr := numberRadius(nsize, label)
		out = append(out, placed{
			kind: it.Kind,
			mx:   x, my: y, mr: mr,
			nx: x, ny: y + dir*(mr+nr+pad),
			nsize: nsize, label: label,
		})
	}
	return out
}

// streamLayout positions every token for a stream render. Pure and deterministic.
func streamLayout(in In) []placed {
	_, _, f := computeStreamFrame(in)
	out := streamTokens(f, in.Buses, f.busAbove)
	return append(out, streamTokens(f, in.Trains, !f.busAbove)...)
}

func Stream(in In) ([]byte, error) {
	dc, fw, fh := designCanvas(in.Width, in.Height)
	if empty(in) {
		drawNote(dc, in, fw, fh)
		return encodeGrayPNG(dc.Image())
	}
	_, _, f := computeStreamFrame(in)

	p0x, p0y := rotate(f.axL, f.midY, f.axL, f.midY, f.tilt)
	p1x, p1y := rotate(f.axR, f.midY, f.axL, f.midY, f.tilt)
	dc.SetColor(inkD2)
	dc.SetLineWidth(4 * f.s)
	dc.DrawLine(p0x, p0y, p1x, p1y)
	dc.Stroke()

	for _, mn := range []int{0, 10, 20, 30} {
		bx := f.axL + float64(mn)/30*f.axLen
		ax, ay := rotate(bx, f.midY-14*f.s, f.axL, f.midY, f.tilt)
		bx2, by2 := rotate(bx, f.midY+14*f.s, f.axL, f.midY, f.tilt)
		dc.SetColor(inkD2)
		dc.SetLineWidth(4 * f.s)
		dc.DrawLine(ax, ay, bx2, by2)
		dc.Stroke()
		lx, ly := rotate(bx, f.midY+56*f.s, f.axL, f.midY, f.tilt)
		lbl := strconv.Itoa(mn)
		if mn == 0 {
			lbl = "now"
		}
		dc.SetFontFace(newFace(instrumentSansReg, 34*f.s))
		dc.SetColor(inkD2)
		dc.DrawStringAnchored(lbl, lx, ly, 0.5, 0.5)
	}

	// Stems from the axis to each token, then the marks themselves.
	for _, p := range streamLayout(in) {
		basex, basey := closestOnAxis(f, p.mx, p.my)
		dc.SetColor(inkD3)
		dc.SetLineWidth(3 * f.s)
		dc.DrawLine(basex, basey, p.mx, p.my)
		dc.Stroke()
		drawPlaced(dc, p)
	}

	drawClock(dc, in, fw, fh)
	return encodeGrayPNG(dc.Image())
}

// closestOnAxis returns the foot of the perpendicular from (x,y) to the tilted
// timeline, used to draw a token's stem back to the axis.
func closestOnAxis(f streamFrame, x, y float64) (float64, float64) {
	ax0, ay0 := rotate(f.axL, f.midY, f.axL, f.midY, f.tilt)
	dx, dy := math.Cos(f.tilt), math.Sin(f.tilt)
	t := (x-ax0)*dx + (y-ay0)*dy
	return ax0 + t*dx, ay0 + t*dy
}
