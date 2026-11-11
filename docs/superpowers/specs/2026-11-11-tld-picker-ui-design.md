# TLD Picker in the macOS App — Design (Plan 2)

**Date:** 2026-11-11 · **Branch:** feature/local-domain-selection · **Approach chosen:** A

## Intent

Plan 1 gave Flock a switchable local TLD (`.test` → `loc`/`local`/custom) through validation, config migration, CA/DNS re-provisioning and `flock tld`. Changing the local domain from the app's Settings, not the terminal, is the remaining user-facing half — on the user's own machine the app is the product.

## Goals

- Settings → General gets a **Local domain** control: a menu with `test` / `loc` / `local` plus **Custom…** that turns into a free text field.
- **Live validation** while typing a custom value: a debounced check against the backend (no IANA list duplication in Swift), inline error (public TLD / not a label / reserved) and warning (`.local` mDNS) before Apply.
- **Apply runs the bundled CLI `flock tld <tld>`** in a progress sheet that streams output — the existing Install-sheet pattern, including the macOS password dialog and the "declined" outcome — so aliases, `.env`, DNS and CA+trust all move in one action.
- After success the Settings/status view updates by itself (the TLD-aware views from `main` already do this).

## Non-goals

- Linux (Rust) and Windows (.NET) settings UIs — follow-up plans; their apps use the same API/CLI surface.
- A TLD control on the Sites pane (per-site names already exist there).
- Any daemon-side privileged work (daemon stays unprivileged by design — approach C rejected).

## Anti-goals

- A half-applied switch: "config moved to `.loc`, sites don't resolve, no guidance". The CLI flow guarantees all-or-guided: a declined password leaves the config switched with the CLI's explicit `run flock install to finish` text, shown in the sheet.
- Validation rules duplicated in Swift.

## Constraints

- SwiftPM app (`macos/`), SwiftUI; no new dependencies; follow the repo's comment/copy style.
- The check endpoint rides the existing token-protected HTTP API the apps already speak (`FlockClient`).
- The CLI binary ships inside the app bundle (`Contents/MacOS/flock`); `Installation.run(binary, args, onLine:)` is the proven runner (streaming + decline mapping).
- Go backend: stdlib only, tests + lint green; the endpoint is dry (never mutates config).

## Approaches considered

- **A. Picker + custom field; Apply → bundled CLI `flock tld` with a streaming sheet.** Single source of truth (the CLI), full cycle one button, mirrors Install. **Chosen** (user picked it, combined menu+inline field form).
- **B. API-only change + "finish in terminal" banner.** No password from the UI, two-step UX, leaves a knowingly half-working state. Rejected.
- **C. Daemon re-provisions DNS/CA.** Breaks the unprivileged-daemon architecture. Rejected.

## Decided shape (how)

- Backend (Go): `POST /v1/tld/check` body `{"tld": "…"}" → `{"ok": bool, "normalized": "loc", "warning": "…", "error": "…"}`; runs `NormalizeTLD`+`ValidateTLD`; no side effects; ~20 lines + tests, mirrors the existing API test patterns.
- UI (Swift): row in GeneralPane near the existing `*.<tld>` line; menu style with three presets + `Custom…`; debounce ~300 ms against the check endpoint; Apply disabled while a check is pending/failed; sheet runs the CLI like Install does (line streaming, decline = "cancelled" state with the CLI's guidance text).
- `.local`: the warning (from the check endpoint / status warnings) shows inline in the picker area — the switch stays allowed.

## Left to the builder

Exact SwiftUI composition (Menu + field vs Picker + "Custom" toggle), placement inside GeneralPane, sheet copy, and how the declined state is worded (CLI text is passed through verbatim).
