# Recipe focus mode: upload a Paprika recipe to the panel

## Goal

Let the e-ink panel double as a kitchen recipe card. Anyone uploads a **Paprika**
recipe export and it takes over the screen — full-screen, static, legible with
floury hands — for a fixed hold (default **3 hours**), then automatically returns
to the normal MUNI/cat rotation.

This inverts the rest of the project: the MUNI designs are *glanceable* and even
relocate every render to fight burn-in, whereas a recipe is something you *dwell*
on while cooking. So the recipe card is deliberately **static** and must not
cycle away mid-cook. (The forced full refresh + grayscale already handle
burn-in, so a still card is safe.)

## How it falls out of the existing model

The device is a pull-only thin client: wake → `GET /api/display` → download the
`image_url` → sleep `refresh_rate` → repeat. "Focus mode" needs no device-side
work — when a recipe is pinned, `/api/display` simply renders the recipe screen
instead of advancing the rotation, and clears the pin once it expires. The
device keeps polling on its normal cadence (60s, or 30s in the rush window), so
a freshly uploaded recipe appears within one `refresh_rate` interval.

Pinning is **independent of the rotation**: there is no `type: recipe` screen in
the `screens` list. Uploading is what triggers the takeover.

## Architecture (file-by-file)

- `internal/recipe` — the normalized recipe model (`Recipe{Title, Servings,
  Time, Source, Ingredients, Steps}`), a leaf package. `SplitLines` turns
  Paprika's newline-joined ingredient/direction strings into trimmed lines.
- `internal/paprika` — `Parse([]byte) ([]recipe.Recipe, error)`. Paprika exports
  come in three shapes, all detected by content (not extension):
  - `.paprikarecipes` — a **ZIP** whose entries are each **gzipped JSON**.
  - `.paprikarecipe` — usually a single **gzipped JSON** recipe…
  - …but some single exports are **plain JSON**.
  Maps the JSON (`name`, `ingredients`, `directions`, `servings`,
  `total/cook/prep_time`, `source/source_url`) into `recipe.Recipe`.
- `internal/pin` — `Store`: a mutex-guarded "pinned recipe + expiry", persisted
  to `pin.json`. `Active(now)` returns the pin and clears it once expired (this
  is what makes the rotation resume on its own). `Set` resets the clock; a new
  upload replaces the current pin.
- `internal/render/recipe.go` — `Recipe(RecipeIn) ([]byte, error)`: a pure,
  testable two-column card (title / metadata strip / bulleted ingredients left /
  numbered steps right), black-on-white, auto-fit. No `recipe` import — kept a
  render leaf, like the MUNI designs.
- `internal/screen/recipe.go` — the `recipe` screen; reads the pin store, maps to
  `render.RecipeIn`, shows a placeholder when nothing is pinned (so
  `/latest?screen=recipe` always previews).
- `internal/server` — `POST /api/recipe` (+ `/unpin`); `/api/display` consults
  the pin before the rotation; `/latest?screen=recipe`. All ingestion funnels
  through one `ingest(data, now)` seam so future channels (Telegram, email, …)
  are thin adapters.
- `internal/config` — `recipes.pin_ttl` (default 3h) and `recipes.state_file`
  (default `pin.json` **inside `image_dir`**, see gotcha below).
- `main.go` — builds the pin store + recipe screen and wires them into the
  server.

