# macOS TLD Picker (Plan 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the macOS app's Settings → General pick the local domain (`test` / `loc` / `local` / Custom with live validation) and apply the full switch by running the bundled CLI `flock tld <tld>` in a streaming sheet.

**Architecture:** A dry-run Go endpoint `POST /v1/tld/check` (no mutation) powers live validation; FlockKit adds a decodable `TLDCheck` + `Client.checkTLD`; FlockUI replaces the static `*.<tld>` line with the picker, and Apply streams `Installation.run(bundledFlock, ["tld", …])` line-by-line exactly like the Install sheet. Privileged provisioning stays in the CLI (macOS password dialog; decline = the CLI's own "run `flock install`" guidance, shown through).

**Tech Stack:** Go 1.27 (API), Swift 6 / SwiftUI (SwiftPM package `macos/`), Swift Testing (`import Testing`, `#expect`).

**Spec:** `docs/superpowers/specs/2026-11-11-tld-picker-ui-design.md` (approach A, goals, non-goals, anti-goals).

## Global Constraints

- No new dependencies (Go: stdlib; Swift: SwiftUI/Foundation only).
- Go: `go test -race ./...` and `make lint` green at every commit (env for every go/make shell: `. /var/folders/z8/0xyccryn70d4jnvxpjnvnnlw0000gn/T/dsh-go-cache/goenv.sh`).
- Swift: `swift build` and `swift test` green in `macos/` at every Swift commit. The sandbox blocks swiftc's caches (`~/Library/org.swift.swiftpm`, clang ModuleCache): when a swift command is denied, rerun the exact command with sandbox approval (danger-full-access) — do not restructure the package to dodge it.
- App strings in English, terse, matching existing FlockUI copy; Go doc comments full-sentence why-style.
- Conventional Commits: scope `(api)`, `(app)`, `(docs)`.
- The check endpoint NEVER mutates config (anti-goal: a validation call that changes state).
- macOS is the only UI in this plan; Windows/.NET and Linux/Rust are follow-up plans (their API/CLI surface is identical).

## Review Focus

- Typed `dev`/`app`/`com` in Custom → red inline error from the check endpoint, Apply disabled — a person expects refusal *before* Apply, not a mid-flow failure.
- Typed `LOC ` / `.LOC` → the UI previews the normalized `loc` and Apply sends the normalized value.
- `local` selected → the mDNS/Bonjour warning shows inline; Apply stays enabled (switch allowed).
- Password declined mid-Apply → the sheet keeps the CLI's guidance lines, the app must not claim success; reopening Settings shows the config on the new TLD (the CLI switches config before provisioning).
- A dev build with no bundled CLI → Apply shows the "no bundled CLI — run `flock tld` in a terminal" guard, not a crash.

---

### Task 1: `POST /v1/tld/check` (Go, dry-run validation)

**Files:**
- Modify: `internal/api/api.go` (response type), `internal/api/configapi.go` (`registerConfig`)
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: `config.NormalizeTLD(raw)`, `config.ValidateTLD(t) (warning, err)` (internal/config/tld.go).
- Produces: route `POST /v1/tld/check`; body `{"tld": "LOC "}` → HTTP 200 `{"ok":true,"normalized":"loc","warning":"","error":""}`; invalid → `{"ok":false,"normalized":"<as normalized>","error":"<message>"}`; `local` → `ok:true` + `warning` naming mDNS. Swift task 2 pins this contract.

- [ ] **Step 1: Write the failing test** in `api_test.go` (follow existing settings-test pattern with fakeBackend): table over `"/v1/tld/check"` — `"loc"` ok; `"LOC "` → normalized `"loc"`; `"dev"` → ok=false, error contains `"public TLD"`; `"local"` → ok=true, warning contains `"mDNS"`; `""` → ok=false. Assert HTTP 200 for all (the endpoint reports, never 4xx) and that `GET /v1/config`'s TLD is unchanged after each call (dry-run).
- [ ] **Step 2: Run** `go test ./internal/api -run TLDCheck -v` → FAIL.
- [ ] **Step 3: Implement:** `type TLDCheck struct { OK bool `json:"ok"`; Normalized string `json:"normalized"`; Warning string `json:"warning,omitempty"`; Error string `json:"error,omitempty"` }` in api.go; in `registerConfig` a `mux.HandleFunc("POST /v1/tld/check", …)`: decode `{tld string}`, `n := config.NormalizeTLD(in); w, err := config.ValidateTLD(n)` → answer per the contract. No `b.Mutate` anywhere.
- [ ] **Step 4: Run** `go test ./internal/api -v` → PASS; `go test -race ./...` + `make lint` → green.
- [ ] **Step 5: Commit** `git commit -m "feat(api): dry-run TLD validation endpoint for the apps"`

### Task 2: FlockKit — `TLDCheck` model + `Client.checkTLD`

**Files:**
- Modify: `macos/Sources/FlockKit/Client.swift` (near `updateSettings`), `macos/Sources/FlockKit/Settings.swift` (model near `SettingsChange`)
- Test: `macos/Tests/FlockKitTests/ClientTests.swift`, `macos/Tests/FlockKitTests/DecodingTests.swift`

**Interfaces:**
- Consumes: Task 1's JSON contract verbatim.
- Produces: `public struct TLDCheck: Decodable, Sendable, Equatable { public var ok: Bool; public var normalized: String; public var warning: String; public var error: String }` (init(from:) with `decodeIfPresent` defaults `""` like Ports does) and `public func checkTLD(_ tld: String) async throws -> TLDCheck` on `FlockClient` (`POST /v1/tld/check`, body `{"tld": …}`, default timeout).

- [ ] **Step 1: Write the failing tests**: DecodingTests — decode the exact JSON shapes Task 1 emits (missing warning/error keys → `""`). ClientTests (StubProtocol) — `checkTLD("dev")` sends POST `/v1/tld/check` with body `"tld":"dev"` and decodes the stub's `{"ok":false,...,"error":"…public TLD…"}`.
- [ ] **Step 2: Run** `swift test --filter 'TLDCheck|checkTLD' ` (in `macos/`; sandbox-approval rule from Global Constraints) → FAIL (undefined).
- [ ] **Step 3: Implement** the struct + method (mirror `updateSettings`'s `decode(...)/encode` style).
- [ ] **Step 4: Run** `swift test` → PASS (all existing tests included); `swift build` → clean.
- [ ] **Step 5: Commit** `feat(app): TLD check model and client call`

### Task 3: GeneralPane — picker, live validation, Apply sheet

**Files:**
- Modify: `macos/Sources/FlockUI/Settings/GeneralPane.swift` (the Network section's "Domain" `LabeledContent`, ~line 55-64)
- Support: reuse `AppModel.installation` (`Installation.current`, `bundledFlock`), `SettingsStore.config` refresh; study `ServicesPane.swift:103` + its install flow and `AppModel.swift:224` (`Installation.run(binary, ["restart"])`) before writing.

**Interfaces:**
- Consumes: Task 2's `checkTLD`/`TLDCheck`; `Installation.run(_:arguments:onLine:)` → `Result{ok, output, permissionDeclined}`.
- Produces: nothing downstream (end user feature).

- [ ] **Step 1: Build the picker state** in GeneralPane: `@State private var draftTLD: String?` (nil = keep presets), `@State private var custom = ""`, `@State private var check: TLDCheck?`, `@State private var applying = false`, `@State private var applyLines: [String] = []`, `@State private var showApplySheet = false`. The `Domain` row becomes: menu-style `Picker` over `["test", "loc", "local"] + "Custom…"`; on Custom → `TextField`; the effective candidate = draft (presets) or normalized/custom text.
- [ ] **Step 2: Live validation**: `.task(id: candidate)` with ~300 ms debounce → call `checkTLD(candidate)` through the store's existing client-access pattern — study how this pane issues reads (`SettingsStore.perform`/`performOnServices`-style closures receive a `FlockClient`; follow that shape, do not invent a new accessor). Guard: candidate non-empty; drop stale responses by re-comparing the task id after the await. Inline under the control: green `→ .<normalized>` when normalized ≠ input; yellow `check.warning`; red `check.error`. **Apply** button `disabled(!check.ok || check.normalized == config.tld || applying)`.
- [ ] **Step 3: Apply sheet**: on tap, guard `AppModel.installation.bundledFlock` (nil → show the guard line "This build of Flock has no bundled CLI — run `flock tld <tld>` in a terminal", mirroring `install(onLine:)`'s guard); else present a sheet listing `applyLines` as they stream from `Installation.run(binary, ["tld", check.normalized]) { applyLines.append($0) }`. On `result.ok` → close sheet, `await store.refresh()` (the "Domain" line and every site row update). On `permissionDeclined` → keep the sheet open, footer: "You declined the password. Flock's config is on .<normalized>; finish the switch by running `flock tld <normalized>` in a terminal." On other failure → keep lines, mark failed. (Copy per spec: CLI's own text streams in the lines; the sheet adds no new facts.)
- [ ] **Step 4: Verify** `swift build && swift test` → green (no new FlockKit tests; pane logic is thin state + calls; the Review Focus classes pin at manual smoke, Task 4).
- [ ] **Step 5: Commit** `feat(app): choose the local domain in Settings with live validation`

### Task 4: App build + docs + smoke handoff

**Files:**
- Modify: `docs/domains.md` (the "Choosing the local domain" section: add the macOS path "in the app: Settings → General → Network → Domain"), `docs/configuration.md` (one clause naming the app control)
- Test: `SKIP_DMG=1 macos/build-app.sh` produces `build/macos/Flock.app` (sandbox-approval rule applies)

- [ ] **Step 1:** Update the two doc pages (prose per file's voice; no UI claims for Windows/Linux).
- [ ] **Step 2: Run** `go run ./tools/docslint && make lint && (cd macos && SKIP_DMG=1 ./build-app.sh)` → app built.
- [ ] **Step 3: Commit** `docs: the app's local domain picker`
- [ ] **Step 4:** Hand the human partner the smoke script (temp `FLOCK_HOME`, daemon from `bin/flock`, the built app): pick `loc` → Apply → password dialog → sites on `.loc`; type `dev` → red refusal, Apply disabled; pick `local` → warning shows; decline the password → guidance shown, config switched.

---

## Final verification

- [ ] `make test && make lint` green; `cd macos && swift build && swift test` green.
- [ ] `SKIP_DMG=1 macos/build-app.sh` → app launches against the temp-home daemon and the picker completes `test → loc` with real DNS (`curl https://demo.loc:8443` through the temp daemon per Plan 1's smoke) and back to `test`.
