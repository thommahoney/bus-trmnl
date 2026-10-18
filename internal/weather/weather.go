// Package weather fetches and caches the hourly forecast from Open-Meteo.
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultBaseURL is the Open-Meteo forecast API root. It needs no API key.
const DefaultBaseURL = "https://api.open-meteo.com/v1"

// Unit systems accepted by Options.Units.
const (
	Imperial = "imperial" // °F, mph, inches
	Metric   = "metric"   // °C, km/h, millimetres
)

// Forecast models accepted by Options.Model. Any Open-Meteo model name works;
// these are the two worth naming.
const (
	// ModelNBM is NOAA's National Blend of Models: a bias-corrected blend on a
	// 2.5km grid, and what the National Weather Service builds its own
	// forecasts from. It covers the contiguous US only.
	ModelNBM = "ncep_nbm_conus"
	// ModelBestMatch lets Open-Meteo pick per location (HRRR/GFS in the US).
	ModelBestMatch = "best_match"
)

// forecastDays is how far ahead to ask for. The screen draws today or, in the
// evening, tomorrow; the third day is slack so a cache that has gone stale
// across midnight can still cover the day being drawn.
const forecastDays = 3

// Current is the observed-ish conditions at fetch time.
type Current struct {
	Time      time.Time
	Temp      float64
	FeelsLike float64
	Wind      float64
	WindDir   int // degrees the wind blows FROM, 0 = north
	Code      int // WMO weather code
	IsDay     bool
}

// Hour is one hourly forecast step.
type Hour struct {
	Time       time.Time
	Temp       float64
	Wind       float64
	Gust       float64
	WindDir    int     // degrees the wind blows FROM, 0 = north
	PrecipProb int     // percent
	Precip     float64 // amount over the preceding hour
	Code       int     // WMO weather code
}

// Day is one daily summary.
type Day struct {
	Date    time.Time // local midnight
	High    float64
	Low     float64
	Sunrise time.Time
	Sunset  time.Time
	Code    int // WMO weather code
}

// Forecast is one Open-Meteo response, with every time in the requested
// location and every value in the requested unit system.
type Forecast struct {
	Current Current
	Hours   []Hour
	Days    []Day
	Units   string    // Imperial or Metric
	Fetched time.Time // when this forecast was retrieved
}

// Day returns the daily summary for the calendar day containing t.
func (f *Forecast) Day(t time.Time) (Day, bool) {
	y, m, d := t.Date()
	for _, day := range f.Days {
		if dy, dm, dd := day.Date.Date(); dy == y && dm == m && dd == d {
			return day, true
		}
	}
	return Day{}, false
}

// HoursBetween returns the hourly steps with from <= Time < to.
func (f *Forecast) HoursBetween(from, to time.Time) []Hour {
	var out []Hour
	for _, h := range f.Hours {
		if !h.Time.Before(from) && h.Time.Before(to) {
			out = append(out, h)
		}
	}
	return out
}

// Options configures a Client.
type Options struct {
	Latitude  float64
	Longitude float64
	Units     string         // Imperial (default) or Metric
	Model     string         // Open-Meteo model name; empty means ModelBestMatch
	Location  *time.Location // forecast times are reported in this zone
	BaseURL   string         // defaults to DefaultBaseURL
}

// Client talks to the Open-Meteo forecast API.
type Client struct {
	opts Options
	http *http.Client
}

// New returns a Client.
func New(opts Options) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	if opts.Units == "" {
		opts.Units = Imperial
	}
	if opts.Location == nil {
		opts.Location = time.UTC
	}
	return &Client{opts: opts, http: &http.Client{Timeout: 20 * time.Second}}
}

// Forecast fetches the current conditions plus the hourly and daily forecast.
func (c *Client) Forecast(ctx context.Context) (*Forecast, error) {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(c.opts.Latitude, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(c.opts.Longitude, 'f', 4, 64))
	q.Set("current", "temperature_2m,apparent_temperature,weather_code,wind_speed_10m,wind_direction_10m,is_day")
	q.Set("hourly", "temperature_2m,precipitation_probability,precipitation,weather_code,wind_speed_10m,wind_gusts_10m,wind_direction_10m")
	q.Set("daily", "temperature_2m_max,temperature_2m_min,sunrise,sunset,weather_code")
	q.Set("timezone", c.opts.Location.String())
	q.Set("forecast_days", strconv.Itoa(forecastDays))
	if c.opts.Model != "" && c.opts.Model != ModelBestMatch {
		q.Set("models", c.opts.Model)
	}
	if c.opts.Units == Metric {
		q.Set("temperature_unit", "celsius")
		q.Set("wind_speed_unit", "kmh")
		q.Set("precipitation_unit", "mm")
	} else {
		q.Set("temperature_unit", "fahrenheit")
		q.Set("wind_speed_unit", "mph")
		q.Set("precipitation_unit", "inch")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.opts.BaseURL+"/forecast?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	f, err := parse(body, c.opts.Location)
	if err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("open-meteo: HTTP %d: %w", resp.StatusCode, err)
		}
		return nil, err
	}
	f.Units = c.opts.Units
	f.Fetched = time.Now()
	return f, nil
}

