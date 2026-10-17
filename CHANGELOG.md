# Changelog

## 1.0.0 (2026-10-17)


### Features

* API for native apps (token-protected TCP), mail detail/attachments, Laravel log viewer, dump capture via bundled Symfony dump server ([cbbc655](https://github.com/flock-php/flock/commit/cbbc65514694300989b8a62eee34ef28bbf9db55))
* **app:** brand logomark for the app, menu bar and tray icons; a start by hand opens the main window ([6dfe0a6](https://github.com/flock-php/flock/commit/6dfe0a66f5126cfc2c7cb34e90a437f6a78ba76e))
* **app:** explain the web server and DNS services and show their fixed ports ([4264f1f](https://github.com/flock-php/flock/commit/4264f1fffd98e447f393f1eb332c84971f1ebda0))
* **app:** flock://install links open a confirmed install in Settings → Services ([677623d](https://github.com/flock-php/flock/commit/677623d14247b61ea9e3e12c74259bf4dcaf5c71))
* daemon, CLI and embedded *.test DNS server ([bd18748](https://github.com/flock-php/flock/commit/bd18748ede9e216cb48df066c76cc4faf84836da))
* FrankenPHP and Octane sites, MCP server and Boost guideline, "not secured" page with a secure link ([cd4fa74](https://github.com/flock-php/flock/commit/cd4fa7422b0b75bd9e93ddc9cd727b3a9fe6e013))
* Herd-style Settings window (general, sites, PHP, Node, services, mail, dumps, debugger, about) ([37271de](https://github.com/flock-php/flock/commit/37271de59152ba024166374eb7ea0f7377492062))
* initial version ([8968b04](https://github.com/flock-php/flock/commit/8968b045bbbbe6c99eda356ab40afe0c18b5b55e))
* install PHP from the binaries manifest instead of building it ([d2bea46](https://github.com/flock-php/flock/commit/d2bea4619822d8d3e90e1dab3da7af533f620d59))
* native light/dark theme for the dumps timeline (system fonts and colors, cards, method pills) ([e449712](https://github.com/flock-php/flock/commit/e4497123d651317f2a2807636b80becec89b5391))
* native macOS menu bar app (SwiftUI) with mail, dumps and logs windows ([6964c63](https://github.com/flock-php/flock/commit/6964c634bd49400ea0dfedcd4ef43f6c706dcf67))
* PHP 8.4, Xdebug toggle, Valkey (Redis), psql/pg_dump shims; Flock env overrides inherited PHP_INI_SCAN_DIR; wait for new FPM pools ([ddcae84](https://github.com/flock-php/flock/commit/ddcae84451ab03c95bf86413fe6e90ba9c7e3a5d))
* PHP pane installs, updates and builds PHP from the UI; build workspace in Caches (no spaces), abort on failed downloads, explain GitHub rate limits ([c78d4c1](https://github.com/flock-php/flock/commit/c78d4c164846d90b66d909bacd056bfd72c3a364))
* PHP releases from php.net, buildability signals, background build manager (auto patch/new-version builds, instant installs), install/update/uninstall API ([58cceaa](https://github.com/flock-php/flock/commit/58cceaabc0f126bfe5ebc27a5e3a3ad6aa47c898))
* **php:** offer PHP on Windows only with its extension pack ([a9838a4](https://github.com/flock-php/flock/commit/a9838a427476cad9b50afb0ec77f9dce857155f9))
* PostgreSQL and its client tools from the binaries manifest ([45107e7](https://github.com/flock-php/flock/commit/45107e78b93b9f4e92f9ba019fca2b9c2f58cda0))
* release channels for prebuilt packages, FrankenPHP from the new manifest ([ab4e3f1](https://github.com/flock-php/flock/commit/ab4e3f1de68918db340661a6db894b711fb8f741))
* service catalog — install MySQL, MariaDB, MongoDB, Redis, Meilisearch, Typesense and RustFS from Settings → Services ([8a1365a](https://github.com/flock-php/flock/commit/8a1365a362109b61fc069c3a76ba90e8653a596b))
* services, Valkey and Node.js from the binaries manifest ([c541ef6](https://github.com/flock-php/flock/commit/c541ef67b045e27a476cdd9d187c872b1bd4d859))
* **services:** install versions of every service side by side; Sites and Services rework ([#6](https://github.com/flock-php/flock/issues/6)) ([d5fd5ef](https://github.com/flock-php/flock/commit/d5fd5ef8964c9f0523499ae3c11aca43d781cc7a))
* settings API — config view, link/patch/unlink sites, parked dirs, default PHP/Node, Node install, services with start/stop and ports ([f96617c](https://github.com/flock-php/flock/commit/f96617c726366c04bbb9c6ba7947530b8b90b105))
* show the PHP extensions a site requires, with hints for missing requirements ([5b77086](https://github.com/flock-php/flock/commit/5b77086036d91ed79526b8b9f13b8d6f00ba2f25))
* sites, PHP-FPM, embedded Caddy with local CA, mail catcher, Node, PostgreSQL, shims and macOS install ([fea4db4](https://github.com/flock-php/flock/commit/fea4db400c418843db393e63b9b463ed5d01cf6a))
* survive busy ports with explanatory warnings, apply port changes on reload, flock restart/logs, Vite HTTPS for secured sites ([4b13eea](https://github.com/flock-php/flock/commit/4b13eea190b3c08e53c8942caa8f447b170725ee))
* terminal-style dumps timeline (latest request only, search, open in editor), Blade source mapping; fix split view toolbar overflow ([2792780](https://github.com/flock-php/flock/commit/279278018459329c18acb16067cddd3b07edc53e))
* wildcard subdomains on Windows, extra domains per site, Node versions, isolate/pin, Windows installer ([dc961d9](https://github.com/flock-php/flock/commit/dc961d972f1aa78a111f22298cb8e33d79d404f2))
* Xdebug pool for requests that ask for it, "not linked" page, blocked hosts helper on Windows ([e829e6b](https://github.com/flock-php/flock/commit/e829e6b6ed86c009da41ec98d8afe8120238cd18))


### Bug Fixes

* **api:** attribute dumps to sites whatever slashes and drive-letter case PHP reports on Windows ([625e0a6](https://github.com/flock-php/flock/commit/625e0a628466ac93b4fa8061ada71d5a43246cec))
* **app:** don't percent-encode site names twice in macOS API requests ([cf72e18](https://github.com/flock-php/flock/commit/cf72e18eb36051d8c1454f141c602784b6e9341e))
* **ci:** Windows restore with VERSION set, Swift 6.3 compiler crash in GeneralPane ([56e0e15](https://github.com/flock-php/flock/commit/56e0e15a5d25656e8ca4a00f54b2e5f5faa9c4e8))
* **cli:** don't ask about purging data when the app runs uninstall --keep-app ([23ac4fa](https://github.com/flock-php/flock/commit/23ac4fa7e736868551d11d6e1ce193a7463feb67))
* **daemon:** report PostgreSQL as available ([859bcde](https://github.com/flock-php/flock/commit/859bcde5a28ebf23d8d565e532571993350133af))
* mail detail always returns cc/to arrays ([ff32c03](https://github.com/flock-php/flock/commit/ff32c033f1b30045c853865000b49724cf05d793))
* **php:** load ldap, shmop and soap on Windows too, as on macOS ([7eb2893](https://github.com/flock-php/flock/commit/7eb28938a71de9cc7b0cac1cd007675c67e39d9e))
* print regular output to stdout ([0893ae2](https://github.com/flock-php/flock/commit/0893ae256307f5d9c505e5188032fb5ea76b553a))
* shims pass absolute argv0 so PHP_BINARY is Flock's PHP; wait for launchd on reinstall ([08f8532](https://github.com/flock-php/flock/commit/08f8532f00b6545fb7014538422f9f7a91b3b04f))


### Refactor

* remove what only older Flock states and local builds needed ([6ea33a1](https://github.com/flock-php/flock/commit/6ea33a15291a59813c42a64e17af8a5a244db34e))


### Style

* gofmt ([aff3f6d](https://github.com/flock-php/flock/commit/aff3f6d2a8b0a9b254802d04be25ff3ff16bff04))
