# Local-domain feature — combined smoke runbook

**Branch:** feature/local-domain-selection · **Run by:** the human partner, in a terminal (sudo).
Two parts: **A** is sandboxed to a temp home (safe any time, proves the app picker); **B** is the production-root matrix (needs the DB-server window closed).

## Part A — global TLD + app picker (Plans 1–2), temp home, zero production impact

**App under test:** `build/macos/Flock.app` — build from 431c666, 2026-11-11. The website's
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
Run **h → i → j → g → k → l** — the suffix state matters: (i) needs `.loc` new to the machine, (j) needs `.loc` active.
- [ ] (h) Domain row → Custom `dev` → red `"dev" is a public TLD…`, Apply disabled
- [ ] (i) Domains → Add Domain… → Label `api2`, Suffix `.loc` → Add → `.loc` is new, so a **`Provisioning .loc…`** sheet chains over it (CLI lines stream) → accept the password → both sheets close; `curl -sI https://api2.loc:8443` 200 **with no `-k`** (the chain installed the `.loc` root — trusted, browser clean)
- [ ] (j) Add Domain… → Label `api3`, Suffix `.loc` → Add → **no chain sheet** (the suffix is active — the alias lands at once, sheet closes); `curl -sI https://api3.loc:8443` answers, no `-k`
- [ ] (g) Domain row → `loc` + Apply → `Switching demo to .loc…` streams, no password prompt (`.loc` was provisioned by (i)) → row and address link show `demo.loc`; `curl -skI https://demo.loc:8443` answers; `flock domain list` (temp home): the inherited alias moved along — `demo-2.loc` (the list shows extra domains; the primary `demo.loc` is the row's, and `flock site tld loc demo` echoes `✓ demo → demo.loc`)
- [ ] (k) Domain row → Custom `lab` + Apply → `Switching demo to .lab…` streams → **decline** the password → failed headline, footer names `flock install` ("config is on .lab") → Close → row shows `demo.lab` within the 2 s poll (the config switched); in a tab with `export FLOCK_HOME=/tmp/flock-tld-test`: `./bin/flock install` → accept → `curl -sI https://demo.lab:8443` trusted, no `-k`
- [ ] (l) Domain row → `Inherit (.test)` + Apply → `Switching demo to .test…` → row back to `demo.test` = `<site>.<global>`; `flock site tld --inherit demo` clears the site's pin, the site follows the global suffix again

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
