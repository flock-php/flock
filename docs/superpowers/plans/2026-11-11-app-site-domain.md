# Per-Site Domain Control in the macOS App (Plan 3b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In the macOS app, each site picks Inherit/Custom suffix (apply = bundled CLI `flock site tld` with a streaming sheet), Add Domain gains label+suffix controls with auto-`flock install` for a brand-new suffix; plus a small Go polish batch (create-time collisions, `tld:123` row, single label regex, MCP echo, global-adoption root dedupe).

**Architecture:** Extract Plan 2's GeneralPane picker into a shared `SuffixPicker` view + `SuffixCheckModel` (debounce/live-check) + a generic `ProvisionSheet` (streaming lines, decline footer); SitesPane's Site section gains a `field("Domain")` using it, and the Add-Domain alert becomes a sheet. Suffix mutations run the CLI (provisioning/.env/collisions live there — the API is config-only by ruling R1); alias adds use the API `{domain, suffix}` and chain `Installation.run(["install"])` when the suffix is new to the machine.

**Tech Stack:** Swift 6 / SwiftUI (SwiftPM `macos/`), Swift Testing; Go 1.27 (polish task).

**Spec:** `docs/superpowers/specs/2026-11-11-app-site-domain-design.md` (Goals 1–4, auto-provision approved by the user, non-goals).

## Global Constraints

- Swift: SwiftUI/Foundation only, no new deps, no Package.swift changes; `swift build --disable-sandbox` + `swift test --disable-sandbox` green in `macos/` at every Swift commit (78 tests baseline).
- Go: stdlib+cobra; `go test -race ./...` + `make lint` + GOOS=windows/linux/darwin builds green (env: `. /var/folders/z8/0xyccryn70d4jnvxpjnvnnlw0000gn/T/dsh-go-cache/goenv.sh && export GOCACHE=/tmp/flock-gocache GOMODCACHE=/tmp/flock-gomodcache`).
- NO privileged commands, NO daemon restarts; the app build (Task 7) is human-gated on the DB-server window — it uses the sandbox-approval path and never restarts the running Flock.
- Wire facts (Go, shipped): `site_tld`/`tld` on site rows; `suffixes []SuffixStatus{tld,wildcard,problem}` omitempty; PATCH tld 3-state (absent=keep, `""`=inherit, value).
- Apply semantics pinned from Plan 2: disabled while check pending/failed/unchanged; CLI receives only the server-normalized suffix; decline footer → `flock install`; success resets drafts + `store.refresh()`.
- Conventional Commits; app strings English, terse.

## Review Focus

- A site inheriting `.test` opens with **Inherit selected** and Apply disabled; typing `dev` in Custom → red public-TLD, Apply disabled.
- Apply on a site suffix streams CLI lines; declining leaves the site row showing the NEW domain (config switched, 2s poll) and the footer naming `flock install`.
- Add alias with suffix `loc` when `.loc` is NOT active → the sheet chains `flock install` automatically; with an already-active suffix → it does NOT re-install.
- The global picker (Plan 2) behaves byte-identically after the component extraction (labels, debounce, decline footer, presets).
- A global switch landing ON a suffix that has site roots (`roots/loc/` → global `loc`) ends with ONE trusted root for `.loc` (Go-pinned, dedupe fix).

---

### Task 1: Go polish batch (create-time collisions, 123-row, label regex, MCP echo, root dedupe)

**Files:**
- Modify: `internal/api/configapi.go` (create site handler insert after `siteTLD` ~L357 before `Upsert`; drop `domainLabel` L219 → config helper), `internal/cli/domains.go` (L175 `domainLabelShape` → config helper), `internal/config/domains.go` (export `HostLabelOK(label string) bool` wrapping hostLabel), `internal/mcpserver/tools_write.go` (echo L762), `internal/certs/roots_scan.go` (`ActiveRootCerts` skips `suffix == global`; `StaleRootDirs(dir, global, active)` treats `roots/<global>/` stale), call sites (`internal/cli/site_tld*.go`, `install_linux.go`, `install_windows.go`, `internal/daemon/stale_roots.go`)
- Test: `internal/api/api_test.go` (`TestSiteTLDPatchAndCreate`: POST-create collision row + `{"tld":123}` 400 row), `internal/certs/roots_test.go` (adoption: roots/loc + global→loc → provisioning list = [dir/ca.crt], sweep retires roots/loc), label helper unit rows.

