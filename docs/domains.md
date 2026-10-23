# Domains & subdomains

## Wildcard subdomains

Every site answers on its subdomains automatically, up to four levels deep. If `acme.test` is a site, all of these reach it:

```text
tenant1.acme.test
api.acme.test
eu.tenant1.acme.test
```

This works for multi-tenant apps and subdomain routing without any configuration, on Windows too.

On Windows, a group policy or a VPN can override the rule that sends `*.test` to Flock. Then only the site names and the extra domains you add (below) resolve, through the hosts file, and Flock tells you so.

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

## How .test resolution works

Flock runs its own small DNS server, so new sites resolve instantly without changing your hosts file:

- **macOS:** `/etc/resolver/test` sends `.test` lookups to Flock's DNS server on port 53535. Because the port is unprivileged, the DNS server doesn't need root.
- **Windows:** a name-resolution policy rule sends `*.test` to Flock. A small helper service also writes your sites into a `# BEGIN Flock` block in the hosts file as a fallback.

:::app
Flock checks every minute that resolution still works. The **Services** section of the Flock panel shows it with the other services.
:::

:::cli
Flock checks every minute that resolution still works, and `flock status` reports it.
:::
