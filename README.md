# bus-trmnl

A self-hosted [TRMNL](https://trmnl.com) **BYOS** (Bring Your Own Server) that
shows live San Francisco **MUNI** arrivals on a TRMNL **X** e-ink device, using
the [511.org](https://511.org/open-data/transit) real-time API.

The device cycles through a configurable list of **screens**, one per wake. Out
of the box the MUNI arrivals screen shows two boards:

- **43 / 44 → Forest Hill Station**, outbound from 9th Ave & Kirkham
- **N Judah → Caltrain**, inbound from 9th Ave & Judah St

A second screen type renders a random **cat photo** (dithered for e-ink); add,
remove, or reorder screens via the `screens:` config. The device wakes **every
30 seconds during the weekday morning rush (7:45–8:15 AM)** and **every 60
seconds** the rest of the time.

## Why BYOS?

TRMNL's hosted cloud caps updates at roughly one every 5 minutes (2 minutes for
TRMNL+), so the sub-minute, time-of-day refresh cadence above isn't possible
there. BYOS points the device at this server instead, which controls the wake
interval directly via the `refresh_rate` it returns on every poll. This is
intended for a **USB-powered** device — 30-second refreshes drain a battery in
days.

See [BYOS docs](https://docs.trmnl.com/go/diy/byos) for how to point your device
at a custom server.

## How it works

The TRMNL device is a stateless thin client: it wakes, calls `GET /api/display`,
downloads the `image_url` it's handed, sleeps for `refresh_rate` seconds, and
repeats. Everything else is server-side:

1. **Rotation** — each successive `/api/display` serves the next screen in the
   configured list, so the device cycles through them with no device-side state.
2. **Rendering** — for the MUNI screen the server recomputes countdowns from
   cached 511 predictions and renders a grayscale PNG sized to the device, with
   the time-of-day `refresh_rate`.
3. **Demand-driven 511** — there's no background poller. The MUNI screen
   refreshes the 511 cache only when it's about to render *and* the cache is
   older than `five11.poll_interval` (single-flighted). Other screens (e.g. the
   cat) make zero 511 calls.

So the **display** can refresh every 30s while staying within 511's **60
requests/hour** token limit (2 stops, fetched at most every 2 min = 60/hour);
`poll_interval` is the floor between fetches. The server logs a warning at
startup if that floor would exceed the limit.

### Device API endpoints

| Endpoint          | Purpose                                                      |
| ----------------- | ----------------------------------------------------------- |
| `GET /api/setup`  | First-boot pairing; returns `api_key` / `friendly_id`.      |
| `GET /api/display`| Returns `{ image_url, filename, refresh_rate, ... }`.       |
| `POST /api/log`   | Accepts device telemetry; returns 204.                      |
| `GET /latest`     | Preview a screen as PNG (`?screen=<name>`); no rotation advance. |
| `GET /images/...` | Serves rendered PNGs.                                        |
| `GET /health`     | Health check.                                               |

## Setup

### 1. Get a 511 API token

Request one at <https://511.org/open-data/token>.

### 2. Find your stop codes

Copy the example config and fill in your token, then use the `discover` command
to look up the 511 stop codes for your stops:

```sh
cp config.example.yaml config.yaml          # edit api_key / base_url
export FIVE11_API_KEY=your-token

go run . discover -config config.yaml -query "9th Ave & Kirkham"
go run . discover -config config.yaml -query "Judah"
```

Put the resulting codes into the `stop_code` fields in `config.yaml`. Tune
`lines`, `destination_contains`, and `direction` per board until you see the
arrivals you expect. (For the N inbound, the destination may read `Caltrain` or
`Embarcadero` depending on the run — adjust `destination_contains` accordingly.)

### 3. Run it

Locally:

```sh
go run . serve -config config.yaml
```

With Docker Compose (recommended for an always-on VPS or home server):

```sh
mkdir -p data && cp config.example.yaml data/config.yaml   # edit it
echo "FIVE11_API_KEY=your-token" > .env
# optionally: echo "DEVICE_ACCESS_TOKEN=some-secret" >> .env
docker compose up -d --build
```

`data/` holds `config.yaml` and the rendered images; it's mounted into the
container at `/data`.

### 4. Point your TRMNL device at the server

Set the device's server URL to this server's `base_url` (its reachable LAN IP or
public hostname). On first boot it calls `/api/setup`, then polls `/api/display`
on the interval the server returns.

## Configuration

See [`config.example.yaml`](config.example.yaml) for the full annotated config.
Key fields:

- `server.base_url` — **must** be reachable from the device; used to build the
  image URL it downloads.
- `five11.poll_interval` — keep `(distinct stops) × (3600 / seconds) ≤ 60`.
- `refresh.rush_rate` / `default_rate` / `rush_windows` — the wake cadence.
- `screens[]` — the rotation, in order, one per wake. Each entry is
  `{type: muni}` or `{type: cat, url: <optional cataas URL>}`. Omit the section
  to default to a single `muni` screen; 511 settings are only required when a
  `muni` screen is present.
- `boards[]` — each board's `stop_code`, `lines`, `destination_contains`,
  `direction`, and `max`.

Secrets use `${ENV_VAR}` expansion, so `FIVE11_API_KEY` and
`DEVICE_ACCESS_TOKEN` stay out of the file.

## Development

```sh
go build ./...
go test ./...
go vet ./...
```

You can also run these (and the server) in Docker without a local Go toolchain —
see [`CLAUDE.md`](CLAUDE.md) for the container-based workflow.

## Notes & caveats

- **Image format & size cap**: the MUNI board renders as an 8-bit grayscale PNG
  at the device's reported `WIDTH`/`HEIGHT` (default 1872×1404 for the TRMNL X).
  The X firmware rejects any image over ~750 KB, so the cat screen
  Floyd–Steinberg-dithers photos to the panel's 16 gray levels (a compact 4-bpp
  PNG) to stay under the cap. See [`CLAUDE.md`](CLAUDE.md) for the firmware
  details.
- **511 quirks**: responses carry a UTF-8 BOM (stripped) and occasionally
  return string fields as single-element arrays (handled by `FlexString`).
- If you add more stops, raise `poll_interval` or
  [request a higher 511 rate limit](https://511.org/about/faq/open-data).