**Interfaces:**
- Produces: `config.HostLabelOK(string) bool`; `certs.ActiveRootCerts(dir, global string, active []string) []string`; `certs.StaleRootDirs(dir, global string, active []string) []string` (signatures gain `global`); API create 400 shape via `invalid()` with config byte-identical.
- Consumes: 3a `SiteHostCollision`/`SiteSelfCollision` (site in proposed state), existing sweep/trust flows.

- [ ] **Step 1:** Failing tests: create `{name:"demo", tld:"loc"}` colliding with existing site/proxy host → 400 + config untouched; `{"tld":123}` → 400 "tld must be a string" (existing path, now pinned); roots adoption scenario (ensure roots/loc under global=test; flip global=loc; provisioning = global at dir only; sweep list contains "loc").
- [ ] **Step 2:** RED → **Step 3:** implement (insert collision check `siteHostClash(cfg, site)`-style on create; replace two regex copies with `config.HostLabelOK`; fix echo to print the effective domain (`<site.Name>.<tld>` → real values, inherit branch keeps its wording); add `global` param through scan/provision call sites — dirFor already maps global→dir, the fix is the SCAN side only).
- [ ] **Step 4:** GREEN, full -race, lint, GOOS builds. **Step 5:** Commits (Conventional, split by scope, tests with their code): `fix(api): reject site-create host collisions and non-string tld` + `fix(certs): retire the site root when the global suffix adopts it` + `refactor(config): one exported label rule for api and cli` + `fix(mcp): set_site_tld echoes the real domain`.
Note: the spec's "global flip collision preflight" item is VERIFIED ALREADY SHIPPED in 3a (`TLDMoveCollision`, cli/tld.go:90-104, pinned by TestTLDMoveCollision*) — no task here.

### Task 2: FlockKit models — tld/site_tld, suffixes, SiteChange.tld, addDomain suffix

**Files:** Modify `macos/Sources/FlockKit/Models.swift` (Status.Site init + CodingKeys; DNSStatus init + `SuffixStatus`), `macos/Sources/FlockKit/Settings.swift` (`SiteChange.tld: String?` appended), `macos/Sources/FlockKit/Client.swift` (`addDomain(site:domain:suffix:)` default `""`, body includes suffix only when non-empty; `linkSite` untouched).
**Test:** `Tests/FlockKitTests/DecodingTests.swift` (site row with `"tld":"loc","site_tld":"loc"` and legacy row without → defaults `""`; dns status with `"suffixes":[…]` and omitted → `[]`; suffix row fields incl. problem-omit), `ClientTests.swift` (addDomain with suffix posts body containing `"suffix":"loc"`; without → body has no suffix key).

- [ ] **Step 1:** Failing decode/wire tests above. **Step 2:** RED. **Step 3:** Implement (decodeIfPresent+`""`/`[]` defaults per file style; `SiteChange.tld` — JSONEncoder omits nil ⇒ absent=keep, `""`=inherit, pin by encoding test). **Step 4:** `swift test --disable-sandbox` green (all). **Step 5:** Commit `feat(app): site tld fields, suffix health and suffix-carrying addDomain`

### Task 3: Extract shared SuffixPicker + ProvisionSheet from GeneralPane (no behavior change)

