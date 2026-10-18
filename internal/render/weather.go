package render

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/fogleman/gg"

	"github.com/thommahoney/bus-trmnl/internal/weather"
)

// The weather screen: current conditions in a header, then three stacked strips
// (temperature, wind, rain) sharing one hour axis, so a glance down any column
// reads one hour. The forecast only changes hourly, which would make this the
// most static frame in the rotation — so, like the MUNI designs, the whole
// composition is relocated every render: it drifts by weatherShift and the
// header swaps between the top and the bottom of the panel.

// The chart covers one day from weatherFirstHour to midnight. From
// weatherTomorrowAt onward today's chart is mostly history, so it shows
// tomorrow instead.
const (
	weatherFirstHour  = 6
	weatherHours      = 24 - weatherFirstHour
	weatherTomorrowAt = 20
)

// weatherStaleAfter is how old a forecast gets before the screen admits it
// with an "as of" note. It only happens when Open-Meteo has been unreachable
// for a few poll intervals.
const weatherStaleAfter = 90 * time.Minute

// WeatherIn is the input to the weather screen render.
type WeatherIn struct {
	Forecast *weather.Forecast
	Label    string // optional place name
	Now      time.Time
	Tick     int // monotonic per-render counter; drives anti-burn-in motion
	Width    int
	Height   int
	Battery  Battery // device charge, shown next to the clock
}

// weatherDay returns local midnight of the day the chart covers, and whether
// that is tomorrow rather than today.
func weatherDay(now time.Time) (day time.Time, tomorrow bool) {
	y, m, d := now.Date()
	if now.Hour() >= weatherTomorrowAt {
		d++
		tomorrow = true
	}
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), tomorrow
}

// box is an axis-aligned region of the panel.
type box struct{ x, y, w, h float64 }

// weatherFrame is the per-render geometry: where the header and each strip sit.
// Every hour column is colW wide, the first starting at plotX; the gutter left
// of plotX holds the strip labels.
type weatherFrame struct {
	header, temp, wind, rain, axis box
	plotX, colW                    float64
}

// colX returns the centre of hour column i.
func (f weatherFrame) colX(i float64) float64 { return f.plotX + (i+0.5)*f.colW }

// weatherShift is the per-render drift, in pixels at the default panel size.
// Seven entries against the two header positions gives a 14-render cycle.
var weatherShift = [][2]float64{{0, 0}, {30, -20}, {-28, 18}, {16, 26}, {-34, -24}, {34, 10}, {-12, -28}}

func computeWeatherFrame(width, height, tick int) (fw, fh float64, f weatherFrame) {
	fw, fh = dims(width, height)
	sh := pick2(weatherShift, tick)
	left := fw*0.05 + sh[0]*fw/DefaultWidth
	top := fh*0.05 + sh[1]*fh/DefaultHeight
	w := fw * 0.90

	const (
		headerH = 0.24
		tempH   = 0.215
		windH   = 0.17
		rainH   = 0.15
		axisH   = 0.075 // hour ticks, sunrise/sunset glyphs, then the hour labels
		bigGap  = 0.02  // between the header and the charts
		gap     = 0.015
	)
	chartsH := tempH + gap + windH + gap + rainH + axisH

	headerY, chartsY := top, top+(headerH+bigGap)*fh
	if ((tick%2)+2)%2 == 1 {
		chartsY, headerY = top, top+(chartsH+bigGap)*fh
	}
	f.header = box{left, headerY, w, headerH * fh}
	f.temp = box{left, chartsY, w, tempH * fh}
	f.wind = box{left, f.temp.y + f.temp.h + gap*fh, w, windH * fh}
	f.rain = box{left, f.wind.y + f.wind.h + gap*fh, w, rainH * fh}
	f.axis = box{left, f.rain.y + f.rain.h, w, axisH * fh}

	gutter := fw * 0.075
	f.plotX = left + gutter
	f.colW = (w - gutter) / weatherHours
	return fw, fh, f
}

