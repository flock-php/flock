# Local Domain (TLD) Selection — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user switch Flock's local TLD from the hardcoded `test` to a chosen one (`loc`, `local`, or any custom single-label TLD that isn't a public TLD), with everything downstream — DNS, CA, sites' aliases, worktree `.env` — following.

**Architecture:** The core is already TLD-parameterized (`config.Config.TLD` flows into `dns.New`, `certs.Ensure`, Caddyfile, `/etc/resolver`, NRPT, hosts helper). This plan adds: a validation layer in `internal/config`, CA root recreation on TLD mismatch, a config migration for stored aliases, `Settings.TLD` in the API, a `flock tld` CLI command that changes TLD and re-provisions the OS integration in one flow, and `.env` retargeting for sites and worktrees.

**Tech Stack:** Go 1.27 (stdlib only: `crypto/x509`, `embed`, cobra), per-OS files (`*_darwin.go`/`*_linux.go`/`*_windows.go`).

**Spec:** This plan's Problem Statement section, derived from the feature request: local domains were pinned to `.test`; selectable TLD with curated examples and guarded custom input.

**Out of scope (Plan 2):** the native apps' Settings UI (Swift `macos/Sources/FlockUI/Settings/`, Rust `linux/src/`, .NET `windows/Flock.Tray/`). They consume `POST /v1/settings {"tld": …}` and `ConfigView.TLD`, both covered here. Worktree `.env` files created **before** the change are retargeted; app-managed env outside `.env` (e.g. `php.ini`) is not.

## Global Constraints

- No new third-party dependencies; stdlib + cobra only (`go.mod` untouched).
- `make test` (`go test -race ./...`) and `make lint` (`gofmt -l .` empty) must pass on every commit.
- Cross-compiles must stay green: OS-specific code only in `*_darwin.go` / `*_linux.go` / `*_windows.go` (+ `install_other.go` fallbacks).
- Comments follow the repo style: full-sentence doc comments on every exported name, explaining *why*.
- Commits: Conventional Commits, scope in parentheses (`feat(tld): …`, `fix(certs): …`).
- Docs: a feature PR updates `docs/` (CONTRIBUTING.md); `go run ./tools/docslint` must pass.

## Problem Statement / Decisions

- `.test` (RFC 6764) is the default and safest; the user may pick `loc`, `local`, etc.
- **Custom TLD is allowed** with hard guardrails: exactly one DNS label; reject anything in the IANA root zone (public TLDs like `dev`/`app`/`com` would swallow real internet names under that suffix; `dev`/`app` are HSTS-preloaded) and RFC 6761 specials (`localhost`, `invalid`).
- `.local` is accepted **with a warning**: mDNS/Bonjour (RFC 6767) reserves it; on macOS the system may keep resolving `.local` via Bonjour, and browsers special-case it.
- Changing the TLD must not require a manual `flock install`: `flock tld <tld>` retires the old OS integration, saves config, reloads the daemon, and provisions the new one, reusing the existing privileged paths.

## Review Focus

- Input: a public TLD (`dev`, `app`, `com`, `io`) → hard rejection naming the reason; a normal person expects the tool to refuse *before* breaking their internet resolution under that suffix.
- Input: `.local` → accepted with a warning (mDNS); Flock's status keeps telling the truth when macOS Bonjour keeps `.local` (the existing wildcard problem text must show, not a crash).
- Input: `" LOC "`, `".LOC"`, `""`, `"a b"`, `"-x"`, `x.y` (multi-label) → normalization/round-trip; stored config is always a bare lowercase label.
- Condition: a site alias under a *third* suffix (`x.example`) and a multi-label subdomain entry (`a.b`) — migration touches only aliases ending in `.<oldTLD>`.
- Condition: the change runs while the daemon is live — DNS server restarts for the new TLD, the root CA is recreated (leaf caches key on CA fingerprint), PHP's CA bundle is rewritten, and the CLI must leave the machine *provisioned for the new TLD* in the same command.

---

### Task 1: TLD normalization and validation

**Files:**
- Create: `internal/config/tld.go`, `internal/config/tlds.txt`, `internal/config/public_tlds.go`, `tools/gen-tlds/main.go`
- Test: `internal/config/tld_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func NormalizeTLD(raw string) string` — trims spaces, strips leading dots, lowercases.
  - `func ValidateTLD(tld string) (warning string, err error)` — takes an **already normalized** value; returns a user-facing warning (currently only for `local`) and an error for invalid, reserved, multi-label, or public TLDs.
  - `func IsPublicTLD(tld string) bool` (unexported data is fine; keep the predicate exported for CLI messaging).

- [ ] **Step 1: Write the failing tests** in `internal/config/tld_test.go`:

