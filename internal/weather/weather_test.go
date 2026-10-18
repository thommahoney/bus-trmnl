package weather

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/forecast.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func pacific(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestParseFixture(t *testing.T) {
	loc := pacific(t)
	f, err := parse(fixture(t), loc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(f.Hours) != 72 || len(f.Days) != 3 {
		t.Fatalf("got %d hours, %d days; want 72, 3", len(f.Hours), len(f.Days))
	}
	if want := time.Date(2026, 10, 3, 0, 0, 0, 0, loc); !f.Hours[0].Time.Equal(want) {
		t.Fatalf("first hour = %v, want %v", f.Hours[0].Time, want)
	}
	if h := f.Hours[9]; h.Temp != 66.4 || h.Wind != 4.5 || h.WindDir != 77 {
		t.Fatalf("9am hour = %+v", h)
	}

	if c := f.Current; c.Temp != 66.4 || c.FeelsLike != 67.8 || !c.IsDay || c.Code != 0 {
		t.Fatalf("current = %+v", c)
	}

	day, ok := f.Day(time.Date(2026, 10, 4, 21, 30, 0, 0, loc))
	if !ok {
		t.Fatal("Day(Oct 4) not found")
	}
	if day.High != 86.9 || day.Low != 55.9 {
		t.Fatalf("Oct 4 = %+v", day)
	}
	if want := time.Date(2026, 10, 4, 7, 8, 0, 0, loc); !day.Sunrise.Equal(want) {
		t.Fatalf("sunrise = %v, want %v", day.Sunrise, want)
	}
	if _, ok := f.Day(time.Date(2026, 10, 9, 0, 0, 0, 0, loc)); ok {
		t.Fatal("Day(Oct 9) found, want it outside the forecast")
	}

	from := time.Date(2026, 10, 3, 6, 0, 0, 0, loc)
	hours := f.HoursBetween(from, from.Add(18*time.Hour))
	if len(hours) != 18 || hours[0].Time.Hour() != 6 || hours[17].Time.Hour() != 23 {
		t.Fatalf("HoursBetween(6a, midnight) = %d hours", len(hours))
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"api error":         `{"error":true,"reason":"Latitude must be in range of -90 to 90°."}`,
		"not json":          `<html>bad gateway</html>`,
		"no hourly data":    `{"current":{"time":"2026-10-03T09:00"},"hourly":{"time":[]}}`,
		"mismatched series": `{"current":{"time":"2026-10-03T09:00"},"hourly":{"time":["2026-10-03T00:00"],"temperature_2m":[]}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(body), time.UTC); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestClientRequestsUnitsAndZone(t *testing.T) {
	body := fixture(t)
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		got = map[string]string{
			"path":     r.URL.Path,
			"lat":      q.Get("latitude"),
			"lon":      q.Get("longitude"),
			"temp":     q.Get("temperature_unit"),
			"wind":     q.Get("wind_speed_unit"),
			"precip":   q.Get("precipitation_unit"),
			"timezone": q.Get("timezone"),
			"models":   q.Get("models"),
		}
		w.Write(body)
	}))
	defer srv.Close()

	c := New(Options{Latitude: 37.7624, Longitude: -122.4662, Units: Metric, Model: ModelNBM, Location: pacific(t), BaseURL: srv.URL})
	f, err := c.Forecast(context.Background())
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if f.Units != Metric || f.Fetched.IsZero() {
		t.Fatalf("Units = %q, Fetched = %v", f.Units, f.Fetched)
	}
	want := map[string]string{
		"path": "/forecast", "lat": "37.7624", "lon": "-122.4662",
		"temp": "celsius", "wind": "kmh", "precip": "mm", "timezone": "America/Los_Angeles",
		"models": "ncep_nbm_conus",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
}

func TestClientBestMatchSendsNoModel(t *testing.T) {
	body := fixture(t)
	for _, model := range []string{"", ModelBestMatch} {
		var has bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			has = r.URL.Query().Has("models")
			w.Write(body)
		}))
		if _, err := New(Options{Model: model, BaseURL: srv.URL}).Forecast(context.Background()); err != nil {
			t.Fatalf("Forecast: %v", err)
		}
		srv.Close()
		if has {
			t.Errorf("model %q: request should leave the model choice to Open-Meteo", model)
		}
	}
}

func TestClientReportsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":true,"reason":"bad latitude"}`))
	}))
	defer srv.Close()

	if _, err := New(Options{BaseURL: srv.URL}).Forecast(context.Background()); err == nil {
		t.Fatal("expected an error for an HTTP 400")
	}
}

type countingFetcher struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *countingFetcher) Forecast(ctx context.Context) (*Forecast, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &Forecast{Fetched: time.Now()}, nil
}

func TestEnsureFreshThrottles(t *testing.T) {
	f := &countingFetcher{}
	s := NewStore(f, time.Hour)

	s.EnsureFresh(context.Background())
	s.EnsureFresh(context.Background())
	if f.calls != 1 {
		t.Fatalf("calls = %d, want 1 (second EnsureFresh should hit the cache)", f.calls)
	}
	if s.Snapshot() == nil {
		t.Fatal("Snapshot() = nil after a successful fetch")
	}

	s.mu.Lock()
	s.lastAttempt = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	s.EnsureFresh(context.Background())
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2 after the cache went stale", f.calls)
	}
}

func TestEnsureFreshFailureKeepsLastForecastAndRetriesSooner(t *testing.T) {
	f := &countingFetcher{}
	s := NewStore(f, time.Hour)
	s.EnsureFresh(context.Background())
	good := s.Snapshot()

	f.err = errors.New("open-meteo down")
	s.mu.Lock()
	s.lastAttempt = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	s.EnsureFresh(context.Background())
	s.EnsureFresh(context.Background())
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2 (failures must not trigger immediate retries)", f.calls)
	}
	if s.Snapshot() != good {
		t.Fatal("a failed fetch should keep the last good forecast")
	}

	// Past the retry interval but well inside the poll interval.
	s.mu.Lock()
	s.lastAttempt = time.Now().Add(-retryInterval - time.Second)
	s.mu.Unlock()
	s.EnsureFresh(context.Background())
	if f.calls != 3 {
		t.Fatalf("calls = %d, want 3 (a failure should retry after retryInterval)", f.calls)
	}
}

func TestSnapshotEmptyBeforeFirstFetch(t *testing.T) {
	s := NewStore(&countingFetcher{err: errors.New("down")}, time.Hour)
	s.EnsureFresh(context.Background())
	if s.Snapshot() != nil {
		t.Fatal("Snapshot() should be nil when nothing has ever been fetched")
	}
}
