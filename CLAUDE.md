# CLAUDE.md

Context for Claude Code (and humans) working on **bus-trmnl**. See `README.md`
for user setup and `HANDOFF.md` / `design/` for deeper history.

## What it is

bus-trmnl is a self-hosted **TRMNL BYOS** ("Bring Your Own Server") written in
Go. It drives a TRMNL **X** e-ink device (10.3", 1872×1404, 16-level grayscale)
by serving full-screen PNGs the device downloads on each wake. The primary
screen shows live **SF MUNI** arrival predictions from the **511.org** SIRI API.
A rotation cycles the device through multiple screens — currently three "moving"
MUNI designs (`radar`, `board`, `stream`), a random cat photo and the day's
weather forecast.

The MUNI designs exist to fight e-ink **burn-in/ghosting** (observed on the
physical panel): each reduces arrivals to a bus/train glyph + countdown and
**relocates its content on every render** so no pixel carries the same value
forever. They are selected per screen via the `design:` config — `radar`/`board`/
`stream`, plus `classic` (the original static detailed board, which is the layout
that burns in). `internal/render/designs.go` holds them; `design/philosophy.md`
covers the visual rationale.

Why BYOS: the hosted TRMNL cloud caps updates at ~5 min; BYOS lets us drive a
sub-minute, time-of-day refresh cadence.

## How the BYOS model works

The device is a stateless thin client: wake → `GET /api/display` → download the
returned `image_url` → sleep for `refresh_rate` seconds → repeat. All logic is
server-side:

- **Rotation is server-side:** each successive `/api/display` serves the next
  screen; the device just re-downloads whatever URL it is handed. A screen with
  `skip_during_rush: true` is passed over while a rush window is in effect (see
  below), so the commute minutes stay on transit. Filenames are
  **content-addressed** (`<screen>-<sha256[:8]>.png`), so a screen whose pixels
  changed gets a new name and redraws, while an unchanged frame keeps the same
  name and the firmware's filename cache (`checkCurrentFileName`) skips both the
  download and the panel refresh. The moving MUNI designs change every render so
  they always redraw; a static screen (a pinned recipe) is fetched once and then
  skipped, saving a full grayscale refresh every wake.
- **refresh_rate** is per-response: 30s during the weekday rush window
  (07:45–08:15 America/Los_Angeles), 60s otherwise. `RefreshConfig.IsRush` is
  the single definition of "rush": both the rate and the `skip_during_rush`
  screen filter key off it, so there is only ever one time range to edit.
- **Forced full refresh:** `/api/display` returns `maximum_compatibility: true`
  so that whenever the device *does* redraw it's a full panel refresh —
  belt-and-suspenders against the observed ghosting, on top of the relocating
  MUNI designs. (It has no effect on wakes the device skips via the
  content-addressed filename cache above.)
- **Demand-driven 511:** there is no background poller. The MUNI screen calls
  `board.Store.EnsureFresh` at render time, which hits 511 only if the cache is
  older than `poll_interval` (default 2m), single-flighted. Non-MUNI screens
  make zero 511 calls. The weather screen follows the same pattern against
  Open-Meteo (`weather.Store.EnsureFresh`, `weather.poll_interval`, default 30m).

## Code structure

- `main.go` — CLI (`serve`, `discover`); builds the `[]screen.Screen` from
  config and wires a `screen.Rotation` into the server.
- `internal/config` — YAML load/validate, `${ENV}` expansion, refresh windows,
  the `screens` list (back-compat: omitted ⇒ `[{type: muni}]`), each MUNI
  screen's `design` (empty ⇒ `board`; validated against the four design names)
  and each screen's `skip_during_rush` (validated so at least one screen
  survives the rush filter — otherwise the rotation would have nothing to show).
  The `weather` block (lat/long, label, units, poll interval) is validated only
  when a `weather` screen is listed.
- `internal/five11` — 511.org SIRI `StopMonitoring` client (handles the UTF-8
  BOM and string-or-array `FlexString` quirks).
- `internal/weather` — Open-Meteo forecast client (no API key) + `Store`
  mirroring `board.Store`: demand-driven, throttled, single-flight, `Fetcher`
  seam. A failed fetch keeps the last good forecast and retries after 5m.
  `testdata/forecast.json` is a captured response.
- `internal/board` — `Store`: fetch/filter/cache arrivals; `EnsureFresh`
  (demand-driven, throttled, single-flight); `Fetcher` interface for test fakes.
