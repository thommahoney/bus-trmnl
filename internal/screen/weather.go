package screen

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/render"
	"github.com/thommahoney/bus-trmnl/internal/weather"
)

// Weather renders the day's hourly forecast. Like Muni it refreshes its cache
// on demand, so Open-Meteo is only contacted when this screen actually
// renders. The forecast changes hourly at best, so the per-screen tick counter
// relocates the layout every render to keep it from burning in.
type Weather struct {
	store *weather.Store
	label string
	tick  atomic.Uint64
}

// NewWeather creates a weather screen. label is an optional place name.
func NewWeather(store *weather.Store, label string) *Weather {
	return &Weather{store: store, label: label}
}

// Name implements Screen.
func (w *Weather) Name() string { return "weather" }

// Render implements Screen. With no forecast at all (nothing has ever been
// fetched) it returns an error so the server falls back to another screen
// rather than drawing an empty chart.
func (w *Weather) Render(ctx context.Context, now time.Time, width, height int) ([]byte, error) {
	w.store.EnsureFresh(ctx)
	f := w.store.Snapshot()
	if f == nil {
		return nil, errors.New("weather: no forecast available")
	}
	return render.Weather(render.WeatherIn{
		Forecast: f,
		Label:    w.label,
		Now:      now,
		Tick:     int(w.tick.Add(1) - 1),
		Width:    width,
		Height:   height,
		Battery:  render.BatteryFromContext(ctx),
	})
}
