// Command bus-trmnl is a self-hosted TRMNL BYOS server that shows SF MUNI
// arrivals from the 511.org real-time API on a TRMNL e-ink device.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/thommahoney/bus-trmnl/internal/board"
	"github.com/thommahoney/bus-trmnl/internal/config"
	"github.com/thommahoney/bus-trmnl/internal/five11"
	"github.com/thommahoney/bus-trmnl/internal/pin"
	"github.com/thommahoney/bus-trmnl/internal/screen"
	"github.com/thommahoney/bus-trmnl/internal/server"
)

func main() {
	log.SetFlags(log.LstdFlags)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "discover":
		runDiscover(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		// Default to serve so `bus-trmnl -config ...` works too.
		runServe(os.Args[1:])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `bus-trmnl - TRMNL BYOS server for SF MUNI arrivals

Usage:
  bus-trmnl serve    -config config.yaml
  bus-trmnl discover -config config.yaml -query "9th Ave"

Commands:
  serve      Run the BYOS HTTP server the device polls.
  discover   List 511 stop codes whose name matches -query (to fill in config).
`)
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "path to config file")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	loc, err := cfg.Location()
	if err != nil {
		log.Fatalf("timezone %q: %v", cfg.Server.Timezone, err)
	}

	if cfg.HasScreen(config.ScreenMuni) {
		warnRateLimit(cfg)
	}

	client := five11.New(cfg.Five11.APIKey, cfg.Five11.Operator, cfg.Five11.BaseURL)
	store := board.NewStore(cfg, client)

	screens := make([]screen.Screen, 0, len(cfg.Screens))
	for _, sc := range cfg.Screens {
		switch sc.Type {
		case config.ScreenMuni:
			screens = append(screens, screen.NewMuni(store, cfg.Refresh, sc.Design))
		case config.ScreenCat:
			screens = append(screens, screen.NewCat(sc.URL))
		}
	}
	rot := screen.NewRotation(screens...)

	// Recipe focus mode: an uploaded Paprika recipe pins to the screen for
	// cfg.Recipes.PinTTL, taking over the rotation until it expires.
	pin.EnsureDir(cfg.Recipes.StateFile)
	pins := pin.NewStore(cfg.Recipes.StateFile, cfg.Recipes.PinTTL.D())
	recipeScreen := screen.NewRecipe(pins)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:    cfg.Server.Listen,
		Handler: server.New(cfg, loc, rot, pins, recipeScreen).Handler(),
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("bus-trmnl serving on %s (base_url %s), %d screen(s), %d board(s)", cfg.Server.Listen, cfg.Server.BaseURL, len(cfg.Screens), len(cfg.Boards))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

// warnRateLimit logs the worst-case 511 request rate. Fetches happen on
// demand — only when the MUNI screen renders, and at most once per
// poll_interval — so the steady-state rate is at or below this.
func warnRateLimit(cfg *config.Config) {
	stops := len(cfg.DistinctStops())
	perHour := float64(stops) * (3600.0 / cfg.Five11.PollInterval.D().Seconds())
	log.Printf("fetching %d distinct stop(s) on demand, at most every %s = ~%.0f 511 requests/hour worst case", stops, cfg.Five11.PollInterval.D(), perHour)
	if perHour > 60 {
		log.Printf("WARNING: ~%.0f req/hour exceeds the 511 default limit of 60/hour; "+
			"increase five11.poll_interval or request a higher limit from 511.", perHour)
	}
}

func runDiscover(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "path to config file")
	query := fs.String("query", "", "case-insensitive substring to match stop names")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	client := five11.New(cfg.Five11.APIKey, cfg.Five11.Operator, cfg.Five11.BaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stops, err := client.Stops(ctx)
	if err != nil {
		log.Fatalf("fetch stops: %v", err)
	}

	q := strings.ToLower(*query)
	matched := 0
	for _, s := range stops {
		name := string(s.Name)
		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		fmt.Printf("%-10s %s\n", s.ID, name)
		matched++
	}
	fmt.Fprintf(os.Stderr, "\n%d of %d stops matched %q for operator %s\n", matched, len(stops), *query, cfg.Five11.Operator)
}
