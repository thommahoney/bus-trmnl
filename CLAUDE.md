# CLAUDE.md

Context for Claude Code (and humans) working on **bus-trmnl**. See `README.md`
for user setup and `HANDOFF.md` / `design/` for deeper history.

## What it is

bus-trmnl is a self-hosted **TRMNL BYOS** ("Bring Your Own Server") written in
Go. It drives a TRMNL **X** e-ink device (10.3", 1872×1404, 16-level grayscale)
by serving full-screen PNGs the device downloads on each wake. The primary
screen shows live **SF MUNI** arrival predictions from the **511.org** SIRI API.
A rotation cycles the device through multiple screens — currently three "moving"
MUNI designs (`radar`, `board`, `stream`) and a random cat photo.

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
  screen; the device just re-downloads whatever URL it is handed (filenames are
  timestamped so it always re-fetches).
- **refresh_rate** is per-response: 30s during the weekday rush window
  (07:45–08:15 America/Los_Angeles), 60s otherwise.
- **Forced full refresh:** `/api/display` returns `maximum_compatibility: true`
  so the device does a full panel refresh every wake — belt-and-suspenders
  against the observed ghosting, on top of the relocating MUNI designs.
- **Demand-driven 511:** there is no background poller. The MUNI screen calls
  `board.Store.EnsureFresh` at render time, which hits 511 only if the cache is
  older than `poll_interval` (default 2m), single-flighted. Non-MUNI screens
  make zero 511 calls.

## Code structure

- `main.go` — CLI (`serve`, `discover`); builds the `[]screen.Screen` from
  config and wires a `screen.Rotation` into the server.
- `internal/config` — YAML load/validate, `${ENV}` expansion, refresh windows,
  the `screens` list (back-compat: omitted ⇒ `[{type: muni}]`) and each MUNI
  screen's `design` (empty ⇒ `board`; validated against the four design names).
- `internal/five11` — 511.org SIRI `StopMonitoring` client (handles the UTF-8
  BOM and string-or-array `FlexString` quirks).
- `internal/board` — `Store`: fetch/filter/cache arrivals; `EnsureFresh`
  (demand-driven, throttled, single-flight); `Fetcher` interface for test fakes.
- `internal/render` — grayscale PNGs with embedded fonts, tuned for e-ink (see
  `design/philosophy.md`). `render.go` is the `classic` board; `designs.go` holds
  the moving designs (`Radar`/`Reflow`/`Stream`) — pure `*Layout` helpers place
  big numerals clear of bus/train markers at any rotation, covered by
  `designs_test.go` (collision + on-canvas + size-cap assertions).
- `internal/screen` — `Screen` interface + `Rotation`; `Muni` (parameterized by
  `design`, name `muni-<design>`, drives anti-burn-in motion via a per-screen
  render counter) and `Cat` (fetches cataas.com, scales, Floyd–Steinberg dithers
  to 16-level grayscale under the device size cap).
- `internal/server` — BYOS HTTP API: `/api/display`, `/api/setup`, `/api/log`,
  `/api/recipe` (+ `/unpin`), `/latest` (preview, `?screen=<name>`), `/images/`,
  `/health`. `renderWithFallback` tries other screens if one fails so the device
  never blanks.
- `internal/paprika` — parses Paprika exports (`.paprikarecipes` ZIP of gzipped
  JSON, bare gzipped JSON, or plain JSON) into `recipe.Recipe`.
- `internal/recipe` — the normalized recipe model (leaf package).
- `internal/pin` — mutex-guarded "pinned recipe + expiry", persisted to
  `pin.json` (default inside `image_dir`, the dir the non-root container can
  write); `Active` clears the pin once it expires.

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
  `muni-radar`, `muni-board`, `muni-stream`, `recipe`), which previews a screen
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
