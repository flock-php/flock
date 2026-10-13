package web

import (
	"encoding/json"
)

// withBind rewrites every server's listen addresses (":80") to host-qualified ones ("127.0.0.1:80").
// The Caddyfile `bind` directive would do this per site, but the catch-all needs it too.
func withBind(cfgJSON []byte, host string) []byte {
	if host == "" {
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
		for i, l := range listen {
			if addr, ok := l.(string); ok && len(addr) > 0 && addr[0] == ':' {
				listen[i] = host + addr
			}
		}
	}

	out, err := json.Marshal(cfg)
	if err != nil {
		return cfgJSON
	}
	return out
}
