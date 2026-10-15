# Changelog

## 1.0.0 (2026-10-15)


### Features

* API for native apps (token-protected TCP), mail detail/attachments, Laravel log viewer, dump capture via bundled Symfony dump server ([cbbc655](https://github.com/flock-php/flock/commit/cbbc65514694300989b8a62eee34ef28bbf9db55))
* daemon, CLI and embedded *.test DNS server ([bd18748](https://github.com/flock-php/flock/commit/bd18748ede9e216cb48df066c76cc4faf84836da))
* Herd-style Settings window (general, sites, PHP, Node, services, mail, dumps, debugger, about) ([37271de](https://github.com/flock-php/flock/commit/37271de59152ba024166374eb7ea0f7377492062))
* initial version ([8968b04](https://github.com/flock-php/flock/commit/8968b045bbbbe6c99eda356ab40afe0c18b5b55e))
* native light/dark theme for the dumps timeline (system fonts and colors, cards, method pills) ([e449712](https://github.com/flock-php/flock/commit/e4497123d651317f2a2807636b80becec89b5391))
* native macOS menu bar app (SwiftUI) with mail, dumps and logs windows ([6964c63](https://github.com/flock-php/flock/commit/6964c634bd49400ea0dfedcd4ef43f6c706dcf67))
* PHP 8.4, Xdebug toggle, Valkey (Redis), psql/pg_dump shims; Flock env overrides inherited PHP_INI_SCAN_DIR; wait for new FPM pools ([ddcae84](https://github.com/flock-php/flock/commit/ddcae84451ab03c95bf86413fe6e90ba9c7e3a5d))
* PHP pane installs, updates and builds PHP from the UI; build workspace in Caches (no spaces), abort on failed downloads, explain GitHub rate limits ([c78d4c1](https://github.com/flock-php/flock/commit/c78d4c164846d90b66d909bacd056bfd72c3a364))
* PHP releases from php.net, buildability signals, background build manager (auto patch/new-version builds, instant installs), install/update/uninstall API ([58cceaa](https://github.com/flock-php/flock/commit/58cceaabc0f126bfe5ebc27a5e3a3ad6aa47c898))
* service catalog — install MySQL, MariaDB, MongoDB, Redis, Meilisearch, Typesense and RustFS from Settings → Services ([8a1365a](https://github.com/flock-php/flock/commit/8a1365a362109b61fc069c3a76ba90e8653a596b))
* settings API — config view, link/patch/unlink sites, parked dirs, default PHP/Node, Node install, services with start/stop and ports ([f96617c](https://github.com/flock-php/flock/commit/f96617c726366c04bbb9c6ba7947530b8b90b105))
* sites, PHP-FPM, embedded Caddy with local CA, mail catcher, Node, PostgreSQL, shims and macOS install ([fea4db4](https://github.com/flock-php/flock/commit/fea4db400c418843db393e63b9b463ed5d01cf6a))
* survive busy ports with explanatory warnings, apply port changes on reload, flock restart/logs, Vite HTTPS for secured sites ([4b13eea](https://github.com/flock-php/flock/commit/4b13eea190b3c08e53c8942caa8f447b170725ee))
* terminal-style dumps timeline (latest request only, search, open in editor), Blade source mapping; fix split view toolbar overflow ([2792780](https://github.com/flock-php/flock/commit/279278018459329c18acb16067cddd3b07edc53e))
* wildcard subdomains on Windows, extra domains per site, Node versions, isolate/pin, Windows installer ([dc961d9](https://github.com/flock-php/flock/commit/dc961d972f1aa78a111f22298cb8e33d79d404f2))


### Bug Fixes

* **ci:** Windows restore with VERSION set, Swift 6.3 compiler crash in GeneralPane ([56e0e15](https://github.com/flock-php/flock/commit/56e0e15a5d25656e8ca4a00f54b2e5f5faa9c4e8))
* **daemon:** report PostgreSQL as available ([859bcde](https://github.com/flock-php/flock/commit/859bcde5a28ebf23d8d565e532571993350133af))
* mail detail always returns cc/to arrays ([ff32c03](https://github.com/flock-php/flock/commit/ff32c033f1b30045c853865000b49724cf05d793))
* print regular output to stdout ([0893ae2](https://github.com/flock-php/flock/commit/0893ae256307f5d9c505e5188032fb5ea76b553a))
* shims pass absolute argv0 so PHP_BINARY is Flock's PHP; wait for launchd on reinstall ([08f8532](https://github.com/flock-php/flock/commit/08f8532f00b6545fb7014538422f9f7a91b3b04f))


### Style

* gofmt ([aff3f6d](https://github.com/flock-php/flock/commit/aff3f6d2a8b0a9b254802d04be25ff3ff16bff04))