// Weather renders the forecast screen to a grayscale PNG.
func Weather(in WeatherIn) ([]byte, error) {
	if in.Forecast == nil {
		return nil, fmt.Errorf("weather: no forecast")
	}
	day, tomorrow := weatherDay(in.Now)
	from := day.Add(weatherFirstHour * time.Hour)
	hours := in.Forecast.HoursBetween(from, day.AddDate(0, 0, 1))
	if len(hours) < 2 {
		return nil, fmt.Errorf("weather: forecast does not cover %s", day.Format("Jan 2"))
	}
	if len(hours) > weatherHours {
		hours = hours[:weatherHours]
	}

	dc, _, _ := designCanvas(in.Width, in.Height)
	fw, fh, f := computeWeatherFrame(in.Width, in.Height, in.Tick)
	metric := in.Forecast.Units == weather.Metric

	// Hours already behind us are drawn faint. None are when showing tomorrow.
	past := func(h weather.Hour) bool { return !tomorrow && h.Time.Add(time.Hour).Before(in.Now) }

	// Faint hour grid behind all three strips, on the labelled hours.
	dc.SetColor(inkD3)
	dc.SetLineWidth(2)
	for i := 0; i < weatherHours; i += 3 {
		x := f.colX(float64(i))
		dc.DrawLine(x, f.temp.y, x, f.rain.y+f.rain.h)
		dc.Stroke()
	}

	drawTempStrip(dc, f, fh, hours, metric)
	drawWindStrip(dc, f, fh, hours, metric, past)
	drawRainStrip(dc, f, fh, hours, metric, past)

	drawHourTicks(dc, f, fh)
	dc.SetFontFace(newFace(ibmPlexMonoReg, fh*0.028))
	dc.SetColor(inkD1)
	for i := 0; i < weatherHours; i += 3 {
		dc.DrawStringAnchored(hourLabel(weatherFirstHour+i), f.colX(float64(i)), f.axis.y+f.axis.h*0.70, 0.5, 0.5)
	}
	if sum, ok := in.Forecast.Day(day); ok {
		drawSunEvents(dc, f, fh, sum)
	}

	if !tomorrow {
		drawNowMarker(dc, f, fh, in.Now)
	}
	drawWeatherHeader(dc, f, fw, fh, in, day, tomorrow, hours)
	return encodeGrayPNG(dc.Image())
}

// drawStripLabel prints a strip's name and unit in the gutter left of the plot.
func drawStripLabel(dc *gg.Context, b box, fh float64, title string, sub ...string) {
	size := fh * 0.024
	dc.SetFontFace(newFace(instrumentSansBold, size))
	dc.SetColor(inkD1)
	dc.DrawString(title, b.x, b.y+size*1.1)
	dc.SetFontFace(newFace(instrumentSansReg, fh*0.021))
	dc.SetColor(inkD2)
	for i, s := range sub {
		dc.DrawString(s, b.x, b.y+size*1.1+fh*0.028*float64(i+1))
	}
}

func drawBaseline(dc *gg.Context, f weatherFrame, y float64) {
	dc.SetColor(inkD2)
	dc.SetLineWidth(2)
	dc.DrawLine(f.plotX, y, f.plotX+f.colW*weatherHours, y)
	dc.Stroke()
}

// column returns the hour column an hourly step belongs in.
func column(h weather.Hour) float64 { return float64(h.Time.Hour() - weatherFirstHour) }

