// Package config loads and validates the bus-trmnl server configuration.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the server.
type Config struct {
	Server  ServerConfig   `yaml:"server"`
	Five11  Five11Config   `yaml:"five11"`
	Device  DeviceConfig   `yaml:"device"`
	Refresh RefreshConfig  `yaml:"refresh"`
	Screens []ScreenConfig `yaml:"screens"`
	Boards  []BoardConfig  `yaml:"boards"`
}

// Screen types usable in ScreenConfig.Type.
const (
	ScreenMuni = "muni"
	ScreenCat  = "cat"
)

// ScreenConfig selects one screen in the device's display rotation. The
// device shows the configured screens in order, one per wake.
type ScreenConfig struct {
	// Type is the screen kind: "muni" or "cat".
	Type string `yaml:"type"`
	// URL overrides the image source for the "cat" screen. Defaults to
	// https://cataas.com/cat.
	URL string `yaml:"url"`
}

// ServerConfig controls the HTTP server and image generation.
type ServerConfig struct {
	// Listen is the address the HTTP server binds to, e.g. ":2300".
	Listen string `yaml:"listen"`
	// BaseURL is the externally reachable URL of this server. It is used to
	// build the absolute image_url the device downloads, so it must be
	// reachable from the device (e.g. "http://192.168.1.10:2300").
	BaseURL string `yaml:"base_url"`
	// Timezone is an IANA name used for the rush-hour windows and clock,
	// e.g. "America/Los_Angeles".
	Timezone string `yaml:"timezone"`
	// ImageDir is where rendered PNGs are written and served from.
	ImageDir string `yaml:"image_dir"`
}

// Five11Config controls access to the 511.org regional transit API.
type Five11Config struct {
	// APIKey is your 511.org token. Supports ${ENV_VAR} expansion.
	APIKey string `yaml:"api_key"`
	// Operator is the 511 agency code; "SF" is San Francisco Muni.
	Operator string `yaml:"operator"`
	// BaseURL is the 511 transit API root.
	BaseURL string `yaml:"base_url"`
	// PollInterval is the minimum spacing between 511 fetches. Stops are
	// fetched on demand when the MUNI screen renders and the cache is older
	// than this, never on a fixed schedule. 511 limits a token to 60
	// requests/hour, so keep (distinct stops) * (3600 / poll seconds) at or
	// below ~60.
	PollInterval Duration `yaml:"poll_interval"`
}

// DeviceConfig controls device authentication.
type DeviceConfig struct {
	// AccessToken, if set, is the API key the device must present in the
	// Access-Token header. Supports ${ENV_VAR} expansion. Leave empty to
	// disable auth (handy on a trusted LAN).
	AccessToken string `yaml:"access_token"`
	// FriendlyID is the human-readable id returned during setup.
	FriendlyID string `yaml:"friendly_id"`
}

// RefreshConfig describes how often the device should wake.
type RefreshConfig struct {
	// RushRate is returned to the device during a rush window.
	RushRate Duration `yaml:"rush_rate"`
	// DefaultRate is returned at all other times.
	DefaultRate Duration `yaml:"default_rate"`
	// RushWindows are the recurring windows during which RushRate applies.
	RushWindows []Window `yaml:"rush_windows"`
}