**Files:**
- Create: `macos/Sources/FlockUI/Settings/SuffixPicker.swift` (view + `@Observable final class SuffixCheckModel` — draft/custom/check/checkNote/candidate/canApply + the 300 ms `.task(id:)` debounce + store access via the store's client pattern; presets + `customTag` + Inherit tag support as parameters: `allowInherit: Bool`, `global: String`)
- Create: `macos/Sources/FlockUI/Settings/ProvisionSheet.swift` (generic: title line for running/failed, ProgressView, streaming ScrollView (Color.clear id "end" + onChange scrollTo), decline footer text parameterized by `subject: String` ("the switch"→"the config is on .X; finish by running `flock install` in a terminal."), Close disabled(running), frame 420)
- Modify: `macos/Sources/FlockUI/Settings/GeneralPane.swift` (swap the inline picker+ApplyTLDSheet for the components; keep state wiring equivalent: candidate, canApply, applyDomain → `ProvisionSheet`, presets, onChange prefill)
- Test: none (no UI test target) — verification is `swift build --disable-sandbox` + full `swift test --disable-sandbox` (78) + the Task 7 smoke classes; self-review diff must show GeneralPane keeps identical strings/behaviors (Review Focus 4).

- [ ] **Step 1:** Implement components (move, don't rewrite: same strings, same rules). **Step 2:** Rewire GeneralPane; delete moved code. **Step 3:** `swift build && swift test --disable-sandbox` green. **Step 4:** Commit `refactor(app): shared suffix picker and provisioning sheet`

### Task 4: SitesPane — per-site Domain control

**Files:** Modify `macos/Sources/FlockUI/Settings/SitesPane.swift` (new `field("Domain")` in `section("Site")` after HTTPS ~L471; per-site `SuffixCheckModel` instances keyed by site — one at a time via the existing `@State`-accessor idiom is fine (one site's detail is open); Apply → `model.installation.bundledFlock` guard (P2 guard line) → `Installation.run(binary, ["site","tld", normalized, site.name])` — inherit selection runs `["site","tld","--inherit", site.name]`; streaming into `ProvisionSheet(subject: site.name)`; on ok `store.refresh()`).
**Test:** none (no UI target); pinned logic = the selection mapping (site.siteTld "" → Inherit tag; "loc" → preset/custom prefill) — implement as a small pure func `siteDomainSelection(_ siteTld: String, global: String) -> String` + presets and pin it in a FlockKit-side test file ONLY if the func lives in FlockKit (keep it in FlockUI; verify at build + smoke).

- [ ] **Step 1:** Implement the field (labels: `Inherit (.<global>)`, `.test`, `.loc`, `.local`, `Custom…`; Apply disabled per pinned rule; busy states). **Step 2:** Apply flow incl. decline footer (site-scoped wording: "…config is on .loc for <site>; finish by running `flock install`…"). **Step 3:** build+test green. **Step 4:** Commit `feat(app): choose a site's suffix in Sites`

### Task 5: SitesPane — Add Domain sheet (label + suffix, auto-install for new suffix)

**Files:** Modify `macos/Sources/FlockUI/Settings/SitesPane.swift` (replace the alert L167-180 with a `.sheet`: `TextField` label (prompt "tenant1, a.b"), `SuffixPicker(allowInherit: true, default Inherit)`, live check when suffix≠inherit, `Add` disabled while label empty / (suffix≠inherit && !canApply) / submitting; submit → `store.perform { try await $0.addDomain(site: name, domain: label, suffix: suffix == .inherit ? "inherit" : normalized) }`; on success: if suffix≠inherit AND suffix not in `model.status?.dns.suffixes` (tld match) AND suffix != dns.tld → chain `Installation.run(binary, ["install"])` streaming in the same sheet ("Provisioning .loc…" section, decline footer; ok → close); else close + refresh). Update `domainHelp` copy to the label+suffix model.
**Test:** none pinned in CI (UI); self-review checks: submit sends the suffix; install NOT chained for active/global suffixes (code-read).

- [ ] **Step 1:** Implement the sheet (+ remove alert path). **Step 2:** Auto-install chain with the new-suffix condition. **Step 3:** build+test green. **Step 4:** Commit `feat(app): add domains with a suffix picker and auto-provisioning`

### Task 6: Docs truth pass

**Files:** Modify `docs/sites.md` (Domain section: "in the app: Sites → Site → Domain…"), `docs/domains.md` (drop "on its way" claims; app per-site control + alias sheet + auto-provision sentence), `docs/cli.md` untouched (CLI rows exist).
- [ ] **Step 1:** Write; verify every claim against Tasks 1–5 code. **Step 2:** `go run ./tools/docslint && make lint`. **Step 3:** Commit `docs: the app's per-site domain controls`

### Task 7: App build + runbook extension (human-gated privileged parts)

- [ ] **Step 1:** `SKIP_DMG=1 macos/build-app.sh` (sandbox-approval rule; controller/human runs it — NOT in task CI). **Step 2:** Extend `docs/superpowers/plans/2026-11-11-smoke-runbook.md` Part A with classes g–l: (g) pick `.loc` for demo in-app → sheet streams → accept → `curl -skI https://demo.loc:8443`; (h) `dev` red disabled; (i) alias `api2 --suffix loc` via sheet: auto-install chains, accept, `curl https://api2.loc:8443` trusted; (j) alias with suffix = already-active → NO install chain; (k) decline mid-apply → site row shows new domain, footer names `flock install`, `flock install` in a terminal repairs; (l) Inherit returns demo to `.test`-side row. **Step 3:** Commit `docs: app per-site smoke classes` (+ record the build timestamp in the runbook).

---

## Final verification

- [ ] `swift build && swift test --disable-sandbox` green; `go test -race ./...`, `make lint`, docslint, GOOS builds green at HEAD.
- [ ] GeneralPane behavior unchanged post-extraction (diff review). 
- [ ] Runbook Part A classes a–l = human gate (privileged; after the DB window).
