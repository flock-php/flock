# Local-domain feature — combined smoke runbook

**Branch:** feature/local-domain-selection · **Run by:** the human partner, in a terminal (sudo).
Two parts: **A** is sandboxed to a temp home (safe any time, proves the app picker); **B** is the production-root matrix (needs the DB-server window closed).

## Part A — global TLD + app picker (Plans 1–2), temp home, zero production impact

**App under test:** `build/macos/Flock.app` — build from 7a0b91f (code), 2026-11-12 (classes a–l run on it unchanged; m–o need it: the name/domain pencils). The website's
`settings-sites-add-domain` screenshot is stale: the PNGs show the old alert, shot before the
Label + Suffix sheet — regen deferred to the website import (human).

```bash
cd ~/DEV/flock
export FLOCK_HOME=/tmp/flock-tld-test
rm -rf $FLOCK_HOME /tmp/flock-demo; mkdir -p /tmp/flock-demo/public
echo '<?php phpinfo(); ?>' > /tmp/flock-demo/public/index.php
printf 'APP_URL=http://demo.test\nSESSION_DOMAIN=demo.test\nSANCTUM_STATEFUL_DOMAINS=localhost,demo.test:5173\nMAIL_FROM=hi@demo.test\n' > /tmp/flock-demo/.env
./bin/flock link demo && ./bin/flock domain add demo-2.test demo   # run inside /tmp/flock-demo for link
python3 - <<'EOF'
import json
p='/tmp/flock-tld-test/config.json'; c=json.load(open(p))
c['ports']={"http":8080,"https":8443,"dns":53953,"smtp":2526,"postgres":5433,"dumps":9922,"agent":9923,"api":53541}
json.dump(c,open(p,'w'),indent=2)
EOF
./bin/flock daemon                                                   # tab 1
open -n --env FLOCK_HOME=/tmp/flock-tld-test build/macos/Flock.app   # fresh bundle with the picker
```

Checklist in the app (Settings → General → Network → Domain):
- [ ] `loc` → Apply → accept the prompts → `/etc/resolver/loc` shows port 53953; `curl -skI https://demo.loc:8443` 200, alias `demo-2.loc`; `.env` rewritten; demo-2.test → demo-2.loc
- [ ] type `dev` in Custom → red "public TLD", Apply disabled
- [ ] type `LOC ` → green `→ .loc` preview
- [ ] `local` → orange mDNS warning, Apply enabled
- [ ] decline a password mid-Apply → sheet keeps CLI lines, footer names `flock install`, config switched
- [ ] back to `test` → `/etc/resolver/test` restored (port 53535 — matches production), production Flock fine

