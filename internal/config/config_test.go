package config

import (
	"testing"
	"time"
)

func TestRateAt(t *testing.T) {
	r := RefreshConfig{
		RushRate:    Duration(30 * time.Second),
		DefaultRate: Duration(60 * time.Second),
		RushWindows: []Window{{
			Days:  []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
			Start: "07:45",
			End:   "08:15",
		}},
	}

	cases := []struct {
		name string
		// 2026-06-01 is a Monday, 2026-06-06 is a Saturday.
		when time.Time
		want time.Duration
	}{
		{"weekday in window", time.Date(2026, 6, 1, 7, 50, 0, 0, time.UTC), 30 * time.Second},
		{"weekday window start inclusive", time.Date(2026, 6, 1, 7, 45, 0, 0, time.UTC), 30 * time.Second},
		{"weekday window end exclusive", time.Date(2026, 6, 1, 8, 15, 0, 0, time.UTC), 60 * time.Second},
		{"weekday before window", time.Date(2026, 6, 1, 7, 44, 0, 0, time.UTC), 60 * time.Second},
		{"weekend in window time", time.Date(2026, 6, 6, 7, 50, 0, 0, time.UTC), 60 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.RateAt(c.when); got != c.want {
				t.Fatalf("RateAt(%v) = %v, want %v", c.when, got, c.want)
			}
		})
	}
}

func TestParseHHMM(t *testing.T) {
	if m, err := parseHHMM("07:45"); err != nil || m != 7*60+45 {
		t.Fatalf("parseHHMM(07:45) = %d, %v", m, err)
	}
	if _, err := parseHHMM("24:00"); err == nil {
		t.Fatal("expected error for 24:00")
	}
	if _, err := parseHHMM("bad"); err == nil {
		t.Fatal("expected error for bad input")
	}
}

func TestDistinctStops(t *testing.T) {
	c := Config{Boards: []BoardConfig{
		{StopCode: "111"},
		{StopCode: "111"},
		{StopCode: "222"},
	}}
	got := c.DistinctStops()
	if len(got) != 2 {
		t.Fatalf("DistinctStops() = %v, want 2 unique", got)
	}
}
