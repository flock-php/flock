# HTTPS

Flock can serve any site over HTTPS with a certificate your browser trusts, so you get no warnings and no `--insecure` flags.

## Securing a site

:::app
In **Settings → Sites**, select the site and switch on **HTTPS** in its **Site** section. Switch it off to go back to plain HTTP. Secured sites show a lock in the **Sites** section of the Flock panel.

![The HTTPS switch in Settings → Sites](screenshot:settings-sites-https)
:::

:::cli
```bash
flock secure acme
```

Run it without a name inside the project folder. To go back to plain HTTP:

```bash
flock unsecure acme
```

You can also secure a site while linking it with `flock link --secure`.
:::

> Remember to update `APP_URL` in `.env` to `https://…`.

A secured site redirects plain `http://` requests to `https://`. If you open a site that isn't secured over `https://`, Flock shows a page with a **Secure in Flock** button instead of a certificate error.

## The local certificate authority

When you install Flock, it creates its own certificate authority (CA) and adds it to your system's trust store: your login keychain on macOS, Trusted Root on Windows.

The CA's root certificate is **name-constrained** to your local TLD and `localhost`. Even if its private key ever leaked, it could not be used to issue certificates for real websites.

Each secured site gets a certificate for `acme.test` and `*.acme.test`. Certificates for deeper subdomains and for the site's aliases are issued on demand the first time they're requested.

## Outgoing HTTPS from PHP

Flock's `php.ini` points PHP at a CA bundle that includes Flock's root. So when one local app calls another over HTTPS, for example `Http::get('https://api.test')`, the certificate is trusted there too.

## Node.js and other tools

Browsers and PHP trust the CA automatically, and so do `node`, `npm` and `npx`: Flock's shims point them at the root certificate with `NODE_EXTRA_CA_CERTS`. Only a Node process started some other way needs that variable set by hand. The certificate is `certs/ca.crt` in Flock's [data directory](configuration.md#data-directory).

## Vite over HTTPS

In a secured site, `npm run dev` serves Vite over HTTPS without any changes to `vite.config.js`: the shims hand the site's certificate to `laravel-vite-plugin` through `VITE_DEV_SERVER_KEY` and `VITE_DEV_SERVER_CERT`. Vite takes the host from `APP_URL`, so set it to `https://acme.test`.
