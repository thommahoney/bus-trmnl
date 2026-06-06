# Session handoff: bus-trmnl

This document is a complete handoff from the cloud (Claude Code on the web)
session that created this project. It's written so a fresh Claude Code session
running on your **local workstation** can pick up with full context — what was
researched, what was built and why, the current state, and exactly what's left.

> TL;DR: This is a self-hosted **TRMNL BYOS** server in Go that shows live SF
> MUNI arrivals (43/44 → Forest Hill, N → Caltrain) on a TRMNL X. It builds,
> tests pass, and it's on branch `claude/trmnl-plugin-research-RpdVs` (PR #1
> into `main`). The only things blocking a real run are: your 511 **stop
> codes**, the server's **`base_url`**, and **pointing the device** at the
> server. Those couldn't be done from the cloud sandbox because it can't reach
> `api.511.org`.

---

## 1. Current state

- **Branch:** `claude/trmnl-plugin-research-RpdVs` (the implementation).
- **Base branch:** `main` — created as an (essentially empty) base so a PR
  could be opened. It only contains a stub README at its root commit; the
  feature branch was rebased on top of it.
- **PR:** #1 — "SF MUNI arrivals on TRMNL (self-hosted BYOS server in Go)",
  `claude/trmnl-plugin-research-RpdVs` → `main`.