// drawTempStrip draws the temperature curve over a filled area, labelling the
// high, the low and every third hour.
func drawTempStrip(dc *gg.Context, f weatherFrame, fh float64, hours []weather.Hour, metric bool) {
	b := f.temp
	unit := "°F"
	if metric {
		unit = "°C"
	}
	drawStripLabel(dc, b, fh, "TEMP", unit)

	hi, lo := 0, 0
	for i, h := range hours {
		if h.Temp > hours[hi].Temp {
			hi = i
		}
		if h.Temp < hours[lo].Temp {
			lo = i
		}
	}
	minT, span := hours[lo].Temp, hours[hi].Temp-hours[lo].Temp
	if span < 1 {
		// A flat day: centre the line rather than dividing by ~zero.
		minT, span = minT-0.5, 1
	}

	labelSize := fh * 0.036
	top := b.y + labelSize*1.8 // headroom for the labels above the curve
	floor := b.y + b.h
	low := b.y + b.h*0.80 // the day's low sits here, leaving area beneath it
	py := func(t float64) float64 { return low - (t-minT)/span*(low-top) }

	first, last := f.colX(column(hours[0])), f.colX(column(hours[len(hours)-1]))
	dc.MoveTo(first, floor)
	for _, h := range hours {
		dc.LineTo(f.colX(column(h)), py(h.Temp))
	}
	dc.LineTo(last, floor)
	dc.ClosePath()
	dc.SetColor(inkD3)
	dc.Fill()

	for i, h := range hours {
		x, y := f.colX(column(h)), py(h.Temp)
		if i == 0 {
			dc.MoveTo(x, y)
		} else {
			dc.LineTo(x, y)
		}
	}
	dc.SetColor(inkD0)
	dc.SetLineWidth(math.Max(4, fh*0.0045))
	dc.Stroke()
	drawBaseline(dc, f, floor)

	// Label the extremes, plus every third hour that isn't crowding one. When
	// the high and low are neighbours only the high is labelled.
	near := func(i, j int) bool { return i-j <= 1 && j-i <= 1 }
	for i, h := range hours {
		switch {
		case i == hi:
		case i == lo && !near(lo, hi):
		case int(column(h))%3 == 0 && !near(i, hi) && !near(i, lo):
		default:
			continue
		}
		x, y := f.colX(column(h)), py(h.Temp)
		dc.SetColor(inkD0)
		dc.DrawCircle(x, y, math.Max(6, fh*0.007))
		dc.Fill()
		drawBigNum(dc, x, y-labelSize*1.0, degrees(h.Temp), labelSize, inkD0, 0.5, 0.5)
	}
}

// drawWindStrip draws a bar per hour for sustained wind with a whisker up to
// the gust speed, and an arrow under each showing where the wind is blowing to.
func drawWindStrip(dc *gg.Context, f weatherFrame, fh float64, hours []weather.Hour, metric bool, past func(weather.Hour) bool) {
	b := f.wind
	unit, scale := "mph", 15.0
	if metric {
		unit, scale = "km/h", 25.0
	}
	drawStripLabel(dc, b, fh, "WIND", unit, "+ gusts")

	// The scale never drops below a light breeze, so a calm day reads as calm
	// instead of stretching 3 mph to full height.
	for _, h := range hours {
		scale = math.Max(scale, math.Max(h.Wind, h.Gust))
	}

	labelSize := fh * 0.030
	top := b.y + labelSize*1.6
	floor := b.y + b.h - fh*0.042 // the arrows sit below the bars
	arrowY := b.y + b.h - fh*0.017
	py := func(v float64) float64 { return floor - v/scale*(floor-top) }
	half := f.colW * 0.28

	for _, h := range hours {
		x := f.colX(column(h))
		bar, whisker := inkD1, inkD0
		if past(h) {
			bar, whisker = inkD3, inkD2
		}
		barY := py(h.Wind)
		dc.SetColor(bar)
		dc.DrawRectangle(x-half, barY, half*2, floor-barY)
		dc.Fill()

		peak := barY
		if h.Gust > h.Wind {
			peak = py(h.Gust)
			dc.SetColor(whisker)
			dc.SetLineWidth(2)
			dc.DrawLine(x, barY, x, peak)
			dc.Stroke()
			dc.SetLineWidth(4)
			dc.DrawLine(x-half*0.7, peak, x+half*0.7, peak)
			dc.Stroke()
		}
		if int(column(h))%3 == 0 {
			drawBigNum(dc, x, peak-labelSize*0.85, strconv.Itoa(round(h.Wind)), labelSize, whisker, 0.5, 0.5)
		}
		drawWindArrow(dc, x, arrowY, math.Min(f.colW*0.2, fh*0.014), h, whisker)
	}
	drawBaseline(dc, f, floor)
}