// response mirrors the Open-Meteo JSON. Hourly and daily series are parallel
// arrays indexed by Time. A null entry decodes to zero, which is the harmless
// reading for the only series that carries them (precipitation probability
// past the model's range).
type response struct {
	Error   bool   `json:"error"`
	Reason  string `json:"reason"`
	Current struct {
		Time      string  `json:"time"`
		Temp      float64 `json:"temperature_2m"`
		FeelsLike float64 `json:"apparent_temperature"`
		Code      int     `json:"weather_code"`
		Wind      float64 `json:"wind_speed_10m"`
		WindDir   float64 `json:"wind_direction_10m"`
		IsDay     int     `json:"is_day"`
	} `json:"current"`
	Hourly struct {
		Time       []string  `json:"time"`
		Temp       []float64 `json:"temperature_2m"`
		PrecipProb []float64 `json:"precipitation_probability"`
		Precip     []float64 `json:"precipitation"`
		Code       []int     `json:"weather_code"`
		Wind       []float64 `json:"wind_speed_10m"`
		Gust       []float64 `json:"wind_gusts_10m"`
		WindDir    []float64 `json:"wind_direction_10m"`
	} `json:"hourly"`
	Daily struct {
		Time    []string  `json:"time"`
		High    []float64 `json:"temperature_2m_max"`
		Low     []float64 `json:"temperature_2m_min"`
		Sunrise []string  `json:"sunrise"`
		Sunset  []string  `json:"sunset"`
		Code    []int     `json:"weather_code"`
	} `json:"daily"`
}

// Open-Meteo reports local wall-clock times with no zone suffix.
const (
	timeLayout = "2006-01-02T15:04"
	dateLayout = "2006-01-02"
)

// parse decodes an Open-Meteo forecast body, interpreting its zone-less times
// in loc (which must match the timezone the request asked for).
func parse(body []byte, loc *time.Location) (*Forecast, error) {
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode open-meteo forecast: %w", err)
	}
	if r.Error {
		return nil, fmt.Errorf("open-meteo: %s", r.Reason)
	}

	h := r.Hourly
	n := len(h.Time)
	if n == 0 {
		return nil, fmt.Errorf("open-meteo: forecast has no hourly data")
	}
	if len(h.Temp) != n || len(h.PrecipProb) != n || len(h.Precip) != n || len(h.Code) != n ||
		len(h.Wind) != n || len(h.Gust) != n || len(h.WindDir) != n {
		return nil, fmt.Errorf("open-meteo: hourly series have mismatched lengths")
	}
	d := r.Daily
	nd := len(d.Time)
	if len(d.High) != nd || len(d.Low) != nd || len(d.Sunrise) != nd || len(d.Sunset) != nd || len(d.Code) != nd {
		return nil, fmt.Errorf("open-meteo: daily series have mismatched lengths")
	}

	f := &Forecast{Hours: make([]Hour, n), Days: make([]Day, nd)}
	for i := range h.Time {
		t, err := time.ParseInLocation(timeLayout, h.Time[i], loc)
		if err != nil {
			return nil, fmt.Errorf("open-meteo: hourly time %q: %w", h.Time[i], err)
		}
		f.Hours[i] = Hour{
			Time:       t,
			Temp:       h.Temp[i],
			Wind:       h.Wind[i],
			Gust:       h.Gust[i],
			WindDir:    int(h.WindDir[i]),
			PrecipProb: int(h.PrecipProb[i]),
			Precip:     h.Precip[i],
			Code:       h.Code[i],
		}
	}
	for i := range d.Time {
		date, err := time.ParseInLocation(dateLayout, d.Time[i], loc)
		if err != nil {
			return nil, fmt.Errorf("open-meteo: daily time %q: %w", d.Time[i], err)
		}
		// A missing sunrise/sunset (polar day or night) is left zero rather
		// than failing the whole forecast.
		rise, _ := time.ParseInLocation(timeLayout, d.Sunrise[i], loc)
		set, _ := time.ParseInLocation(timeLayout, d.Sunset[i], loc)
		f.Days[i] = Day{Date: date, High: d.High[i], Low: d.Low[i], Sunrise: rise, Sunset: set, Code: d.Code[i]}
	}

	cur, err := time.ParseInLocation(timeLayout, r.Current.Time, loc)
	if err != nil {
		return nil, fmt.Errorf("open-meteo: current time %q: %w", r.Current.Time, err)
	}
	f.Current = Current{
		Time:      cur,
		Temp:      r.Current.Temp,
		FeelsLike: r.Current.FeelsLike,
		Wind:      r.Current.Wind,
		WindDir:   int(r.Current.WindDir),
		Code:      r.Current.Code,
		IsDay:     r.Current.IsDay != 0,
	}
	return f, nil
}
