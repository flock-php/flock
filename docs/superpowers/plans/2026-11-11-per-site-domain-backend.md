# Per-Site Local Domain — Backend (Plan 3a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sites and their aliases can inherit the global TLD or pin an explicit suffix; every active suffix gets its own DNS integration and name-constrained CA root; the CLI applies suffix changes with full provisioning sync.

**Architecture:** `Site.TLD` ("" = inherit) and structured `Site.Domains` entries `{label, tld, alias}` with explicit inherit markers (the Q2 trap — no suffix inference). A `certs.Roots` store keeps one root per active suffix; the DNS server, resolver files / resolved drop-in / NRPT / hosts helper generalize from one TLD to a suffix set with privileged set-diff sync; the daemon reports per-suffix health and flags stale system files. API and CLI expose site/alias suffix changes; site-scoped `.env` retargeting mirrors Plan 1.

**Tech Stack:** Go 1.27, stdlib only; cobra; per-OS files as in the existing install code.

**Spec:** `docs/superpowers/specs/2026-11-11-per-site-domain-design.md` (model, Q2 trap, active-suffix rules, non-goals).

**Human constraint (binding):** NO app builds, NO daemon restarts, NO privileged provisioning during this plan — the partner's DB server must not be disturbed. All tests run in temp roots, in-process. The privileged smoke matrix ships as a document for later execution.

## Global Constraints

