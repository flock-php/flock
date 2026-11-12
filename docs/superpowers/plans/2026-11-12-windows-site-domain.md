# Windows Site-Domain Parity (Plan 3c) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Windows app gets global TLD + per-site suffix + alias/subdomain dialogs + renames — parity with macOS — on top of the daemon's existing wire (models & client in Flock.Kit, UI in Flock.Tray).

**Architecture:** `windows/Flock.Kit` (pure C#, xunit) models the wire and exposes `CheckTldAsync`/`AddDomainAsync(suffix)`/`UpdateSettings(Tld)`/`SiteChange.Tld|Host|Title`; `Flock.Tray` (WinUI 3) adds one shared `SuffixPickerControl`, a `DomainApplyService` that streams bundled-CLI runs (`flock.exe tld|site tld|site host`, no shell — `CliProcessRunner`) with the declined-UAC sentinel footer, and rewires the existing panes. Title edits go through the API. Validation and provisioning live in the CLI/daemon only (R1).

**Tech Stack:** C# net10.0 (`Flock.Kit` cross-OS; `Flock.Tray` `net10.0-windows10.0.22621.0` WinUI 3 — built/type-checked via `EnableWindowsTargeting`), xunit.

**Spec:** `docs/superpowers/specs/2026-11-12-windows-site-domain-design.md`

## Global Constraints

- Toolchain: `export PATH="$HOME/.dotnet:$PATH"; export DOTNET_CLI_TELEMETRY_OPTOUT=1` (SDK 10 at `~/.dotnet`). Gates: `dotnet test windows/Flock.Kit.Tests` (runs here, any commit touching Kit); for every Tray commit: `dotnet msbuild windows/Flock.Tray -restore -t:Compile -p:RuntimeIdentifier=win-x64 -p:EnableWindowsTargeting=true` green (type-check; no Windows runner).
- No new NuGet dependencies; no changes to `.csproj`/sln files; no daemon/privileged execution — tests fake HTTP (existing `ClientTests` harness) and never spawn processes.
- Wire truth (do not restate loosely): site rows send `tld` (effective), `site_tld`, `site_host`, `title` (all `site_host`/`title` omitempty); `dns.suffixes` `[{tld,wildcard,problem?}]` omitempty; `POST /v1/tld/check` → `{ok,normalized,warning?,error?}` (keys may be absent — decode to nulls); PATCH tld/host are `json.RawMessage` 3-state (absent=keep — omit from the body; `""`=inherit/default; value=set); POST /v1/settings takes `"tld"`.
- R1: global/site suffix and host renames run `InstallationService`-style bundled `flock.exe` (`Path.Combine(AppContext.BaseDirectory, "flock.exe")` — follow `Infrastructure/Installation.cs:18-19` locating) via `CliProcessRunner`; declined sentinel = output contains `"administrator permission was declined"` → footer tells to run `flock install`; API paths (title, addDomain) never retarget `.env` — that's fine per shipped doctrine.
- UI strings inline English matching existing voice; ContentDialog pattern of `SitesPane.xaml.cs:300-330`; busy-gating through the panes' existing `Start(...)`/`OperationRunner`.
- Conventional Commits (`feat(windows): …`, `test(windows): …`, `docs(windows): …`).

## Review Focus

- A site row from an OLD daemon (no `site_host`/`title`/`suffixes` keys) decodes with empty strings/empty list — no throw anywhere in Kit.
- The suffix picker's live check sends the RAW typed text to `/v1/tld/check` and Apply passes ONLY the server-normalized value to the CLI — never local guesswork.
- A new suffix chosen in Add Alias chains exactly one bundled `flock install` when the suffix ∉ active set (`dns.suffixes` ∪ global tld), and does NOT chain for an active/global suffix.
- Declined UAC mid-apply: the dialog states config switched + finish with `flock install`; the pane refreshes (row shows the new state).
- Title edits never touch `host`/`tld` keys in the PATCH body (absent = keep — a body that sends `tld:null`… verify omission, not null).

---

### Task 1: Flock.Kit — wire models, tld/check client, suffix-carrying addDomain

**Files:**
- Modify: `windows/Flock.Kit/Settings.cs` (`SettingsChange` += `string? Tld`; `SiteChange` += `string? Tld, Host, Title` — serialization must OMIT nulls: check how the existing records serialize (anonymous-object builders vs `JsonSerializer` `IgnoreNull`) — pin by a serialize test), `windows/Flock.Kit/Models.cs` (`Status.Site` += `Tld, SiteTld, SiteHost, Title` with null-coalescing defaults; `DnsStatus` += `List<SuffixStatus> Suffixes`; `record SuffixStatus(string Tld, bool Wildcard, string? Problem)`), `windows/Flock.Kit/FlockClient.cs` (`AddDomainAsync(string site, string domain, string? suffix = null)` — body includes `suffix` only when non-null/non-empty; new `Task<TldCheck> CheckTldAsync(string tld)` POST `/v1/tld/check`; `UpdateSettingsAsync` passes Tld through the existing change-object pattern)
- Create: `windows/Flock.Kit/Models.cs` — `record TldCheck(bool Ok, string Normalized, string? Warning, string? Error);` (inside Models.cs, no new file)
- Test: `windows/Flock.Kit.Tests/ClientTests.cs` (fake-HTTP harness style): site-row full + legacy-without-keys decode; suffixes present/absent; TldCheck missing-key decode; AddDomain body with/without suffix (assert request JSON); SiteChange/SettingsChange serialization omits nulls and `"tld": ""` survives as empty string for inherit.

**Interfaces:**
- Produces: `TldCheck`, `SuffixStatus`, extended `Status.Site.Tld/SiteTld/SiteHost/Title`, `FlockClient.CheckTldAsync`, `AddDomainAsync(site, domain, suffix?)`, `SettingsChange.Tld`, `SiteChange.Tld/Host/Title` — consumed by Tasks 2–5.
- Consumes: nothing new.

- [ ] **Step 1:** Failing tests (RED: missing fields/methods). **Step 2:** Implement. **Step 3:** `dotnet test windows/Flock.Kit.Tests` green. **Step 4:** Tray still type-checks (fields added, nothing consumes yet). **Step 5:** Commit `feat(windows): Kit models and client for tld, suffixes, host, title`

### Task 2: SuffixPickerControl + DomainApplyService (shared machinery)

**Files:**
- Create: `windows/Flock.Tray/Controls/SuffixPickerControl.xaml(.cs)` — ComboBox (Inherit optional per `AllowInherit` + `.test/.loc/.internal/.alt/.localhost/.example` + `Custom…`) + TextBox shown for Custom; properties: `Current` (effective suffix — labels Inherit row `Inherit (.<current>)`), `AllowInherit`, exposes `Draft`, `Check` (null|ok|warning|error TextBlock lines like macOS feedback), `CanApply` (check.ok && normalized != Current); debounced 300 ms via `DispatcherTimer` calling a `CheckAsync` callback.
- Create: `windows/Flock.Tray/Infrastructure/DomainApplyService.cs` — `RunAsync(string[] args, Action<string> onLine)`: locate bundled flock.exe (Installation.cs pattern), `CliProcessRunner`, returns `(bool Ok, bool Declined)` with the sentinel; `const string DeclinedSentinel = "administrator permission was declined";`
- Test: `dotnet msbuild windows/Flock.Tray -restore -t:Compile -p:RuntimeIdentifier=win-x64 -p:EnableWindowsTargeting=true` (no unit tests for XAML; the service's sentinel/paths pure parts live in a static class `DomainApply` with a `IsDeclined(string output)` pure method — xunit-testable? it is in Tray (win-only project) — keep the pure static + a Flock.Kit-side `public static bool IsDeclined(string)` in `Kit/Installation.cs` (exists? if a Kit Installation helper exists extend it, else add there) and TEST IT IN Flock.Kit.Tests.
- [ ] Steps 1–5: Kit pure sentinel test RED/GREEN → build the control+service → type-check green → commit `feat(windows): shared suffix picker and CLI apply machinery`

### Task 3: Global TLD picker (General → Network)

**Files:** Modify `windows/Flock.Tray/Settings/GeneralPane.xaml` (Domain card: replace read-only text with `SuffixPickerControl` (no Inherit) + Apply button + declined footer text block), `GeneralPane.xaml.cs` (wire picker→`CheckAsync` → `FlockClient.CheckTldAsync`; Apply → `DomainApplyService.RunAsync(["tld", normalized])` with output shown (append lines into an inline `TextBox`/ItemsControl while running — the plan leaves widget choice: reuse BusyContent + an Expander with the lines); success → refresh config/status; declined → footer text), `ViewModels/GeneralViewModel.cs` (`DomainValue` stays as Current feed).
- [ ] Steps: wire + type-check; manual-visual deferred to runbook; commit `feat(windows): global TLD picker in General`

### Task 4: Per-site Domain row + alias/subdomain dialogs

**Files:** Modify `windows/Flock.Tray/Settings/SitesPane.xaml.cs` — `Detail()` Site section (after HTTPS field): `Field("Domain", suffixPicker+Apply)` (AllowInherit=true; Current=`site.Tld`; draft from `site.SiteTld`; Apply → `site tld <normalized>`/`--inherit` via DomainApplyService; lines+declined footer inline in the field area); replace the `Window.Ask` Add Domain (L1018-1029) with `AddAliasAsync(site)` ContentDialog (Name TextBox + SuffixPickerControl AllowInherit + check lines; primary Add → `AddDomainAsync(name, suffix ?? "inherit")`; on success AND suffix non-inherit AND suffix ∉ `dns.Tld` ∪ `dns.Suffixes` → chain `DomainApplyService.RunAsync(["install"])` with same output/declined treatment) and `AddSubdomainAsync(site)` (Name only → `AddDomainAsync(name)`); domains list grouped "Aliases"/"Subdomains" (two `Field` rows by `domain.Alias`); caption copy mirrors the macOS texts (read `docs/domains.md` + pane voice); keep remove buttons per entry.
- [ ] Steps: implement → type-check → commit `feat(windows): per-site suffix and alias/subdomain dialogs`

### Task 5: Renames — title and host

**Files:** Modify `windows/Flock.Tray/Settings/SitesPane.xaml.cs` — header: name display = `Title ?? Name`; a small `Ui.Link("Rename", …)` (or TextBox-on-click per spec's left-to-builder) → ContentDialog one TextBox (empty = inherit) → `UpdateSiteAsync(name, new SiteChange(Title: typed))`; next to the URL link a `Ui.Link("Rename domain", …)` → ContentDialog: name TextBox (prefill `SiteHost` empty→`Name`) + grey `. <site.Tld>` tail TextBlock; primary → `DomainApplyService.RunAsync(["site","host",typed,site.Name])` streaming + declined footer; when `SiteHost` non-empty also "Use the folder name" → `["site","host","--default", name]`.
- [ ] Steps: implement → type-check → commit `feat(windows): rename display name and domain name`

### Task 6: Docs + Windows runbook

**Files:** Create `docs/superpowers/plans/2026-11-12-windows-smoke.md` (VM runbook: setup from the CI-built/installed Flock on Windows; classes A–G mirroring macOS: global pick to .loc + NRPT/hosts-helper check via `Get-DnsClientNrptRule` + `curl https://demo.loc`; decline UAC mid-apply → footer → `flock install` repairs; alias `api2 --suffix lab` new suffix auto-install chain; subdomain move on host rename; renames title/host; legacy daemon row sanity). Modify `docs/domains.md`/`docs/sites.md`: the macOS-only wording for per-site controls → "macOS and Windows" where Task 3–5 shipped; `windows/README.md` capability line.
- [ ] Steps: write; truth-check every claim against Tasks 1–5 code; `go run ./tools/docslint && make lint`; commit `docs(windows): the Windows app speaks local domains` (+runbook in the same or split commit).

---

## Final verification

- [ ] `dotnet test windows/Flock.Kit.Tests` green; Tray type-check green at HEAD; `make test`/`make lint` untouched-Green (no Go changes expected beyond zero).
- [ ] Review Focus 1–5 pins named per task.
- [ ] Windows VM smoke = human gate (runbook A–G).
