package render

import (
	"bytes"
	"image/png"
	"math"
	"testing"
	"time"

	"github.com/fogleman/gg"

	"github.com/thommahoney/bus-trmnl/internal/board"
)

const maxImageSize = 750000 // TRMNL X firmware MAX_IMAGE_SIZE

func sampleIn(tick int) In {
	return In{
		Buses:  []Item{{Kind: "bus", Min: 4}, {Kind: "bus", Min: 12}, {Kind: "bus", Min: 21}},
		Trains: []Item{{Kind: "train", Min: 0}, {Kind: "train", Min: 9}, {Kind: "train", Min: 17}},
		Now:    time.Date(2026, 6, 16, 8, 2, 0, 0, time.UTC),
		Tick:   tick,
		Width:  DefaultWidth,
		Height: DefaultHeight,
	}
}

func assertPNG(t *testing.T, data []byte, name string) {
	t.Helper()
	if len(data) == 0 {
		t.Fatalf("%s: empty output", name)
	}
	if len(data) > maxImageSize {
		t.Fatalf("%s: %d bytes exceeds device MAX_IMAGE_SIZE %d", name, len(data), maxImageSize)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: png decode: %v", name, err)
	}
	if got := img.Bounds().Dx(); got != DefaultWidth {
		t.Fatalf("%s: width %d, want %d", name, got, DefaultWidth)
	}
}

var designs = map[string]func(In) ([]byte, error){
	"radar":  Radar,
	"reflow": Reflow,
	"stream": Stream,
}

func TestDesignsRenderValidPNG(t *testing.T) {
	for name, fn := range designs {
		for tick := 0; tick < 7; tick++ {
			data, err := fn(sampleIn(tick))
			if err != nil {
				t.Fatalf("%s tick %d: %v", name, tick, err)
			}
			assertPNG(t, data, name)
		}
	}
}

func TestDesignsHandleEmpty(t *testing.T) {
	in := In{Now: time.Now(), Tick: 1, Width: DefaultWidth, Height: DefaultHeight, Note: "data unavailable"}
	for name, fn := range designs {
		data, err := fn(in)
		if err != nil {
			t.Fatalf("%s empty: %v", name, err)
		}
		assertPNG(t, data, name+"-empty")
	}
}

func TestDesignsRenderPartialGroups(t *testing.T) {
	in := sampleIn(0)
	in.Trains = nil // only buses present
	for name, fn := range designs {
		data, err := fn(in)
		if err != nil {
			t.Fatalf("%s buses-only: %v", name, err)
		}
		assertPNG(t, data, name+"-buses-only")
	}
}

func TestItemsClassifyAndCap(t *testing.T) {
	now := time.Date(2026, 6, 16, 8, 0, 0, 0, time.UTC)
	mk := func(ref string, mins int) board.Arrival {
		return board.Arrival{LineRef: ref, Expected: now.Add(time.Duration(mins) * time.Minute)}
	}
	boards := []board.Board{
		{Arrivals: []board.Arrival{mk("43", 12), mk("44", 4), mk("43", 20), mk("44", 28)}},
		{Arrivals: []board.Arrival{mk("N", 9), mk("N", 2)}},
	}
	buses, trains := Items(boards, now)
	if len(buses) != 3 {
		t.Fatalf("buses = %d, want 3 (capped)", len(buses))
	}
	if buses[0].Min != 4 || buses[1].Min != 12 || buses[2].Min != 20 {
		t.Fatalf("buses not soonest-first: %+v", buses)
	}
	if len(trains) != 2 || trains[0].Min != 2 || trains[0].Kind != "train" {
		t.Fatalf("trains = %+v, want [{train 2} {train 9}]", trains)
	}
}

// numberBox returns the bounding box of label centred at (nx,ny) in
// BigShoulders-Bold at the given size — recomputed independently of the layout
// code so a regression to a too-small offset is actually caught.
func numberBox(nx, ny, size float64, label string) (x0, y0, x1, y1 float64) {
	dc := gg.NewContext(1, 1)
	dc.SetFontFace(newFace(bigShouldersBold, size))
	w, h := dc.MeasureString(label)
	return nx - w/2, ny - h/2, nx + w/2, ny + h/2
}

// circleBoxGap is the clearance between a circle and an axis-aligned box;
// negative means they overlap.
func circleBoxGap(cx, cy, r, x0, y0, x1, y1 float64) float64 {
	qx := math.Max(x0, math.Min(cx, x1))
	qy := math.Max(y0, math.Min(cy, y1))
	return math.Hypot(cx-qx, cy-qy) - r
}

