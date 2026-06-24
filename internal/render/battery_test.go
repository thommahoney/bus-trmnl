package render

import (
	"context"
	"testing"
)

func TestBatteryLabel(t *testing.T) {
	cases := []struct {
		b    Battery
		want string
	}{
		{Battery{}, ""}, // absent => nothing
		{Battery{Percent: 0, Present: false}, ""}, // 0% but absent => nothing
		{Battery{Percent: 73, Present: true}, "73%"},
		{Battery{Percent: 0, Present: true}, "0%"},
		{Battery{Percent: 100, Present: true}, "100%"},
		{Battery{Percent: 150, Present: true}, "100%"}, // clamped high
		{Battery{Percent: -5, Present: true}, "0%"},    // clamped low
	}
	for _, c := range cases {
		if got := batteryLabel(c.b); got != c.want {
			t.Errorf("batteryLabel(%+v) = %q, want %q", c.b, got, c.want)
		}
	}
}

func TestBatteryContextRoundTrip(t *testing.T) {
	ctx := ContextWithBattery(context.Background(), Battery{Percent: 42, Present: true})
	got := BatteryFromContext(ctx)
	if !got.Present || got.Percent != 42 {
		t.Fatalf("round trip = %+v, want {42 true}", got)
	}
	// Absent from a bare context.
	if BatteryFromContext(context.Background()).Present {
		t.Error("expected absent battery from a bare context")
	}
}
