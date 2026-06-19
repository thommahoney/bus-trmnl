package screen

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/board"
	"github.com/thommahoney/bus-trmnl/internal/config"
	"github.com/thommahoney/bus-trmnl/internal/render"
)

// Muni renders the MUNI arrivals board in one of several designs. It refreshes
// the 511 cache on demand, so 511 is only contacted when this screen actually
// renders. Each design relocates its content every render to avoid e-ink
// burn-in; the per-screen tick counter drives that motion.
type Muni struct {
	store   *board.Store
	refresh config.RefreshConfig
	design  string
	tick    atomic.Uint64
}

// NewMuni creates a MUNI arrivals screen for the given design (one of
// config.MuniRadar, MuniBoard, MuniStream or MuniClassic). An empty design
// defaults to the reflowing board.
func NewMuni(store *board.Store, refresh config.RefreshConfig, design string) *Muni {
	if design == "" {
		design = config.MuniBoard
	}
	return &Muni{store: store, refresh: refresh, design: design}
}

// Name implements Screen. Designs get distinct names ("muni-radar", …) so they
// each get their own filenames and /latest?screen= preview.
func (m *Muni) Name() string { return "muni-" + m.design }

// Render implements Screen.
func (m *Muni) Render(ctx context.Context, now time.Time, width, height int) ([]byte, error) {
	m.store.EnsureFresh(ctx)
	boards, fetchStats := m.store.Snapshot()
	tick := int(m.tick.Add(1) - 1)

	if m.design == config.MuniClassic {
		meta := &render.Metadata{FetchStats: fetchStats, RefreshRate: m.refresh.RateAt(now)}
		return render.Screen(boards, now, width, height, meta)
	}

	buses, trains := render.Items(boards, now)
	in := render.In{
		Buses:  buses,
		Trains: trains,
		Now:    now,
		Tick:   tick,
		Width:  width,
		Height: height,
		Note:   noteFor(boards),
	}
	switch m.design {
	case config.MuniRadar:
		return render.Radar(in)
	case config.MuniStream:
		return render.Stream(in)
	default:
		return render.Reflow(in)
	}
}

// noteFor returns the status line shown when there are no arrivals to draw:
// "data unavailable" when every board failed to fetch, otherwise empty (the
// designs then show their default "no arrivals").
func noteFor(boards []board.Board) string {
	anyOK := false
	for _, b := range boards {
		if b.Err == nil {
			anyOK = true
			break
		}
	}
	if !anyOK && len(boards) > 0 {
		return "data unavailable"
	}
	return ""
}
