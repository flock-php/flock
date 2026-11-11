# Per-Site Domain Control in the macOS App (Plan 3b) — Design

**Date:** 2026-11-11 · **Branch:** feature/local-domain-selection · **Depends on:** Plan 3a (API/CLI shipped on this branch)

## Intent

The user picks a site's local domain **in the app**: inherit the global TLD (default) or pin a suffix (`<folder>.<suffix>`), and adds extra domains as label + suffix (inherit/pinned) — with the new-suffix provisioning running automatically in the sheet, one click to trusted HTTPS.

## Goals

1. **Site Domain control** (per-site section in Sites → Site): shows the effective domain; a menu `Inherit (.<global>)` / presets `test` / `loc` / `local` / `Custom…` suffix field, live-validated via `POST /v1/tld/check` (Plan 2 component). Apply runs the bundled CLI `flock site tld <suffix> <site>` / `--inherit` in the streaming sheet (Plan 2 pattern: lines, decline footer → `flock install`).
2. **Add Domain sheet** (replaces the one-field alert): label field (`tenant1`, `a.b`, `demo2`) + suffix menu (`Inherit` + presets + Custom…, live check). Submit → `add_domain {domain, suffix}` via the store (API). If the suffix is new to the machine, the sheet **automatically runs `flock install`** (streaming, same sheet, prompts included) so the alias resolves and is trusted right away (user-approved).
3. Site rows/status reflect the effective domain immediately after apply (store refresh; the 3a daemon is live-update).
4. **Backend polish carried from the 3a final review** (one task): global `flock tld` gains a `SiteHostCollision` preflight across inheriting sites; `POST /v1/sites` collision parity with PATCH; single exported label regex in config used by api/cli; MCP `set_site_tld` echo polish; retire the redundant `roots/<suffix>` store when a global switch lands on that suffix; add the `{"tld":123}` 400 test row.

## Non-goals

Windows/Linux app UIs (3c/3d); proxy suffixes (non-goal); per-site free-form hostnames (names stay folder-based); changing the global TLD UI (Plan 2, exists).

## Anti-goals

- An alias added in-app that doesn't resolve/serve trusted TLS without an explicit, visible repair (auto-install covers this; a declined install keeps Plan 2's footer truth).
- UI drift from the CLI semantics: the app must use `flock site tld` (not raw PATCH tld) for suffix changes — provisioning, collisions and `.env` retargeting live in the CLI.
- Claims of per-site control on platforms that lack it.

## Constraints

- Swift: SwiftUI, no new dependencies, `swift build`/`swift test --disable-sandbox` green per commit; Go polish tasks keep `-race` + `make lint` + GOOS builds green.
- CLI apply = `Installation.run(bundledFlock, ["site","tld",…], onLine:)`; guard the no-bundled-CLI case like Plan 2.
- API/MCP semantics pinned in 3a are the contract; the UI never reimplements validation (checks go to `/v1/tld/check`).
- Conventional Commits; app strings English, terse; docs updated (sites.md/domains.md drop the "apps on their way" wording where 3b lands).
- Builds of the app are gated on the human's DB-server window (no restarts while it runs).

## Approach (chosen; alternatives considered)

- **A (chosen): reuse Plan 2 machinery** — suffix picker + live check from the global picker (extract a shared `TLDCheck`-powered suffix control into FlockUI components), streaming apply sheet from the same precedent; Add Domain sheet composes label+suffix client-side and sends the structured API call; new-suffix provisioning auto-runs the proven `Installation.run(["install"])`. No new backend surface beyond the polish list.
- **B: API-only (`PATCH tld`) with "finish in terminal" banners** — rejected: half-working state, the R1 discipline.
- **C: daemon does privileged provisioning** — rejected (architecture, since Plan 1).

## Decided shape (how)

- Extract from GeneralPane's picker (Plan 3) a reusable `SuffixPicker` view + a small `SuffixModel` (debounce/check/verdict) into `FlockUI/Settings/Components` (site + global share it; GeneralPane refactored to use it).
- Sites pane: site section gains the Domain row (replacing the static `Text(site.domain)`) with the picker + Apply; busy/disabled states per Plan 2 rules; `SiteStatus.siteTld` (raw) drives the initial selection; presets come from `dns.tld` + [test, loc, local] + Custom.
- Add Domain: sheet with `TextField(label)` + `SuffixPicker(inherit default)`; submit via `store.performOnSites { addDomain(site, domain, suffix) }` (extend FlockKit `addDomain` with `suffix:` → API body); on success, when the check/verdict shows the suffix is new (CLI's guidance condition — the suffix ∉ the active set, readable from `status.dns.suffixes`), the sheet chains `Installation.run(["install"])` streaming; decline → footer → `flock install`.
- Go polish task: as Goals 4 (files: internal/cli/tld.go preflight, internal/api/configapi.go create-parity, internal/config label regex export, mcpserver echo, roots dedupe, api test row).

## Left to the builder

Exact FlockUI component names/placement inside the site section, sheet copy, and the install-chain trigger condition's precise signal (active-set read vs suffixNewNote from the CLI's response) — decided in the plan.

## Plan split note

Single plan (3b): one Go polish task + Swift UI tasks; app build/smoke at the end (human-gated by the DB window).