- `internal/render` — grayscale PNGs with embedded fonts, tuned for e-ink (see
  `design/philosophy.md`). `render.go` is the `classic` board; `designs.go` holds
  the moving designs (`Radar`/`Reflow`/`Stream`) — pure `*Layout` helpers place
  big numerals clear of bus/train markers at any rotation, covered by
  `designs_test.go` (collision + on-canvas + size-cap assertions). All four MUNI
  clocks render the device battery `%` next to the time; `battery.go` carries the
  per-request `Battery` (from the `Percent-Charged` / `Battery-Voltage` headers)
  through the render context. `weather.go` is the forecast screen (see below).
  `lowbattery.go` stamps the low-battery badge onto
  a *finished* PNG (see below).
- `internal/screen` — `Screen` interface + `Rotation` (a `[]Slot`, each screen
  plus its `SkipDuringRush` flag; `Next(rush bool)` steps over skipped slots,
  while `Peek`/`All` stay unfiltered — `All` backs `renderWithFallback`, where
  showing any screen beats blanking the panel); `Muni` (parameterized by
  `design`, name `muni-<design>`, drives anti-burn-in motion via a per-screen
  render counter) and `Cat` (fetches cataas.com, scales, Floyd–Steinberg dithers
  to 16-level grayscale under the device size cap); `Weather` (errors when no
  forecast has ever been fetched, so `renderWithFallback` shows something else).
- `internal/server` — BYOS HTTP API: `/api/display`, `/api/setup`, `/api/log`,
  `/api/recipe` (+ `/unpin`), `/latest` (preview, `?screen=<name>`, `?battery=NN`),
  `/images/`, `/health`. `renderWithFallback` tries other screens if one fails so
  the device never blanks. `/api/display` reads the device's `Percent-Charged`
  and `Battery-Voltage` headers into the render context so the clock can show
  battery and `stampLowBattery` can add the warning overlay.
- `internal/paprika` — parses Paprika exports (`.paprikarecipes` ZIP of gzipped
  JSON, bare gzipped JSON, or plain JSON) into `recipe.Recipe`.
- `internal/recipe` — the normalized recipe model (leaf package).
- `internal/pin` — mutex-guarded "pinned recipe + expiry", persisted to
  `pin.json` (default inside `image_dir`, the dir the non-root container can
  write); `Active` clears the pin once it expires.

## Weather screen

`type: weather` renders current conditions in a header plus three stacked
strips — temperature curve, wind bars (whisker = gusts, arrows point where the
wind blows *to*), chance-of-rain bars — on one shared hour axis, 6am to
midnight. `render.weatherDay` picks the day: **today until 20:00, then
tomorrow**, since by evening today's chart is mostly history (the header's big
temperature is always the current reading; the right-hand block names the
charted day). Hours already past are drawn faint and a dashed "now" line
crosses all three strips. The axis ticks every hour (long on the labelled
3-hourly ones), and sunrise/sunset glyphs (half sun + up/down arrow,
`drawSunEvent`) hang below it at their actual times; the header repeats the
same glyphs beside the times rather than a bare "sun", which read as Sunday.

The forecast only changes hourly, so this would be the most static frame in the
rotation. `computeWeatherFrame` therefore relocates it per render: a 7-step
drift (`weatherShift`) and the header swapping between top and bottom, a
14-render cycle. `weather_test.go` asserts the regions stay on-canvas and
disjoint at every step.

It is deliberately **not** `skip_during_rush` in the live config — by user
choice the forecast shows during the commute even though that wake then carries
no MUNI times. A forecast older than 90 minutes is labelled "as of …"; one that
no longer covers the charted day makes `Render` fail over to another screen.

**Model choice:** `weather.model` defaults to `ncep_nbm_conus` (NOAA's National
Blend of Models, US-only), not Open-Meteo's `best_match`. In the US best_match
is HRRR on a 3km grid whose cell for 9th & Judah is centred ~1.5km southwest;
over Oct 2–4 2026 it ran hot on sea-breeze afternoons (84°F forecast at 2pm vs
65–75°F at the Golden Gate Park, downtown and Cole Valley stations). NBM's 2.5km
cell lands ~100m from the stop and its mean error against those stations was
lower (3.3 vs 4.1°F at GG Park). That was a 2.5-day sample during a heat event,
so re-check before treating it as settled: Open-Meteo accepts
`models=a,b,c` to pull several side by side, and
`api.weather.gov/stations/{604PG,F2543,SFOC1}/observations` gives ground truth.