// drawWindArrow draws an arrow pointing the way the wind is blowing — the
// opposite of the meteorological "from" bearing — or a dot when it is calm.
func drawWindArrow(dc *gg.Context, cx, cy, s float64, h weather.Hour, col color.Color) {
	dc.SetColor(col)
	if h.Wind < 1 {
		dc.DrawCircle(cx, cy, s*0.25)
		dc.Fill()
		return
	}
	to := float64(h.WindDir+180) * math.Pi / 180
	ux, uy := math.Sin(to), -math.Cos(to)
	hx, hy := cx+ux*s, cy+uy*s
	dc.SetLineWidth(3)
	dc.DrawLine(cx-ux*s, cy-uy*s, cx+ux*s*0.3, cy+uy*s*0.3)
	dc.Stroke()
	// Arrowhead: a triangle with its base back along the shaft.
	bx, by := hx-ux*s*0.95, hy-uy*s*0.95
	dc.MoveTo(hx, hy)
	dc.LineTo(bx-uy*s*0.55, by+ux*s*0.55)
	dc.LineTo(bx+uy*s*0.55, by-ux*s*0.55)
	dc.ClosePath()
	dc.Fill()
}

// drawRainStrip draws the chance of precipitation per hour on a fixed 0–100%
// scale, with the day's expected total in the gutter.
func drawRainStrip(dc *gg.Context, f weatherFrame, fh float64, hours []weather.Hour, metric bool, past func(weather.Hour) bool) {
	b := f.rain
	var total float64
	maxProb := 0
	for _, h := range hours {
		total += h.Precip
		if h.PrecipProb > maxProb {
			maxProb = h.PrecipProb
		}
	}
	sub := []string{"% chance"}
	if total > 0 {
		sub = append(sub, precipLabel(total, metric))
	}
	drawStripLabel(dc, b, fh, "RAIN", sub...)

	labelSize := fh * 0.030
	top := b.y + labelSize*1.5
	floor := b.y + b.h
	half := f.colW * 0.28
	drawBaseline(dc, f, floor)

	if maxProb < 10 && total == 0 {
		dc.SetFontFace(newFace(instrumentSansReg, fh*0.030))
		dc.SetColor(inkD2)
		dc.DrawStringAnchored("no rain expected", f.plotX+f.colW*weatherHours/2, b.y+b.h*0.55, 0.5, 0.5)
		return
	}
	for _, h := range hours {
		if h.PrecipProb <= 0 {
			continue
		}
		x := f.colX(column(h))
		col := inkD0
		if past(h) {
			col = inkD3
		}
		y := floor - float64(h.PrecipProb)/100*(floor-top)
		dc.SetColor(col)
		dc.DrawRectangle(x-half, y, half*2, floor-y)
		dc.Fill()
		if h.PrecipProb >= 10 {
			if past(h) {
				col = inkD2
			}
			drawBigNum(dc, x, y-labelSize*0.8, strconv.Itoa(h.PrecipProb), labelSize, col, 0.5, 0.5)
		}
	}
}

// drawHourTicks marks every hour on the axis line: a long tick on the labelled
// hours, a short one on the hours between them.
func drawHourTicks(dc *gg.Context, f weatherFrame, fh float64) {
	dc.SetColor(inkD2)
	dc.SetLineWidth(2)
	for i := 0; i < weatherHours; i++ {
		l := fh * 0.006
		if i%3 == 0 {
			l = fh * 0.011
		}
		x := f.colX(float64(i))
		dc.DrawLine(x, f.axis.y, x, f.axis.y+l)
		dc.Stroke()
	}
}

// hourPos maps a clock time onto the hour columns. Each column is centred on
// its hour, so the fractional hour maps straight onto column positions. ok is
// false when t falls outside the chart.
func hourPos(t time.Time) (pos float64, ok bool) {
	pos = float64(t.Hour()-weatherFirstHour) + float64(t.Minute())/60
	return pos, pos >= -0.5 && pos <= weatherHours-0.5
}