```go
func TestNormalizeTLD(t *testing.T)          // ".LOC " → "loc"; "Loc" → "loc"; "  " → ""
func TestValidateTLDAcceptsLocalLabels(t *testing.T) // "test" (no warning), "loc" (no warning), "local" (warning mentions Bonjour/mDNS), "home-ar" ok
func TestValidateTLDRejectsUnusableLabels(t *testing.T) // "", "a b", "-x", "x-", strings.Repeat("a",64), "x.y" (multi-label: "one label")
func TestValidateTLDRejectsReservedAndPublic(t *testing.T) // "localhost" ("reserved"), "invalid" ("reserved"), "dev"/"app"/"com"/"io" (error mentions public TLD)
```

- [ ] **Step 2: Run** `go test ./internal/config -run 'TLD' -v` — expect FAIL (undefined).

- [ ] **Step 3: Implement.** `internal/config/tlds.txt` is the IANA root-zone list, one lowercase label per line, generated by `tools/gen-tlds/main.go` (fetch `https://data.iana.org/TLD/tlds-alpha-by-domain.txt`, lowercase, drop `#` comments, sort; commit the output file). `public_tlds.go` embeds it (`//go:embed tlds.txt`) into a `map[string]struct{}` built in `init`. `tld.go`:

```go
// ValidateTLD takes a normalized TLD and returns a warning shown when it is accepted
// (mDNS owns "local") and an error when it must not be used.
func ValidateTLD(tld string) (string, error)
```

Label rule: `^[a-z][a-z0-9-]{0,61}[a-z0-9]$` (rejects one-char labels and edge hyphens; `x.y` fails it → "one label"). Reject `localhost` and `invalid` explicitly with "reserved" wording before consulting the public list.

- [ ] **Step 4: Run** the test command — expect PASS. `make lint`.
- [ ] **Step 5: Commit** `git add -A && git commit -m "feat(config): validate and normalize a chosen local TLD"`

---

### Task 2: CA root follows the TLD

**Files:**
- Modify: `internal/certs/certs.go` (`Ensure`, ~line 37)
- Test: `internal/certs/certs_test.go` (extend `TestRootIsNameConstrainedToTheTLD`)

**Interfaces:**
- Consumes: nothing new.
- Produces: `Ensure(dir, tld)` semantics — an existing root whose `PermittedDNSDomains` lack `"."+tld` is **replaced** (files overwritten); a matching root is loaded as today. Callers (`daemon.reload`, `cli.ensureCA`) unchanged.

- [ ] **Step 1: Write the failing test** in `certs_test.go`:

```go
func TestEnsureRecreatesRootWhenTLDChanges(t *testing.T) {
	ca1, err := Ensure(t.TempDir(), "test")   // fingerprint F1, PermittedDNSDomains [".test","test","localhost"]
	ca2, err := Ensure(dir, "loc")            // new fingerprint ≠ F1; PermittedDNSDomains contain ".loc"
	// Ensure(dir, "loc") again → fingerprint == ca2 (stable, not recreated every reload)
}
```

- [ ] **Step 2: Run** `go test ./internal/certs -run TestEnsureRecreates -v` — expect FAIL (old root kept).
- [ ] **Step 3: Implement** in `Ensure`: after a successful `load()`, `if !ca.serves(tld) { return ca, ca.create(tld) }`-style flow; `func (ca *CA) serves(tld string) bool { return slices.Contains(ca.cert.PermittedDNSDomains, "."+tld) }`. `create` already overwrites `ca.key`/`ca.crt`. Note in a comment why leaf/site certs keep working (leaf cache keyed by fingerprint; `SiteCert` re-issues when signed by a different CA).
- [ ] **Step 4: Run** `go test ./internal/certs -v` — PASS.
- [ ] **Step 5: Commit** `fix(certs): recreate the root CA when the local TLD changes`

---

### Task 3: `Config.SetTLD` migrates stored aliases

**Files:**
- Modify: `internal/config/tld.go`
- Test: `internal/config/tld_test.go`

**Interfaces:**
- Consumes: `Config.Sites []Site` (`Domains []string`, see `config.go:119-128`).
- Produces: `func (c *Config) SetTLD(new string) (old string)` — sets `c.TLD = new`; rewrites every site `Domains` entry ending in `"."+old` to `"."+new` (aliases only: `"app-2.test"` → `"app-2.loc"`); leaves subdomain entries (`"tenant1"`, `"a.b"`) and aliases under other suffixes (`"x.example"`) untouched. Proxies derive `<name>.<TLD>` (`proxies.go:22`) — no change needed.

