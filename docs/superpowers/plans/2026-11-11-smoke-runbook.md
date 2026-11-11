# Local-domain feature — combined smoke runbook

**Branch:** feature/local-domain-selection · **Run by:** the human partner, in a terminal (sudo).
Two parts: **A** is sandboxed to a temp home (safe any time, proves the app picker); **B** is the production-root matrix (needs the DB-server window closed).

## Part A — global TLD + app picker (Plans 1–2), temp home, zero production impact

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

Teardown (order matters — never `flock uninstall` on a temp home):
```bash
security remove-trusted-cert -d /tmp/flock-tld-test/certs/ca.crt
security delete-certificate -Z "$(openssl x509 -in /tmp/flock-tld-test/certs/ca.crt -noout -fingerprint -sha1 | cut -d= -f2 | tr -d :)"
sudo rm -f /etc/resolver/loc
rm -rf /tmp/flock-tld-test /tmp/flock-demo
```

## Part B — per-site domains (Plan 3a), PRODUCTION root

Full matrix: [2026-11-11-per-site-domain-smoke.md](2026-11-11-per-site-domain-smoke.md). Prereqs before S1:
- [ ] DB-server window closed (S1–S5 restart the real daemon, rewrite `.env`s, flip the global TLD in S2)
- [ ] Your linked projects' `.env` changes are committed (retarget rewrites them; S5 restores the hosts)
- [ ] Record the starting state (the matrix's "Record the starting state once" line)
- [ ] `make build` (the CLI binaries the matrix uses must be HEAD-built; the app bundle isn't needed for Part B)

Mark ⚠ + one line on any failed step right in the matrix file — deviations come back to this thread.