// drawSunEvents hangs a sunrise and a sunset glyph from the axis line at the
// times the sun rises and sets, in the band between the ticks and the hour
// labels.
func drawSunEvents(dc *gg.Context, f weatherFrame, fh float64, d weather.Day) {
	r := fh * 0.012
	y := f.axis.y + f.axis.h*0.40 // the glyph's horizon
	for _, ev := range []struct {
		t    time.Time
		rise bool
	}{{d.Sunrise, true}, {d.Sunset, false}} {
		if ev.t.IsZero() {
			continue
		}
		if pos, ok := hourPos(ev.t); ok {
			drawSunEvent(dc, f.colX(pos), y, r, ev.rise, inkD0)
		}
	}
}

// drawSunEvent draws a half sun on a horizon line centred on (cx, y), with an
// arrow beside it pointing up for a sunrise and down for a sunset. It spans
// about 2.6r horizontally either side of cx and 1.7r above and 0.9r below y.
func drawSunEvent(dc *gg.Context, cx, y, r float64, rise bool, col color.Color) {
	lw := math.Max(2, r*0.22)
	dc.SetColor(col)
	dc.SetLineWidth(lw)
	sx := cx - r*0.6 // the sun sits left of centre, the arrow right
	dc.DrawArc(sx, y, r, math.Pi, 2*math.Pi)
	dc.ClosePath()
	dc.Fill()
	for _, a := range []float64{-150, -115, -65, -30} {
		rad := a * math.Pi / 180
		dc.DrawLine(sx+math.Cos(rad)*r*1.3, y+math.Sin(rad)*r*1.3, sx+math.Cos(rad)*r*1.65, y+math.Sin(rad)*r*1.65)
		dc.Stroke()
	}
	dc.DrawLine(cx-r*2.6, y, cx+r*2.6, y)
	dc.Stroke()

	// Arrow right of the sun: shaft plus a filled head at the leading end.
	ax, top, bottom := cx+r*1.75, y-r*1.7, y-r*0.15
	head := r * 0.55
	if rise {
		dc.DrawLine(ax, bottom, ax, top+head)
		dc.Stroke()
		dc.MoveTo(ax-head, top+head)
		dc.LineTo(ax+head, top+head)
		dc.LineTo(ax, top)
	} else {
		dc.DrawLine(ax, top, ax, bottom-head)
		dc.Stroke()
		dc.MoveTo(ax-head, bottom-head)
		dc.LineTo(ax+head, bottom-head)
		dc.LineTo(ax, bottom)
	}
	dc.ClosePath()
	dc.Fill()
}

// drawNowMarker draws a dashed line down all three strips at the current time,
// if it falls inside the charted hours.
func drawNowMarker(dc *gg.Context, f weatherFrame, fh float64, now time.Time) {
	pos, ok := hourPos(now)
	if !ok {
		return
	}
	x := f.colX(pos)
	top, bottom := f.temp.y, f.rain.y+f.rain.h
	dc.SetColor(inkD0)
	dc.SetLineWidth(3)
	dc.SetDash(fh*0.008, fh*0.008)
	dc.DrawLine(x, top, x, bottom)
	dc.Stroke()
	dc.SetDash()

	s := fh * 0.011
	dc.MoveTo(x-s, top)
	dc.LineTo(x+s, top)
	dc.LineTo(x, top+s*1.3)
	dc.ClosePath()
	dc.Fill()
}

