# Per-Site Local Domains — Privileged Smoke Matrix (Plan 3a)

**Date:** 2026-11-11 · **Branch:** feature/local-domain-selection · **Run by:** the human partner

**No step in this matrix runs in CI.** Everything here is privileged: resolver files, NRPT rules, CA roots, OS trust. It needs `sudo` / administrator on your machine, restarts the real daemon and touches the real DNS and trust stores — run it **after the DB-server window closes**.

**Proves:** a site's own suffix provisions end to end (S1); a pinned alias is never dragged, the Q2 sequence (S2); removing the last site under a suffix warns and `flock install` sweeps clean (S3); declining a prompt is fully repaired by `flock install` (S4); `--inherit` returns the site and retires the suffix (S5).

**Machine:** your daily machine on its production Flock root (no `FLOCK_HOME`), Flock installed and running. Paths below write `<data>` for the [data directory](../../configuration.md#data-directory): `~/Library/Application Support/Flock` on macOS, `%AppData%\Flock` on Windows, `~/.local/share/flock` on Linux.

## Cast

- [ ] Site `demo`, linked and secured (`flock link` + `flock secure`): a Laravel project whose `.env` names its hosts (`APP_URL`, `SESSION_DOMAIN`, `MAIL_FROM`).
- [ ] Site `shop`, linked, inheriting the global suffix.
- [ ] Global suffix `.test`: `flock tld` prints `.test`; `flock status` shows one `dns *.test` row.

Record the starting state once: `flock status` output, `flock domain list`, and the suffix rows the machine has (`/etc/resolver/` listing, the resolved drop-in, `Get-DnsClientNrptRule` — per OS below).

## S1 — Site suffix: `flock site tld loc demo` provisions everything

Run `flock site tld loc demo`. Expect the password / administrator prompt for the DNS and trust steps (macOS: one authorization dialog per certificate, the `.loc` root its own), then `✓ demo → demo.loc from now on`.

- [ ] **Resolver, per suffix:**
    - macOS: `/etc/resolver/loc` exists, points at `127.0.0.1` port `53535`; `/etc/resolver/test` untouched.
    - Linux: `/etc/systemd/resolved.conf.d/flock.conf` `Domains=` lists `test` **and** `loc`; `resolvectl status` shows both routed to Flock.
    - Windows: `Get-DnsClientNrptRule` shows one rule per suffix, `.test` and `.loc`, both to Flock's DNS server; the `# BEGIN Flock` hosts-file block carries `demo.loc`.
- [ ] **Root per suffix:** `<data>/certs/roots/loc/ca.crt` exists; the global root is still `<data>/certs/ca.crt` (it never moves); the `.loc` root is name-constrained to `.loc` + `localhost` (`openssl x509 -in <data>/certs/roots/loc/ca.crt -noout -text` → Name Constraints).
- [ ] **Trust per OS:**
    - macOS: `security verify-cert -c "<data>/certs/roots/loc/ca.crt" -p ssl` passes; Keychain Access → login shows the Flock `.loc` root trusted as a root.
    - Linux: an anchor for the `.loc` root exists (`flock-<user>-loc.crt` in `/usr/local/share/ca-certificates` / the `ca-trust` anchors dir) and `update-ca-certificates` / `update-ca-trust` ran.
    - Windows: `certutil -store Root` lists the Flock `.loc` root.
- [ ] **Bundle:** `<data>/certs/cacert.pem` now carries both roots (`grep -c "BEGIN CERTIFICATE"` grows by one); outgoing HTTPS from PHP/Node to `.loc` verifies (`php -r 'echo file_get_contents("https://demo.loc/") ? "ok\n" : "";'`).
- [ ] **HTTPS trusted:** `curl -sI https://demo.loc` answers without `-k`; the browser opens `https://demo.loc` with no warning.
- [ ] **Health:** `flock status` shows a `dns` row for `*.test` and for `*.loc`.
- [ ] **`.env` retargeted:** `demo/.env` (and its worktrees') hosts now name `demo.loc`; `demo.test` no longer answers.

## S2 — Q2 sequence: the pinned alias is never dragged

- [ ] On `shop`, add an inherited alias and a pinned one:
  `flock domain add app2 --suffix inherit` → `app2.test`; `flock domain add pin1 --suffix loc` → `pin1.loc`. If the config didn't use `.loc` before, the add prints ``! .loc is new to Flock's setup: run `flock install` to provision its DNS and HTTPS`` — run it, then `curl -sI https://pin1.loc` answers trusted.
- [ ] Flip the global: `flock tld loc`. Expect: `shop → shop.loc`; `app2.test` **moves** to `app2.loc` (it follows); `pin1.loc` and the pinned site `demo` do not move. The global root is recreated for `.loc` and re-trusted (the prompt Plan 1's switch always shows); the pinned `.loc` root at `certs/roots/loc/` stays as it is. `.test` retires only if nothing serves under it any more.
- [ ] Flip again: `flock tld xyz`. Expect: `shop.loc → shop.xyz`, `app2.loc → app2.xyz`; **`pin1.loc` and `demo.loc` stay byte-identical hosts** — that is the Q2 trap pinned.
- [ ] `.loc` survives as a suffix because `pin1`/`demo` serve under it: its resolver file / NRPT rule / drop-in entry and its trusted root are all still in place. Check the S1 list for `.loc` once more, and `curl -sI https://pin1.loc` is trusted while the global suffix is `.xyz`.

## S3 — Last site under a suffix removed: stale warning, `flock install` sweeps

- [ ] Retire through the unprivileged paths: `flock unlink demo`, `flock domain remove pin1 shop`. Neither asks for a password.
- [ ] `flock status` shows **one** aggregated line: ``stale .loc setup: run `flock install` ``. The system files are deliberately still there (resolver file / NRPT rule / root store / trust entry) — nothing auto-deletes.
- [ ] Run `flock install`. Expect the sweep: the `.loc` root leaves the OS trust (macOS: one dialog, then "the .loc root certificate is no longer trusted"; Windows: the elevated run's `certutil -delstore`; Linux: the anchor rewrite drops `flock-<user>-loc.crt`), the resolver file / NRPT rule / drop-in entry for `.loc` is removed, `<data>/certs/roots/loc/` is deleted, and the bundle is rewritten without it.
- [ ] `flock status` shows no stale warning and no `.loc` row; `curl -sI https://pin1.loc` no longer resolves; the global suffix's own trust is untouched (`security verify-cert -c <data>/certs/ca.crt -p ssl` passes / anchors for the global root intact).

## S4 — Declined prompts: `flock install` repairs fully

- [ ] Run `flock site tld dev shop` and **decline** the password / administrator prompt. Expect the error to say the site is served at `shop.dev` already and to name `flock install` to finish the switch. The config carries the override (`flock site tld` shows `site suffix .dev`), the `.env` files are retargeted, the root exists at `<data>/certs/roots/dev/` (the daemon created it unprivileged) — the OS integration is not there yet.
- [ ] `flock status` names it: a `dns` row for `*.dev` that doesn't resolve.
- [ ] Run `flock install`. It converges the whole set: the resolver file / NRPT rule for `.dev`, the trust for the `.dev` root (macOS: the per-suffix authorization dialog, per certificate), the bundle, the roots of every other suffix.
- [ ] After it: `curl -sI https://shop.dev` answers with a trusted certificate; `flock status` is green for every suffix row.

## S5 — `flock site tld --inherit` returns

- [ ] Run `flock site tld --inherit shop`. Expect the site back under the global suffix and its `.env` hosts retargeted again.
- [ ] Nothing serves under `.dev` any more, so the sync retires it right there (expect the password / authorization prompts, the mirror of the S1 ones): the resolver file / rule is removed, the `.dev` root is untrusted and `<data>/certs/roots/dev/` deleted — the sweep S3 needed `flock install` for, run inline.
- [ ] Pin-to-the-global case: `flock site tld xyz` on a site while the global suffix is `xyz` prints that the site **pins** `.xyz` (it stops following the global suffix); a later global flip leaves that site and `--inherit` puts it back.
- [ ] Return the machine: global back to `.test` (`flock tld test`), the cast sites left inherited, and `flock install` converges the set. `flock status` green, `curl -sI https://shop.test` trusted, `/etc/resolver/` (macOS) / the drop-in / `Get-DnsClientNrptRule` carry `.test` only, `<data>/certs/roots/` holds no stores outside the active set.

## Definition of done

- [ ] S1–S5 all green on your OS; every expectation checked, deviations recorded in this file (mark the checkbox of a failed step with ⚠ and one line of what happened).
- [ ] The same matrix run on the other two platforms (a Windows machine is the only proof of the NRPT rules, the hosts-helper `--tlds` block and the `certutil` sweep; Linux is the only proof of the drop-in and the anchor rewrite).
- [ ] No privileged step ever runs in CI; failures go to the Plan 3a thread.
