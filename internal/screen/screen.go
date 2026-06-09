// Package screen defines the full-panel screens the device cycles through
// and the rotation that orders them.
package screen

import (
	"context"
	"sync"
	"time"
)

// Screen produces a full-panel PNG for one slot in the rotation.
type Screen interface {
	// Name is a short slug used in filenames, logs and the /latest preview.
	Name() string
	Render(ctx context.Context, now time.Time, width, height int) ([]byte, error)
}

// Rotation cycles through screens, one per device wake.
type Rotation struct {
	mu      sync.Mutex
	screens []Screen
	next    int
}

// NewRotation creates a Rotation starting at the first screen.
func NewRotation(screens ...Screen) *Rotation {
	return &Rotation{screens: screens}
}

// Next returns the current screen and advances the rotation.
func (r *Rotation) Next() Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.screens[r.next]
	r.next = (r.next + 1) % len(r.screens)
	return s
}

// Peek returns the current screen without advancing the rotation.
func (r *Rotation) Peek() Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.screens[r.next]
}

// ByName returns the named screen, if configured.
func (r *Rotation) ByName(name string) (Screen, bool) {
	for _, s := range r.screens {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

// All returns the screens in rotation order.
func (r *Rotation) All() []Screen {
	return append([]Screen(nil), r.screens...)
}