- **CI:** none configured (only GitHub's automatic Dependency Graph workflow).
  No build/test/lint runs against the PR yet.
- **Verified locally in the sandbox:** `go build ./...`, `go vet ./...`,
  `gofmt -l .` (clean), `go test ./...` (pass), and an end-to-end smoke test of
  the HTTP server (rendered + served a PNG, correct `refresh_rate`, graceful
  "data unavailable" on 511 failure).

To resume locally:

```sh
git clone https://github.com/thommahoney/bus-trmnl.git
cd bus-trmnl
git fetch origin
git checkout claude/trmnl-plugin-research-RpdVs
go build ./... && go test ./...
```

---

## 2. What the product is (problem statement)

Put a board on a **TRMNL X** (10.3" e-ink) showing live SF MUNI arrivals:

- **43 / 44** outbound toward **Forest Hill Station**, boarding at
  **9th Ave & Kirkham**.
- **N Judah** toward **Caltrain** (inbound/downtown), boarding at
  **9th Ave & Judah St**.

Refresh cadence: **every 30 seconds Mon–Fri 7:45–8:15 AM**, **every 60 seconds**
otherwise. Device is **USB-powered** (so frequent refresh is fine). Data comes
from **511.org** (user has a token). Server is **self-hosted, always-on**,
deployed via **Docker** on a VPS/home server, written in **Go**.

---

## 3. Research findings (how TRMNL + BYOS + 511 actually work)

### 3.1 TRMNL device model
- TRMNL devices are **thin e-ink clients**. They wake on a refresh interval,
  **pull a pre-rendered image** from a server, display it, and sleep. The device
  runs no plugin logic.
- **Plugin types:** private (your account/devices only) vs public (marketplace).
- **Cloud data strategies:** polling (TRMNL fetches a URL you provide),
  webhook (you POST to TRMNL), static/computed.
- **Templating** on the cloud is **Liquid** + TRMNL's Framework CSS. Layout
  variants: `full`, `half_horizontal`, `half_vertical`, `quadrant`. Local dev
  tool is **`trmnlp`** (`init`/`serve`/`lint`/`push`/`pull`).

### 3.2 Why the cloud could not meet the 30-second requirement
Three independent constraints:
1. **Webhook rate limit:** HTTP 429 if you push more than once per **5 minutes**
   (12/hour standard, 30/hour ≈ every 2 min for TRMNL+).
2. **Cloud refresh floor:** fastest data refresh is **15 min**; the screen can
   switch at most every **5 min**.
3. **Webhook ≠ display.** A webhook only updates the *cached screen on the
   server*; the device still only shows it when it next **pulls** on its own
   refresh interval.

Battery reality (relevant even off-cloud): refresh every 5 min ≈ ~40 days;
every 60 min ≈ ~1 year; **every 30s ≈ days**. The X's multi-month battery rating
assumes infrequent refresh — hence the USB-power decision.

### 3.3 BYOS (Bring Your Own Server) — the chosen path
- BYOS lets you **point the device at your own server** instead of TRMNL cloud.
  No cloud rate limits.
- The server controls the wake cadence: each `/api/display` response includes a
  **`refresh_rate` in seconds**. Return `30` during rush, `60` otherwise — this
  is exactly how the dynamic cadence is implemented.
- Reference servers: `usetrmnl/byos_sinatra` (Ruby), `usetrmnl/byos_phoenix`
  (Elixir), `usetrmnl/larapaper` (PHP), `usetrmnl/terminus` (has the API spec at
  `doc/api.adoc`). Closest analog to this project:
  **`giglabo/munich-glance`** — a Python/FastAPI BYOS server for Munich transit
  with dynamic refresh + sleep mode. Worth reading for ideas.

### 3.4 BYOS device API contract (from terminus `doc/api.adoc`)
The device calls these endpoints:

- **`GET /api/setup`** — first-boot pairing. Sends `ID` (MAC) header. Server
  returns JSON: `api_key`, `friendly_id`, `image_url`, `message`.
- **`GET /api/display`** — the main poll. Sends headers including `ID`,
  `Access-Token`, `WIDTH`, `HEIGHT`, `PERCENT_CHARGED`, `BATTERY_VOLTAGE`,
  `USB_CONNECTED`, `FW_VERSION`, `RSSI`, `REFRESH_RATE`, `WAKE_TIME`. Server
  returns JSON:
  - `filename`, `image_url`, `image_url_timeout`
  - **`refresh_rate`** (seconds until next wake) ← the key field
  - `firmware_url`, `firmware_version`, `update_firmware`, `reset_firmware`
  - `special_function` (e.g. `"sleep"`), `temperature_profile`, `touchbar_mode`
- **`POST /api/log`** — device telemetry; respond `204 No Content`.

The device downloads the PNG at `image_url` (must be an absolute, device-
reachable URL) and displays it.

### 3.5 TRMNL X hardware
- 10.3" e-ink, **1872×1404**, **16-level grayscale (4-bit)**. Full refresh
  ≤1.2s, partial ≤200ms. Supports landscape and portrait.
- **Open question carried forward:** the exact image format/bit-depth the X
  firmware expects. We render **8-bit grayscale PNG** at the device-reported
  size, which is a safe default, but verify on first pairing and adjust
  `internal/render` if needed (e.g. 1-bit, dithering, or a specific palette).

### 3.6 511.org (the data source)
- Token: https://511.org/open-data/token. **Rate limit: 60 requests/hour per
  token** (60 per 3600s). Request an increase via
  `511sfbaydeveloperresources@googlegroups.com`.
- **SIRI `StopMonitoring`** is the recommended real-time source for Muni:
  `GET https://api.511.org/transit/StopMonitoring?api_key=KEY&agency=SF&stopcode=CODE&format=json`.
  Regional GTFS-RT bundles are available with `agency=RG`.
- **SFMTA's own GTFS is schedule-only** — use 511 for real-time.
- **Quirks (handled in code):** responses are prefixed with a **UTF-8 BOM**
  (must strip before JSON decode); some fields (`PublishedLineName`,
  `DestinationName`) come back as either a string or a single-element array.

Response shape used:
`ServiceDelivery → StopMonitoringDelivery → MonitoredStopVisit[] →
MonitoredVehicleJourney → { LineRef, DirectionRef, PublishedLineName,
DestinationName, MonitoredCall.ExpectedArrivalTime }`.

---

## 4. Decisions made (and why)

| Decision | Choice | Why |
| --- | --- | --- |
| Plugin content | SF MUNI arrivals | User's request |
| Data freshness | 30s rush / 60s otherwise | User's request |
| Platform | **BYOS**, not TRMNL cloud | Only way to get sub-5-min, time-of-day cadence |
| Power | USB-powered | 30s refresh would drain a battery in days |
| Language | **Go** | User's choice |
| Deploy | **Docker** on VPS/home server | User's choice |
| Distribution | Private (own device) | User's choice |
| Data fetch vs display | **Decoupled** | Respect 511's 60/hr limit while letting the display update every 30s |
| PR base | New empty `main`, feature rebased on it | Repo had only the feature branch; a PR needs a base with shared history |

**The decoupling decision is the most important architectural one.** 511 allows
60 req/hour. With 2 distinct stops, polling each every 2 minutes = exactly
60/hour. But the *display* needs to update every 30s. Resolution: a background
poller refreshes the cache from 511 at a safe rate; the device-facing handler
recomputes "minutes until arrival" from the cached absolute timestamps on every
request. So the countdown stays accurate to the second even though the
underlying predictions are only re-fetched every couple of minutes.

---

## 5. What was built (architecture + file-by-file)

Single Go binary, module `github.com/thommahoney/bus-trmnl`, Go 1.25 (the
`golang.org/x/image` dep requires ≥1.25).

```
main.go                      # `serve` and `discover` subcommands
internal/config/config.go    # YAML config, ${ENV} expansion, refresh-window logic
internal/five11/types.go     # SIRI types + FlexString (string-or-array)
internal/five11/client.go    # 511 HTTP client (BOM stripping, StopMonitoring, stops)
internal/board/board.go      # poller, filtering, thread-safe cache, refresh loop
internal/render/render.go    # grayscale PNG rendering (embedded Go fonts)
internal/server/server.go    # BYOS device API + image serving
config.example.yaml          # annotated config with CHANGE-ME placeholders
Dockerfile                   # multi-stage, distroless static, non-root
docker-compose.yml           # always-on deploy; mounts ./data -> /data
README.md                    # user-facing setup/run instructions
```

### Component notes
- **config** — Loads YAML, runs `os.ExpandEnv` so `${FIVE11_API_KEY}` /
  `${DEVICE_ACCESS_TOKEN}` stay out of the file. `RefreshConfig.RateAt(t)`
  resolves the wake interval from weekday + `HH:MM` windows. `DistinctStops()`
  dedupes stop codes (43 and 44 share one stop). Sensible defaults baked in
  (listen `:2300`, tz `America/Los_Angeles`, poll `2m`, rush 30s / default 60s,
  Mon–Fri 07:45–08:15).
- **five11** — `Client.StopMonitoring(stopCode)` and `Client.Stops()` (for
  `discover`). `get()` strips the UTF-8 BOM and surfaces non-200 bodies in
  errors. `FlexString.UnmarshalJSON` accepts string or `[]string`.
- **board** — `Store` holds per-board snapshots behind an `RWMutex`. `Run(ctx)`
  fetches immediately then on `poll_interval`. `refresh()` fetches each distinct
  stop once, then maps visits onto boards via `filterArrivals` (line +
  `destination_contains` + optional `direction` filters, sorted soonest-first,
  capped at `max`). `Arrival.MinutesUntil(now)` is recomputed at render time.
- **render** — `Screen(boards, now, w, h)` draws with `fogleman/gg` using
  embedded `gofont` faces (no external font files), then converts to
  `image.Gray` and PNG-encodes. Default size 1872×1404; uses device-reported
  `WIDTH`/`HEIGHT` when present. Layout: header ("SF MUNI" + clock), divider,
  one equal vertical slice per board (title + rows of `line | destination |
  ETA`), footer "updated HH:MM:SS". ETA shows "Now" for ≤0 min.
- **server** — Routes `/api/setup`, `/api/display`, `/api/log`, `/health`, and
  `/images/` (file server). `/api/display` optionally checks the `Access-Token`
  header against `device.access_token` (empty = no auth, for trusted LANs),
  reads `WIDTH`/`HEIGHT`, computes `refresh_rate` via `RateAt(now-in-tz)`,
  renders a timestamped PNG, prunes images older than 10 min, and returns the
  BYOS JSON. Header lookup tolerates both `Access-Token` and `ACCESS_TOKEN`
  casings.
- **main** — `serve` wires it all up with graceful shutdown and logs a warning
  if `(distinct stops) × (3600 / poll seconds) > 60` (the 511 budget).
  `discover -query "..."` lists 511 stop codes whose name matches, to fill in
  `stop_code`.

### Dependencies
`github.com/fogleman/gg`, `github.com/golang/freetype`, `golang.org/x/image`
(gofont + opentype), `gopkg.in/yaml.v3`.

---

## 6. Tests

- `internal/config` — `RateAt` (rush window boundaries, weekend), `parseHHMM`,
  `DistinctStops`.
- `internal/board` — `filterArrivals` (line/destination/direction/max/sort),
  `MinutesUntil`.
- `internal/five11` — `FlexString` decoding, `StopMonitoring` JSON decode.

There are no tests for `render`/`server` (verified manually via the smoke test);
adding a golden-image test for `render` and an `httptest` test for
`/api/display` would be good next steps.

---

## 7. What's left to do (the actual blockers)

These could **not** be done from the cloud sandbox because it can't reach
`api.511.org` (host not allowlisted) or your device. Your local workstation
can:

1. **Get the two 511 stop codes.** Set your token and run:
   ```sh
   export FIVE11_API_KEY=your-token
   cp config.example.yaml config.yaml      # edit api_key/base_url first
   go run . discover -config config.yaml -query "9th Ave & Kirkham"
   go run . discover -config config.yaml -query "Judah"
   ```
   Put the codes into the `stop_code` fields. The 43/44 board and N board are
   at different stops, so you'll have two distinct codes.
2. **Tune the filters against live data.** Confirm the 43/44 board shows
   outbound-to-Forest-Hill only, and that the N board's `destination_contains`
   matches reality — inbound N may report **`Caltrain`** or **`Embarcadero`**
   depending on the run. Adjust `destination_contains` (or use `direction`) until
   correct.
3. **Set `server.base_url`** to the server's device-reachable address (LAN IP or
   public hostname + port).
4. **Pair the device:** point the TRMNL X's server URL at this server, then
   watch the logs for `/api/setup` and `/api/display` calls.
5. **Verify the image format** the X firmware actually wants (see §3.5). If the
   panel looks wrong, adjust `internal/render` (bit depth / dithering).

### Optional enhancements discussed but not built
- **Overnight sleep window** via `special_function: "sleep"` + a sleep image
  (the device sleeps to save power / avoid burn-in). Wire into
  `server.handleDisplay` based on a configured quiet-hours window.
- **Go CI workflow** (`go build`/`vet`/`test` on PRs) — there's currently no CI.
- **Golden-image render test** and **`httptest` server test**.
- **Multiple destinations / more stops** — raise `poll_interval` or get a 511
  rate-limit increase if you add stops (keep within 60 req/hour).

---

## 8. Gotchas to remember

- **Go 1.25+ required** (transitive `golang.org/x/image` constraint).
- **511 rate limit is 60/hour per token.** The startup warning enforces
  awareness; don't drop `poll_interval` below what keeps you under 60/hr.
- **`base_url` must be reachable from the device**, not `localhost` — the device
  downloads the image from it.
- **Time zone matters**: `refresh_rate` and the clock are computed in
  `server.timezone`. The rush window is evaluated in that zone.
- **Secrets**: `config.yaml`, `.env`, `/data`, and `/images` are gitignored.
  Use `${ENV}` expansion for the 511 token and device access token.
- **511 BOM + string-or-array** quirks are already handled; if you add new 511
  fields, reuse `FlexString` and remember the BOM strip lives in
  `five11.Client.get`.

---

## 9. Open items outside the code

- **PR #1** is open into `main`. The cloud session subscribed to its activity;
  if you'd rather manage it locally, merging or closing it ends that watch.
- The cloud sandbox could not run `discover` or fetch live arrivals — **first
  real end-to-end test happens on your workstation.**

---

## 10. Source links

- TRMNL BYOS: https://docs.trmnl.com/go/diy/byos
- BYOS announcement: https://trmnl.com/blog/introducing-byos
- Device API spec: https://github.com/usetrmnl/terminus/blob/main/doc/api.adoc
- Refresh rates: https://help.trmnl.com/en/articles/10113695-how-refresh-rates-work
- Munich reference: https://github.com/giglabo/munich-glance
- 511 transit data: https://511.org/open-data/transit
- 511 open data FAQ (limits): https://511.org/about/faq/open-data
- TRMNL X spec: https://trmnl.com/products/x/spec-sheet