Per-site Domain row + Add Domain sheet (Plan 3b), Settings → Sites → select `demo`.
Run **h → i → j → g → k → l → m → n → o** — the suffix state matters: (i) needs `.loc` new to the machine, (j) needs `.loc` active. m–o run off (l)'s exit: `demo` inherited back on `.test`, aliases `api2.loc`/`api3.loc` still pinned from (i)/(j).
- [ ] (h) Domain row → Custom `dev` → red `"dev" is a public TLD…`, Apply disabled
- [ ] (i) Domains → Add Domain… → Label `api2`, Suffix `.loc` → Add → `.loc` is new, so a **`Provisioning .loc…`** sheet chains over it (CLI lines stream) → accept the password → both sheets close; `curl -sI https://api2.loc:8443` 200 **with no `-k`** (the chain installed the `.loc` root — trusted, browser clean)
- [ ] (j) Add Domain… → Label `api3`, Suffix `.loc` → Add → **no chain sheet** (the suffix is active — the alias lands at once, sheet closes); `curl -sI https://api3.loc:8443` answers, no `-k`
- [ ] (g) Domain row → `loc` + Apply → `Switching demo to .loc…` streams, no password prompt (`.loc` was provisioned by (i)) → row and address link show `demo.loc`; `curl -skI https://demo.loc:8443` answers; `flock domain list` (temp home): the inherited alias moved along — `demo-2.loc` (the list shows extra domains; the primary `demo.loc` is the row's, and `flock site tld loc demo` echoes `✓ demo → demo.loc`)
- [ ] (k) Domain row → Custom `lab` + Apply → `Switching demo to .lab…` streams → **decline** the password → failed headline, footer names `flock install` ("config is on .lab") → Close → row shows `demo.lab` within the 2 s poll (the config switched); in a tab with `export FLOCK_HOME=/tmp/flock-tld-test`: `./bin/flock install` → accept → `curl -sI https://demo.lab:8443` trusted, no `-k`
- [ ] (l) Domain row → `Inherit (.test)` + Apply → `Switching demo to .test…` → row back to `demo.test` = `<site>.<global>`; `flock site tld --inherit demo` clears the site's pin, the site follows the global suffix again
- [ ] (m) Rename the domain name (the label, not the suffix): the pencil next to the **URL** (`Change the domain name…`) → `shop` → Rename → **`Renaming the domain…`** sheet streams the bundled CLI's `flock site host shop demo` — `✓ /tmp/flock-demo/.env: APP_URL, SESSION_DOMAIN, SANCTUM_STATEFUL_DOMAINS, MAIL_FROM`, then `✓ demo → shop.test from now on` → both sheets close on their own; the list row and the header link read `shop.test:8443`; `curl -skI https://shop.test:8443` 200; `curl -skI https://demo.test:8443` now 404s on Flock's unlinked page (header `X-Flock-Page: unlinked` — the `.test` wildcard still resolves the old host, the daemon just doesn't route it). `grep shop.test /tmp/flock-demo/.env` → all four keys name `shop.test`, schemes and ports kept (`APP_URL=http://shop.test`, `SESSION_DOMAIN=shop.test`, `SANCTUM_STATEFUL_DOMAINS=localhost,shop.test:5173`, `MAIL_FROM=hi@shop.test`), `grep demo.test` empty. The pencil again → a **Use the folder name** button appears only once an override is set (`site.host` non-empty) → `flock site host --default demo` streams → `✓ demo → demo.test from now on`, `.env` swapped back, `curl -skI https://demo.test:8443` answers again. No password prompt anywhere: a rename moves no suffix, so nothing is provisioned.
- [ ] (n) A rename keeps aliases, moves subdomains. Before touching the host again (state = (m)'s exit: primary `demo.test`, no override): Subdomains field → **Add Subdomain…** → `t1` → Add → the row lists `t1` and `curl -skI https://t1.demo.test:8443` answers; confirm (j)'s pinned alias is still there (it survived (g)/(k)/(l) because a picked suffix pins an alias absolutely): `curl -skI https://api3.loc:8443` answers, no `-k`. Now rename via one CLI line in the (k) tab — `cd ~/DEV/flock && ./bin/flock site host shop demo` → `✓ demo → shop.test from now on`. After it: `curl -skI https://t1.shop.test:8443` answers (the subdomain rode the name), `curl -skI https://t1.demo.test:8443` 404s unlinked, and `curl -skI https://api3.loc:8443` **still** answers — the alias is a name of its own and never moves. Leave it renamed: (o) runs on `shop.test`.
- [ ] (o) Display title: the pencil next to the **name** (`Rename…`) → `Demo CMS` → Rename → both the list row and the header heading show **Demo CMS**; the URL/domain are untouched — `curl -skI https://shop.test:8443` answers exactly as in (n) (the site *name* `demo` is what the folder and `flock` commands still key on; only the display string changed). Empty the field → Rename (hint: "Leave empty to inherit the folder name.") → the row and header fall back to `demo`, URL still `shop.test:8443`, `.env` still `shop.test`. Refusal path: the sheet validates nothing client-side and the title touches no host, cert or `.env` — nothing to refuse here, skip.

Teardown impact of m–o: none beyond (i)/(k) — host and title rename no suffix (`ActiveSuffixes` reads the TLD fields only), so no new roots or `/etc/resolver` entries; run it exactly as printed below.

Teardown (order matters — never `flock uninstall` on a temp home):
```bash
security remove-trusted-cert -d /tmp/flock-tld-test/certs/ca.crt
security delete-certificate -Z "$(openssl x509 -in /tmp/flock-tld-test/certs/ca.crt -noout -fingerprint -sha1 | cut -d= -f2 | tr -d :)"
# per-suffix roots, one pair of lines per extra root the matrix provisioned (loc, lab):
security remove-trusted-cert -d /tmp/flock-tld-test/certs/roots/loc/ca.crt
security delete-certificate -Z "$(openssl x509 -in /tmp/flock-tld-test/certs/roots/loc/ca.crt -noout -fingerprint -sha1 | cut -d= -f2 | tr -d :)"
security remove-trusted-cert -d /tmp/flock-tld-test/certs/roots/lab/ca.crt
security delete-certificate -Z "$(openssl x509 -in /tmp/flock-tld-test/certs/roots/lab/ca.crt -noout -fingerprint -sha1 | cut -d= -f2 | tr -d :)"
sudo rm -f /etc/resolver/loc /etc/resolver/lab
rm -rf /tmp/flock-tld-test /tmp/flock-demo
```

## Part B — per-site domains (Plan 3a), PRODUCTION root

Full matrix: [2026-11-11-per-site-domain-smoke.md](2026-11-11-per-site-domain-smoke.md). Prereqs before S1:
- [ ] DB-server window closed (S1–S5 restart the real daemon, rewrite `.env`s, flip the global TLD in S2)
- [ ] Your linked projects' `.env` changes are committed (retarget rewrites them; S5 restores the hosts)
- [ ] Record the starting state (the matrix's "Record the starting state once" line)
- [ ] `make build` (the CLI binaries the matrix uses must be HEAD-built; the app bundle isn't needed for Part B)

Mark ⚠ + one line on any failed step right in the matrix file — deviations come back to this thread.
