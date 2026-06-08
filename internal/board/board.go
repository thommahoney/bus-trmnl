// Package board fetches, filters and caches arrival predictions per board.
package board

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/config"
	"github.com/thommahoney/bus-trmnl/internal/five11"
)

// Arrival is a single predicted departure shown on a board.
type Arrival struct {
	LineRef     string // short route code, e.g. "43", "N"
	Line        string
	Destination string
	Expected    time.Time
}

// MinutesUntil returns whole minutes until the arrival relative to now,
// clamped at zero.
func (a Arrival) MinutesUntil(now time.Time) int {
	d := a.Expected.Sub(now)
	if d < 0 {
		return 0
	}
	return int(d.Minutes())
}

// Board is the rendered state of one configured board.
type Board struct {
	Title    string
	Arrivals []Arrival
	Updated  time.Time
	Err      error
}

// FetchStats holds timing metadata from the last 511 poll cycle.
type FetchStats struct {
	Duration time.Duration // how long the fetch took
	At       time.Time     // when the fetch completed
}

// Store holds the latest board snapshots and refreshes them from 511.
type Store struct {
	cfg    *config.Config
	client *five11.Client

	mu        sync.RWMutex
	boards    []Board
	lastFetch FetchStats
}

// NewStore creates a Store with empty boards in config order.
func NewStore(cfg *config.Config, client *five11.Client) *Store {
	boards := make([]Board, len(cfg.Boards))
	for i, b := range cfg.Boards {
		boards[i] = Board{Title: b.Title}
	}
	return &Store{cfg: cfg, client: client, boards: boards}
}

// Snapshot returns a copy of the current board state, safe for rendering.
func (s *Store) Snapshot() ([]Board, FetchStats) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Board, len(s.boards))
	for i := range s.boards {
		b := s.boards[i]
		b.Arrivals = append([]Arrival(nil), s.boards[i].Arrivals...)
		out[i] = b
	}
	return out, s.lastFetch
}

// Run fetches once immediately and then on the configured poll interval until
// the context is cancelled.
func (s *Store) Run(ctx context.Context) {
	s.refresh(ctx)
	ticker := time.NewTicker(s.cfg.Five11.PollInterval.D())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refresh(ctx)
		}
	}
}

// refresh fetches every distinct stop once, then maps the visits onto boards.
func (s *Store) refresh(ctx context.Context) {
	fetchStart := time.Now()

	visitsByStop := map[string][]five11.MonitoredStopVisit{}
	errByStop := map[string]error{}
	for _, stop := range s.cfg.DistinctStops() {
		visits, err := s.client.StopMonitoring(ctx, stop)
		if err != nil {
			log.Printf("511 fetch stop %s failed: %v", stop, err)
			errByStop[stop] = err
			continue
		}
		visitsByStop[stop] = visits
	}

	fetchDuration := time.Since(fetchStart)
	now := time.Now()
	updated := make([]Board, len(s.cfg.Boards))
	for i, bc := range s.cfg.Boards {
		b := Board{Title: bc.Title, Updated: now}
		if err := errByStop[bc.StopCode]; err != nil {
			b.Err = err
			updated[i] = b
			continue
		}
		b.Arrivals = filterArrivals(bc, visitsByStop[bc.StopCode])
		updated[i] = b
	}

	s.mu.Lock()
	s.boards = updated
	s.lastFetch = FetchStats{Duration: fetchDuration, At: now}
	s.mu.Unlock()
}

// filterArrivals applies the board's line/destination/direction filters and
// returns up to Max arrivals sorted by soonest first.
func filterArrivals(bc config.BoardConfig, visits []five11.MonitoredStopVisit) []Arrival {
	var arrivals []Arrival
	for _, v := range visits {
		j := v.MonitoredVehicleJourney
		if len(bc.Lines) > 0 && !matchesLine(bc.Lines, j.LineRef, string(j.PublishedLineName)) {
			continue
		}
		if bc.Direction != "" && !strings.EqualFold(strings.TrimSpace(bc.Direction), strings.TrimSpace(j.DirectionRef)) {
			continue
		}
		dest := string(j.DestinationName)
		if bc.DestinationContains != "" && !strings.Contains(strings.ToLower(dest), strings.ToLower(bc.DestinationContains)) {
			continue
		}
		expected := parseTime(j.MonitoredCall.ExpectedArrivalTime)
		if expected.IsZero() {
			expected = parseTime(j.MonitoredCall.ExpectedDepartureTime)
		}
		if expected.IsZero() {
			continue
		}
		line := string(j.PublishedLineName)
		if line == "" {
			line = j.LineRef
		}
		arrivals = append(arrivals, Arrival{LineRef: j.LineRef, Line: line, Destination: dest, Expected: expected})
	}

	sort.Slice(arrivals, func(i, k int) bool {
		return arrivals[i].Expected.Before(arrivals[k].Expected)
	})
	if bc.Max > 0 && len(arrivals) > bc.Max {
		arrivals = arrivals[:bc.Max]
	}
	return arrivals
}

func matchesLine(lines []string, lineRef, published string) bool {
	for _, l := range lines {
		if strings.EqualFold(l, lineRef) || strings.EqualFold(l, published) {
			return true
		}
	}
	return false
}

// parseTime parses a 511 ISO-8601 timestamp, returning the zero time on
// failure or empty input.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
