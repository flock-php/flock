# Per-Site Local Domain (Inherit / Custom) — Design (Plan 3)

**Date:** 2026-11-11 · **Branch:** feature/local-domain-selection (builds on Plans 1–2)

## Intent

Today every site's domain is `<folder>.<global TLD>` and a custom suffix is only reachable by making the TLD global. The user wants domain choice **per site**: inherit the global TLD (default, current behavior) or pick their own suffix — `<folder>.<suffix>` with the same picker/validator as the global setting — and the same for extra domains: a label input plus a suffix control. A site on a custom suffix must genuinely open in browsers: DNS and valid HTTPS for that suffix, provisioned automatically.

## Goals

1. **Model:** `Site.TLD string` (json `"tld"`, `""` = inherit). Effective site domain = `<name>.<Site.TLD || global TLD>`. Aliases keep storing the full host (`api.shop.loc`); their suffix is what the UI decomposes. Worktrees derive `<slug>.<effective site domain>` automatically.
2. **API:** site create/patch accept `tld` (validated via Plan 1's `ValidateTLD`; the `/v1/tld/check` dry-run powers live UI validation). The alias endpoint stays full-host.
3. **Active suffix set** = global TLD ∪ every site override ∪ suffixes of every alias host. Flock provisions and monitors **all** of them:
   - **macOS:** `/etc/resolver/<suffix>` per active suffix (privileged-setup gains multi-suffix sync: write missing, remove stale — files carrying only Flock's marker).
   - **Linux:** the resolved drop-in lists every active suffix (`Domains=a b c`).
   - **Windows:** an NRPT rule per active suffix; the hosts-helper block is regenerated wholesale from config (all site hosts), no fixed-TLD filtering.
   - **CA:** a root per active suffix (same name-constrained shape as Plan 1) under `certs/roots/<suffix>/`; leaf issuance and `SiteCert` pick the root by host suffix; PHP/Node CA bundle concatenates all roots; OS trust installs/trusts each root (keychain/certutil/system store).
4. **CLI:** `flock site tld <suffix>|--inherit [site]` (default: current site) sets `Site.TLD`; `flock domain add <label>.<suffix>` / `flock domain add <label> --suffix <suffix>`; applying a suffix change runs the provisioning sync (the Plan 1 privileged machinery, per suffix) with the same decline semantics (config switched, guidance to `flock install`).
5. **Daemon health:** wildcard status per active suffix (existing check generalized: sample `<rand>.<suffix>` → loopback); services/warnings name the broken suffix; `flock status` lists all.
6. **macOS app:** Sites pane — per-site Domain control (Inherit / Custom + suffix picker like the global one, live `/v1/tld/check`), Add Domain sheet gains the suffix control (label input + suffix), Apply runs the bundled CLI provisioning sync; global picker (Plan 2) stays.

## Non-goals

- Windows/Linux app UIs (follow-up plans, same API).
- Per-proxy suffixes — proxies keep inheriting the global TLD.
- Multi-label site names (`my.shop.loc` as a site's own domain — site names stay one label; multi-label belongs to aliases).
- IDN/punycode suffixes (ASCII label rule as in Plan 1).

## Anti-goals

- A site on a custom suffix that routes correctly in Caddy but doesn't resolve, or resolves with an untrusted cert.
- Stale system files: a suffix dropped from the active set must have its resolver file/NRPT rule/root trust removed.
- A site override silently changing other sites' domains (global TLD edits keep Plans 1's semantics).

## Constraints

- Daemon stays unprivileged: provisioning runs via the CLI (sync command), the daemon only reports per-suffix health.
- Root per suffix stays name-constrained (one suffix + localhost); no root may sign for a public TLD (suffix validation as in Plan 1).
- Plan 1's single-root layout migrates cleanly: the existing `certs/ca.crt` root for the global TLD is kept in place; additional roots live under `certs/roots/<suffix>/`.
- Go stdlib only; SwiftPM `--disable-sandbox` rule; Swift tests + Go race suite + docslint green per commit; Conventional Commits.

## Approaches considered

- **A. Suffix override per site (`Site.TLD`), full system provisioning per active suffix, one CA root per suffix.** The user's chosen shape (inherit = folder+global; custom = folder+suffix; aliases = label+suffix). Chosen.
- **B. Free full hostname per site** (rejected by the user — names stay folder-based; aliases cover free-form names).
- **C. Custom suffixes that only resolve when set globally** (rejected: a half-working feature — the confirmed UX is auto-provisioning).

## Decided shape (how)

- `config.Site.TLD` + `func (s Site) DomainSuffix(c *Config) string`; `Domain(site)` (config.go:287) uses the override. Sites discovered from parked dirs inherit.
- API: `POST /v1/sites` and `PATCH /v1/sites/{name}` take `"tld"`; validation error → 400 (same `invalid()` shape as settings); `ConfigView`/`SiteStatus` carry the effective `tld` for the UI.
- `dns` package: `Sync(suffixes []string, port)` on the privileged helper (darwin resolver files, linux drop-in content, windows NRPT) replacing the single-TLD calls in `privileged-setup`; CA: `certs.Store(rootDir, tlds)` → map suffix→CA; `web/certsource.go` picks by suffix; `ca.WriteBundle` concatenates.
- CLI `flock site tld`: mutate + `flock tld`-style sync (`provisionSuffixes(cfg)`: retire stale, add new, trust new roots, daemon reload); decline keeps Plan 1's guidance shape.
- Alias UI: label field + suffix menu (`Inherit` + presets + Custom…) composed into the full host client-side; server keeps full-host semantics.

## Left to the builder

The plan splits into: **3a backend** (model, API, CA store, dns sync, CLI, daemon health, docs) and **3b macOS UI** (Sites pane controls, Add Domain sheet, provisioning apply). Within each: exact function signatures, test tables, migration test for the existing root layout, and the smoke matrix.

## Plan-3 → Plan split note

writing-plans will produce two plans (3a, 3b) because the UI depends on 3a's shipped API and each must land as working, testable software on its own.
