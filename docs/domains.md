# Domains & subdomains

## Choosing the local domain

Flock serves every site under one local suffix, the **TLD**. It is `test` by default, so a linked folder `acme` opens at `acme.test`. You can choose your own:

```bash
flock tld         # show the current TLD
flock tld loc     # switch: acme.loc from now on
```

:::app
The macOS app asks for the same choice in **Settings → General → Network**: pick **.test**, **.loc** or **.local**, or type your own under **Custom…**. What you pick is checked against the rules below as you type: refusals show in red and disable **Apply**, warnings in orange, and a normalized name previews in green. **Apply** runs the whole switch, the password prompt included. Decline the password and the config is on the new suffix already; the app tells you to finish with `flock tld` in a terminal.
:::

`loc` is the usual choice. A switch touches everything that names the suffix: site aliases that end in the old one move to the new one (`app-2.test` → `app-2.loc`), the `.env` files of linked sites and worktrees are rewritten (`APP_URL`, `SESSION_DOMAIN`, `SANCTUM_STATEFUL_DOMAINS` and `MAIL_FROM` now name the new hosts), the daemon reloads, the old suffix's DNS setup is retired and the new one is pointed at Flock, and the certificate authority — its root is name-constrained to the TLD — is recreated and re-trusted, so browsers keep trusting your sites' HTTPS. macOS and Linux ask for your password for the DNS and certificate steps, and Windows asks for administrator approval. If you decline a prompt, the config is on the new suffix already; run `flock install` to finish the setup.

What you may choose:

- A single DNS label: 2 to 63 characters of letters, digits and inner hyphens, starting with a letter. Input is normalized, so `flock tld "LOC "` and `flock tld .loc` both mean `loc`.
- Names delegated in the public root zone are refused: sites under `.dev` would swallow the real internet behind that suffix.
- `localhost` and `invalid` are refused; they are reserved for special use.
- `local` is accepted with a warning: mDNS (Bonjour) owns `.local`, and macOS may keep resolving those names itself. Prefer `loc`.

`flock status` shows the current suffix under **dns**. The local API takes the suffix too: `POST /v1/settings` with `{"tld": "loc"}` updates the config and migrates the site aliases; pointing the OS at a new suffix is `flock tld`'s job.

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

Give a site additional domains. A name without a dot is treated as a subdomain of the site, and a full name ending in your TLD is added as an alias.

:::app
In **Settings → Sites**, select the site and click **Add Domain…** under **Domains** in its **Site** section. Enter `tenant1` for `tenant1.acme.test`, or a full name like `acme-admin.test` for a separate alias, and click **Add**.

![Adding a domain in Settings → Sites](screenshot:settings-sites-add-domain)

Extra domains are listed under **Domains**, with aliases marked **(alias)**. To remove one, click the minus button next to it and confirm with **Remove**.
:::

:::cli
```bash
# Run inside the project, or pass the site name as the second argument
flock domain add tenant1          # tenant1.acme.test
flock domain add acme-admin.test  # a completely separate alias
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

Switching the TLD (above) moves all three: the old suffix's setup is retired and the new one takes its place, with the permission prompt the platform requires.

:::app
Flock checks every minute that resolution still works. The **Services** section of the Flock panel shows it with the other services. On Linux, it's under **Network** in **General**.
:::

:::cli
Flock checks every minute that resolution still works, and `flock status` reports it.
:::
