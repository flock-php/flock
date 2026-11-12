# Windows App: Local-Domain Parity (Plan 3c) — Design

**Date:** 2026-11-12 · **Branch:** feature/local-domain-selection · **Depends on:** Plans 1–3b + Site Names (all shipped on this branch; the daemon API already carries everything)

## Intent

Bring the Windows app (WinUI 3, `windows/Flock.Tray` + cross-OS `windows/Flock.Kit`) to feature parity with the macOS app for local domains: a global TLD picker, a per-site Domain suffix control, alias/subdomain add dialogs with suffix pickers and auto-provisioning, and the two renames (display name, domain name).

## Goals

1. **Flock.Kit (pure C#, xunit on any OS):** models for what the daemon already sends — `FlockConfig.Tld` write path, `SettingsChange.Tld`, `SiteChange.Tld/Host/Title`, `Status.Site.Tld/SiteTld/SiteHost/Title`, `DnsStatus.Suffixes []SuffixStatus`, `TLDCheck`; client calls `POST /v1/tld/check`, `addDomain(site, domain, suffix)`. This layer is TDD'd on the controller's macOS (CI runs it in the windows matrix too).
2. **Global TLD picker** (Settings → General → Network → Domain card, today read-only): presets `.test/.loc/.internal/.alt/.localhost/.example` + custom entry with live `/v1/tld/check` (warning/error lines), Apply runs the bundled `flock.exe tld <suffix>` streaming; declined UAC = config switched + finish with `flock install` (macOS footer doctrine).
3. **Per-site Domain row** (Sites → Site section): suffix picker Inherit/`.<global>`/presets/custom + Apply → bundled `flock.exe site tld … <site>` / `--inherit` streamed.
4. **Alias & Subdomain dialogs** replace the single full-name `Window.Ask`: Add Alias (label + suffix picker w/ live check; new suffix → auto-run bundled `flock install`), Add Subdomain (name only). Lists group under Aliases / Subdomains by the `alias` flag.
5. **Renames:** display name (in the detail header, edit → API `title`) and domain name (dialog → bundled `flock.exe site host …`, suffix tail visible as on macOS).
6. **Smoke:** a Windows runbook (classes mirroring macOS a–o, trimmed) for the human's VM.

## Non-goals

Linux (3d); visual theming work; localization (all inline English, as today); `.flockrc` writes for title/host (machine-local, like macOS); proxy changes; new daemon behavior (none needed — the API is done).

## Anti-goals

- Re-implementing validation or provisioning in C#: suffix/host mutations run the bundled CLI (R1, proven by `InstallationService` running `flock install` today); only live *checks* hit the API.
- A half-wired UI: no control may appear for a Kit field that has no test.
- Tray regressions: the windows CI matrix (build + Kit tests + screenshots) stays green per commit that touches `windows/`.

## Constraints

- `net10.0-windows10.0.22621.0`, WinUI 3 unpackaged, CommunityToolkit.Mvvm — no new NuGet dependencies; `EnableWindowsTargeting` type-check (`dotnet msbuild windows/Flock.Tray -t:Compile`) must stay green on the controller's macOS for every Tray edit; `Flock.Kit.Tests` run fully here.
- CLI runs only via `CliProcessRunner` (no shell, no runas — UAC is flock's business).
- Dialogs: `ContentDialog`/`Ui.*` helpers; busy via the panes' `Start(...)`/`OperationRunner` discipline.
- Conventional Commits; docs updates where user-visible (docs/domains.md app block, docs/sites.md, windows/README.md line if behavior claims change).

## Decided shape (how)

- One reusable `SuffixPickerControl` (UserControl: preset ComboBox + custom TextBox + live check lines) shared by Goals 2–4 (XAML + code-behind, no new MVVM framework).
- A `TldApplyFlow` service in `Flock.Tray/Infrastructure` mirroring macOS `applyDomain`: guard bundled exe → stream lines to a content area → declined footer text → refresh on success. Tray-side "declined" detection: CLI output contains the same sentinel string the macOS `Installation.swift` pins.
- Kit is the single source of wire knowledge; Tray code never hand-builds JSON.

## Left to the builder

Exact XAML names/placements inside the panes, the streaming dialog's layout (inline Expander vs second ContentDialog), preset order strings, and how the title-edit affordance fits the header row (TextBox-on-click vs small dialog).

## Plan split note

One plan: T1 Kit → T2 global picker → T3 per-site Domain row + SuffixPickerControl → T4 alias/subdomain dialogs → T5 renames → T6 docs + Windows runbook.