// drawWeatherHeader draws the current conditions on the left and, on the
// right, the charted day's summary plus the clock.
func drawWeatherHeader(dc *gg.Context, f weatherFrame, fw, fh float64, in WeatherIn, day time.Time, tomorrow bool, hours []weather.Hour) {
	b := f.header
	cur := in.Forecast.Current
	unit := "mph"
	if in.Forecast.Units == weather.Metric {
		unit = "km/h"
	}

	// Left: the big current temperature, then a glyph and two lines of detail.
	numSize := b.h * 0.86
	temp := degrees(cur.Temp)
	dc.SetFontFace(newFace(bigShouldersBold, numSize))
	tw, _ := dc.MeasureString(temp)
	drawBigNum(dc, b.x, b.y+b.h*0.47, temp, numSize, inkD0, 0, 0.5)

	colX := b.x + tw + fw*0.03
	gr := b.h * 0.17
	drawWeatherGlyph(dc, glyphFor(cur.Code, cur.IsDay), colX+gr, b.y+b.h*0.27, gr)
	dc.SetFontFace(newFace(instrumentSansBold, fh*0.042))
	dc.SetColor(inkD0)
	dc.DrawStringAnchored(conditionText(cur.Code, cur.IsDay), colX+gr*2+fw*0.015, b.y+b.h*0.27, 0, 0.35)

	wind := "calm"
	if cur.Wind >= 1 {
		wind = fmt.Sprintf("%s %d %s", compass(cur.WindDir), round(cur.Wind), unit)
	}
	dc.SetFontFace(newFace(instrumentSansReg, fh*0.032))
	dc.SetColor(inkD1)
	dc.DrawStringAnchored(fmt.Sprintf("feels %s · wind %s", degrees(cur.FeelsLike), wind), colX, b.y+b.h*0.60, 0, 0.35)

	note := in.Label
	if in.Now.Sub(in.Forecast.Fetched) > weatherStaleAfter {
		note = joinMeta(note, "as of "+shortTime(in.Forecast.Fetched.In(in.Now.Location())))
	}
	if note != "" {
		dc.SetFontFace(newFace(instrumentSansReg, fh*0.027))
		dc.SetColor(inkD2)
		dc.DrawStringAnchored(note, colX, b.y+b.h*0.83, 0, 0.35)
	}

	// Right: which day the charts cover, its high/low, and the clock.
	right := b.x + b.w
	title := "TODAY"
	if tomorrow {
		title = "TOMORROW"
	}
	dc.SetFontFace(newFace(instrumentSansBold, fh*0.034))
	dc.SetColor(inkD0)
	dc.DrawStringAnchored(title+" · "+strings.ToUpper(day.Format("Mon Jan 2")), right, b.y+b.h*0.11, 1, 0.35)

	summary, ok := in.Forecast.Day(day)
	if !ok {
		// No daily row for this day: fall back to the charted hours.
		summary.High, summary.Low, summary.Code = hours[0].Temp, hours[0].Temp, hours[0].Code
		for _, h := range hours {
			summary.High = math.Max(summary.High, h.Temp)
			summary.Low = math.Min(summary.Low, h.Temp)
		}
	}
	drawBigNum(dc, right, b.y+b.h*0.37, "H "+degrees(summary.High)+"   L "+degrees(summary.Low), fh*0.072, inkD0, 1, 0.5)

	// The day's condition, then its sunrise and sunset behind the same glyphs
	// the chart uses, laid out right to left.
	detailY := b.y + b.h*0.70
	dc.SetFontFace(newFace(instrumentSansReg, fh*0.028))
	x := right
	if !summary.Sunrise.IsZero() && !summary.Sunset.IsZero() {
		r := fh * 0.0085
		baseline := detailY + fh*0.028*0.35 // where the text sits, for the glyph's horizon
		for _, ev := range []struct {
			t    time.Time
			rise bool
		}{{summary.Sunset, false}, {summary.Sunrise, true}} {
			s := shortTime(ev.t)
			w, _ := dc.MeasureString(s)
			dc.SetColor(inkD1)
			dc.DrawStringAnchored(s, x, detailY, 1, 0.35)
			x -= w + r*0.8 + r*2.6
			drawSunEvent(dc, x, baseline-r*0.1, r, ev.rise, inkD1)
			x -= r*2.6 + fw*0.012
		}
		dc.SetColor(inkD1)
		dc.DrawStringAnchored("·", x, detailY, 1, 0.35)
		w, _ := dc.MeasureString("·")
		x -= w + fw*0.008
	}
	dc.SetColor(inkD1)
	dc.DrawStringAnchored(conditionText(summary.Code, true), x, detailY, 1, 0.35)

	clock := in.Now.Format("3:04 PM")
	if lbl := batteryLabel(in.Battery); lbl != "" {
		clock += "   " + lbl
	}
	dc.SetFontFace(newFace(ibmPlexMonoReg, fh*0.026))
	dc.SetColor(inkD2)
	dc.DrawStringAnchored(clock, right, b.y+b.h*0.89, 1, 0.35)
}

