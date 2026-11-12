# Site Names (Host Override + Display Title) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `Site.Host` renames the name part of a site's primary domain (folder identity untouched, `.env` retargeted, subdomains/worktrees follow, aliases don't move) and `Site.Title` renames what the app shows — each with CLI/API/MCP parity and a pencil-in-a-sheet UI.

**Architecture:** `Config.Domain(s)` is the single host chokepoint — it becomes `s.hostName() + "." + s.Suffix(c)` (`hostName()` = `Host` when set, else `Name`), and every derived surface (server_name, certs, hosts-file, status) follows. Hand-written input is sanitized at the one read path (`migrateDomains`), like `TLD` today. Host mutation + `.env` retargeting lives in the CLI (`flock site host`); the API/MCP carry config-only changes (the established `set_site_tld` precedent); Title is display-only everywhere (no routing, no retarget).

**Tech Stack:** Go 1.27 (stdlib+cobra), SwiftUI (SwiftPM `macos/`), Swift Testing.

**Spec:** `docs/superpowers/specs/2026-11-12-site-names-design.md`

## Global Constraints

- Go: stdlib+cobra, `go.mod` untouched; `go test -race ./...` + `make lint` + GOOS=windows/linux/darwin `go build ./...` + GOOS=windows/linux `go vet ./...` green at every Go commit. Env: `. /var/folders/z8/0xyccryn70d4jnvxpjnvnnlw0000gn/T/dsh-go-cache/goenv.sh && export GOCACHE=/tmp/flock-gocache GOMODCACHE=/tmp/flock-gomodcache`.
- Swift: no new deps, `Package.swift` untouched; `cd macos && swift build --disable-sandbox && swift test --disable-sandbox` green (82 baseline) at every Swift commit.
- NO privileged commands, NO daemon runs/restarts, NO app builds in tasks (Task 6 is the human-gated build); helper artifacts `-o /tmp`.
- Site **identity** (`Site.Name`, config key, `WorktreeEntry.Site`, `Parent`) never changes in this plan. Host labels are single DNS labels (reuse `config.HostLabelOK`); titles are free text trimmed, control characters (incl. newlines) stripped, 80 runes max.
- Wire fields append after `site_tld` in `SiteStatus` with `omitempty` (additive tail, keep the pinned comment); Conventional Commits; app strings English, terse.

## Review Focus

- A legacy config (no `host`/`title` keys) produces byte-identical domains, status JSON (keys absent), and hosts-file entries — defaults are `""` = today.
- Renaming a host moves the primary host, its subdomains, and its worktree hosts (serving + `.env`); it never moves explicit aliases, and inherited aliases are name-free so nothing to move.
- A hand-edited `config.json` with `host:"A.B."` (non-label) sanitizes at Load to `""` (folder default) — no crash, no rejected startup; special words that pass the label shape (`localhost`) stay (Host is a label, not a suffix); API/CLI inputs reject with zero mutation instead.
- A host landing on another site's primary/alias/proxy host (or its own alias host) is refused identically by CLI, API, and MCP.
- Setting a Title changes no host anywhere: `Domain`, `domain` status field, certs, and subdomain hosts are byte-identical before/after.

---

### Task 1: Config model — Host/Title fields, sanitization, Host-aware Domain + worktree rows

**Files:**
- Modify: `internal/config/config.go` (Site struct ~L121 append `Host string \`json:"host,omitempty"\`` after `Name`; `Title string \`json:"title,omitempty"\``; `Domain()` L323-327; new unexported `func (s Site) hostName() string` next to `Suffix` in domain_entry.go), `internal/config/domain_entry.go` (`hostName`), `internal/config/config.go` `migrateDomains` loop L272-279 (call `sanitizeHost(&c.Sites[i])`-style: normalize lowercase/trim, keep iff `HostLabelOK`, clear when equals `Name`; exported `config.SanitizeTitle(s string) string` — trim, drop control runes `unicode.IsControl`, cap 80 runes — called from the sanitize loop storing `c.Sites[i].Title`), `internal/config/site_hosts.go` (L150 `primary := site.Name + "." + suffix` → `site.hostName() + "." + suffix`), `internal/config/worktrees.go` (L196-199 synthesized row: add `Host: slug + "." + parent.hostName()` — doc the two-label internal shape; `Name` formula L185 unchanged)
- Test: `internal/config/config_test.go` / `domains_test.go` (or new `site_names_test.go`): `Domain()` fallback + override; Load sanitize table (`"LIGA "`→`liga`, `"a.b"`→`""`, `""`→`""`, title 200-rune→80, `"x\ny"`→`"xy"`, host==Name→`""`); worktree row `Domain(worktreeRow)` = `slug.liga.test` when parent Host=liga; `TestTLDMoveCollision` rows keep passing with a Host-override site (the L150 fix — add one row).

**Interfaces:**
- Produces: `Site.Host`, `Site.Title` (json `host`/`title`); `Site.hostName() string`; `Config.Domain` Host-aware; worktree rows carry multi-label internal `Host`.
- Consumes: existing `HostLabelOK`, `sanitizeSuffix` pattern.

