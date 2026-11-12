package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/config"
)

func TestDatabaseURL(t *testing.T) {
	writeEnv := func(t *testing.T, dir, env string) *config.Site {
		t.Helper()
		root := filepath.Join(dir, "public")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
			t.Fatal(err)
		}
		return &config.Site{Name: "acme", Path: dir}
	}

	for _, tc := range []struct {
		name, env, want string
	}{
		{"postgres", "DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_PORT=5432\nDB_DATABASE=acme\nDB_USERNAME=postgres\nDB_PASSWORD=secret",
			"postgres://postgres:secret@127.0.0.1:5432/acme"},
		{"mysql without a password", "DB_CONNECTION=mysql\nDB_DATABASE=acme\nDB_USERNAME=root",
			"mysql://root@127.0.0.1:3306/acme"},
		{"quotes and an empty port", "DB_CONNECTION=pgsql\nDB_HOST=\"localhost\"\nDB_DATABASE='acme'\nDB_USERNAME=postgres\nDB_PASSWORD=",
			"postgres://postgres@localhost:5432/acme"},
		{"sqlite", "DB_CONNECTION=sqlite\nDB_DATABASE=database/database.sqlite", "sqlite:database/database.sqlite"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := databaseURL(writeEnv(t, t.TempDir(), tc.env))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("a site served from a subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		site := writeEnv(t, dir, "DB_CONNECTION=pgsql\nDB_DATABASE=acme")
		// Only public/ is served; without a .env of its own, the project's counts.
		site.Root = "public"
		got, err := databaseURL(site)
		if err != nil {
			t.Fatal(err)
		}
		if want := "postgres://127.0.0.1:5432/acme"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no .env", func(t *testing.T) {
		if _, err := databaseURL(&config.Site{Path: t.TempDir()}); err == nil {
			t.Fatal("no error without a .env")
		}
	})
}

// Every DB_CONNECTION Flock knows makes a URL, with the scheme and default port its client needs.
func TestConnectionURLDrivers(t *testing.T) {
	for _, tc := range []struct{ connection, database, want string }{
		{"pgsql", "acme", "postgres://postgres:secret@127.0.0.1:5432/acme"},
		{"postgres", "acme", "postgres://postgres:secret@127.0.0.1:5432/acme"},
		{"postgresql", "acme", "postgres://postgres:secret@127.0.0.1:5432/acme"},
		{"mysql", "acme", "mysql://postgres:secret@127.0.0.1:3306/acme"},
		{"mariadb", "acme", "mysql://postgres:secret@127.0.0.1:3306/acme"},
		{"mongodb", "acme", "mongodb://postgres:secret@127.0.0.1:27017/acme"},
		{"sqlite", "/tmp/acme.sqlite", "sqlite:/tmp/acme.sqlite"},
	} {
		got, err := connectionURL("acme", map[string]string{
			"DB_CONNECTION": tc.connection, "DB_DATABASE": tc.database,
			"DB_USERNAME": "postgres", "DB_PASSWORD": "secret",
		})
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.connection, got, err, tc.want)
		}
	}

	if _, err := connectionURL("acme", map[string]string{"DB_CONNECTION": "oracle", "DB_DATABASE": "acme"}); err == nil {
		t.Error("an unknown DB_CONNECTION made a URL")
	}
}

func TestSiteURL(t *testing.T) {
	cfg := &config.Config{TLD: "test"}
	cfg.Ports.HTTP, cfg.Ports.HTTPS = 80, 443
	if got := siteURL(cfg, config.Site{Name: "acme"}); got != "http://acme.test" {
		t.Errorf("got %q", got)
	}
	if got := siteURL(cfg, config.Site{Name: "acme", Secured: true}); got != "https://acme.test" {
		t.Errorf("got %q", got)
	}
	cfg.Ports.HTTP, cfg.Ports.HTTPS = 8080, 8443
	if got := siteURL(cfg, config.Site{Name: "acme"}); got != "http://acme.test:8080" {
		t.Errorf("got %q", got)
	}
	if got := siteURL(cfg, config.Site{Name: "acme", Secured: true}); got != "https://acme.test:8443" {
		t.Errorf("got %q", got)
	}
}

func TestMaskedURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"postgres://postgres:secret@127.0.0.1:5432/acme", "postgres://postgres:****@127.0.0.1:5432/acme"},
		{"mysql://root@127.0.0.1:3306/acme", "mysql://root@127.0.0.1:3306/acme"},
		{"sqlite:/tmp/acme/database.sqlite", "sqlite:/tmp/acme/database.sqlite"},
		{"postgres://a%40b:p%40ss@127.0.0.1/acme", "postgres://a%40b:****@127.0.0.1/acme"},
	} {
		if got := maskedURL(tc.in); got != tc.want {
			t.Errorf("maskedURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A password with a $ or ' reaches the client whole, even when the client is a shell line.
func TestRunClientKeepsTheURLWhole(t *testing.T) {
	out := filepath.Join(t.TempDir(), "got")
	url := "postgres://u:p$a'ss@h/db"

	// A client with %s is a shell line, and the URL reaches it as $0: a $ or ' in the password is
	// neither expanded nor able to end the command.
	client := `printf "$0" > ` + out + ` %s`
	if err := runClient(client, url); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != url {
		t.Errorf("shell client got %q, want %q", got, url)
	}
}

func TestSplitArgs(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{`open -a TablePlus`, []string{"open", "-a", "TablePlus"}},
		{`open -a "Table Plus"`, []string{"open", "-a", "Table Plus"}},
		{`open -a 'Table Plus' --flag`, []string{"open", "-a", "Table Plus", "--flag"}},
	} {
		got, err := splitArgs(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q = %q, want %q", tc.in, got, tc.want)
		}
	}

	for _, in := range []string{`open -a "Table Plus`, ``, `   `} {
		if _, err := splitArgs(in); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

// A directory only names a site for flock open and flock db; the other site commands still take a
// name, so a path can't quietly change a site's settings.
func TestSiteOrDirArg(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "acme", "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &state{cfg: &config.Config{Sites: []config.Site{{Name: "acme", Path: filepath.Join(dir, "acme")}}}}

	if site, err := siteOrDirArg(s, []string{app}); err != nil || site.Name != "acme" {
		t.Errorf("directory: %+v, %v", site, err)
	}
	if site, err := siteOrDirArg(s, []string{"acme"}); err != nil || site.Name != "acme" {
		t.Errorf("name: %+v, %v", site, err)
	}
	if _, err := siteOrDirArg(s, []string{dir}); err == nil {
		t.Error("a directory that isn't a site was accepted")
	}
	if _, err := siteOrDirArg(s, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "no site named") {
		t.Errorf("unknown name: %v", err)
	}

	if _, err := siteArg(s, []string{app}); err == nil {
		t.Error("siteArg took a directory: other commands would act on it")
	}
}