// ── formatting ───────────────────────────────────────────────────────────

func round(v float64) int { return int(math.Round(v)) }

// degrees formats a temperature for display, e.g. "62°".
func degrees(t float64) string { return strconv.Itoa(round(t)) + "°" }

// hourLabel formats an hour of the day for the axis: "6a", "12p", "9p".
func hourLabel(hour int) string {
	suffix := "a"
	if hour%24 >= 12 {
		suffix = "p"
	}
	h := hour % 12
	if h == 0 {
		h = 12
	}
	return strconv.Itoa(h) + suffix
}

// shortTime formats a clock time compactly: "7:07a", "6:49p".
func shortTime(t time.Time) string {
	suffix := "a"
	if t.Hour() >= 12 {
		suffix = "p"
	}
	return t.Format("3:04") + suffix
}

// precipLabel formats a precipitation total with its unit. Traces round up to
// the smallest printable amount rather than reading as zero.
func precipLabel(total float64, metric bool) string {
	if metric {
		return fmt.Sprintf("%.1f mm", math.Max(total, 0.1))
	}
	return fmt.Sprintf("%.2f in", math.Max(total, 0.01))
}

// compass turns a bearing in degrees into an eight-point compass name.
func compass(deg int) string {
	points := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	return points[(((deg%360)+360)%360*2+45)/90%8]
}

// ── conditions ───────────────────────────────────────────────────────────

// conditionText names a WMO weather code. day picks "sunny" over "clear".
func conditionText(code int, day bool) string {
	switch code {
	case 0:
		if day {
			return "Sunny"
		}
		return "Clear"
	case 1:
		if day {
			return "Mostly sunny"
		}
		return "Mostly clear"
	case 2:
		return "Partly cloudy"
	case 3:
		return "Overcast"
	case 45, 48:
		return "Fog"
	case 51, 53, 55:
		return "Drizzle"
	case 56, 57:
		return "Freezing drizzle"
	case 61:
		return "Light rain"
	case 63:
		return "Rain"
	case 65:
		return "Heavy rain"
	case 66, 67:
		return "Freezing rain"
	case 71, 73, 75, 77:
		return "Snow"
	case 80, 81:
		return "Showers"
	case 82:
		return "Heavy showers"
	case 85, 86:
		return "Snow showers"
	case 95, 96, 99:
		return "Thunderstorm"
	}
	return "Unknown"
}

// Glyph kinds drawn by drawWeatherGlyph.
const (
	glyphSun         = "sun"
	glyphMoon        = "moon"
	glyphPartly      = "partly"
	glyphPartlyNight = "partly-night"
	glyphCloud       = "cloud"
	glyphFog         = "fog"
	glyphDrizzle     = "drizzle"
	glyphRain        = "rain"
	glyphSnow        = "snow"
	glyphStorm       = "storm"
)

// glyphFor picks the condition glyph for a WMO weather code.
func glyphFor(code int, day bool) string {
	switch code {
	case 0, 1:
		if day {
			return glyphSun
		}
		return glyphMoon
	case 2:
		if day {
			return glyphPartly
		}
		return glyphPartlyNight
	case 45, 48:
		return glyphFog
	case 51, 53, 55, 56, 57:
		return glyphDrizzle
	case 61, 63, 65, 66, 67, 80, 81, 82:
		return glyphRain
	case 71, 73, 75, 77, 85, 86:
		return glyphSnow
	case 95, 96, 99:
		return glyphStorm
	}
	return glyphCloud
}

