// Package server implements the TRMNL BYOS device-facing HTTP API.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	"github.com/thommahoney/bus-trmnl/internal/render"
	"github.com/thommahoney/bus-trmnl/internal/screen"
)

// Server serves the BYOS endpoints the TRMNL device polls.
type Server struct {
	cfg *config.Config
	loc *time.Location
	rot *screen.Rotation

	mu       sync.Mutex
	lastFile string
	lastGen  time.Time
}

// New creates a Server.
func New(cfg *config.Config, loc *time.Location, rot *screen.Rotation) *Server {
	return &Server{cfg: cfg, loc: loc, rot: rot}
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

	filename, err := s.renderWithFallback(r.Context(), s.rot.Next(), now, width, height)
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

// renderToFile renders one screen to a uniquely named PNG and prunes stale
// files. The filename changes each cycle so the device re-downloads.
func (s *Server) renderToFile(ctx context.Context, scr screen.Screen, now time.Time, width, height int) (string, error) {
	png, err := scr.Render(ctx, now, width, height)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.cfg.Server.ImageDir, 0o755); err != nil {
		return "", err
	}
	filename := scr.Name() + "-" + strconv.FormatInt(now.Unix(), 10) + ".png"
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
		if scr, ok = s.rot.ByName(name); !ok {
			http.Error(w, "unknown screen "+strconv.Quote(name), http.StatusNotFound)
			return
		}
	}

	png, err := scr.Render(r.Context(), now, width, height)
	if err != nil {
		log.Printf("latest render failed: %v", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.Write(png)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
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