- [ ] **Step 1:** Failing tests above (RED: `Domain` ignores Host; worktree host lacks liga; sanitizer rows). **Step 2:** Implement. **Step 3:** GREEN + full `-race`. **Step 4:** lint + GOOS builds/vet. **Step 5:** Commit `feat(config): site host override and display title fields`

### Task 2: CLI `flock site host` (+ `--default`) with `.env` retargeting

**Files:**
- Create: `internal/cli/site_title.go` (`newSiteTitleCommand()`: `Use: "title [words…] [site]"`, `--default`; joins words with single spaces, `config.SanitizeTitle`, set + `Upsert` + `s.commit` — no retarget, no elevation; success line echoes the title)
- Modify: `internal/worktree/retarget.go` (new `func HostRenameSwaps(c *config.Config, name, oldHost, newHost string) []HostSwap` — site `.env`: `oldHost+"."+suffix → newHost+"."+suffix`; each `WorktreeEntry` of the site: `slug+"."+oldHost+"."+suffix → slug+"."+newHost+"."+suffix`; aliases excluded; suffix = `c.Domain` side from `site.Suffix(c)`; arguments-only rule kept), `internal/cli/site_host.go` (new `newSiteHostCommand()` mirroring `newSiteTLDCommand` structure: `Use: "host [label] [site]"`, `--default`; resolve via `siteArg`+`siteArgs`; worktree guard like site_tld L141-144; pure core `changeSiteHost(s, raw, siteName, def, out) (oldPrimary, newPrimary string, changed bool, err error)`: normalize (lower/trim) → `--default` → `""`; else `HostLabelOK` else error quoting the input → equal-to-current check (`site.hostName()`) → collision probe: copy site with new Host, run the `siteHostsFree` pair (SiteHostCollision + SiteSelfCollision) → set + `Parked=false` + `Upsert` → retarget loop (HostRenameSwaps + `RetargetDotenv`, `✓/!` lines) → `s.commit`), `internal/cli/root.go` or the `newSiteCommand` builder (site_tld.go:23-32 — `AddCommand(newSiteHostCommand())`)
- Test: `internal/cli/site_host_test.go` (temp-root `state`, like `tld_test.go`): pin (a) rename demo→liga swaps demo/.env + worktree env, old host gone, status echo `✓`; (b) `liga.cms` rejected, config+.env byte-identical; (c) trailing-dot rejected; (d) collision onto another site → error, untouched; (e) `--default` restores folder-name host + swaps back; (f) equal input → "already" no-op; (g) inherited/explicit aliases' hosts unchanged in status; (h) `site title "CMS  Liga"` stores sanitized (`CMS Liga`), `--default` clears, domain byte-unchanged before/after.

**Interfaces:**
- Consumes: Task 1 model; `RetargetDotenv`; `siteArg`; `s.commit`.
- Produces: `worktree.HostRenameSwaps`; CLI subcommand registered on `flock site`.

- [ ] **Step 1:** Failing tests. **Step 2:** RED. **Step 3:** Implement. **Step 4:** GREEN + full `-race`/lint/GOOS. **Step 5:** Commit `feat(cli): flock site host renames the primary domain name`

### Task 3: API + MCP + status — host/title surfaces