## Low-battery warning

The device dies silently: the panel holds its last image, so a dead device looks
like a working one showing stale arrivals. To make that visible, any frame
rendered while the device reports a low pack voltage gets a loud "LOW BATTERY"
badge stamped on it.

**It keys off voltage, not percentage.** ~38 days of `/api/display` telemetry
showed the reported percentage flattening out near empty — the device sat at 4%
for three straight days, and spent 8 days below 10% before dying — so "<10%" is
a multi-day window, not an actionable signal. Voltage keeps falling the whole
way (3.67V at that 4% plateau, 3.01V at death). `device.low_battery_voltage`
(default `3.70`, `0` disables) is the threshold; 3.70 would have fired ~40h and
~62h ahead of the two observed cycle ends, where 3.2V gives barely an hour.

Mechanically it is a **post-process on the encoded PNG**
(`render.LowBatteryOverlay`, applied in `server.stampLowBattery`), not something
each screen draws: one call site in `renderToFile` covers every screen, the
recipe pin and every `renderWithFallback` path. The overlay preserves the
source's pixel format, so a dithered cat stays paletted/4-bpp and under the
750KB firmware cap (~415KB → ~470KB with the badge). It composites through the
badge's alpha mask so only the rounded pill lands on the frame. Because the
badge changes the pixels, content-addressing gives the frame a new filename and
the device redraws instead of skipping the wake. A charging device
(`Battery-Charging: 1` / `Usb-Connected: true`) is never nagged.

Preview it without a real flat battery:
`curl "localhost:2300/latest?screen=cat&battery=2&voltage=3.42"`.

## Recipe focus mode

Optional feature, independent of the rotation. `POST /api/recipe` (open, no
token — by user choice) parses a Paprika file and **pins** the recipe for
`recipes.pin_ttl` (default 3h). While a pin is active, `/api/display` renders
the `recipe` screen instead of advancing the rotation; when it expires the
rotation resumes on its own. `internal/server.ingest` is the single seam every
ingestion channel funnels through (today: web `curl`/iOS Shortcut; later:
Telegram/email/MMS as thin adapters). The card (`render.Recipe`) is deliberately
**static** — you read it while cooking — the opposite of the moving MUNI designs.
The pin file lives in `image_dir` because the distroless `nonroot` container
can't write `/data` directly; `pruneOld` only reaps `*.png` so it's left alone.

## How we develop together (IMPORTANT)

**All build/test/run goes through Docker — never the system Go.**

- Run / apply code changes: `docker compose up -d --build` (serves :2300;
  recreates the container in place — do **not** `docker compose down`).
- Bounce without a rebuild (e.g. after a `data/config.yaml` change, which is
  volume-mounted): `docker compose restart`.
- Build / vet / fmt / test in a throwaway Go container:
  ```sh
  docker run --rm -v "$PWD":/src -w /src -v bus-trmnl-gocache:/go \
    -e GOFLAGS=-buildvcs=false golang:1.25-bookworm \
    sh -c 'gofmt -l . && go build ./... && go vet ./... && go test ./...'
  ```
  (`-buildvcs=false` avoids git "dubious ownership" when running as root over
  the mounted checkout; the `/go` volume caches modules between runs.)
- Verify a render visually: `curl localhost:2300/latest?screen=cat` (or
  `muni-radar`, `muni-board`, `muni-stream`, `weather`, `recipe`), which previews a screen
  without advancing the rotation.

## TRMNL X firmware (the device)