## HTTP surface

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/api/recipe` | `POST` | Upload a Paprika file (raw body or multipart form file); pins the first recipe for `pin_ttl`. |
| `/api/recipe/unpin` | `POST` | Clear the pin; rotation resumes on the next wake. |
| `/latest?screen=recipe` | `GET` | Preview the current/placeholder card as PNG. |

Both mutating endpoints are **intentionally open (no token)** — a deliberate
choice for a trusted personal deployment ("anyone with the link can upload").
Adding a `recipes.upload_token` later is a one-liner. Trade-off accepted: a
public, unauthenticated endpoint that hijacks the screen for 3h.

### Ingestion channels

v1 ships the **iOS Shortcut** path (Share a recipe out of the Paprika app →
`POST /api/recipe`). Because every channel collapses to the same `ingest` seam,
these are documented-but-deferred: a web upload page, **Telegram** (the
recommended free "text it in" channel), email (inbound-parse webhook), Twilio
MMS, and a watched dropbox dir. See README for the Shortcut setup.

## Rendering real recipes (learnings)

Synthetic test data hid several things that real Paprika exports surfaced; the
renderer normalizes them in `render/recipe.go`:

- **Vulgar fractions vanish.** The embedded fonts lack `½`, `⅓`, etc., so
  `3 ½ cups` rendered as `3  cups`. `normalizeText` substitutes ASCII
  (`½`→`1/2`, `⅓`→`1/3`, …) plus smart quotes/ellipsis.
- **Section headers inside the lists.** Paprika folds headers into the
  ingredient/direction strings, e.g. `**For the polenta**` or a bare `Make
  Dough`. `isHeadingLine` detects them — strong signals (`**…**` wrap, trailing
  `:`) everywhere, plus a short-title heuristic **only for steps** (ingredient
  lines are legitimately short; a digit prefix like `2 eggs` is never a header).
  Headers render as bold sub-heads with no bullet/number, and **steps renumber**
  ignoring them.
- **Both columns can overflow.** Auto-fit shrinks the body font to the largest
  size that fits; if even the smallest legible size overflows, the offending
  column (ingredients *or* steps) is truncated with a `+N more on your phone`
  note. (The uploader's phone holds the full recipe, so the panel stays an
  ambient card.)

## Gotchas

- **Pin file lives in `image_dir`.** The deploy runs as the distroless
  `nonroot` user (uid 65532), which can write `image_dir` (the server already
  writes PNGs there) but **not** its parent `/data`. So `pin.json` defaults
  inside `image_dir`, and `server.pruneOld` only reaps `*.png` so it never
  deletes the pin file.
- **nginx body cap.** nginx has no `client_max_body_size`, so it defaults to
  **1 MB** and returns 413 before the app sees the request. Text recipes
  (≈60–150 KB) are fine, but a recipe with an embedded `photo_data` JPEG can
  exceed 1 MB. Add `client_max_body_size 16m;` to the nginx server blocks to
  support photo recipes (the app already caps uploads at 16 MB).
- **A bad upload preserves the current pin.** Parse failures return 400 without
  touching the existing pin, so garbage can't knock a good recipe off-screen.
- **Refresh cadence is unchanged while pinned.** The device still wakes every
  `refresh_rate` (60s / 30s rush), so a static recipe keeps the device polling —
  that is also why a new upload appears within ~a minute.

## Operational notes from first real use

- The device reports rich battery telemetry in request headers on every
  `/api/display` poll (`Percent-Charged`, `Battery-Voltage`, `Battery-Charging`,
  `Usb-Connected`, `Rssi`, `Fw-Version`, `Model`, …). A flat battery freezes the
  panel on its last image (e-ink holds with no power) — which looks identical to
  "stuck", so check `Percent-Charged`/USB before assuming a server problem.
- **Battery readout on the clock.** The time-showing screens (the four MUNI
  designs) now print the charge next to the clock, e.g. `9:53 PM   73%`. The
  percent comes from the `Percent-Charged` request header, plumbed from
  `server.handleDisplay` → a `render.Battery` in the render context
  (`render.ContextWithBattery`/`BatteryFromContext`) → `render.In`/`Metadata`,
  so the fixed `Screen.Render` interface didn't have to change. Cat/recipe show
  no clock, so they show no battery. `/latest?screen=…&battery=NN` previews it
  without a device. Absent telemetry (preview, setup) renders the clock alone.

## Deferred / possible next steps

- nginx `client_max_body_size` bump (for photo recipes).
- A web upload page and an `/api/recipe/unpin` shortcut (the endpoint exists).
- Additional ingestion channels (Telegram first).
- Optional `recipes.upload_token`.
- Rendering the recipe photo, and honoring `:`-style ingredient sub-headers.
- Surfacing device battery telemetry.