// drawWeatherGlyph draws a condition icon from primitives, centred at (cx,cy)
// and fitting a circle of radius r.
func drawWeatherGlyph(dc *gg.Context, kind string, cx, cy, r float64) {
	lw := math.Max(3, r*0.11)
	switch kind {
	case glyphSun:
		drawSun(dc, cx, cy, r, lw)
	case glyphMoon:
		drawMoon(dc, cx, cy, r*0.85)
	case glyphPartly:
		drawSun(dc, cx-r*0.3, cy-r*0.3, r*0.7, lw)
		drawCloud(dc, cx+r*0.15, cy+r*0.22, r*0.75, lw)
	case glyphPartlyNight:
		drawMoon(dc, cx-r*0.3, cy-r*0.3, r*0.6)
		drawCloud(dc, cx+r*0.15, cy+r*0.22, r*0.75, lw)
	case glyphCloud:
		drawCloud(dc, cx, cy, r*0.95, lw)
	default:
		// Everything else is a cloud with something falling out of it.
		drawCloud(dc, cx, cy-r*0.28, r*0.8, lw)
		dc.SetColor(inkD0)
		dc.SetLineWidth(lw)
		dc.SetLineCapRound()
		top, bottom := cy+r*0.36, cy+r*0.92
		switch kind {
		case glyphFog:
			for i, half := range []float64{0.75, 0.5} {
				y := top + r*0.1 + float64(i)*r*0.34
				dc.DrawLine(cx-r*half, y, cx+r*half, y)
				dc.Stroke()
			}
		case glyphDrizzle, glyphRain:
			length := bottom - top
			if kind == glyphDrizzle {
				length *= 0.45
			}
			for _, dx := range []float64{-0.42, 0, 0.42} {
				x := cx + r*dx
				dc.DrawLine(x+length*0.2, top, x-length*0.2, top+length)
				dc.Stroke()
			}
		case glyphSnow:
			for _, dx := range []float64{-0.42, 0, 0.42} {
				dc.DrawCircle(cx+r*dx, (top+bottom)/2, r*0.1)
				dc.Fill()
			}
		case glyphStorm:
			dc.MoveTo(cx+r*0.12, top-r*0.1)
			dc.LineTo(cx-r*0.28, top+r*0.3)
			dc.LineTo(cx, top+r*0.3)
			dc.LineTo(cx-r*0.14, bottom)
			dc.LineTo(cx+r*0.3, top+r*0.14)
			dc.LineTo(cx+r*0.02, top+r*0.14)
			dc.ClosePath()
			dc.Fill()
		}
		dc.SetLineCapButt()
	}
}

func drawSun(dc *gg.Context, cx, cy, r, lw float64) {
	dc.SetColor(inkD0)
	dc.SetLineWidth(lw)
	dc.DrawCircle(cx, cy, r*0.46)
	dc.Stroke()
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		c, s := math.Cos(a), math.Sin(a)
		dc.DrawLine(cx+c*r*0.70, cy+s*r*0.70, cx+c*r, cy+s*r)
		dc.Stroke()
	}
}

// drawMoon draws a crescent: a dark disc with a white one biting into it.
func drawMoon(dc *gg.Context, cx, cy, r float64) {
	dc.SetColor(inkD0)
	dc.DrawCircle(cx, cy, r*0.85)
	dc.Fill()
	dc.SetColor(color.White)
	dc.DrawCircle(cx+r*0.38, cy-r*0.26, r*0.72)
	dc.Fill()
}

// drawCloud draws an outlined, white-filled cloud about 2s wide centred at
// (cx,cy). The outline of a union of shapes can't be stroked directly, so it
// is painted twice: dark at full size, then white inset by the line width.
// The white fill also hides whatever the cloud overlaps (a sun or moon).
func drawCloud(dc *gg.Context, cx, cy, s, lw float64) {
	puffs := [][3]float64{{-0.50, 0.15, 0.35}, {-0.08, -0.14, 0.48}, {0.48, 0.10, 0.40}}
	paint := func(inset float64) {
		for _, p := range puffs {
			dc.DrawCircle(cx+p[0]*s, cy+p[1]*s, p[2]*s-inset)
			dc.Fill()
		}
		dc.DrawRectangle(cx-0.50*s, cy-0.05*s, 0.98*s, 0.55*s-inset)
		dc.Fill()
	}
	dc.SetColor(inkD0)
	paint(0)
	dc.SetColor(color.White)
	paint(lw)
}
