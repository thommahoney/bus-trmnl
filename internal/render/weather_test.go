package render

import (
	"testing"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/weather"
)

// sampleForecast builds three days of hourly data starting at midnight on the
// day of now, with a little rain in the afternoons.
func sampleForecast(now time.Time, units string) *weather.Forecast {
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	f := &weather.Forecast{
		Current: weather.Current{Time: now, Temp: 62.4, FeelsLike: 60.1, Wind: 7.2, WindDir: 270, Code: 2, IsDay: true},
		Units:   units,
		Fetched: now.Add(-10 * time.Minute),
	}
	for day := 0; day < 3; day++ {
		date := midnight.AddDate(0, 0, day)
		f.Days = append(f.Days, weather.Day{
			Date: date, High: 78.4, Low: 55.6, Code: 61,
			Sunrise: date.Add(7*time.Hour + 7*time.Minute),
			Sunset:  date.Add(18*time.Hour + 49*time.Minute),
		})
		for hr := 0; hr < 24; hr++ {
			h := weather.Hour{
				Time:    date.Add(time.Duration(hr) * time.Hour),
				Temp:    56 + 22*float64(12-abs(hr-14))/12,
				Wind:    4 + float64(hr%7),
				Gust:    6 + float64(hr%7)*2.5,
				WindDir: hr * 15,
				Code:    2,
			}
			if hr >= 13 && hr <= 17 {
				h.PrecipProb, h.Precip = (hr-12)*20, 0.03
			}
			f.Hours = append(f.Hours, h)
		}
	}
	return f
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sampleWeatherIn(tick int) WeatherIn {
	now := time.Date(2026, 10, 3, 9, 54, 0, 0, time.UTC)
	return WeatherIn{
		Forecast: sampleForecast(now, weather.Imperial),
		Label:    "Inner Sunset",
		Now:      now,
		Tick:     tick,
		Width:    DefaultWidth,
		Height:   DefaultHeight,
		Battery:  Battery{Percent: 84, Present: true},
	}
}

func TestWeatherRendersValidPNG(t *testing.T) {
	// A full cycle of the drift × header-position motion.
	for tick := 0; tick < 2*len(weatherShift); tick++ {
		data, err := Weather(sampleWeatherIn(tick))
		if err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		assertPNG(t, data, "weather")
	}
}

func TestWeatherRendersVariants(t *testing.T) {
	base := sampleWeatherIn(0)
	day := base.Now.Truncate(24 * time.Hour)

	cases := map[string]func(*WeatherIn){
		"metric":             func(in *WeatherIn) { in.Forecast.Units = weather.Metric },
		"evening (tomorrow)": func(in *WeatherIn) { in.Now = day.Add(21 * time.Hour) },
		"before the chart":   func(in *WeatherIn) { in.Now = day.Add(3 * time.Hour) },
		"stale forecast":     func(in *WeatherIn) { in.Forecast.Fetched = in.Now.Add(-5 * time.Hour) },
		"no label":           func(in *WeatherIn) { in.Label = "" },
		"no daily summary":   func(in *WeatherIn) { in.Forecast.Days = nil },
		"dry and flat": func(in *WeatherIn) {
			for i := range in.Forecast.Hours {
				h := &in.Forecast.Hours[i]
				h.Temp, h.Wind, h.Gust, h.PrecipProb, h.Precip = 60, 0, 0, 0, 0
			}
		},
		"every condition glyph": func(in *WeatherIn) { in.Forecast.Current.Code = 95 },
		"night":                 func(in *WeatherIn) { in.Forecast.Current.IsDay = false },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := sampleWeatherIn(1)
			mutate(&in)
			data, err := Weather(in)
			if err != nil {
				t.Fatalf("Weather: %v", err)
			}
			assertPNG(t, data, name)
		})
	}
}

func TestWeatherGlyphsDraw(t *testing.T) {
	dc, _, _ := designCanvas(400, 400)
	for _, code := range []int{0, 1, 2, 3, 45, 51, 61, 71, 80, 95, 1234} {
		for _, day := range []bool{true, false} {
			drawWeatherGlyph(dc, glyphFor(code, day), 200, 200, 80)
		}
	}
}

func TestWeatherErrorsWithoutCoverage(t *testing.T) {
	if _, err := Weather(WeatherIn{Now: time.Now()}); err == nil {
		t.Fatal("expected an error with no forecast")
	}
	// A forecast fetched days ago no longer covers the day being drawn; the
	// screen must fail (so the server falls back) rather than draw nothing.
	in := sampleWeatherIn(0)
	in.Now = in.Now.AddDate(0, 0, 5)
	if _, err := Weather(in); err == nil {
		t.Fatal("expected an error when the forecast does not cover the day")
	}
}

func TestWeatherDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		now      time.Time
		wantDay  int
		tomorrow bool
	}{
		{"morning", time.Date(2026, 10, 3, 7, 50, 0, 0, loc), 3, false},
		{"small hours", time.Date(2026, 10, 3, 0, 30, 0, 0, loc), 3, false},
		{"just before 8pm", time.Date(2026, 10, 3, 19, 59, 0, 0, loc), 3, false},
		{"8pm flips to tomorrow", time.Date(2026, 10, 3, 20, 0, 0, 0, loc), 4, true},
		{"late evening", time.Date(2026, 10, 3, 23, 59, 0, 0, loc), 4, true},
		{"month rollover", time.Date(2026, 10, 31, 21, 0, 0, 0, loc), 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			day, tomorrow := weatherDay(c.now)
			if day.Day() != c.wantDay || tomorrow != c.tomorrow {
				t.Fatalf("weatherDay(%v) = %v, %v; want day %d, %v", c.now, day, tomorrow, c.wantDay, c.tomorrow)
			}
			if day.Hour() != 0 || day.Location() != loc {
				t.Fatalf("weatherDay(%v) = %v, want local midnight", c.now, day)
			}
		})
	}
}

// Every region must stay on the panel and clear of its neighbours at every
// step of the drift, with the header both above and below the charts.
func TestWeatherFrameOnCanvasAndDisjoint(t *testing.T) {
	for tick := -3; tick < 4*len(weatherShift); tick++ {
		fw, fh, f := computeWeatherFrame(DefaultWidth, DefaultHeight, tick)
		boxes := map[string]box{"header": f.header, "temp": f.temp, "wind": f.wind, "rain": f.rain, "axis": f.axis}
		for name, b := range boxes {
			if b.x < 0 || b.y < 0 || b.x+b.w > fw || b.y+b.h > fh {
				t.Fatalf("tick %d: %s %+v leaves the %vx%v panel", tick, name, b, fw, fh)
			}
			for other, o := range boxes {
				if name < other && b.y < o.y+o.h-0.01 && o.y < b.y+b.h-0.01 {
					t.Fatalf("tick %d: %s %+v overlaps %s %+v", tick, name, b, other, o)
				}
			}
		}
		if last := f.colX(weatherHours-1) + f.colW/2; last > f.temp.x+f.temp.w+0.01 {
			t.Fatalf("tick %d: last hour column ends at %v, past the strip edge %v", tick, last, f.temp.x+f.temp.w)
		}
		if f.plotX <= f.temp.x {
			t.Fatalf("tick %d: no gutter for the strip labels", tick)
		}
	}
}

func TestWeatherFrameMoves(t *testing.T) {
	_, _, a := computeWeatherFrame(DefaultWidth, DefaultHeight, 0)
	_, _, b := computeWeatherFrame(DefaultWidth, DefaultHeight, 1)
	if a.header.y >= a.temp.y || b.header.y <= b.temp.y {
		t.Fatalf("header should sit above the charts on even ticks and below on odd: %+v / %+v", a.header, b.header)
	}
	if a.plotX == b.plotX {
		t.Fatal("consecutive renders should drift horizontally")
	}
}

func TestWeatherFormatting(t *testing.T) {
	for hour, want := range map[int]string{0: "12a", 6: "6a", 12: "12p", 15: "3p", 23: "11p"} {
		if got := hourLabel(hour); got != want {
			t.Errorf("hourLabel(%d) = %q, want %q", hour, got, want)
		}
	}
	for deg, want := range map[int]string{0: "N", 22: "N", 23: "NE", 90: "E", 180: "S", 270: "W", 338: "N", 359: "N", -90: "W"} {
		if got := compass(deg); got != want {
			t.Errorf("compass(%d) = %q, want %q", deg, got, want)
		}
	}
	if got := degrees(61.5); got != "62°" {
		t.Errorf("degrees(61.5) = %q", got)
	}
	if got := shortTime(time.Date(2026, 10, 3, 18, 49, 0, 0, time.UTC)); got != "6:49p" {
		t.Errorf("shortTime = %q", got)
	}
	if got := precipLabel(0.004, false); got != "0.01 in" {
		t.Errorf("precipLabel(trace) = %q", got)
	}
	if got := precipLabel(2.34, true); got != "2.3 mm" {
		t.Errorf("precipLabel(metric) = %q", got)
	}
}

func TestConditions(t *testing.T) {
	cases := []struct {
		code  int
		day   bool
		text  string
		glyph string
	}{
		{0, true, "Sunny", glyphSun},
		{0, false, "Clear", glyphMoon},
		{2, false, "Partly cloudy", glyphPartlyNight},
		{3, true, "Overcast", glyphCloud},
		{45, true, "Fog", glyphFog},
		{63, true, "Rain", glyphRain},
		{95, true, "Thunderstorm", glyphStorm},
	}
	for _, c := range cases {
		if got := conditionText(c.code, c.day); got != c.text {
			t.Errorf("conditionText(%d, %v) = %q, want %q", c.code, c.day, got, c.text)
		}
		if got := glyphFor(c.code, c.day); got != c.glyph {
			t.Errorf("glyphFor(%d, %v) = %q, want %q", c.code, c.day, got, c.glyph)
		}
	}
}
