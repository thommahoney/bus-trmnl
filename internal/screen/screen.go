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

// Slot is one screen in the rotation plus when it is eligible to be shown.
type Slot struct {
	Screen Screen
	// SkipDuringRush drops this screen from the rotation while a rush window
	// is in effect, so the commute screens are never interrupted during the
	// minutes that matter most.
	SkipDuringRush bool
}

// Rotation cycles through screens, one per device wake.
type Rotation struct {
	mu    sync.Mutex
	slots []Slot
	next  int
}

// NewRotation creates a Rotation starting at the first screen.
func NewRotation(slots ...Slot) *Rotation {
	return &Rotation{slots: slots}
}

// Next returns the current screen and advances the rotation. When rush is
// true, slots marked SkipDuringRush are passed over (and still advanced past,
// so the rotation resumes mid-cycle once rush ends rather than replaying).
func (r *Rotation) Next(rush bool) Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Bounded by the slot count so an all-skipped rotation cannot spin.
	for range r.slots {
		s := r.advance()
		if rush && s.SkipDuringRush {
			continue
		}
		return s.Screen
	}
	// Every slot is skipped during rush. Config validation rejects that, so
	// this is unreachable in practice; showing a skipped screen still beats
	// leaving the device with nothing.
	return r.advance().Screen
}

// advance returns the current slot and steps the cursor. Callers hold r.mu.
func (r *Rotation) advance() Slot {
	s := r.slots[r.next]
	r.next = (r.next + 1) % len(r.slots)
	return s
}

// Peek returns the current screen without advancing the rotation. It ignores
// rush windows: its callers are the setup handshake and the /latest preview,
// neither of which is the commute path.
func (r *Rotation) Peek() Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.slots[r.next].Screen
}

// ByName returns the named screen, if configured.
func (r *Rotation) ByName(name string) (Screen, bool) {
	for _, s := range r.slots {
		if s.Screen.Name() == name {
			return s.Screen, true
		}
	}
	return nil, false
}

// All returns every screen in rotation order, including ones skipped during
// rush: it backs renderWithFallback, where showing any screen beats blanking
// the panel.
func (r *Rotation) All() []Screen {
	out := make([]Screen, 0, len(r.slots))
	for _, s := range r.slots {
		out = append(out, s.Screen)
	}
	return out
}
