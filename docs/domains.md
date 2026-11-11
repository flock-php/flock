# Domains & subdomains

## Choosing the local domain

Flock serves every site under one local suffix, the **TLD**. It is `test` by default, so a linked folder `acme` opens at `acme.test`. You can choose your own:

```bash
flock tld         # show the current TLD
flock tld loc     # switch: acme.loc from now on
```

:::app
The macOS app asks for the same choice in **Settings → General → Network**: pick **.test**, **.loc** or **.local**, or type your own under **Custom…**. What you pick is checked against the rules below as you type: refusals show in red and disable **Apply**, warnings in orange, and a normalized name previews in green. **Apply** runs the whole switch, the password prompt included. Decline the password and the config is on the new suffix already; the app tells you to run `flock install` to finish the setup.
:::

`loc` is the usual choice. A switch touches everything that names the suffix: the aliases that follow the sites move to the new one (`app-2.test` → `app-2.loc`), the `.env` files of linked sites and worktrees are rewritten (`APP_URL`, `SESSION_DOMAIN`, `SANCTUM_STATEFUL_DOMAINS`, `MAIL_FROM` and Laravel's `MAIL_FROM_ADDRESS` now name the new hosts), the daemon reloads, a suffix nothing serves under anymore is retired, and the new one is pointed at Flock with its own name-constrained certificate authority, recreated and re-trusted, so browsers keep trusting your sites' HTTPS. Sites that carry a suffix of their own, and aliases pinned to a suffix, never move (below). macOS and Linux ask for your password for the DNS and certificate steps, and Windows asks for administrator approval. If you decline a prompt, the config is on the new suffix already; run `flock install` to finish the setup.

What you may choose:

- A single DNS label: 2 to 63 characters of letters, digits and inner hyphens, starting with a letter. Input is normalized, so `flock tld "LOC "` and `flock tld .loc` both mean `loc`.
- Names delegated in the public root zone are refused: sites under `.dev` would swallow the real internet behind that suffix.
- `localhost` and `invalid` are refused; they are reserved for special use.
- `local` is accepted with a warning: mDNS (Bonjour) owns `.local`, and macOS may keep resolving those names itself. Prefer `loc`.

`flock status` shows the current suffix under **dns** — one row for every suffix in use. The local API takes the suffix too: `POST /v1/settings` with `{"tld": "loc"}` updates the config and moves the aliases that follow (inherited); explicit ones stay. Pointing the OS at a new suffix is `flock tld`'s job.

### Per-site domains

A site follows the global suffix by default: `<folder>.<global TLD>`. Give one site a suffix of its own:

```bash
flock site tld                 # show what the current site is served under
flock site tld loc             # this site → acme.loc (provisioning asks for your password)
flock site tld loc demo        # the named site
flock site tld --inherit       # follow the global suffix again (the default)
```

The suffix obeys the same rules as the global one (above). The switch retargets the site's and its worktrees' `.env` files, moves the aliases that follow the site, and provisions the new suffix on the spot. A site with a suffix of its own is never dragged by a global `flock tld` change, and neither is an alias pinned to a suffix.

Pinning lives in the extra domains too:

```bash
flock domain add shop                   # a subdomain: shop.acme.loc
flock domain add shop --suffix loc      # shop.loc, pinned: it follows nothing
flock domain add shop --suffix inherit  # an alias that rides the site's suffix
```

The rule: a full name ending in the site's suffix is an inherited alias — it moves with the site. `--suffix loc` (and any full name under a new non-public suffix, like `flock domain add shop.loc` on a `.test` site) pins the alias to that suffix: no global switch and no site switch ever moves it, not even when the global suffix later becomes `.loc` itself.

Every suffix in use is provisioned in full: DNS routing per suffix, and a dedicated name-constrained CA root per suffix (`certs/roots/<suffix>/`), each trusted in its own right by the OS. `flock install` converges the whole set — DNS, roots, trust and the PHP/Node bundles — so it is the command that finishes a declined prompt and clears the daemon's "stale .loc setup: run \`flock install\`" warning when the last site under a suffix is gone.

The macOS app takes the same choices. The **Domain** row in the **Site** section of **Settings → Sites** picks the site's suffix, **Apply** runs the whole switch, password prompt included, and a declined prompt leaves the config switched: the app tells you to run `flock install`. **Add Domain…** is a sheet that enters the alias's label (`tenant1`, `a.b`) and its suffix: **Inherit** rides the site's suffix and moves with it, a picked suffix pins the alias. A suffix new to the machine is provisioned on the spot — the sheet chains `flock install`, prompts included; decline the password and the alias is added already, and `flock install` finishes it. The API takes the same choices: `PATCH /v1/sites/{name}` with `{"tld": "loc"}` (`""` inherits, an omitted key keeps the current suffix), `POST /v1/sites/{name}/domains` with `{"domain": "demo2", "suffix": "loc"}` (`"inherit"` for an inherited alias), and `POST /v1/tld/check`, which normalizes and validates a suffix as you type it.

## Wildcard subdomains

Every site answers on its subdomains automatically, up to four levels deep. If `acme.test` is a site, all of these reach it:

```text
tenant1.acme.test
api.acme.test
eu.tenant1.acme.test
```

This works for multi-tenant apps and subdomain routing without any configuration, on every platform.

On Windows, a group policy or a VPN can override the rule that sends `*.<tld>` to Flock. Then only the site names and the extra domains you add (below) resolve, through the hosts file, and Flock tells you so. Linux has no such fallback: if a VPN or another resolver takes over your TLD's lookups, sites don't open until it's fixed, and Flock tells you that too.

## Extra domains and aliases

Give a site additional domains. A name without a dot is treated as a subdomain of the site, and a full name ending in the site's suffix is added as an alias that follows the site. `--suffix` adds the alias under a suffix of its own: pinned, or `inherit` to follow the site ([Per-site domains](#per-site-domains)).

:::app
In **Settings → Sites**, select the site and click **Add Domain…** under **Domains** in its **Site** section. Enter `tenant1` for `tenant1.acme.test`, and click **Add**. On Windows and Linux a full name like `acme-admin.test` adds a separate alias; on macOS the sheet pairs a label with a suffix: **Inherit** rides the site's suffix and moves with it, a picked suffix pins the alias, and a suffix new to the machine is provisioned on the spot ([Per-site domains](#per-site-domains)).

![Adding a domain in Settings → Sites](screenshot:settings-sites-add-domain)

Extra domains are listed under **Domains**, with aliases marked **(alias)**. To remove one, click the minus button next to it and confirm with **Remove**.
:::

:::cli
```bash
# Run inside the project, or pass the site name as the second argument
flock domain add tenant1          # tenant1.acme.test
flock domain add acme-admin.test  # a completely separate alias
flock domain add shop --suffix loc    # shop.loc, pinned: it follows nothing
flock domain list
flock domain remove acme-admin.test
```
:::

An alias can also sit under another site's domain. If another project has the alias `accounts.acme.test`, that name and its subdomains reach that project, and the rest of `*.acme.test` still reaches `acme`.

## How your TLD resolves

Flock runs its own small DNS server, so new sites resolve instantly without changing your hosts file:

- **macOS:** `/etc/resolver/<tld>` sends `.<tld>` lookups to Flock's DNS server on port 53535. Because the port is unprivileged, the DNS server doesn't need root.
- **Windows:** a name-resolution policy rule sends `*.<tld>` to Flock. A small helper service also writes your sites into a `# BEGIN Flock` block in the hosts file as a fallback.
- **Linux:** a systemd-resolved drop-in, `/etc/systemd/resolved.conf.d/flock.conf`, sends only `.<tld>` lookups to Flock's DNS server on port 53535.

Every suffix Flock serves sites under gets its own piece: a resolver file per suffix on macOS, one name-resolution rule per suffix on Windows, all suffixes listed in the one drop-in on Linux. Switching the TLD (above) moves all three: a suffix nothing serves under anymore is retired and the new one takes its place, with the permission prompt the platform requires.

:::app
Flock checks every minute that resolution still works. The **Services** section of the Flock panel shows it with the other services. On Linux, it's under **Network** in **General**.
:::

:::cli
Flock checks every minute that resolution still works, and `flock status` reports it.
:::