Firmware source: **github.com/usetrmnl/trmnl-firmware** (PlatformIO/ESP32). Key
facts we rely on (verified from source + the device's `/api/log` telemetry):

- The X builds with the **FastEPD** library and is a **true 16-level grayscale**
  panel. PNG bit depth selects the draw mode: 1-bpp→2 levels, 2-bpp→4,
  **≥4-bpp→16 levels** (`BB_MODE_4BPP`). Grayscale always full-refreshes (no
  ghosting).
- **Image-size cap:** the firmware rejects any download over `MAX_IMAGE_SIZE`
  with `"Receiving failed; file size too big"` — **750000 bytes** for the X
  (`include/config.h`; 90000 for non-X boards). Full-panel 8-bit grayscale
  photos (~0.7–1.3 MB) get bounced, which is why `internal/screen/cat.go`
  dithers cats to ≤16-level 4-bpp PNGs (~0.24–0.73 MB) with an 8→4-level
  fallback under the cap.
- **Firmware updates are server-driven OTA:** the device updates only when
  `/api/display` returns `update_firmware: true` + a `firmware_url`. We send
  `false`, so the device stays put (currently FW 1.8.5). Update by flashing
  manually or via the official cloud.
- BYOS response fields the firmware honors: `image_url`, `filename`,
  `refresh_rate`, `update_firmware`/`firmware_url`, `reset_firmware`,
  `special_function`, `temperature_profile` ("default"/"a"/"b" waveform LUT),
  `maximum_compatibility` (forces a full refresh every cycle). The full contract
  is in `HANDOFF.md` §3.4.

## Deployment

- Runs as a `docker compose` service (`docker-compose.yml`): container
  `bus-trmnl` on **:2300**, `restart: unless-stopped`, `./data:/data` (holds the
  gitignored `config.yaml` and rendered images). `config.example.yaml` is the
  template; secrets come from a `.env` (`FIVE11_API_KEY`, `DEVICE_ACCESS_TOKEN`)
  via `${ENV}` expansion.
- Exposed publicly via **nginx** at `trmnl.thom.is` (`nginx/`), proxying all
  paths to `localhost:2300`, so the device can poll over the internet.
- **TLS via certbot (webroot flow):**
  1. Bootstrap with `nginx/trmnl.thom.is.pre-cert` — serves the ACME challenge
     from `/var/www/html/.well-known/` over HTTP and redirects the rest to HTTPS.
  2. `sudo certbot certonly --webroot -w /var/www/html -d trmnl.thom.is`
  3. Swap in `nginx/trmnl.thom.is`, which adds the SSL server block using the
     letsencrypt cert. certbot's systemd timer renews it.

## Conventions & gotchas

- Go: standard `gofmt`; small packages with interface seams (`Fetcher`,
  `Screen`) for testability.
- Rendering: text wants crisp grayscale; photos want dithering (the cat path).
- Device auth: `device.access_token` is optional (empty ⇒ open, intended for a
  trusted LAN or behind nginx). The live deploy currently runs with it empty.
- **Overnight MUNI lines:** 511 publishes the rail-substitution runs as their
  own lines, not as variants of the rail line — `NOWL` ("OWL JUDAH", ~00:59–05:29
  nightly), `NBUS` ("JUDAH BUS", the pre-train early trip), plus LOWL/KBUS/FBUS/
  TBUS elsewhere. A board filtering `lines: ["N"]` therefore goes empty once the
  trains stop, which is what put "no arrivals" on the panel all night; the live
  N board lists all three. `render.kindOf` deliberately renders them with the
  **train** glyph despite an actual bus pulling up: the glyph identifies the
  *line*, not the vehicle, and on an unlabelled panel it is the only thing
  separating the N board from the 43/44 board.
  The 43/44 board has no overnight counterpart — the 43 has no owl service and
  the 44 Owl runs Glen Park–Hunters Point, ~1.2km from 9th Ave at the closest.
  All of this was verified against the 511 GTFS feed
  (`/transit/datafeeds?operator_id=SF`), which is how to re-check spans when a
  service change lands.

## Known limitations / good first cleanups

- `/latest` is **unauthenticated**, and `?screen=cat` triggers an outbound
  cataas fetch on every request — an amplification vector given the public nginx
  exposure. Consider auth and/or a short cat cache.
- The `server` package has **no automated tests** (the `renderWithFallback`
  fallback path is only smoke-tested).

## Where to read more

- `README.md` — user-facing setup.
- `HANDOFF.md` — original build notes: full BYOS API contract, X hardware, and a
  decision log.
- `design/multi-screen-plan.md` — rotation + demand-driven 511 design.
- `design/philosophy.md` — the e-ink rendering/visual rationale.
- `design/recipe-feature.md` — Paprika upload, 3h pinned focus mode, the
  real-recipe rendering learnings, and the nginx/battery operational notes.
- Product docs: TRMNL BYOS — https://docs.trmnl.com/go/diy/byos; API spec —
  `usetrmnl/terminus` `doc/api.adoc`. Transit data — https://511.org/open-data/transit.
