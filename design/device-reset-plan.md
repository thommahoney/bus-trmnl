# Plan: BYOS-triggered device reset to clear corrupted NVS (fix boot loops)

> Status: proposed — not yet implemented. Designed 2026-07-01.

## Context / why

A boot-looping TRMNL X (observed after a full-battery-drain event) crashes from
**corrupted NVS/preferences state**, resetting every cycle *before* it can
deep-sleep (`Update-Source: powercycle`, `Wake-Time: 0` on every poll). Diagnosis
notes:

- Server + nginx are healthy: every image is delivered in full (`200`, exact
  `bytes_sent`, no `499`). Not a URL/truncation/size issue.
- Setting `maximum_compatibility: false` did **not** help (~95% powercycle) — so
  the forced full refresh isn't the trigger.
- Re-flashing 1.8.8 did **not** fix it, because flashing the app partition does
  **not** erase NVS.
- The reset is a genuine crash, not a firmware decision: with our valid responses
  (`status:0`, `update_firmware:false`) the firmware logic always ends in
  `goToSleep()` (deep sleep), never `ESP.restart()`.
- The "point the device at hosted trmnl.com, then back to BYOS" trick fixed it:
  the device hit the hosted server with a stale `api_key`, the hosted server
  returned a reset, and the firmware's `resetDeviceCredentials()` ran
  `preferences.clear()` — wiping the corrupted NVS.

Our BYOS has no way to send that reset today. This plan adds it so a stuck device
self-heals without the hosted detour.

## Firmware contract (evidence, `usetrmnl/trmnl-firmware` @ 1.8.8)

- `handleApiDisplayResponse` (`src/bl.cpp`): status `0` + `reset_firmware:true`
  → `result = HTTPS_RESET` (bl.cpp:2264). status `500` → `HTTPS_RESET`
  (bl.cpp:2282). status `202` → `HTTPS_NO_REGISTER`.
- `bl_init`: `if (request_result == HTTPS_RESET) resetDeviceCredentials();`
  (bl.cpp:1408).
- `resetDeviceCredentials()` (bl.cpp:3055-3068):
  `WifiCaptivePortal.resetSettings(); preferences.clear(); ESP.restart();`
  → **full NVS wipe + Wi-Fi wipe + reboot.**
- After the reset the device has no `api_key` → captive-portal onboarding →
  `/api/setup`. `handleApiSetupResponse` (bl.cpp:2802) on status 200 saves
  `api_key` + `friendly_id`. **Our existing `/api/setup` already returns those
  fields correctly**, and NVS is already clean by the time setup runs (the reset
  cleared it). So there's nothing to fix in the setup response itself — the only
  missing lever is the ability to return **`reset_firmware: true`** from
  `/api/display`.

## Approach — a one-shot, admin-triggered reset

1. **State:** a small persisted "reset pending" flag, JSON in `image_dir`
   (writable by the non-root container; `pruneOld` only reaps `*.png`), mirroring
   `internal/pin`. Keyed by device `Id` (MAC) — or a single global flag, since
   there's one device.
2. **Endpoint:** `POST /api/device/reset` (open, like the recipe endpoints) →
   arms the flag. (Optionally accept an `Id`.)
3. **`handleDisplay`:** if the flag is armed for the polling device's `Id`
   header, return `{ status: 0, reset_firmware: true, image_url: "",
   filename: "", refresh_rate: <n> }` and **clear the flag atomically in the
   same request** — one-shot. Empty `image_url` makes the firmware skip the
   pre-reset draw (the `image_url.length() > 0` guard at bl.cpp:2175) and go
   straight to `HTTPS_RESET`. (Sending a normal image also works; it just draws
   once, then resets.)
4. Firmware wipes NVS + Wi-Fi + reboots → captive portal → user re-onboards
   Wi-Fi + server URL (`trmnl.thom.is`) → `/api/setup` → clean `timer`-wake
   operation.

**Why one-shot is critical:** the flag MUST be cleared the instant we serve
`reset_firmware:true`. Otherwise, after the user re-onboards, the next poll would
reset again → an intentional-reset loop.

## Caveats (inherent to the firmware — document them)

- `resetDeviceCredentials()` also **wipes Wi-Fi** (coupled in firmware). There is
  no server-triggered "clear app NVS but keep Wi-Fi." A reset means re-entering
  Wi-Fi + the BYOS server URL via the captive portal afterward.
- It also clears `api_key`/`friendly_id`, so the device re-runs `/api/setup`
  (already handled).

## Files

- `internal/devreset/` (new, ~`pin`-sized) — persisted one-shot flag(s):
  `Arm(id)`, `TakeIfArmed(id) bool`, load/save JSON in `image_dir`.
- `internal/server/server.go` — `POST /api/device/reset`; in `handleDisplay`
  read the `Id` header (existing `header()` helper) and, when armed, emit
  `reset_firmware:true` + empty image and clear the flag; wire store into
  `New()`.
- `main.go` — construct + wire the store.
- Docs — README "Recovering a boot-looping device" (with the Wi-Fi re-onboard
  caveat), CLAUDE.md note on the reset path + firmware mechanism.

## Optional enhancement (default OFF; mention only)

**Auto-heal:** detect a boot loop server-side — e.g. ≥N consecutive
`Update-Source: powercycle` + `Wake-Time: 0` polls from the same `Id` within a
short window — and auto-arm exactly one reset. Powerful but risky (could reset a
device power-cycling for a benign reason), so keep it opt-in; manual trigger is
the recommended default.

## Verification

- **Unit:** the reset store — arm, take-once (second take returns false),
  persistence across reload.
- **Endpoint logic via curl (no device needed):**
  - `curl -X POST localhost:2300/api/device/reset`
  - `curl -H "Id: 3C:0F:02:C4:6A:24" .../api/display` → response has
    `reset_firmware: true`; a second identical poll → normal
    (`reset_firmware: false`), proving the one-shot clear.
- **On the physical device (careful — this factory-resets it):** arm the reset,
  let it poll, confirm it wipes + reboots into the captive portal; re-onboard;
  confirm it returns to `timer` wakes with no loop.
- Build/vet/test via the docker golang container; deploy with
  `docker compose up -d --build`.

## Not doing now

- The full registration/plugin handshake (status `202` / `empty_state` /
  `PREFERENCES_DEVICE_REGISTERED_KEY`) — not needed to clear NVS; a later parity
  improvement if we want BYOS to mirror the hosted setup screens.
