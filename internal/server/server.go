// Package server implements the TRMNL BYOS device-facing HTTP API.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/config"
	"github.com/thommahoney/bus-trmnl/internal/paprika"
	"github.com/thommahoney/bus-trmnl/internal/pin"
	"github.com/thommahoney/bus-trmnl/internal/recipe"
	"github.com/thommahoney/bus-trmnl/internal/render"
	"github.com/thommahoney/bus-trmnl/internal/screen"
)

// maxUpload caps a recipe upload body.
const maxUpload = 16 << 20

// Server serves the BYOS endpoints the TRMNL device polls.
type Server struct {
	cfg    *config.Config
	loc    *time.Location
	rot    *screen.Rotation
	pins   *pin.Store
	recipe screen.Screen

	mu       sync.Mutex
	lastFile string
	lastGen  time.Time
}

// New creates a Server. pins and recipeScreen back the recipe focus-mode
// feature: when a recipe is pinned, /api/display shows recipeScreen instead of
// the rotation.
func New(cfg *config.Config, loc *time.Location, rot *screen.Rotation, pins *pin.Store, recipeScreen screen.Screen) *Server {
	return &Server{cfg: cfg, loc: loc, rot: rot, pins: pins, recipe: recipeScreen}
}

// Handler returns the HTTP mux for the server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/setup", s.handleSetup)
	mux.HandleFunc("/api/setup/", s.handleSetup)
	mux.HandleFunc("/api/display", s.handleDisplay)
	mux.HandleFunc("/api/display/", s.handleDisplay)
	mux.HandleFunc("/api/log", s.handleLog)
	mux.HandleFunc("/api/log/", s.handleLog)
	mux.HandleFunc("/api/recipe", s.handleRecipeUpload)
	mux.HandleFunc("/api/recipe/unpin", s.handleRecipeUnpin)
	mux.HandleFunc("/latest", s.handleLatest)
	mux.HandleFunc("/health", s.handleHealth)
	mux.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir(s.cfg.Server.ImageDir))))
	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s from=%s headers=%v", r.Method, r.URL.Path, r.RemoteAddr, r.Header)
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// header reads a device header, tolerating the two casings firmware uses.
func header(r *http.Request, names ...string) string {
	for _, n := range names {
		if v := r.Header.Get(n); v != "" {
			return v
		}
	}
	return ""
}

func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.Device.AccessToken == "" {
		return true
	}
	return header(r, "Access-Token", "ACCESS_TOKEN") == s.cfg.Device.AccessToken
}

// handleSetup pairs a freshly flashed device with this server.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	mac := header(r, "ID")
	log.Printf("setup request from device %s", mac)

	friendlyID := friendlyFromMAC(mac)

	apiKey := s.cfg.Device.AccessToken
	if apiKey == "" {
		apiKey = generateToken()
		log.Printf("generated api_key for device %s: %s", mac, apiKey)
	}

	width := atoiDefault(header(r, "WIDTH"), render.DefaultWidth)
	height := atoiDefault(header(r, "HEIGHT"), render.DefaultHeight)
	now := time.Now().In(s.loc)

	imageURL := ""
	filename, err := s.renderWithFallback(r.Context(), s.rot.Peek(), now, width, height)
	if err != nil {
		log.Printf("setup: render failed: %v", err)
	} else {
		imageURL = s.cfg.Server.BaseURL + "/images/" + filename
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      200,
		"api_key":     apiKey,
		"friendly_id": friendlyID,
		"image_url":   imageURL,
		"message":     "Welcome to bus-trmnl",
	})
}

func friendlyFromMAC(mac string) string {
	clean := strings.ReplaceAll(mac, ":", "")
	if len(clean) >= 6 {
		return strings.ToUpper(clean[len(clean)-6:])
	}
	if clean != "" {
		return strings.ToUpper(clean)
	}
	return "TRMNL"
}

func generateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// handleDisplay renders the next screen in the rotation and tells the device
// when to wake.
func (s *Server) handleDisplay(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"status": 401, "error": "invalid access token"})
		return
	}

	width := atoiDefault(header(r, "WIDTH"), render.DefaultWidth)
	height := atoiDefault(header(r, "HEIGHT"), render.DefaultHeight)

	now := time.Now().In(s.loc)
	refresh := int(s.cfg.Refresh.RateAt(now).Seconds())

	// Recipe focus mode takes over the rotation: while a recipe is pinned and
	// unexpired, show only it and leave the rotation where it is. Active()
	// clears an expired pin, so the rotation resumes on its own once the 3h
	// hold lapses.
	var next screen.Screen
	if s.recipe != nil && s.pins != nil {
		if _, ok := s.pins.Active(now); ok {
			next = s.recipe
		}
	}
	if next == nil {
		next = s.rot.Next()
	}

	ctx := render.ContextWithBattery(r.Context(), parseBattery(r))
	filename, err := s.renderWithFallback(ctx, next, now, width, height)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"status": 500, "error": "render failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          0,
		"filename":        filename,
		"image_url":       s.cfg.Server.BaseURL + "/images/" + filename,
		"refresh_rate":    refresh,
		"reset_firmware":  false,
		"update_firmware": false,
		// Force a full panel refresh on every wake so a screen transition never
		// leaves ghosting from the previous image.
		"maximum_compatibility": true,
	})
}

// renderWithFallback renders scr, falling back to the other configured
// screens in rotation order so a flaky source doesn't blank the device.
func (s *Server) renderWithFallback(ctx context.Context, scr screen.Screen, now time.Time, width, height int) (string, error) {
	filename, err := s.renderToFile(ctx, scr, now, width, height)
	if err == nil {
		return filename, nil
	}
	log.Printf("render %s failed: %v", scr.Name(), err)
	for _, alt := range s.rot.All() {
		if alt == scr {
			continue
		}
		f, altErr := s.renderToFile(ctx, alt, now, width, height)
		if altErr == nil {
			log.Printf("fell back to %s screen", alt.Name())
			return f, nil
		}
		log.Printf("render %s failed: %v", alt.Name(), altErr)
	}
	return "", err
}

// renderToFile renders one screen to a content-addressed PNG and prunes stale
// files. The filename is the screen name plus a hash of the image bytes, so an
// unchanged frame keeps the same filename and the device's filename cache skips
// the re-download and panel refresh (firmware checkCurrentFileName); any change
// to the pixels yields a new name and a redraw. Dynamic screens (the moving
// MUNI designs, cats) change every render, so they still redraw every wake.
func (s *Server) renderToFile(ctx context.Context, scr screen.Screen, now time.Time, width, height int) (string, error) {
	png, err := scr.Render(ctx, now, width, height)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.cfg.Server.ImageDir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(png)
	filename := scr.Name() + "-" + hex.EncodeToString(sum[:8]) + ".png"
	path := filepath.Join(s.cfg.Server.ImageDir, filename)
	if err := os.WriteFile(path, png, 0o644); err != nil {
		return "", err
	}

	s.mu.Lock()
	s.lastFile = filename
	s.lastGen = now
	s.mu.Unlock()

	s.pruneOld(now)
	return filename, nil
}

// pruneOld removes rendered images older than ten minutes.
func (s *Server) pruneOld(now time.Time) {
	entries, err := os.ReadDir(s.cfg.Server.ImageDir)
	if err != nil {
		return
	}
	cutoff := now.Add(-10 * time.Minute)
	for _, e := range entries {
		// Only reap rendered frames — never sidecar state like pin.json that
		// also lives in image_dir.
		if !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(s.cfg.Server.ImageDir, e.Name()))
		}
	}
}

