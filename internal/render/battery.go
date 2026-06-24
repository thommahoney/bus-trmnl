package render

import (
	"context"
	"strconv"
)

// Battery is the device's reported charge, plumbed from the /api/display request
// headers through the Screen interface to the screens that show a clock. Present
// is false when there is no device telemetry (e.g. the /latest preview or
// first-boot setup), in which case nothing is drawn.
type Battery struct {
	Percent int
	Present bool
}

type batteryCtxKey struct{}

// ContextWithBattery attaches device battery telemetry to ctx so a Screen's
// Render can pick it up without widening the interface.
func ContextWithBattery(ctx context.Context, b Battery) context.Context {
	return context.WithValue(ctx, batteryCtxKey{}, b)
}

// BatteryFromContext returns the battery telemetry attached to ctx, or a
// zero (absent) Battery if none was set.
func BatteryFromContext(ctx context.Context) Battery {
	if b, ok := ctx.Value(batteryCtxKey{}).(Battery); ok {
		return b
	}
	return Battery{}
}

// batteryLabel formats the charge for display next to the clock, e.g. "73%".
// It returns "" when no telemetry is present so the clock renders alone.
func batteryLabel(b Battery) string {
	if !b.Present {
		return ""
	}
	p := b.Percent
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return strconv.Itoa(p) + "%"
}
