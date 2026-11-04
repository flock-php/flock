package web

import (
	"encoding/json"
)

// binds is every host the servers listen on: Bind and the Listen addresses, or none (every
// address) when Bind is empty.
func binds(cfg Config) []string {
	if cfg.Bind == "" {
		return nil
	}
	return append([]string{cfg.Bind}, cfg.Listen...)
}

// withBind rewrites every server's listen addresses (":80") to host-qualified ones ("127.0.0.1:80"),
// one per host. The Caddyfile `bind` directive would do this per site, but the catch-all needs it too.
func withBind(cfgJSON []byte, hosts []string) []byte {
	if len(hosts) == 0 {
		return cfgJSON
	}

	var cfg map[string]any
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return cfgJSON
	}

	apps, _ := cfg["apps"].(map[string]any)
	httpApp, _ := apps["http"].(map[string]any)
	servers, _ := httpApp["servers"].(map[string]any)
	for _, s := range servers {
		srv, _ := s.(map[string]any)
		listen, _ := srv["listen"].([]any)
		var bound []any
		for _, l := range listen {
			addr, ok := l.(string)
			if !ok || len(addr) == 0 || addr[0] != ':' {
				bound = append(bound, l)
				continue
			}
			for _, host := range hosts {
				bound = append(bound, host+addr)
			}
		}
		srv["listen"] = bound
	}

	out, err := json.Marshal(cfg)
	if err != nil {
		return cfgJSON
	}
	return out
}
