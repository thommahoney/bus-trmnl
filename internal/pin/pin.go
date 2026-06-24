// Package pin holds the currently "pinned" recipe — the one that takes over the
// display in focus mode — together with the time it expires. It is safe for
// concurrent use and persists to disk so a restart mid-cook doesn't drop the
// recipe.
package pin

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/recipe"
)

// state is the on-disk representation.
type state struct {
	Recipe recipe.Recipe `json:"recipe"`
	Until  time.Time     `json:"until"`
}

// Store guards the pinned recipe.
type Store struct {
	path string
	ttl  time.Duration

	mu sync.Mutex
	st *state // nil when nothing is pinned
}

// NewStore creates a pin store backed by path (e.g. /data/pin.json), holding a
// pin for ttl after each upload. Any pin persisted from a previous run is
// loaded, and dropped if it has already expired.
func NewStore(path string, ttl time.Duration) *Store {
	s := &Store{path: path, ttl: ttl}
	s.load()
	return s
}

// Set pins rec, expiring ttl from now, and persists it. A new pin replaces any
// existing one and resets the clock.
func (s *Store) Set(rec recipe.Recipe, now time.Time) {
	s.mu.Lock()
	s.st = &state{Recipe: rec, Until: now.Add(s.ttl)}
	s.save()
	s.mu.Unlock()
}

// Clear removes any pinned recipe.
func (s *Store) Clear() {
	s.mu.Lock()
	s.st = nil
	_ = os.Remove(s.path)
	s.mu.Unlock()
}

// Active returns the pinned recipe and true if one is pinned and not yet
// expired at now. An expired pin is cleared as a side effect.
func (s *Store) Active(now time.Time) (recipe.Recipe, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st == nil {
		return recipe.Recipe{}, false
	}
	if !now.Before(s.st.Until) {
		s.st = nil
		_ = os.Remove(s.path)
		return recipe.Recipe{}, false
	}
	return s.st.Recipe, true
}

// Current returns the pinned recipe and its expiry without checking the clock,
// for previews. ok is false when nothing is pinned.
func (s *Store) Current() (rec recipe.Recipe, until time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st == nil {
		return recipe.Recipe{}, time.Time{}, false
	}
	return s.st.Recipe, s.st.Until, true
}

// save writes the current state to disk. The caller holds s.mu.
func (s *Store) save() {
	if s.path == "" || s.st == nil {
		return
	}
	b, err := json.Marshal(s.st)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("pin: persist failed (%s): %v", s.path, err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("pin: persist rename failed (%s): %v", s.path, err)
	}
}

// load reads a persisted pin, discarding it if expired or unreadable.
func (s *Store) load() {
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return
	}
	if time.Now().Before(st.Until) {
		s.st = &st
	} else {
		_ = os.Remove(s.path)
	}
}

// EnsureDir makes the directory holding the pin file, best effort.
func EnsureDir(path string) {
	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
	}
}