- stdlib + cobra only; go.mod untouched.
- `go test -race ./...` and `make lint` green at every commit (env: `. /var/folders/z8/0xyccryn70d4jnvxpjnvnnlw0000gn/T/dsh-go-cache/goenv.sh`).
- Cross-builds stay green: `GOOS=windows go build ./...` and `GOOS=linux go build ./...` per commit touching per-OS code.
- Legacy compatibility: `Site.Domains` on disk as `[]string` must load (migration in `config.Load`, like Plan 1's Load sanitize); `POST /v1/sites/{name}/domains {"domain":"full.host"}` keeps working and means what today's `NormalizeDomain` means.
- Explicit alias marker: an entry with a non-empty `tld` NEVER moves on global/site suffix changes; `tld:""` follows. This is pinned by a named test in Task 1.
- Conventional Commits (`feat(config): …`); full-sentence why-comments; docs in Task 7.

## Review Focus

- Legacy config with `["tenant1","app-2.test","x.example"]` under TLD `test` loads into entries whose resolved hosts are byte-identical to today (incl. `x.example` → `x.example.demo.test`).
- The Q2 trap sequence: alias `demo2` explicit `.loc` under global `.test` → global `.test→.loc` → global `.loc→.xyz` leaves `demo2.loc` untouched, while an inherited `app-2` follows test→loc→xyz.
- Site with `Site.TLD="loc"` is unaffected by global TLD changes (domain, aliases with tld:"", worktree hosts, status).
- A public TLD never gets a CA root, a resolver file, an NRPT rule, or a drop-in entry; validation rejects it on every entry point.
- Removing the last site/alias on a suffix leaves system files in place with exactly one aggregated stale-suffix warning naming the repair command; no password prompt, no auto-delete.

---

### Task 1: Config model — Site.TLD, structured Domains, effective domains, migration

**Files:**
- Create: `internal/config/domain_entry.go`
- Modify: `internal/config/config.go` (Site struct, `Domain(s)` ~287, `Load` sanitize area), `internal/config/domains.go` (ExtraDomains, NormalizeDomain), `internal/config/tld.go` (SetTLD), `internal/config/proxies.go` (ProxyDomain unchanged — proxies inherit)
- Test: `internal/config/domain_entry_test.go`, extend `domains_test.go`, `tld_test.go`, `config_test.go`

**Interfaces:**
- Produces:
  - `type Domain struct { Label string `json:"label"`; TLD string `json:"tld,omitempty"`; Alias bool `json:"alias,omitempty"` }` with custom `UnmarshalJSON` accepting a legacy plain string (→ the raw form kept for the migration step).
  - `Site.TLD string `json:"tld,omitempty"``; `Site.Domains []Domain`.
  - `func (s Site) Suffix(c *Config) string` — `s.TLD` or `c.TLD`; `func (c *Config) Domain(s Site) string { return s.Name + "." + s.Suffix(c) }` (existing signature preserved).
  - `func (c *Config) ActiveSuffixes() []string` — sorted: global TLD ∪ site `TLD` ∪ alias `TLD` (non-empty, validated values only).
  - `ExtraDomains(s Site)` resolves each entry's `Host`: subdomain → `Label + "." + c.Domain(s)`; inherited alias (`TLD==""`) → `Label + "." + s.Suffix(c)` (its own name under the site's effective suffix, exactly today's `app-2.test`); explicit alias → `Label + "." + d.TLD`. `Entry` (API-facing): subdomain → `Label`; inherited alias → `Label + "." + s.Suffix(c)`; explicit → `Label + "." + d.TLD`.
  - `NormalizeDomain(s Site, name string)` returns a `Domain` entry (was string): input ends in site effective suffix → inherited alias; ends in a *validated non-public* other suffix → explicit alias (`{label, tld}`); else subdomain label (today's behavior for `x.example` preserved).
  - `SetTLD(new) (old string)`: sets `c.TLD` only — inherited entries follow by construction; explicit never move. The string-suffix rewrite from Plan 1 disappears (migration at Load converts legacy; post-migration hosts are derived).
  - Load migration: legacy `[]string` → `Domain` objects (rule in Review Focus 1) + `Site.TLD` sanitize (NormalizeTLD/ValidateTLD, invalid → "").
- Consumes: Plan 1 `NormalizeTLD`/`ValidateTLD`/`IsPublicTLD`.

- [ ] **Step 1: Failing tests** `domain_entry_test.go`: (a) legacy `["tenant1","app-2.test","x.example"]` under test → ExtraDomains hosts `tenant1.demo.test`, `app-2.test`(alias), `x.example.demo.test`; (b) Q2-trap sequence via `SetTLD` twice — pinned hosts as in Review Focus; (c) `Site.TLD:"loc"` + global change → domain stays `demo.loc`, inherited alias follows `demo.loc→demo.loc` (suffix unchanged), explicit stays; (d) `ActiveSuffixes` = sorted set incl. alias tlds, public excluded (can't be stored — see e); (e) `NormalizeDomain("demo2.loc")` on `.test` site → `{label:"demo2", tld:"loc", alias:true}`; `"tenant9"` → subdomain; `"x.com"` stays subdomain (public suffixes are not aliases).
- [ ] **Step 2:** RED (undefined).
- [ ] **Step 3:** Implement model + Load migration (`Site` custom UnmarshalJSON: `domains` accepts `[]string` legacy → migrate, `[]Domain` → keep; sanitize `Site.TLD`).
- [ ] **Step 4:** GREEN + fix the whole `./...` suite (consumers: `daemon.go:658-688` status rows use `.Entry/.Host` — adapt; `api/content.go:177`, `mcpserver/server.go:184`, `worktree/retarget.go` TLDHostSwaps — site-aware hosts: swap = `<name>.<site.Suffix(old)>` → `.new` only for sites WITHOUT override; inherited aliases ride the suffix so their .env hosts change too — extend TLDHostSwaps to emit alias swaps for inherited aliases: `<alias-label>.<old-effective>` → `<alias-label>.<new-effective>`).
- [ ] **Step 5:** `go test -race ./...`, `make lint`, GOOS builds.
- [ ] **Step 6:** Commit `feat(config): per-site TLD overrides and explicit alias markers`

### Task 2: `certs.Roots` — one name-constrained root per active suffix

**Files:**
- Create: `internal/certs/roots.go`
- Modify: `internal/certs/certs.go` (reuse `Ensure` per dir), `internal/web/certsource.go` (pick root by host suffix), `internal/daemon/daemon.go` (reload: ensure all roots; bundle = concat)
- Test: `internal/certs/roots_test.go`, `internal/web/certsource_test.go` (pattern), daemon focused test

**Interfaces:**
- Produces:
  - `type Roots struct { ... }` with `func OpenRoots(dir string) *Roots` (dir = `<root>/certs`: global root at `dir/ca.crt` as today; extra at `dir/roots/<suffix>/` via the existing `Ensure`), `func (r *Roots) Ensure(tlds []string) error` (create missing; keep global at legacy path), `func (r *Roots) For(host string) (*CA, bool)` — longest-suffix match over loaded roots, and `func (r *Roots) All() []*CA`, `func (r *Roots) Bundle(extra []byte) error` — concatenates every root cert into the PHP/Node bundle (extends `WriteBundle`).
  - `certsource`: on TLS handshake pick `Roots.For(name)`; no root for the suffix → serve nothing (existing fail-fast path).
- Consumes: Task 1 `ActiveSuffixes` (daemon passes `cfg.ActiveSuffixes()` to `Roots.Ensure`).

- [ ] **Step 1: Failing tests** `roots_test.go`: Ensure(["test","loc"]) → two roots, distinct PermittedDNSDomains (".test" root can't sign `a.loc` — verify leaf issuance refused; loc root signs `demo.loc`); reload is stable (fingerprints unchanged); `For("x.demo.loc")` picks loc root; bundle contains both PEMs. `certsource`: leaf for `.loc` host served only when loc root exists.
- [ ] **Step 2:** RED. **Step 3:** Implement (`roots.go`), daemon.reload: `roots.Ensure(cfg.ActiveSuffixes())` replaces the single `certs.Ensure`; bundle write concatenates all.
- [ ] **Step 4:** GREEN + full suite; `daemon_test` asserts PHP CA bundle contains both roots.
- [ ] **Step 5:** Commit `feat(certs): one name-constrained root per active suffix`

### Task 3: DNS + OS integration for a suffix set

**Files:**
- Modify: `internal/dns/server.go` (`New(addr, tlds []string)`, `TLDs()`), `internal/dns/resolver.go` (`SyncResolverFiles(dirs, tlds, port)` — write missing, delete stale with the Flock marker), `internal/dns/resolved.go` (`ResolvedContent(tlds, port)` — `Domains=a b c`), `internal/dns/nrpt_windows.go` (`SetNRPT(tlds, ...)` per-suffix rules), `internal/hosts/hosts.go` + `helper_windows.go` (source parses with the suffix SET: `ParseSources(data, tlds []string)`; helper `--tlds a,b` — update `cmd/flock-helper/main.go`), `internal/daemon/daemon.go` (`ensureDNS` compares sorted set), `internal/cli/install_darwin.go` / `install_linux.go` / `install_windows.go` (privileged-setup takes `--tlds` list; retire = set-diff), `internal/cli/conflicts.go` (resolverConflict per suffix)
- Test: `server_test.go`, `resolver_test.go` (Sync set-diff incl. foreign file untouched), `resolved_test.go`, `nrpt` unit (string layer), `hosts_test.go`, `daemon_test.go` (DNS restart on set change only), `install_unix_test.go` pattern.

**Interfaces:**
- Produces: `dns.New(addr string, tlds []string) *Server` answering every name under any listed suffix; `dns.Server.TLDs() []string`; `dns.SyncResolvers(tlds []string, port int) (written, removed []string, err error)` per OS file layer (darwin: /etc/resolver/<s>, linux: drop-in wholesale, windows: rules + helper source); `hosts.ParseSources(data []byte, tlds []string) []string`.
- Consumes: Task 1 `ActiveSuffixes`.

- [ ] **Step 1: Failing tests**: server answers `a.loc` and `a.test` with the set [test, loc], refuses `a.dev`; SyncResolvers on a temp dir: writes missing, removes only marker-owned files, leaves foreign resolver files alone; ResolvedContent lists all suffixes sorted; ParseSources filters over the set.
- [ ] **Step 2:** RED. **Step 3:** Implement per-OS (helper flag rename `--tld`→`--tlds`, comma list; bump `hosts.HelperRevision` and reconfigure via install path).
- [ ] **Step 4:** GREEN; GOOS=windows/linux builds (helper cmd included).
- [ ] **Step 5:** Commit `feat(dns): resolve and provision every active suffix`

### Task 4: Daemon per-suffix health + stale-suffix warnings

**Files:**
- Modify: `internal/daemon/wildcard.go` (check per suffix, state map), `internal/daemon/daemon.go` (status assembly; stale scan: per-OS marker files/rules outside `ActiveSuffixes()`), `internal/api/api.go` (`DNSStatus` gains `Suffixes []SuffixStatus` — `{tld, wildcard, problem}`; primary fields = global, backward compatible), `internal/cli/status.go` (list all)
- Test: `wildcard_test.go` (two-suffix state), `daemon_test.go` (stale resolver file in temp FS → warning names `flock install`), `api_test.go` (JSON shape stable for old clients).

- [ ] **Step 1: Failing tests**: statuses per Review Focus 5: suffix [test, loc], loc resolver missing → `Suffixes[{loc, false, problem}]` + the aggregated "*.loc domains don't resolve… run `flock install`" warning; stale marker file for `old` not in the set → "stale .old setup: run `flock install`" (one line, not N).
- [ ] **Step 2:** RED. **Step 3:** Implement (stale scan per-OS in wildcard.go/daemon_unix.go/windows equivalents; macOS = resolver dir scan by marker; linux = drop-in extras; windows = NRPT rules with the Flock comment outside the set).
- [ ] **Step 4:** GREEN. **Step 5:** Commit `feat(daemon): per-suffix DNS health and stale-suffix warnings`

### Task 5: API — site tld override + structured alias input

**Files:**
- Modify: `internal/api/api.go` (SiteChange `TLD *string`; SiteStatus `TLD string` effective + `SiteTLD string` raw; SiteDomain entry shape), `internal/api/configapi.go` (POST/PATCH sites: validate via ValidateTLD, 400 `invalid()`; domain add body `{domain, suffix?}` where suffix ∈ {"" = inherit via normalize, "loc", "inherit"=explicit inherit}), `internal/api/api_test.go`, `internal/mcpserver/tools_write.go` (`set_site_tld` action + `add_domain` suffix param)
- Test: api_test rows: PATCH tld "loc" → 200 + SiteStatus shows effective; "dev" → 400; "" → inherit; domain add `{domain:"demo2", suffix:"loc"}` → stored explicit (survives global change in the same test); legacy `{domain:"demo2.test"}` unchanged semantics.

- [ ] **Step 1:** Failing tests above (fakeBackend Mutate pattern; MCP table-driven action test per mcpserver_test.go).
- [ ] **Step 2:** RED. **Step 3:** Implement.
- [ ] **Step 4:** GREEN. **Step 5:** Commit `feat(api): per-site TLD and alias suffix control`

### Task 6: CLI — `flock site tld`, `flock domain add --suffix`, provisioning sync

**Files:**
- Create: `internal/cli/site_tld.go`
- Modify: `internal/cli/root.go`, `internal/cli/domains.go` (flags), `internal/cli/tld.go` (extract `provisionSuffixes(s, out) (warning string, err error)` = set-diff of ActiveSuffixes: retire stale (privileged per-OS), ensure roots for new (certs), trust new roots (per-OS install helpers), `ensureCA`-style, daemon commit; Plan 1 decline semantics preserved: config switched, guidance `flock install`), `internal/worktree/retarget.go` (site-scoped swaps from Task 1)
- Test: `site_tld_test.go` (temp root, no privileged calls — seam per Plan 1 T5): `flock site tld loc demo` sets Site.TLD, retargets `demo/.env` + worktree `.env` incl. inherited alias hosts, leaves explicit alias hosts; `--inherit` clears; `dev` rejected byte-identical; `flock domain add demo2 --suffix loc` stores explicit.

- [ ] **Step 1:** Failing tests (drive the pure core; provisioning behind per-OS funcs not invoked).
- [ ] **Step 2:** RED. **Step 3:** Implement; register command.
- [ ] **Step 4:** GREEN + GOOS builds. **Step 5:** Commit `feat(cli): flock site tld and alias suffix flags with provisioning sync`

### Task 7: Docs + smoke matrix (deferred execution)

**Files:** Modify `docs/domains.md` (per-site domain + inherited/explicit alias semantics incl. the Q2 rule in prose), `docs/cli.md` (`flock site tld`, `domain add --suffix`), `docs/configuration.md` (site tld), `docs/sites.md` (Domain section).
Create: `docs/superpowers/plans/2026-11-11-per-site-domain-smoke.md` — the privileged smoke matrix for the human partner to run AFTER the DB server window (resolver files per suffix, CA trust per suffix, Windows NRPT/helper, decline paths, stale-suffix repair). No privileged step runs in CI.

- [ ] **Step 1:** Write; claims verified against Task 1–6 code.
- [ ] **Step 2:** `go run ./tools/docslint && make lint`.
- [ ] **Step 3:** Commit `docs: per-site local domains`.

---

## Final verification

- [ ] `go test -race ./...`, `make lint`, GOOS windows/linux/darwin `go build ./...` green at HEAD.
- [ ] No app builds, no daemon runs with production root, no privileged commands — the smoke document is the human gate.
