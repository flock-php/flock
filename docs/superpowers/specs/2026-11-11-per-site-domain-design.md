# Per-Site Local Domain (Inherit / Custom) — Design (Plan 3)

**Date:** 2026-11-11 · **Branch:** feature/local-domain-selection (builds on Plans 1–2)

## Intent

Today every site's domain is `<folder>.<global TLD>`; a custom suffix is only reachable by changing the global TLD. The user wants the choice **per site**: inherit (default, current) or their own suffix — `<folder>.<suffix>` picked with the same control/validator as the global picker — and the same for extra domains (label + suffix). A site on a custom suffix must genuinely open in browsers: DNS and valid HTTPS provisioned automatically (user confirmed).

## Q2 trap (why the model is explicit, not heuristic)

With string-suffix inference: site `demo` inherits `.test`, gets alias `demo2.loc` (explicit); global `.test→.loc` makes `.loc` the site's effective suffix, so the *next* global change would drag the explicit alias along. Inference is unsound. The alias carries an explicit inherit/override marker; the global switch follows only what inherits.

## Model

`Site.Domains` migrates from `[]string` to structured entries; JSON keeps the legacy string form on the wire where noted:

```json
{ "label": "tenant1" }                        // subdomain → tenant1.<site domain>
{ "label": "app-2",  "tld": "",    "alias": true }   // inherited alias → follows the site's effective suffix
{ "label": "demo2", "tld": "loc", "alias": true }   // explicit alias → never rewritten
```

- `Site.TLD string` ("" = inherit): effective site domain = `<name>.<Site.TLD || global TLD>`; aliases with `tld:""` resolve against the effective suffix at use time; `tld:"loc"` is absolute.
- **Migration** (on Load, in-place): string entry ending in `"."+global TLD` → `{label: <entry minus suffix>, tld:"", alias:true}` (preserves Plan 1's follow-the-global behavior exactly); every other string (e.g. `"a.b"`, `"x.example"`) → `{label: entry}` subdomain (same host as today). `Site.TLD`/`Domain.TLD` sanitized with Plan 1's rules (bad → inherit).
- Classification (code-anchored, domains.go:30 / NormalizeDomain): alias ⟺ `alias:true` (new) / ends in the site's effective suffix (legacy wire). Aliases under public TLDs: **out of scope** — the code has never had them; no CA root is ever created for a public suffix.
- Worktrees: `<slug>.<effective site domain>` — automatic.
- Proxies: inherit the global TLD only (non-goal: per-proxy suffix).

## Goals

1. **API:** `POST /v1/sites` / `PATCH /v1/sites/{name}` accept `"tld"` (validated via Plan 1's `ValidateTLD`, 400 on reject; "" = inherit). Domain add accepts the structured entry (and the legacy full-host string, decomposed: suffix == site effective → inherited alias; else → subdomain label, matching today). `SiteStatus`/`ConfigView` carry raw override + effective domain for the UI.
2. **Active suffix set** = global TLD ∪ site overrides ∪ explicit alias TLDs (all validated, non-public). Provisioning sync (privileged, CLI-run) reconciles **all** of them:
   - macOS: `/etc/resolver/<suffix>` per active suffix — write missing, remove stale (Flock-marker-guarded, per-suffix).
   - Linux: one resolved drop-in listing every active suffix.
   - Windows: NRPT rule per suffix; hosts-helper block regenerated wholesale from config.
   - **CA:** one root per active suffix, each name-constrained to that suffix + localhost, under `certs/roots/<suffix>/` (the existing global root stays at `certs/ca.crt`); leaf issuance and `SiteCert` pick the root by host suffix; PHP/Node bundle concatenates all roots; OS trust installs/trusts each (keychain/certutil/browsers); roots for suffixes leaving the set get removed + untrusted.
3. **CLI:** `flock site tld <suffix>|--inherit [site]`, `flock domain add <label> [--suffix <suffix>|--inherit]` (alias), `flock domain add <sublabel>` (subdomain, as today); apply runs the sync (Plan 1 machinery, set-diffed) with Plan 1 decline semantics; site-suffix changes retarget that site's + its worktrees' `.env` (mirror of Plan 1's `TLDHostSwaps`, site-scoped) and rewrite the site's **inherited** aliases (explicit ones never move — the Q2 trap rule).
4. **Removals stay unprivileged** (`unlink`/`unpark`/`domain remove`): the daemon detects stale suffixes (Flock-marked resolver files / NRPT rules / trusted roots outside the active set) → warning "…stale .loc setup: run `flock install`"; the next sync repairs. No password prompts on routine cleanup (user-confirmed).
5. **Daemon health:** wildcard resolution check per active suffix; status/services rows per suffix; `flock status` lists all.
6. **macOS app (3b):** Sites pane — per-site Domain control (Inherit / Custom, the Plan 2 picker component) with Apply (bundled CLI `flock site tld`, streaming sheet, decline footer → `flock install`); Add Domain sheet — label + suffix picker (Inherit / presets / Custom, live `/v1/tld/check`); inherited aliases follow, explicit stay (picker shows the state).

## Non-goals

Windows/Linux app UIs (follow-ups); per-proxy suffixes; multi-label site names; IDN suffixes; aliases under public TLDs (absent from the code, security).

## Anti-goals

- Explicit aliases dragged by global/site suffix changes (the Q2 trap).
- Custom-suffix sites that route in Caddy but don't resolve or serve untrusted TLS.
- Stale resolver files/NRPT rules/trusted roots surviving past a sync.
- Global TLD changes altering sites that carry overrides (override wins, unchanged).

## Constraints

Daemon unprivileged (provisioning = CLI sync); stdlib-only Go; `--disable-sandbox` SwiftPM rule; Go race + swift tests + docslint green per commit; Conventional Commits; legacy string wire form for Domains stays accepted (old CLIs/apps), storage is structured; one plan each for backend (3a) and macOS UI (3b), 3b consumes 3a's API.

## Approaches considered

- **A. Explicit inherit/override markers (`Site.TLD`, `Domain.TLD`/`alias`) + per-suffix provisioning and CA roots.** User-chosen (their Q2 answer: store the marker; inherited follows, overridden stays).
- **B. Suffix-inference from stored strings.** Rejected — the Q2 trap proves it unsound.
- **C. Free full hostname per site.** Rejected earlier by the user (names stay folder-based).

## Left to the builder

Plan 3a (backend: model + migration, API, CA store, dns/hosts sync generalization, CLI, daemon health, `.env` site-scope, docs) and Plan 3b (macOS UI: site Domain control, Add Domain sheet with suffix picker, reuse of the Plan 2 picker + streaming sheet). Exact signatures, test tables (incl. the Q2 trap sequence as a pinned test), and the smoke matrix.