// Window is a recurring weekday time range, e.g. Mon-Fri 07:45-08:15.
type Window struct {
	// Days are three-letter weekday names: Mon Tue Wed Thu Fri Sat Sun.
	Days []string `yaml:"days"`
	// Start and End are "HH:MM" in the server timezone, inclusive of Start
	// and exclusive of End.
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

// BoardConfig is one stop/line grouping shown on the display.
type BoardConfig struct {
	// Title is the heading shown above this board's arrivals.
	Title string `yaml:"title"`
	// StopCode is the 511 stop code. Use the `discover` command to find it.
	StopCode string `yaml:"stop_code"`
	// Lines limits results to these route short names (e.g. "43","44","N").
	// Empty means no line filter.
	Lines []string `yaml:"lines"`
	// DestinationContains keeps only arrivals whose destination contains
	// this (case-insensitive) substring. Empty means no filter.
	DestinationContains string `yaml:"destination_contains"`
	// Direction keeps only arrivals matching this DirectionRef (e.g. "IB",
	// "OB"). Empty means no filter.
	Direction string `yaml:"direction"`
	// Max caps how many arrivals are shown. Zero means a sensible default.
	Max int `yaml:"max"`
}

// Duration is a time.Duration that unmarshals from strings like "30s".
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	p, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(p)
	return nil
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Load reads, expands ${ENV} references, parses, and validates the config.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := os.ExpandEnv(string(raw))

	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":2300"
	}
	if c.Server.Timezone == "" {
		c.Server.Timezone = "America/Los_Angeles"
	}
	if c.Server.ImageDir == "" {
		c.Server.ImageDir = "images"
	}
	if c.Five11.Operator == "" {
		c.Five11.Operator = "SF"
	}
	if c.Five11.BaseURL == "" {
		c.Five11.BaseURL = "https://api.511.org/transit"
	}
	if c.Five11.PollInterval.D() == 0 {
		c.Five11.PollInterval = Duration(2 * time.Minute)
	}
	if c.Refresh.RushRate.D() == 0 {
		c.Refresh.RushRate = Duration(30 * time.Second)
	}
	if c.Refresh.DefaultRate.D() == 0 {
		c.Refresh.DefaultRate = Duration(60 * time.Second)
	}
	if len(c.Refresh.RushWindows) == 0 {
		c.Refresh.RushWindows = []Window{{
			Days:  []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
			Start: "07:45",
			End:   "08:15",
		}}
	}
	if len(c.Screens) == 0 {
		c.Screens = []ScreenConfig{{Type: ScreenMuni}}
	}
	for i := range c.Boards {
		if c.Boards[i].Max == 0 {
			c.Boards[i].Max = 3
		}
	}
}

func (c *Config) validate() error {
	if c.Server.BaseURL == "" {
		return fmt.Errorf("server.base_url is required so the device can fetch images")
	}
	for i, s := range c.Screens {
		switch s.Type {
		case ScreenMuni, ScreenCat:
		default:
			return fmt.Errorf("screens[%d]: unknown type %q (want %q or %q)", i, s.Type, ScreenMuni, ScreenCat)
		}
	}
	// 511 settings only matter when a MUNI screen is in the rotation.
	if c.HasScreen(ScreenMuni) {
		if c.Five11.APIKey == "" {
			return fmt.Errorf("five11.api_key is required (set it or use ${FIVE11_API_KEY})")
		}
		if len(c.Boards) == 0 {
			return fmt.Errorf("at least one board is required")
		}
		for i, b := range c.Boards {
			if b.StopCode == "" {
				return fmt.Errorf("boards[%d] (%q) is missing stop_code; run the discover command to find it", i, b.Title)
			}
		}
	}
	for _, w := range c.Refresh.RushWindows {
		if _, err := parseHHMM(w.Start); err != nil {
			return fmt.Errorf("refresh window start %q: %w", w.Start, err)
		}
		if _, err := parseHHMM(w.End); err != nil {
			return fmt.Errorf("refresh window end %q: %w", w.End, err)
		}
	}
	return nil
}

// HasScreen reports whether a screen of the given type is configured.
func (c *Config) HasScreen(t string) bool {
	for _, s := range c.Screens {
		if s.Type == t {
			return true
		}
	}
	return false
}

// Location returns the configured timezone.
func (c *Config) Location() (*time.Location, error) {
	return time.LoadLocation(c.Server.Timezone)
}

// DistinctStops returns the unique stop codes across all boards.
func (c *Config) DistinctStops() []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range c.Boards {
		if !seen[b.StopCode] {
			seen[b.StopCode] = true
			out = append(out, b.StopCode)
		}
	}
	return out
}

// RateAt returns the refresh interval that applies at time t.
func (r RefreshConfig) RateAt(t time.Time) time.Duration {
	day := t.Weekday().String()[:3]
	minutes := t.Hour()*60 + t.Minute()
	for _, w := range r.RushWindows {
		if !containsDay(w.Days, day) {
			continue
		}
		start, err1 := parseHHMM(w.Start)
		end, err2 := parseHHMM(w.End)
		if err1 != nil || err2 != nil {
			continue
		}
		if minutes >= start && minutes < end {
			return r.RushRate.D()
		}
	}
	return r.DefaultRate.D()
}

func containsDay(days []string, day string) bool {
	for _, d := range days {
		if strings.EqualFold(d, day) {
			return true
		}
	}
	return false
}

// parseHHMM converts "HH:MM" to minutes since midnight.
func parseHHMM(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("out of range")
	}
	return h*60 + m, nil
}