**Files:**
- Modify: `internal/api/configapi.go` (PATCH body struct: `Host json.RawMessage` + `Title *string` with the absent/null comment mirroring TLD; apply inside mutate after the tld block: host = normalize+`HostLabelOK` (invalid→`invalid()`), set on copy → `siteHostClash(cfg, site)` → store; title = trim/control-strip/80-cap (same helper Task 1 used — export a `config.SanitizeTitle(string) string` from Task 1 and reuse; adjust Task 1 if it made it unexported), `Upsert`; POST create body: accept `host`/`title` through the same paths (collision already wired); Go client `SiteChange` (~L751) += `Host *string`, `Title *string`), `internal/api/api.go` (`SiteStatus` += `SiteHost string json:"site_host,omitempty"` + `Title string json:"title,omitempty"` after `SiteTLD`, one pinned sentence each), `internal/daemon/daemon.go` (~L689: fill `SiteHost: site.Host, Title: site.Title`), `internal/mcpserver/tools_write.go` (enum L88 += `"set_site_host","set_site_title"`; `updateSiteIn` += `Host string` (jsonschema desc: `set_site_host: single label replacing the name part; "default" = the folder name`), `Title string`; cases mirroring `set_site_tld`: trims, `"default"→""`, `s.Daemon.UpdateSite(..., api.SiteChange{Host:&h})` / `{Title:&t}`, `echoDomain=true` for host)
- Test: `internal/api/api_test.go` — `TestSiteTLDPatchAndCreate`-style table `TestSiteHostAndTitle`: host `LOC `→ no (HostLabelOK lowercase: `"LIGA "`→200, `site_host:"liga"`, status domain `liga.test`); `"a.b"`→400 config-identical; `{"host":null}`=keep vs `{"host":""}`=default (RawMessage 3-state); title round-trip incl. cap; host==self-alias → 400 (self-collision); `TestSiteStatusJSONAdditive` extended row with `site_host`+`title` keys, defaults stay absent (byte-pin unchanged for the old row). `internal/mcpserver/mcpserver_test.go`: two action rows (host echo shows real domain; title echoes) plus a host-collision row (the API's 400 text surfaces as the MCP error).

**Interfaces:**
- Consumes: Task 1 `HostLabelOK`/`SanitizeTitle`/Host-aware `Domain`; existing `siteHostClash`.
- Produces: PATCH fields; `api.SiteStatus.SiteHost/Title`; Go-client `SiteChange.Host/Title`; MCP actions.

- [ ] **Step 1:** Failing API rows + golden extension + MCP rows. **Step 2:** RED. **Step 3:** Implement. **Step 4:** GREEN, full `-race`/lint/GOOS. **Step 5:** Commit `feat(api,mcp): site host override and title over the wire`

### Task 4: FlockKit + SitesPane — title display and the two pencils

**Files:** Modify `macos/Sources/FlockKit/Models.swift` (`Status.Site` += `host`, `title` decodeIfPresent→"" after `siteTld`; CodingKeys), `macos/FlockKit/Settings.swift` (`SiteChange` += `host: String?`, `title: String?` — encoding absent=nil), `macos/Sources/FlockUI/Settings/SitesPane.swift` (siteRow L287-310 + detail header L455-500: primary text = `site.title.isEmpty ? site.name : site.title`; pencil `Image(systemName: "pencil")` buttons: header title-pencil → one-field sheet (prefill `site.title`, prompt/placeholder = folder name, caption "empty inherits the folder name") → `store.perform { updateSite(name, SiteChange(title:…)) }`; address-pencil next to `Link(site.url)` → host sheet: one TextField prefilled `site.host.isEmpty ? site.name : site.host`, caption "the suffix is chosen in Domain below", submit → bundled CLI `["site","host",normalized?, site.name]` streaming `ProvisionSheet` (guard `bundledFlock` — reuse `applySuffix`'s guard wording adapted; NO live check endpoint — the CLI validates, its lines stream in the sheet incl. collision errors; when `site.host` non-empty show a "Use the folder name" secondary button → `["site","host","--default", site.name]`)), `macos/Tests/FlockKitTests/DecodingTests.swift` (additive decode + legacy-absent rows, `decodesTheSitesSuffixOverride` pattern), `ClientTests`/encode pin for `SiteChange(host:title:)`.
Note: CLI host input is user text — pass the raw typed label (the CLI normalizes/validates; there is no check endpoint for labels) and surface its error lines.

**Interfaces:** Consumes: Task 3 wire (`site_host`/`title`), existing `SuffixPicker`'s ProvisionSheet + `model.installation`. Produces: nothing downstream.

- [ ] **Step 1:** Failing decode/encode tests. **Step 2:** RED. **Step 3:** Implement FlockKit then UI. **Step 4:** `swift build && swift test --disable-sandbox` green (82+2). **Step 5:** Commit `feat(app): rename a site's display name and primary domain name`

### Task 5: Docs

**Files:** Modify `docs/cli.md` (`flock site host`, `flock site title` rows in the L32 block), `docs/sites.md` (L38 naming paragraph: display name inherits the folder name and is editable; L71 ## Domain section: the host part is editable too — one word, subdomains and worktree hosts move, aliases stay), `docs/configuration.md` (site bullet next to L37 `tld`: `host` and `title` per site).
- [ ] **Step 1:** Write; cross-check every sentence against Tasks 1-4 code. **Step 2:** `go run ./tools/docslint && make lint`. **Step 3:** Commit `docs: per-site display names and domain-name overrides`

### Task 6: App build + smoke classes (human-gated build)

- [ ] **Step 1:** `SKIP_DMG=1 macos/build-app.sh` (controller-level approval; records HEAD sha+date in the runbook). **Step 2:** Append classes m–o to Part A of `docs/superpowers/plans/2026-11-11-smoke-runbook.md`: (m) pencil at the URL → rename demo→`shop` → row/link/curl follow (`https://shop.<suffix>:8443`), `.env` swapped, old host dead; `--default` button restores; (n) subdomain `t1` and pinned alias survive the rename (aliases stay, subdomains move); (o) title-pencil → "CMS Liga" — list/header change, URL/domain unchanged. **Step 3:** Commit `docs: site-names smoke classes` (+build stamp).

---

## Final verification

- [ ] `go test -race ./...` + `make lint` + GOOS builds + docslint green at HEAD; `swift build/test --disable-sandbox` green.
- [ ] Review Focus classes 1-5 each pinned by the named tests above.
- [ ] Runbook classes m–o = human gate (app build gated on the DB window; classes themselves are temp-home).