// handleLog accepts device telemetry and returns 204.
func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if len(body) > 0 {
		log.Printf("device log from %s: %s", header(r, "ID"), body)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleLatest renders a screen directly into the response without writing to
// disk or advancing the rotation. ?screen=<name> previews a specific screen;
// the default is whichever screen is next in the rotation.
func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	width := atoiDefault(r.URL.Query().Get("width"), render.DefaultWidth)
	height := atoiDefault(r.URL.Query().Get("height"), render.DefaultHeight)
	now := time.Now().In(s.loc)

	scr := s.rot.Peek()
	if name := r.URL.Query().Get("screen"); name != "" {
		var ok bool
		if s.recipe != nil && name == s.recipe.Name() {
			scr, ok = s.recipe, true
		} else {
			scr, ok = s.rot.ByName(name)
		}
		if !ok {
			http.Error(w, "unknown screen "+strconv.Quote(name), http.StatusNotFound)
			return
		}
	}

	// ?battery=<percent> previews the on-clock battery readout without a device.
	ctx := r.Context()
	if bs := r.URL.Query().Get("battery"); bs != "" {
		if n, err := strconv.Atoi(bs); err == nil {
			ctx = render.ContextWithBattery(ctx, render.Battery{Percent: n, Present: true})
		}
	}

	png, err := scr.Render(ctx, now, width, height)
	if err != nil {
		log.Printf("latest render failed: %v", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.Write(png)
}

// handleRecipeUpload accepts a Paprika export, parses it, and pins the first
// recipe to the display for the configured hold (default 3h), replacing any
// current pin. It is intentionally open (no token) so anyone with the link can
// upload — see the recipe feature notes in CLAUDE.md. The body may be a
// multipart form file or the raw file bytes (e.g. an iOS Shortcut).
func (s *Server) handleRecipeUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST a Paprika file", http.StatusMethodNotAllowed)
		return
	}
	data, err := readUpload(r)
	if err != nil {
		http.Error(w, "could not read upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	rec, err := s.ingest(data, time.Now().In(s.loc))
	if err != nil {
		log.Printf("recipe upload rejected: %v", err)
		http.Error(w, "could not parse Paprika recipe: "+err.Error(), http.StatusBadRequest)
		return
	}
	_, until, _ := s.pins.Current()
	log.Printf("pinned recipe %q until %s", rec.Title, until.Format(time.Kitchen))
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       200,
		"title":        rec.Title,
		"pinned_until": until.Format(time.RFC3339),
	})
}

// handleRecipeUnpin clears the pinned recipe, returning the device to its
// normal rotation immediately.
func (s *Server) handleRecipeUnpin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST to unpin", http.StatusMethodNotAllowed)
		return
	}
	s.pins.Clear()
	log.Printf("recipe unpinned")
	writeJSON(w, http.StatusOK, map[string]any{"status": 200, "pinned": false})
}

// ingest parses a recipe file and pins the first recipe found. It is the single
// seam every ingestion channel funnels through (web/Shortcut today; Telegram,
// email, etc. later).
func (s *Server) ingest(data []byte, now time.Time) (recipe.Recipe, error) {
	recs, err := paprika.Parse(data)
	if err != nil {
		return recipe.Recipe{}, err
	}
	if len(recs) == 0 {
		return recipe.Recipe{}, errNoRecipes
	}
	s.pins.Set(recs[0], now)
	return recs[0], nil
}

var errNoRecipes = errors.New("no recipes found in file")

// readUpload pulls the file bytes from either a multipart form (any field) or
// the raw request body.
func readUpload(r *http.Request) ([]byte, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUpload); err != nil {
			return nil, err
		}
		for _, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				f, err := fh.Open()
				if err != nil {
					return nil, err
				}
				defer f.Close()
				return io.ReadAll(io.LimitReader(f, maxUpload))
			}
		}
		return nil, errNoFile
	}
	return io.ReadAll(io.LimitReader(r.Body, maxUpload))
}

var errNoFile = errors.New("no file in form upload")

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// parseBattery reads the device's charge from the request headers the TRMNL
// firmware sends on every /api/display poll. Absent (e.g. non-device clients)
// yields a zero Battery, which renders nothing.
func parseBattery(r *http.Request) render.Battery {
	s := header(r, "Percent-Charged", "Battery-Percent")
	if s == "" {
		return render.Battery{}
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return render.Battery{}
	}
	return render.Battery{Percent: n, Present: true}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
