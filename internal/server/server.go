// Package server implements the TRMNL BYOS device-facing HTTP API.
package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/board"
	"github.com/thommahoney/bus-trmnl/internal/config"
	"github.com/thommahoney/bus-trmnl/internal/render"
)

// Server serves the BYOS endpoints the TRMNL device polls.
type Server struct {
	cfg   *config.Config
	loc   *time.Location
	store *board.Store

	mu       sync.Mutex
	lastFile string
	lastGen  time.Time
}

// New creates a Server.
func New(cfg *config.Config, loc *time.Location, store *board.Store) *Server {
	return &Server{cfg: cfg, loc: loc, store: store}
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
	mux.HandleFunc("/health", s.handleHealth)
	mux.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir(s.cfg.Server.ImageDir))))
	return mux
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
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      200,
		"api_key":     s.cfg.Device.AccessToken,
		"friendly_id": s.cfg.Device.FriendlyID,
		"image_url":   "",
		"message":     "Welcome to bus-trmnl",
	})
}

// handleDisplay renders the current board and tells the device when to wake.
func (s *Server) handleDisplay(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"status": 401, "error": "invalid access token"})
		return
	}

	width := atoiDefault(header(r, "WIDTH"), render.DefaultWidth)
	height := atoiDefault(header(r, "HEIGHT"), render.DefaultHeight)

	now := time.Now().In(s.loc)
	refresh := int(s.cfg.Refresh.RateAt(now).Seconds())

	filename, err := s.renderToFile(now, width, height)
	if err != nil {
		log.Printf("render failed: %v", err)
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

// renderToFile renders the current snapshot to a uniquely named PNG and prunes
// stale files. The filename changes each cycle so the device re-downloads.
func (s *Server) renderToFile(now time.Time, width, height int) (string, error) {
	png, err := render.Screen(s.store.Snapshot(), now, width, height)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.cfg.Server.ImageDir, 0o755); err != nil {
		return "", err
	}
	filename := "muni-" + strconv.FormatInt(now.Unix(), 10) + ".png"
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
