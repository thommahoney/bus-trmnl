# Implementation plan: multiple screens with cycling

## Goal

Today the server renders exactly one thing — the MUNI arrivals board — and
hands its URL to the device on every `/api/display` poll. We want a list of
configured **screens** that the device cycles through, one per wake. Proof of
concept: two screens, the existing MUNI board and a random cat picture,
alternating on each refresh.

## How cycling falls out of the existing polling model

The TRMNL device is dumb: it wakes, calls `/api/display`, downloads whatever
`image_url` it's given, sleeps for `refresh_rate` seconds, repeats. So "cycling"
requires no device-side work at all — the server just renders a different
screen on each successive `/api/display` call. The filename already changes
every cycle (unix timestamp suffix), so the device always re-downloads.

## Design

### 1. New package `internal/screen` — the `Screen` interface

```go
// Screen produces a full-panel PNG for one slot in the rotation.
type Screen interface {
    // Name is a short slug used in filenames, logs, and the /latest preview.
    Name() string
    Render(ctx context.Context, now time.Time, width, height int) ([]byte, error)
}
```

Two implementations for the POC:

- **`muni.go`** — wraps the existing pipeline: holds the `*board.Store` and the
  refresh-rate function, calls `store.Snapshot()` + `render.Screen(...)`.
  Pure refactor; the render package itself doesn't change.
- **`cat.go`** — fetches a random cat photo over HTTP, fits it to the panel,
  and encodes a PNG:
  - Source: `https://cataas.com/cat` (no API key required; URL configurable).
  - Fetch with a ~10 s timeout via the request context.
  - Decode (JPEG/PNG), scale to fit with `golang.org/x/image/draw`
    (already an indirect dependency tree we use for fonts), center on a white
    canvas, convert to grayscale, encode PNG.
  - **Cache the last successful image** in memory. On fetch failure, reuse the
    cached one; if there's never been one, return an error so the rotation can
    fall back (see below).

### 2. Rotation state

A small `screen.Rotation` type owning `[]Screen` and a cursor:

```go
func (r *Rotation) Next() Screen   // returns current screen, advances cursor
func (r *Rotation) Peek() Screen   // returns current screen without advancing
func (r *Rotation) ByName(string) (Screen, bool)
```

- Mutex-protected; state is in-memory only (a restart resets to screen 0 —
  fine, there's one device and no durability requirement).
- `handleDisplay` calls `Next()`. `handleSetup` and `/latest` use `Peek()` /
  `ByName()` so previews and device pairing don't skip slots in the rotation.
- **Fallback:** if the chosen screen's `Render` fails (e.g. cataas is down and
  no cached image), try the remaining screens in rotation order before
  returning 500. The MUNI screen renders from local cache and effectively
  can't fail, so the device never blanks out.

### 3. Config: a `screens` list

```yaml
screens:
  - type: muni
  - type: cat
    url: "https://cataas.com/cat"   # optional, this is the default
```

- New `ScreensConfig []ScreenConfig` on `Config`, where `ScreenConfig` is
  `{Type string, URL string}` for now.
- **Back-compat default:** if `screens` is omitted, default to `[{type: muni}]`
  — existing configs keep working with identical behavior (single screen,
  "rotation" of one).
- Validation: `type` must be `muni` or `cat`; at least one screen; the existing
  "at least one board" rule only applies when a `muni` screen is configured.
- `main.go` builds the `[]Screen` slice from config (a small factory switch)
  and passes a `*screen.Rotation` into `server.New`.

### 4. Server changes (`internal/server/server.go`)

- `Server` gains the `*screen.Rotation`; `renderToFile` takes a `Screen` and
  delegates rendering to it instead of calling `render.Screen` directly.
- Filename becomes `<screen-name>-<unix>.png` (e.g. `cat-1760000000.png`)
  instead of the hardcoded `muni-` prefix. Pruning is unchanged (it sweeps the
  whole image dir by mtime).
- `/latest` gains `?screen=<name>` to preview a specific screen on demand
  without touching the rotation; no param means `Peek()`.

### 5. Refresh rate (unchanged for the POC)

The device keeps waking at the configured rush/default rate regardless of which
screen is showing, so the two screens simply alternate at that cadence.
A per-screen `dwell` override (e.g. show the cat for 5 minutes but arrivals
every 30 s) is an obvious follow-up — the hook is that `handleDisplay` already
computes `refresh_rate` per response — but it's out of scope here.

## Out of scope / follow-ups

- **Per-screen refresh rates** (see above).
- **Floyd–Steinberg dithering** for photos. Plain grayscale will look okay on
  the panel; dithering would look better but is a rendering nicety, not
  structural.
- **More screen types** (weather, calendar, …) — the point of the interface is
  that these become additive: one file in `internal/screen` plus a config type.
- **Per-device rotations** — there's one device; the cursor stays global.

## File-by-file summary

| File | Change |
|---|---|
| `internal/screen/screen.go` | new — `Screen` interface, `Rotation` |
| `internal/screen/muni.go` | new — wraps store + `render.Screen` |
| `internal/screen/cat.go` | new — cataas fetch, fit-to-panel, cache |
| `internal/config/config.go` | `screens` section, defaults, validation |
| `internal/server/server.go` | hold `Rotation`; render via `Screen`; filename prefix; `/latest?screen=` |
| `main.go` | build screens from config, wire `Rotation` |
| `config.example.yaml` | document the `screens` section |
| `internal/config/config_test.go` | defaults + validation tests |
| `internal/screen/*_test.go` | rotation order/wraparound; cat screen against `httptest.Server` with a fixture image, including failure → cached-image path |

## Testing & verification

- `go build ./... && go vet ./... && go test ./...` stay green; `gofmt -l .`
  clean (repo convention).
- Manual smoke test: run `serve` with both screens configured, hit
  `/api/display` repeatedly and confirm the `image_url` alternates
  `muni-*` / `cat-*`; hit `/latest?screen=cat` for a visual check.
  (Note: the cloud sandbox may not be able to reach cataas.com, same as it
  couldn't reach api.511.org — the fixture-based tests cover the cat path, and
  the live fetch gets verified on real hardware.)
