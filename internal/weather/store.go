package weather

import (
	"context"
	"log"
	"sync"
	"time"
)

// retryInterval is how soon a failed fetch is retried. It is shorter than a
// typical poll interval so one blip doesn't leave the screen stale for half an
// hour, and long enough that an outage isn't hammered on every device wake.
const retryInterval = 5 * time.Minute

// Fetcher is the subset of the client the store needs. It lets tests
// substitute a fake.
type Fetcher interface {
	Forecast(ctx context.Context) (*Forecast, error)
}

// Store holds the latest forecast and refreshes it from Open-Meteo on demand.
type Store struct {
	client Fetcher
	poll   time.Duration

	fetchMu sync.Mutex // single-flights EnsureFresh

	mu          sync.RWMutex
	forecast    *Forecast
	lastAttempt time.Time
	lastErr     error
}

// NewStore creates an empty Store that refetches at most every poll.
func NewStore(client Fetcher, poll time.Duration) *Store {
	return &Store{client: client, poll: poll}
}

// Snapshot returns the last good forecast, or nil if none has been fetched.
// The forecast is shared and must not be modified.
func (s *Store) Snapshot() *Forecast {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.forecast
}

// EnsureFresh fetches a new forecast if the last attempt is older than the
// poll interval (or the shorter retry interval after a failure); otherwise it
// is a no-op, so callers may invoke it on every render. Concurrent callers
// single-flight. A failed fetch keeps the last good forecast.
func (s *Store) EnsureFresh(ctx context.Context) {
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()

	s.mu.RLock()
	last, failed := s.lastAttempt, s.lastErr != nil
	s.mu.RUnlock()
	wait := s.poll
	if failed && retryInterval < wait {
		wait = retryInterval
	}
	if !last.IsZero() && time.Since(last) < wait {
		return
	}

	f, err := s.client.Forecast(ctx)
	if err != nil {
		log.Printf("weather fetch failed: %v", err)
	}

	s.mu.Lock()
	s.lastAttempt = time.Now()
	s.lastErr = err
	if err == nil {
		s.forecast = f
	}
	s.mu.Unlock()
}