- [ ] **Step 1: Write the failing test** (fixture style of `domains_test.go`): sites `app` with `["tenant1","a.b","app-2.test","x.example"]`; after `SetTLD("loc")`: `["tenant1","a.b","app-2.loc","x.example"]`, `old == "test"`. Second call `SetTLD("test")` round-trips.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** (plain string suffix replace over `c.Sites[i].Domains`).
- [ ] **Step 4: Run** `go test ./internal/config -v` → PASS.
- [ ] **Step 5: Commit** `feat(config): carry site aliases along a TLD change`

---

### Task 4: `tld` in the settings API and daemon warnings

**Files:**
- Modify: `internal/api/api.go` (`Settings` struct, ~line 219), `internal/api/configapi.go` (`applySettings`, ~line 137), `internal/daemon/daemon.go` (`reload`, warnings ~line 568)
- Test: `internal/api/api_test.go`, `internal/daemon/daemon_test.go`

**Interfaces:**
- Consumes: Task 1 `NormalizeTLD`/`ValidateTLD`, Task 3 `SetTLD`.
- Produces: `Settings.TLD *string` (JSON `"tld"`) — the API the apps (Plan 2) and tests drive; `GET /v1/config`'s `ConfigView.TLD` already exists. The daemon adds one warning line when `cfg.TLD == "local"` (mDNS caveat) into the `warnings` slice in `reload`.

- [ ] **Step 1: Write the failing tests** in `api_test.go` (pattern: existing `POST /v1/settings` tests):

```go
// settings tld "LOC " → 200, config.json TLD=="loc"; "dev" → 400, body mentions public TLD;
// "" → 400; "local" → 200 (daemon warning checked in daemon_test)
```

and in `daemon_test.go`: config `TLD:"local"` → `status.Warnings` contains the mDNS/Bonjour warning; `TLD:"loc"` → it doesn't.

- [ ] **Step 2: Run** `go test ./internal/api ./internal/daemon -run 'Settings|TLD' -v` → FAIL.
- [ ] **Step 3: Implement**: `applySettings` — `if s.TLD != nil { t := NormalizeTLD(*s.TLD); if _, err := ValidateTLD(t); err != nil { return invalid("%v", err) }; cfg.SetTLD(t) }` (validate first, mutate config inside the same `Mutate` closure — a rejected TLD never touches aliases). Daemon: append the `local` warning where `warnings` is built in `reload`.
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(api): expose the local TLD through the settings endpoint`

---

### Task 5: `flock tld` command — change and re-provision

**Files:**
- Create: `internal/cli/tld.go`
- Modify: `internal/cli/root.go` (register in `AddCommand`, line 25), `internal/cli/install.go` (`Use/Short` text: `".test DNS"` → `"local DNS"`, TLD-neutral), per-OS `internal/cli/install_darwin.go` / `install_linux.go` / `install_windows.go` (+ `install_other.go` if needed)
- Test: `internal/cli/tld_test.go`

**Interfaces:**
- Consumes: Tasks 1–4; `state.commit` (`helpers.go:51`), `ensureCA` (`install.go:131`), `ensurePrivileged(cmd, s, bin, ca)` (per-OS), `runPrivileged`, privileged-setup `--remove --tld <old>` (darwin `install_darwin.go:410`, windows `privilegedSetup(tld, caPath, source, remove, noHelper)` `install_windows.go:730`), `dns.ResolvedFile(tld)` (linux drop-in), Task 6 `RetargetDotenv`.
- Produces:
  - `func (s *state) changeTLD(raw string, out io.Writer) (old string, warning string, err error)` — pure core: normalize/validate, no-op when equal, `RetargetDotenv` for parent sites and worktrees (Task 6), `s.cfg.SetTLD`, `s.commit(ctx, out)` (daemon reload picks up new DNS/CA/web), OS `retireTLD(s, out, old)` privileged cleanup of the **old** TLD (resolver file / NRPT rule / drop-in / hosts-helper source), then `ensureCA` + `ensurePrivileged(cmd, s, bin, ca)` provisioning the **new** TLD and trusting the (recreated) root; prints the mDNS warning when `local`.
  - New per-OS hook `func retireTLD(s *state, out io.Writer, old string) error` — darwin: `privileged-setup --remove --tld old` only when the resolver file for `old` exists; linux: remove `/etc/systemd/resolved.conf.d/flock.conf` drop-in when it was Flock's (compare content like `install_linux.go` does) and let `ensurePrivileged` write the new one; windows: update the helper + NRPT via the existing privileged-setup path (it is TLD-parameterized; the helper reconfig is covered by `flock install` text at `daemon.go:1250`).
  - `newTLDCommand()`: `flock tld` prints the current TLD; `flock tld <tld>` runs `changeTLD` and prints `serving .loc from now on` + warnings.

- [ ] **Step 1: Write the failing tests** `internal/cli/tld_test.go` (no privileged calls: `retireTLD`/`ensurePrivileged` behind the per-OS files; the unit test drives `changeTLD` with a fake `state` in `t.TempDir()` and a stubbed commit path — follow the seam `autoinstall_test.go`/`status_test.go` use):
  - `changeTLD("LOC ")` on a temp root with config + one site with alias `app-2.test` → config saved `loc`, alias `app-2.loc`.
  - `changeTLD("dev")` → error naming public TLD, config untouched (alias not rewritten).
  - `changeTLD(current)` → "already" message, no writes.
- [ ] **Step 2: Run** `go test ./internal/cli -run TLD -v` → FAIL.
- [ ] **Step 3: Implement** `tld.go` + registration; keep cobra glue thin (`RunE` → `changeTLD`); per-OS `retireTLD` files with `install_other.go` no-op.
- [ ] **Step 4: Run** `go test ./internal/cli -v` → PASS. `GOOS=windows go build ./... && GOOS=linux go build ./... && GOOS=darwin go build ./...`.
- [ ] **Step 5: Commit** `feat(cli): flock tld switches the local domain and re-provisions it`

---

### Task 6: `.env` retargeting for sites and worktrees

**Files:**
- Create: `internal/worktree/retarget.go`
- Test: `internal/worktree/retarget_test.go`

**Interfaces:**
- Consumes: `Dotenv` (`dotenv.go:10`: `ReadDotenv`, `Get/Set/Write`); stored worktree entries `cfg.Worktrees []WorktreeEntry` (`worktrees.go:23`); site names/paths from `cfg.Sites`.
- Produces:
  - `func RetargetDotenv(path, oldHost, newHost string) (changed []string, err error)` — reads the file; for keys `APP_URL`, `SESSION_DOMAIN`, `SANCTUM_STATEFUL_DOMAINS`, `MAIL_FROM`, `MAIL_FROM_ADDRESS` (Laravel's skeleton writes the address key; the display-name `MAIL_FROM_NAME` carries no host): replace `oldHost` → `newHost` where the host matches exactly (URL host equality, domain equality, comma-list membership, `user@host`); values containing the old host embedded in URLs (`https://x.old.test/…`) are replaced too (this is what setup.go:227-235 semantics expect, simplified to suffix-aware host matching). Writes only when `changed` is non-empty.
  - `func (c *Config) TLDHostSwaps(oldTLD, newTLD string) []HostSwap` — `HostSwap{Path, Old, New}`: for every linked site `<name>.<oldTLD>` → `<name>.<newTLD>` (`.env` under `Path`), for every worktree entry → `<slug>.<parentName>.<oldTLD>` → new. Task 5 calls this and loops `RetargetDotenv`.

