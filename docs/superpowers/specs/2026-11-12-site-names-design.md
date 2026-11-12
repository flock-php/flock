# Site Names: Primary-Domain Override and Display Title — Design

**Date:** 2026-11-12 · **Branch:** feature/local-domain-selection · **Follows:** Plans 3a/3b

## Intent

A site has three names and today two of them are welded together. Split them:

1. **Identity** — `Site.Name` = the folder name, auto-discovered, the key for everything (config, CLI, API paths, worktree parent). Never edited (unchanged).
2. **Display name** — what the app list shows. Defaults to the folder name ("inherits") and the user can change it — purely cosmetic, no domain or config-key effect.
3. **Primary domain name** — the name part of `<name>.<suffix>`. Generated from the folder name; the user wants to rename just it — `cms-liga-local.test` → `liga.test` — without moving the folder and without touching the site's display name. The first-level suffix stays where it is today: the Domain row's suffix picker.

## Goals

### Host (primary domain)

1. **Model:** `Site.Host` (config.json): "" = the site name (today's behavior, byte-stable for existing configs); otherwise a **single DNS label** replacing the name in the primary host. One chokepoint already exists — `Config.Domain(s)` — everything derived (server_name, leaf cert SANs, hosts-file entries, subdomain/alias hosts, status rows) follows by construction.
2. **CLI:** `flock site host <label> [site]` / `flock site host --default [site]` — validates (HostLabelOK + no leading/trailing dot + collision check), retargets the site's `.env` (primary host old→new incl. worktree-derived hosts, same machinery as `site tld`), commits, prints ✓. No elevation (wildcard DNS covers the new name; leaf certs re-issue on demand).
3. **API/MCP:** `PATCH /v1/sites/{name} {"host":"liga"}` (3-state like tld: absent=keep, ""=default, label=set) with the collision rule shared via config; MCP `update_site` action `set_site_host`; status gains raw `site_host` (additive).
4. **App (macOS):** a pencil button next to the site's address link → a small sheet with ONE name field (prefilled with the current name part) + a hint line "the suffix is chosen in Domain below" → runs the bundled CLI (`site host …`) streaming in the shared ProvisionSheet (decline not possible — no elevation; failure lines shown as-is).
5. **Collision rule:** the proposed primary host must not land on any other site's primary/alias/proxy host — generalizes `SiteHostCollision` to read Host-aware primaries (same rule for CLI/API/MCP, one place).

### Title (display name)

6. **Model/API:** `Site.Title` (config.json): "" = the site name (inherit). Free text, trimmed, control characters stripped, length capped (plan picks the cap) — it is display-only, so no label/dot rules and no collisions. `PATCH {"title":"…", "":default}`; status carries it (additive); CLI parity: `flock site title <words…> [site]` / `flock site title --default [site]`; MCP `update_site` action `set_site_title`.
7. **App (macOS):** every user-facing place that prints the site name shows Title when set (list rows, detail header, menu-bar list — the plan enumerates surfaces); editing: the detail header gets a pencil (or the list row's context menu "Rename…") → a one-field sheet, submit through the API (no CLI, no provisioning — it changes nothing else). Identity stays visible where it matters (path line, tooltips).

## Non-goals

Renaming the identity (`Site.Name`) or the folder; multi-label primaries ("a.b" as a host — single label keeps worktree hosts and subdomain math boring); `.flockrc` storage (machine-local config.json, like TLD); Windows/Linux app controls (follow-ups); changing aliases/subdomains semantics (3b shipped).

## Anti-goals

- A renamed site whose old `.env` hosts still point at the dead host (retarget is part of the host mutation).
- Collisions that make two hosts serve different sites.
- A title that changes any URL, config key, or CLI path (display-only).
- The app reimplementing validation: the CLI owns the host mutation; the sheet is the runner (R1 discipline).

## Constraints

Go: stdlib+cobra, `-race`+lint+GOOS gates; SwiftPM 82-test baseline; no privileged commands in CI (this feature needs none); Conventional Commits; docs updated (domains.md/sites.md/cli.md); app build human-gated as usual.

## Left to the plan

Exact `.env` swap list for worktrees (reuse `SwapsForSite`-style helper with old/new primaries); title length cap and control-char policy; pencil/rename placements and the full list of surfaces showing Title; MCP wording.

## Plan split note

One plan: Go task (host model+collision+CLI/API/MCP), Go task or same (title model+CLI/API/MCP), Swift task (both pencils + surfaces), docs task, build task.
