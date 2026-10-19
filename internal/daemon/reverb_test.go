package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/supervisor"
)

func TestReverbSiteRunsReverbStartOrSaysWhyNot(t *testing.T) {
	d := &daemon{root: t.TempDir(), procs: supervisor.New(), restarts: map[string]int{"app": 2}}
	cfg := config.Default()
	cfg.Ports.HTTPS = 8443
	app := t.TempDir()
	cfg.Upsert(config.Site{Name: "app", Path: app, Secured: true, Reverb: true, ReverbPort: freePort(t)})
	site, _ := cfg.FindSite("app")

	// Not a Laravel app with Reverb yet.
	if spec, st := d.reverbSite(cfg, site, "8.4", "https", nil); spec != nil || st.State != api.ReverbUnavailable || !strings.Contains(st.Problem, "install:broadcasting --reverb") {
		t.Fatalf("without Reverb: %v %+v", spec, st)
	}

	for _, file := range []string{"artisan", filepath.Join("vendor", "laravel", "reverb", "composer.json")} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(app, file)), 0o755)
		if err := os.WriteFile(filepath.Join(app, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec, st := d.reverbSite(cfg, site, "8.4", "https", []string{"VAR_DUMPER_FORMAT=server"})
	if spec == nil || st.Problem != "" || st.Log != "reverb-app" || st.Port != site.ReverbPort {
		t.Fatalf("with Reverb: %v %+v", spec, st)
	}
	if want := []string{"REVERB_HOST=app.test", "REVERB_PORT=8443", "REVERB_SCHEME=https"}; !slices.Equal(st.Env, want) {
		t.Errorf(".env lines: %v", st.Env)
	}
	args := strings.Join(spec.Args, " ")
	if !strings.HasPrefix(args, "artisan reverb:start --host=127.0.0.1 --port=") || !strings.Contains(args, "--hostname=127.0.0.1") || spec.Dir != app {
		t.Errorf("command: %s in %s", args, spec.Dir)
	}
	if !slices.Contains(spec.Env, "VAR_DUMPER_FORMAT=server") || !slices.Contains(spec.Env, "FLOCK_RESTART=2") {
		t.Errorf("env: %v", spec.Env)
	}

	// Another site's FrankenPHP configured on the same port.
	cfg.Upsert(config.Site{Name: "other", Path: t.TempDir(), Server: config.ServerFrankenPHP, Port: site.ReverbPort})
	if spec, st := d.reverbSite(cfg, site, "8.4", "https", nil); spec != nil || st.State != api.ReverbFailed || !strings.Contains(st.Problem, "also configured") {
		t.Fatalf("port conflict: %v %+v", spec, st)
	}
}
