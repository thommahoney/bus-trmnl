# Plan: weekly auto-update of TRMNL X firmware via BYOS

> **Status: proposed — not yet implemented.** Researched and designed 2026-06,
> deferred. This doc is the implementation plan to pick up later.

## Context

The TRMNL X firmware is server-driven OTA: the device flashes a binary **only**
when `/api/display` returns `update_firmware: true` + a `firmware_url`. Today
`bus-trmnl` hardcodes `update_firmware: false` (`internal/server/server.go`
`handleDisplay`, the response map ~lines 184–194), so the device never updates
(observed FW 1.8.5; latest at planning time was 1.8.8). The device already sends
`Fw-Version`, `Model` (`x`), `Id`, `Usb-Connected`, and `Percent-Charged` headers
on every poll — all currently unused except battery (`parseBattery`).

Goal: once per week, offer the **latest** firmware to an out-of-date X so it
self-updates, hands-off. Per the user's choices: **auto-resolve the latest
version** (don't pin a version), and hand the device an **external `firmware_url`**
(don't self-host the binary).

### Safety reality this plan must respect

- **The device flashes whatever URL it's given** — version-gating is entirely the
  server's job. A wrong/incompatible binary can brick the X.
- **Known bug — usetrmnl/trmnl-firmware#347** ("TRMNL X firmware update loop on
  Terminus BYOS"): an X on BYOS can enter a tight *update loop*, retrying a failed
  flash repeatedly. Mitigation: **never re-offer on every poll** — offer at most
  once per week per device and stop once the device reports the new version.
- The latest GitHub *release* ships **no prebuilt `.bin` assets**; terminus (the
  reference BYOS) self-hosts a downloaded binary via a 6-hourly poller
  (`FIRMWARE_ROOT`, default `public/assets/firmware`). Since the user wants an
  external URL, the firmware URL is built from an **admin-provided URL template**
  with the auto-resolved version substituted — the server discovers *which*
  version, the template says *where* that version's binary lives.

## Approach

Demand-driven, mirroring the project's existing patterns (no background
goroutine — like `board.Store.EnsureFresh` and `internal/pin` persistence).

### 1. New `internal/firmware` package

- `Resolver` (models `internal/board.Store`): resolves the latest version via an
  outbound GET, **throttled to `check_interval`** and **single-flighted**, caching
  the result. Default source: the GitHub releases API
  (`https://api.github.com/repos/usetrmnl/trmnl-firmware/releases/latest` →
  `tag_name`, strip leading `v`). On error, keep the last known version
  (fail safe = no offer).
- `Store` (models `internal/pin.Store`): persists, as JSON in `image_dir` (the
  only dir the non-root container can write; `pruneOld` only reaps `*.png`):
  the last resolved version, and **per-device offer state** keyed by `Id`
  (last-offered version + timestamp + consecutive-fail count). Atomic
  write-to-`.tmp`-then-rename, load-on-start.
- `Decision(now, model, fwVersion, deviceID, batt) (offer bool, url, version)`:
  returns an offer only when **all** hold:
  - `enabled` is true;
  - `model` matches config (`x`, case-insensitive);
  - `fwVersion` strictly **older** than the resolved latest (semver compare
    helper: split on `.`, numeric compare — handles `1.8.5` vs `1.8.8`);
  - it's been ≥ `check_interval` since this device was last offered (weekly
    throttle → defeats the #347 loop);
  - device is **safe to flash**: `Usb-Connected` true **or**
    `Percent-Charged ≥ min_battery_percent` (prevents a mid-flash brick);
  - consecutive-fail count for this target `< max_attempts` (give up + log a
    warning if a device keeps reporting the old version after N weekly offers).
  `url` = `url_template` with `{version}` substituted.
- Records each offer (updates timestamp/version; bumps fail count when a device
  re-appears still on the old version, resets it once it reports the new one).
- Unit tests for the semver compare and the full decision matrix.

### 2. `internal/config` — `FirmwareConfig` (follow `RecipesConfig`)

```yaml
firmware:
  enabled: false              # default OFF (safety); explicit opt-in
  model: "x"                  # only offer to this device model
  check_interval: "168h"      # re-resolve latest + re-offer cadence (weekly)
  version_url: "https://api.github.com/repos/usetrmnl/trmnl-firmware/releases/latest"
  url_template: ""            # external firmware_url; {version} substituted. REQUIRED when enabled
  min_battery_percent: 50     # skip flash below this unless USB-connected
  state_file: ""              # default: <image_dir>/firmware.json
```

`applyDefaults` sets the defaults (Duration `check_interval`, `model: "x"`,
`min_battery_percent: 50`, `version_url`, `state_file` inside `image_dir`).
`validate`: when `enabled`, require `url_template` (and that it contains
`{version}`); else the feature stays inert.

### 3. `internal/server/server.go` — wire into `handleDisplay`

- Extend `parseBattery`/header reads to also capture `Usb-Connected` and read
  `Fw-Version`, `Model`, `Id` via the existing `header(r, "Fw-Version",
  "FW_VERSION")` multi-casing helper.
- After the render succeeds, call the firmware `Decision`. Build the response map
  conditionally: when an offer is returned, set `update_firmware: true`,
  `firmware_url: <url>`, `firmware_version: <version>`, and record the offer;
  otherwise keep the current `update_firmware: false`. Keep `reset_firmware:
  false` and `maximum_compatibility: true` as-is.
- Add the firmware `Store`/`Resolver` to `Server` and `New(...)` (alongside the
  pin store), constructed in `main.go`.
- Log every offer (device id, from→to version) — high-signal for an OTA event.

### 4. Supporting

- `main.go`: build the firmware resolver+store, pass to `server.New`.
- `config.example.yaml`: documented, commented-out `firmware:` block (default off,
  with the bricking/loop warning inline).
- Docs: promote this file to `design/firmware-updates.md` (mechanism, gating,
  #347 mitigation, why external-URL-template); README "Firmware updates"
  subsection; CLAUDE.md code-map + `/api/display` note.

### Reused patterns (don't reinvent)

- `internal/board.Store.EnsureFresh` — demand-driven + throttled + single-flight
  resolution.
- `internal/pin` — atomic JSON persistence in `image_dir`, load-on-start.
- `internal/server` `header()` + `parseBattery` — header reading.
- `internal/config` `Duration` + `applyDefaults`/`validate` — config block.

## Open implementation detail (verify while building, not a blocker)

The exact canonical X `.bin` URL pattern for `url_template`. Confirm by reading
terminus's firmware poller and firmware API with an authenticated tool
(`gh api repos/usetrmnl/terminus/contents/bin/pollers/firmware`, and the firmware
index it pulls from) — WebFetch hit 404s on these. Until confirmed,
`url_template` stays admin-supplied/required so nothing ships a guessed URL.

## Verification

- **Unit tests**: semver compare; decision matrix — model mismatch → no offer;
  version equal/newer → no offer; version older + within throttle window → no
  offer; older + USB/charged + outside window → offer; low battery & no USB → no
  offer; fail-count ≥ max → no offer.
- **Manual, via curl only (never the real device — a wrong/looping flash can
  brick it):** set `firmware.enabled: true`, a real `url_template`, then:
  - `GET /api/display` with `-H "Fw-Version: 1.0.0" -H "Model: x" -H
    "Usb-Connected: true"` → response includes `update_firmware:true` +
    `firmware_url`/`firmware_version`.
  - Immediately repeat → **no** offer (weekly throttle).
  - `-H "Fw-Version: 9.9.9"` → no offer (not behind).
  - `-H "Model: og"` → no offer (model gate).
  - low `Percent-Charged`, no USB → no offer (battery gate).
  - Confirm `firmware.json` is written under `image_dir` and reloads on restart.
- **Before enabling against the live X**: confirm `url_template` resolves to the
  correct X binary for the resolved version (`gh`/terminus), and that the device
  is on USB. Roll out with `enabled:false` first → verify the decision logs say
  "would offer …" (add a dry-run log line) → then enable.

## Out of scope / deferred

- Self-hosting/mirroring the binary (terminus-style download poller) — possible
  later for reliability; user chose external URL.
- `reset_firmware`, `temperature_profile`, other BYOS response knobs.

## Key references

- BYOS contract: `HANDOFF.md` §3.4; `CLAUDE.md` firmware-fields section.
- Firmware source / releases: github.com/usetrmnl/trmnl-firmware (latest 1.8.8 at
  planning time; releases ship no `.bin` assets).
- Reference BYOS firmware poller: github.com/usetrmnl/terminus
  (`bin/pollers/firmware`, `FIRMWARE_ROOT`).
- Update-loop bug: usetrmnl/trmnl-firmware#347.
