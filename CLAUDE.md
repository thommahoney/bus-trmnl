# CLAUDE.md

Context for Claude Code (and humans) working on **bus-trmnl**. See `README.md`
for user setup and `HANDOFF.md` / `design/` for deeper history.

## What it is

bus-trmnl is a self-hosted **TRMNL BYOS** ("Bring Your Own Server") written in
Go. It drives a TRMNL **X** e-ink device (10.3", 1872×1404, 16-level grayscale)
by serving full-screen PNGs the device downloads on each wake. The primary
screen shows live **SF MUNI** arrival predictions from the **511.org** SIRI API.
A rotation cycles the device through multiple screens — currently the MUNI
arrivals board and a random cat photo.

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
- **Demand-driven 511:** there is no background poller. The MUNI screen calls
  `board.Store.EnsureFresh` at render time, which hits 511 only if the cache is
  older than `poll_interval` (default 2m), single-flighted. Non-MUNI screens
  make zero 511 calls.

## Code structure

- `main.go` — CLI (`serve`, `discover`); builds the `[]screen.Screen` from
  config and wires a `screen.Rotation` into the server.
- `internal/config` — YAML load/validate, `${ENV}` expansion, refresh windows,
  the `screens` list (back-compat: omitted ⇒ `[{type: muni}]`).
- `internal/five11` — 511.org SIRI `StopMonitoring` client (handles the UTF-8
  BOM and string-or-array `FlexString` quirks).
- `internal/board` — `Store`: fetch/filter/cache arrivals; `EnsureFresh`
  (demand-driven, throttled, single-flight); `Fetcher` interface for test fakes.
- `internal/render` — boards → grayscale PNG with embedded fonts, tuned for
  e-ink (see `design/philosophy.md`).
- `internal/screen` — `Screen` interface + `Rotation`; `Muni` (wraps
  board+render) and `Cat` (fetches cataas.com, scales, Floyd–Steinberg dithers
  to 16-level grayscale under the device size cap).
- `internal/server` — BYOS HTTP API: `/api/display`, `/api/setup`, `/api/log`,
  `/latest` (preview, `?screen=<name>`), `/images/`, `/health`.
  `renderWithFallback` tries other screens if one fails so the device never
  blanks.

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
- Verify a render visually: `curl localhost:2300/latest?screen=cat` (or `muni`),
  which previews a screen without advancing the rotation.

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
- `README.md` is **stale** — it predates the multi-screen rotation, cat screen,
  demand-driven 511, and dithering. Update it when convenient.

## Where to read more

- `README.md` — user-facing setup (stale, see above).
- `HANDOFF.md` — original build notes: full BYOS API contract, X hardware, and a
  decision log.
- `design/multi-screen-plan.md` — rotation + demand-driven 511 design.
- `design/philosophy.md` — the e-ink rendering/visual rationale.
- Product docs: TRMNL BYOS — https://docs.trmnl.com/go/diy/byos; API spec —
  `usetrmnl/terminus` `doc/api.adoc`. Transit data — https://511.org/open-data/transit.
