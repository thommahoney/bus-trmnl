package pin

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/recipe"
)

func sample() recipe.Recipe {
	return recipe.Recipe{Title: "Pancakes", Steps: []string{"Mix", "Cook"}}
}

func TestSetActiveExpiry(t *testing.T) {
	s := NewStore("", time.Hour)
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

	if _, ok := s.Active(now); ok {
		t.Fatal("nothing should be pinned initially")
	}
	s.Set(sample(), now)

	if r, ok := s.Active(now.Add(59 * time.Minute)); !ok || r.Title != "Pancakes" {
		t.Fatalf("expected active pin, got ok=%v r=%+v", ok, r)
	}
	if _, ok := s.Active(now.Add(61 * time.Minute)); ok {
		t.Fatal("pin should have expired after ttl")
	}
	// Expiry clears it.
	if _, _, ok := s.Current(); ok {
		t.Fatal("expired pin should be cleared")
	}
}

func TestClear(t *testing.T) {
	s := NewStore("", time.Hour)
	now := time.Now()
	s.Set(sample(), now)
	s.Clear()
	if _, ok := s.Active(now); ok {
		t.Fatal("Clear should unpin")
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pin.json")
	now := time.Now()

	s1 := NewStore(path, time.Hour)
	s1.Set(sample(), now)

	// A fresh store at the same path should reload the unexpired pin.
	s2 := NewStore(path, time.Hour)
	r, ok := s2.Active(now)
	if !ok || r.Title != "Pancakes" {
		t.Fatalf("expected reloaded pin, got ok=%v r=%+v", ok, r)
	}
}

func TestPersistenceExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pin.json")

	s1 := NewStore(path, time.Hour)
	s1.Set(sample(), time.Now().Add(-2*time.Hour)) // already expired

	s2 := NewStore(path, time.Hour)
	if _, _, ok := s2.Current(); ok {
		t.Fatal("expired persisted pin should not reload")
	}
}