// stressIn covers the worst labels: 88 (widest two-digit) and 0 ("now"), with
// distinct minutes per group so same-group tokens don't legitimately coincide.
func stressIn(tick int) In {
	return In{
		Buses:  []Item{{Kind: "bus", Min: 0}, {Kind: "bus", Min: 8}, {Kind: "bus", Min: 88}},
		Trains: []Item{{Kind: "train", Min: 5}, {Kind: "train", Min: 23}, {Kind: "train", Min: 88}},
		Now:    time.Date(2026, 6, 16, 8, 2, 0, 0, time.UTC),
		Tick:   tick,
		Width:  DefaultWidth,
		Height: DefaultHeight,
	}
}

var layouts = map[string]func(In) []placed{
	"radar":  radarLayout,
	"stream": streamLayout,
}

// TestNumbersClearMarkers is the core item-4 guard: across every rotation and
// the widest labels, no big number may touch its bus/train marker.
func TestNumbersClearMarkers(t *testing.T) {
	for name, layout := range layouts {
		for tick := 0; tick < 24; tick++ {
			for _, p := range layout(stressIn(tick)) {
				x0, y0, x1, y1 := numberBox(p.nx, p.ny, p.nsize, p.label)
				if gap := circleBoxGap(p.mx, p.my, p.mr, x0, y0, x1, y1); gap <= 0 {
					t.Fatalf("%s tick %d: number %q touches its marker (gap %.1f)", name, tick, p.label, gap)
				}
			}
		}
	}
}

// TestMarksOnCanvas guards item 1: data marks (markers + their numbers) must
// stay on the panel even though the radar's decorative rings bleed off.
func TestMarksOnCanvas(t *testing.T) {
	for name, layout := range layouts {
		for tick := 0; tick < 24; tick++ {
			for _, p := range layout(stressIn(tick)) {
				if p.mx-p.mr < 0 || p.mx+p.mr > DefaultWidth || p.my-p.mr < 0 || p.my+p.mr > DefaultHeight {
					t.Fatalf("%s tick %d: marker off-canvas at (%.0f,%.0f) r%.0f", name, tick, p.mx, p.my, p.mr)
				}
				x0, y0, x1, y1 := numberBox(p.nx, p.ny, p.nsize, p.label)
				if x0 < 0 || y0 < 0 || x1 > DefaultWidth || y1 > DefaultHeight {
					t.Fatalf("%s tick %d: number %q off-canvas [%.0f,%.0f,%.0f,%.0f]", name, tick, p.label, x0, y0, x1, y1)
				}
			}
		}
	}
}

// TestRadarMarkersDoNotCollide ensures packed spoke markers never overlap.
func TestRadarMarkersDoNotCollide(t *testing.T) {
	for tick := 0; tick < 24; tick++ {
		ps := radarLayout(stressIn(tick))
		for i := 0; i < len(ps); i++ {
			for j := i + 1; j < len(ps); j++ {
				d := math.Hypot(ps[i].mx-ps[j].mx, ps[i].my-ps[j].my)
				if d <= ps[i].mr+ps[j].mr {
					t.Fatalf("tick %d: radar markers %d and %d overlap (d %.1f, radii %.1f+%.1f)", tick, i, j, d, ps[i].mr, ps[j].mr)
				}
			}
		}
	}
}

// TestStreamMarkersDoNotCollide checks closely-timed same-band arrivals are
// nudged apart rather than rendered as overlapping discs.
func TestStreamMarkersDoNotCollide(t *testing.T) {
	in := In{
		Buses:  []Item{{Kind: "bus", Min: 16}, {Kind: "bus", Min: 18}, {Kind: "bus", Min: 19}},
		Trains: []Item{{Kind: "train", Min: 5}, {Kind: "train", Min: 6}, {Kind: "train", Min: 7}},
		Now:    time.Date(2026, 6, 16, 8, 2, 0, 0, time.UTC),
		Width:  DefaultWidth,
		Height: DefaultHeight,
	}
	for tick := 0; tick < 24; tick++ {
		in.Tick = tick
		ps := streamLayout(in)
		for i := 0; i < len(ps); i++ {
			for j := i + 1; j < len(ps); j++ {
				d := math.Hypot(ps[i].mx-ps[j].mx, ps[i].my-ps[j].my)
				if d <= ps[i].mr+ps[j].mr {
					t.Fatalf("tick %d: stream markers %d and %d overlap (d %.1f, radii %.1f+%.1f)", tick, i, j, d, ps[i].mr, ps[j].mr)
				}
			}
		}
	}
}

func TestKindOf(t *testing.T) {
	cases := map[string]string{"43": "bus", "44": "bus", "5R": "bus", "N": "train", "KT": "train", "": "bus"}
	for ref, want := range cases {
		if got := kindOf(ref, ""); got != want {
			t.Fatalf("kindOf(%q) = %q, want %q", ref, got, want)
		}
	}
}
