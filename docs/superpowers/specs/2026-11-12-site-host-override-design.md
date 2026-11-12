# Renaming a Site's Primary Domain (Site.Host override) — Design

**Date:** 2026-11-12 · **Branch:** feature/local-domain-selection · **Follows:** Plans 3a/3b

## Intent

The primary domain of a site is generated from its folder name (`<folder>.<suffix>`). The user wants to rename just that name part — `cms-liga-local.test` → `liga.test` — without moving or renaming the folder, and without touching the site's identity (the app list still says `cms-liga-local`). The first-level suffix stays where it is today: the Domain row's suffix picker.

## Goals

1. **Model:** `Site.Host` (config.json): "" = the site name (today's behavior, byte-stable for existing configs); otherwise a **single DNS label** replacing the name in the primary host. One chokepoint already exists — `Config.Domain(s)` — everything derived (server_name, leaf cert SANs, hosts-file entries, subdomain/alias hosts, status rows) follows by construction.
2. **CLI:** `flock site host <label> [site]` / `flock site host --default [site]` — validates (HostLabelOK + no leading/trailing dot + collision check), retargets the site's `.env` (primary host old→new incl. worktree-derived hosts, same machinery as `site tld`), commits, prints ✓. No elevation (wildcard DNS covers the new name; leaf certs re-issue on demand).
3. **API/MCP:** `PATCH /v1/sites/{name} {"host":"liga"}` (3-state like tld: absent=keep, ""=default, label=set) with the collision rule shared via config; MCP `update_site` action `set_site_host`; status gains raw `site_host` (additive).
4. **App (macOS):** a pencil button next to the site's address link → a small sheet with ONE name field (prefilled with the current name part) + a hint line "the suffix is chosen in Domain below" → runs the bundled CLI (`site host …`) streaming in the shared ProvisionSheet (decline not possible — no elevation; failure lines shown as-is).
5. **Collision rule:** the proposed primary host must not land on any other site's primary/alias/proxy host — generalizes `SiteHostCollision` to read Host-aware primaries (same rule for CLI/API/MCP, one place).

## Non-goals

Renaming the SITE (folder identity stays); multi-label primaries ("a.b" as a primary — single label keeps worktree hosts and subdomain math boring); `.flockrc` storage (machine-local config.json, like TLD); Windows/Linux app controls (follow-ups); changing aliases/subdomains semantics (3b shipped).

## Anti-goals

- A renamed site whose old `.env` hosts still point at the dead name (retarget is part of the mutation).
- Collisions that make two hosts serve different sites.
- The app reimplementing validation: the CLI owns the mutation; the sheet is the runner (R1 discipline).

## Constraints

Go: stdlib+cobra, `-race`+lint+GOOS gates; SwiftPM 82-test baseline; no privileged commands in CI (this feature needs none); Conventional Commits; docs updated (domains.md/sites.md/cli.md); app build human-gated as usual.

## Left to the plan

Exact `.env` swap list for worktrees (reuse `SwapsForSite`-style helper with old/new primaries), the pencil placement (next to the address link vs inside the Domain row), MCP wording.

## Plan split note

One plan: Go task (model+collision+CLI+API/MCP), Swift task (pencil sheet), docs task, build task.
