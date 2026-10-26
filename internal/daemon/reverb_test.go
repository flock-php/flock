package daemon

import (
	"maps"
	"net"
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

func TestReleasingAPHPVersionStopsTheReverbServersOnIt(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultPHP = "8.4"
	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir(), Reverb: true})
	cfg.Upsert(config.Site{Name: "old", Path: t.TempDir(), PHP: "8.3", Reverb: true})
	cfg.Upsert(config.Site{Name: "off", Path: t.TempDir()})

	keep := notUsingPHP(cfg, "8.4", []string{"php8.4", "php8.4-xdebug", "php8.3", "dump-server", "reverb-app", "reverb-old", "frankenphp-off"})

	want := map[string]bool{"php8.4": false, "php8.4-xdebug": false, "php8.3": true, "dump-server": false,
		"reverb-app": false, "reverb-old": true, "frankenphp-off": true}
	if !maps.Equal(keep, want) {
		t.Errorf("keep = %v, want %v", keep, want)
	}
}

func TestAnEnabledReverbServerSaysWhyItCantRun(t *testing.T) {
	cfg := config.Default()
	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir(), Reverb: true, ReverbPort: 21000})
	cfg.Upsert(config.Site{Name: "plain", Path: t.TempDir()})
	app, _ := cfg.FindSite("app")
	plain, _ := cfg.FindSite("plain")

	st := reverbBlocked(cfg, app, "http", "PHP 8.4 is not installed")
	if st == nil || st.State != api.ReverbUnavailable || st.Problem != "PHP 8.4 is not installed" || st.Log != "reverb-app" || len(st.Env) != 3 {
		t.Fatalf("blocked: %+v", st)
	}
	if reverbBlocked(cfg, plain, "http", "anything") != nil {
		t.Error("a site with Reverb off has no Reverb status")
	}
}

func TestABusyReverbPortIsAWarningSoTheReloadIsRetried(t *testing.T) {
	d := &daemon{root: t.TempDir(), procs: supervisor.New(), restarts: map[string]int{}}
	cfg := config.Default()
	app := t.TempDir()
	for _, file := range []string{"artisan", filepath.Join("vendor", "laravel", "reverb", "composer.json")} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(app, file)), 0o755)
		if err := os.WriteFile(filepath.Join(app, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	cfg.Upsert(config.Site{Name: "app", Path: app, Reverb: true, ReverbPort: busy.Addr().(*net.TCPAddr).Port})
	site, _ := cfg.FindSite("app")

	spec, st := d.reverbSite(cfg, site, "8.4", "http", nil)
	if spec != nil || st.State != api.ReverbFailed {
		t.Fatalf("busy port: %v %+v", spec, st)
	}
	if w := reverbPreflightWarning("app", st); !strings.HasPrefix(w, "reverb: app: ") {
		t.Errorf("warning = %q", w)
	}
	if w := reverbPreflightWarning("app", api.SiteReverb{State: api.ReverbUnavailable, Problem: "not installed"}); w != "" {
		t.Errorf("a project without Reverb isn't a warning: %q", w)
	}
}
