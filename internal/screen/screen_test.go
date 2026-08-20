package screen

import (
	"context"
	"testing"
	"time"
)

type stubScreen struct{ name string }

func (s stubScreen) Name() string { return s.name }
func (s stubScreen) Render(ctx context.Context, now time.Time, width, height int) ([]byte, error) {
	return []byte(s.name), nil
}

// slot is a terse constructor for the test tables below.
func slot(name string, skipRush bool) Slot {
	return Slot{Screen: stubScreen{name}, SkipDuringRush: skipRush}
}

func TestRotationCyclesInOrder(t *testing.T) {
	r := NewRotation(slot("muni", false), slot("cat", false))
	want := []string{"muni", "cat", "muni", "cat", "muni"}
	for i, w := range want {
		if got := r.Next(false).Name(); got != w {
			t.Fatalf("Next() #%d = %q, want %q", i, got, w)
		}
	}
}

func TestNextSkipsRushScreensDuringRush(t *testing.T) {
	r := NewRotation(
		slot("muni-radar", false),
		slot("muni-board", false),
		slot("muni-stream", false),
		slot("cat", true),
	)
	// The cat drops out entirely: every wake lands on a muni screen.
	want := []string{"muni-radar", "muni-board", "muni-stream", "muni-radar", "muni-board"}
	for i, w := range want {
		if got := r.Next(true).Name(); got != w {
			t.Fatalf("rush Next() #%d = %q, want %q", i, got, w)
		}
	}
}

func TestNextResumesRotationAfterRush(t *testing.T) {
	r := NewRotation(slot("muni", false), slot("cat", true))
	// Consumes the muni slot, leaving the cursor on the cat.
	if got := r.Next(true).Name(); got != "muni" {
		t.Fatalf("rush Next() = %q, want muni", got)
	}
	// Now the cursor is on a skipped slot: the cat is stepped over rather than
	// stalling the rotation there, and the wake still shows a muni screen.
	if got := r.Next(true).Name(); got != "muni" {
		t.Fatalf("rush Next() #2 = %q, want muni", got)
	}
	// Rush ends with the rotation mid-cycle; it picks up where it stands
	// instead of restarting, so the cat comes back on the next ordinary wake.
	if got := r.Next(false).Name(); got != "cat" {
		t.Fatalf("post-rush Next() = %q, want cat", got)
	}
	if got := r.Next(false).Name(); got != "muni" {
		t.Fatalf("post-rush Next() #2 = %q, want muni", got)
	}
}

func TestNextOutsideRushKeepsSkippedScreens(t *testing.T) {
	r := NewRotation(slot("muni", false), slot("cat", true))
	want := []string{"muni", "cat", "muni", "cat"}
	for i, w := range want {
		if got := r.Next(false).Name(); got != w {
			t.Fatalf("Next() #%d = %q, want %q", i, got, w)
		}
	}
}

// Config validation rejects an all-skipped rotation, but Next must still
// return a screen rather than spin or panic if one ever reaches it.
func TestNextWithEverySlotSkippedStillReturnsAScreen(t *testing.T) {
	r := NewRotation(slot("cat", true), slot("dog", true))
	for i := range 4 {
		if got := r.Next(true); got == nil {
			t.Fatalf("rush Next() #%d = nil, want a screen", i)
		}
	}
}

func TestPeekDoesNotAdvance(t *testing.T) {
	r := NewRotation(slot("muni", false), slot("cat", false))
	if got := r.Peek().Name(); got != "muni" {
		t.Fatalf("Peek() = %q, want muni", got)
	}
	if got := r.Peek().Name(); got != "muni" {
		t.Fatalf("second Peek() = %q, want muni (must not advance)", got)
	}
	if got := r.Next(false).Name(); got != "muni" {
		t.Fatalf("Next() after Peek = %q, want muni", got)
	}
}

func TestByName(t *testing.T) {
	r := NewRotation(slot("muni", false), slot("cat", true))
	if s, ok := r.ByName("cat"); !ok || s.Name() != "cat" {
		t.Fatalf("ByName(cat) = %v, %v", s, ok)
	}
	if _, ok := r.ByName("dog"); ok {
		t.Fatal("ByName(dog) should not be found")
	}
}

// All backs renderWithFallback, which must be able to reach a rush-skipped
// screen: showing a cat beats blanking the panel when every muni render fails.
func TestAllIncludesRushSkippedScreens(t *testing.T) {
	r := NewRotation(slot("muni", false), slot("cat", true))
	all := r.All()
	if len(all) != 2 || all[0].Name() != "muni" || all[1].Name() != "cat" {
		t.Fatalf("All() = %v, want [muni cat]", all)
	}
}
