package screen

import (
	"context"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/board"
	"github.com/thommahoney/bus-trmnl/internal/config"
	"github.com/thommahoney/bus-trmnl/internal/render"
)

// Muni renders the MUNI arrivals board. It refreshes the 511 cache on demand,
// so 511 is only contacted when this screen actually renders.
type Muni struct {
	store   *board.Store
	refresh config.RefreshConfig
}

// NewMuni creates the MUNI arrivals screen.
func NewMuni(store *board.Store, refresh config.RefreshConfig) *Muni {
	return &Muni{store: store, refresh: refresh}
}

// Name implements Screen.
func (m *Muni) Name() string { return "muni" }

// Render implements Screen.
func (m *Muni) Render(ctx context.Context, now time.Time, width, height int) ([]byte, error) {
	m.store.EnsureFresh(ctx)
	boards, fetchStats := m.store.Snapshot()
	meta := &render.Metadata{FetchStats: fetchStats, RefreshRate: m.refresh.RateAt(now)}
	return render.Screen(boards, now, width, height, meta)
}
