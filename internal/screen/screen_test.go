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

func TestRotationCyclesInOrder(t *testing.T) {
	r := NewRotation(stubScreen{"muni"}, stubScreen{"cat"})
	want := []string{"muni", "cat", "muni", "cat", "muni"}
	for i, w := range want {
		if got := r.Next().Name(); got != w {
			t.Fatalf("Next() #%d = %q, want %q", i, got, w)
		}
	}
}

func TestPeekDoesNotAdvance(t *testing.T) {
	r := NewRotation(stubScreen{"muni"}, stubScreen{"cat"})
	if got := r.Peek().Name(); got != "muni" {
		t.Fatalf("Peek() = %q, want muni", got)
	}
	if got := r.Peek().Name(); got != "muni" {
		t.Fatalf("second Peek() = %q, want muni (must not advance)", got)
	}
	if got := r.Next().Name(); got != "muni" {
		t.Fatalf("Next() after Peek = %q, want muni", got)
	}
}

func TestByName(t *testing.T) {
	r := NewRotation(stubScreen{"muni"}, stubScreen{"cat"})
	if s, ok := r.ByName("cat"); !ok || s.Name() != "cat" {
		t.Fatalf("ByName(cat) = %v, %v", s, ok)
	}
	if _, ok := r.ByName("dog"); ok {
		t.Fatal("ByName(dog) should not be found")
	}
}
