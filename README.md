# bus-trmnl

A self-hosted [TRMNL](https://trmnl.com) **BYOS** (Bring Your Own Server) that
displays live San Francisco **MUNI** arrivals on a TRMNL e-ink device, using the
[511.org](https://511.org/open-data/transit) real-time API.

Out of the box it shows two boards:

- **43 / 44 → Forest Hill Station**, outbound from 9th Ave & Kirkham
- **N Judah → Caltrain**, inbound from 9th Ave & Judah St

The device wakes **every 30 seconds during the weekday morning rush (7:45–8:15
AM)** and **every 60 seconds** the rest of the time.

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

Two decoupled loops:

1. **Data loop** — a background poller fetches each distinct stop from 511's
   SIRI `StopMonitoring` endpoint on `five11.poll_interval`, filters by
   line/destination/direction, and caches the predictions.
2. **Device loop** — when the device calls `GET /api/display`, the server
   recomputes countdowns from the cached prediction timestamps, renders a
   grayscale PNG sized to the device, and returns it along with the
   time-of-day `refresh_rate`.

Decoupling means the **display** can refresh every 30s while we stay within
511's **60 requests/hour** token limit (2 stops × every 2 min = 60/hour). The
server logs a warning at startup if your polling schedule would exceed that.

### Device API endpoints

| Endpoint          | Purpose                                                      |
| ----------------- | ----------------------------------------------------------- |
| `GET /api/setup`  | First-boot pairing; returns `api_key` / `friendly_id`.      |
| `GET /api/display`| Returns `{ image_url, filename, refresh_rate, ... }`.       |
| `POST /api/log`   | Accepts device telemetry; returns 204.                      |
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

## Notes & caveats

- **Image format**: the server renders 8-bit grayscale PNG at the device's
  reported `WIDTH`/`HEIGHT` (default 1872×1404 for the TRMNL X). Verify the
  exact format your firmware expects during first setup and adjust
  `internal/render` if needed.
- **511 quirks**: responses carry a UTF-8 BOM (stripped) and occasionally
  return string fields as single-element arrays (handled by `FlexString`).
- If you add more stops, raise `poll_interval` or
  [request a higher 511 rate limit](https://511.org/about/faq/open-data).
