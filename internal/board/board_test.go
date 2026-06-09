package board

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/config"
	"github.com/thommahoney/bus-trmnl/internal/five11"
)

func visit(line, dir, dest, expected string) five11.MonitoredStopVisit {
	return five11.MonitoredStopVisit{MonitoredVehicleJourney: five11.MonitoredVehicleJourney{
		LineRef:           line,
		DirectionRef:      dir,
		PublishedLineName: five11.FlexString(line),
		DestinationName:   five11.FlexString(dest),
		MonitoredCall:     five11.MonitoredCall{ExpectedArrivalTime: expected},
	}}
}

func TestFilterArrivals(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	t1 := base.Add(5 * time.Minute).Format(time.RFC3339)
	t2 := base.Add(2 * time.Minute).Format(time.RFC3339)

	visits := []five11.MonitoredStopVisit{
		visit("43", "OB", "Forest Hill Station", t1),
		visit("44", "OB", "Forest Hill Station", t2),
		visit("6", "OB", "Haight", t2),                         // wrong line
		visit("43", "IB", "Forest Hill Station via Other", t2), // kept (no direction filter)
	}

	bc := config.BoardConfig{
		Lines:               []string{"43", "44"},
		DestinationContains: "forest hill",
		Max:                 5,
	}
	got := filterArrivals(bc, visits)
	if len(got) != 3 {
		t.Fatalf("got %d arrivals, want 3: %+v", len(got), got)
	}
	// Sorted soonest first (non-decreasing).
	for i := 1; i < len(got); i++ {
		if got[i].Expected.Before(got[i-1].Expected) {
			t.Fatalf("arrivals not sorted: %+v", got)
		}
	}
}

func TestFilterArrivalsMaxAndDirection(t *testing.T) {
	base := time.Now().UTC()
	mk := func(off time.Duration) string { return base.Add(off).Format(time.RFC3339) }
	visits := []five11.MonitoredStopVisit{
		visit("N", "IB", "Caltrain", mk(1*time.Minute)),
		visit("N", "OB", "Ocean Beach", mk(2*time.Minute)),
		visit("N", "IB", "Caltrain", mk(3*time.Minute)),
		visit("N", "IB", "Caltrain", mk(4*time.Minute)),
	}
	bc := config.BoardConfig{Lines: []string{"N"}, Direction: "IB", Max: 2}
	got := filterArrivals(bc, visits)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (max)", len(got))
	}
	for _, a := range got {
		if a.Destination != "Caltrain" {
			t.Fatalf("direction filter failed: %+v", a)
		}
	}
}

type countingFetcher struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *countingFetcher) StopMonitoring(ctx context.Context, stop string) ([]five11.MonitoredStopVisit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return nil, f.err
}

func ensureFreshConfig() *config.Config {
	return &config.Config{
		Five11: config.Five11Config{PollInterval: config.Duration(time.Hour)},
		Boards: []config.BoardConfig{{Title: "test", StopCode: "111"}},
	}
}

func TestEnsureFreshThrottles(t *testing.T) {
	f := &countingFetcher{}
	s := NewStore(ensureFreshConfig(), f)

	s.EnsureFresh(context.Background())
	s.EnsureFresh(context.Background())
	if f.calls != 1 {
		t.Fatalf("calls = %d, want 1 (second EnsureFresh should hit the cache)", f.calls)
	}

	s.mu.Lock()
	s.lastFetch.At = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	s.EnsureFresh(context.Background())
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2 after the cache went stale", f.calls)
	}
}

func TestEnsureFreshFailedFetchStillThrottles(t *testing.T) {
	f := &countingFetcher{err: errors.New("511 down")}
	s := NewStore(ensureFreshConfig(), f)

	s.EnsureFresh(context.Background())
	s.EnsureFresh(context.Background())
	if f.calls != 1 {
		t.Fatalf("calls = %d, want 1 (failures must not trigger immediate retries)", f.calls)
	}
	boards, _ := s.Snapshot()
	if boards[0].Err == nil {
		t.Fatal("board should carry the fetch error")
	}
}

func TestMinutesUntil(t *testing.T) {
	now := time.Now()
	a := Arrival{Expected: now.Add(90 * time.Second)}
	if got := a.MinutesUntil(now); got != 1 {
		t.Fatalf("MinutesUntil = %d, want 1", got)
	}
	past := Arrival{Expected: now.Add(-time.Minute)}
	if got := past.MinutesUntil(now); got != 0 {
		t.Fatalf("MinutesUntil(past) = %d, want 0", got)
	}
}