- [ ] **Step 1: Write the failing tests** (write-file + exact-content style of `worktree_test.go:86-104`): a `.env` with `APP_URL=http://acme.test # the site`, `SESSION_DOMAIN=.acme.test`-style keys, `SANCTUM_STATEFUL_DOMAINS=localhost,acme.test:5173`, `MAIL_FROM=old@acme.test` and `MAIL_FROM_ADDRESS=noreply@acme.test` (quoted form keeps its quoting; `x@acme.test.evil.com` stays), and a comment line mentioning `old.test` (comments untouched) → after `RetargetDotenv(path, "acme.test", "acme.loc")` the file equals the expected string with hosts swapped, `changed` lists the keys; second call is a no-op.
- [ ] **Step 2: Run** `go test ./internal/worktree -run Retarget -v` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(worktree): retarget .env domains when the TLD changes`

*(Task 5 consumes Task 6 — implement 6 before wiring 5's loop, or land 5 with the call and 6 supplying it; keep each commit green by landing 6 first.)*

---

### Task 7: Documentation

**Files:**
- Modify: `docs/domains.md` ("How .test resolution works" → TLD-neutral + a *Choosing the local domain* section: `flock tld`, `loc`/`local`/custom rules, `.local` mDNS caveat, public-TLD refusal), `docs/configuration.md` (config table: `tld`), `docs/cli.md` (`flock tld`), `docs/installation.md` (note that `flock tld` re-runs DNS/CA setup).
- Test: `go run ./tools/docslint`

- [ ] **Step 1:** Update the pages; every `.test` in prose becomes `<tld>`/`.test` *by default*.
- [ ] **Step 2: Run** `go run ./tools/docslint && make lint` → clean.
- [ ] **Step 3: Commit** `docs: describe local TLD selection`

---

## Final verification

- [ ] `make test && make lint` — green.
- [ ] `GOOS=windows go vet ./... && GOOS=linux go vet ./... && GOOS=darwin go vet ./...`
- [ ] Manual smoke (macOS, dev build, temp root): `flock tld loc` on a linked site → `curl -sI https://<site>.loc` answers (after the command re-provisioned DNS/CA), old alias moved, `.env` rewritten, `flock status` shows `dns *.loc`.
