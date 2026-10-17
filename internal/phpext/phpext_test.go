package phpext

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const laravelComposer = `{"require": {"php": "^8.2", "laravel/framework": "^12.0", "ext-intl": "*", "ext-Zend OPcache": "*"},
  "require-dev": {"ext-pcov": "*"}}`

const laravelLock = `{"packages": [
  {"name": "laravel/framework", "require": {"php": "^8.2", "ext-mbstring": "*", "ext-ctype": "*"}},
  {"name": "mongodb/mongodb", "require": {"ext-mongodb": "^2.1", "ext-intl": "*"}}],
 "packages-dev": [{"name": "phpunit/phpunit", "require": {"ext-dom": "*"}}]}`

func TestRequirementsComeFromComposerJSONAndTheLock(t *testing.T) {
	dir := project(t, map[string]string{"composer.json": laravelComposer, "composer.lock": laravelLock})
	got := Requirements(dir)
	want := map[string][]string{
		"intl":         {ComposerJSON, "mongodb/mongodb"},
		"zend-opcache": {ComposerJSON},
		"pcov":         {ComposerJSON},
		"mbstring":     {"laravel/framework"},
		"ctype":        {"laravel/framework"},
		"mongodb":      {"mongodb/mongodb"},
		"dom":          {"phpunit/phpunit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if len(Requirements(t.TempDir())) != 0 {
		t.Fatal("a folder without composer files requires nothing")
	}
}

func TestLaravelHints(t *testing.T) {
	env := `APP_NAME="My App"
# DB_CONNECTION=sqlite
DB_CONNECTION=pgsql
CACHE_STORE=memcached
SESSION_DRIVER='redis'
QUEUE_CONNECTION=database # jobs table
export IMAGE_DRIVER=imagick
`
	dir := project(t, map[string]string{"composer.json": laravelComposer, "artisan": "", ".env": env})
	got := Hints(dir)
	want := map[string]string{
		"pdo_pgsql": "DB_CONNECTION=pgsql in .env",
		"memcached": "CACHE_STORE=memcached in .env",
		"redis":     "SESSION_DRIVER=redis in .env",
		"imagick":   "IMAGE_DRIVER=imagick in .env",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}

	predis := project(t, map[string]string{".env": "CACHE_STORE=redis\nREDIS_CLIENT=predis\nDB_CONNECTION=mysql\n",
		"config/image.php": `<?php return ['driver' => \Intervention\Image\Drivers\Imagick\Driver::class];`})
	if got := Hints(predis); got["redis"] != "" || got["pdo_mysql"] == "" || got["imagick"] != "the Imagick driver in config/image.php" {
		t.Fatalf("predis project: %v", got)
	}
}

func TestCheck(t *testing.T) {
	dir := project(t, map[string]string{"composer.json": laravelComposer, "composer.lock": laravelLock, "artisan": "",
		".env": "DB_CONNECTION=pgsql\nSESSION_DRIVER=file\n"})
	available := map[string]bool{"intl": true, "zend-opcache": true, "mbstring": true, "ctype": true, "dom": true, "pcov": true}

	r := Check(dir, "8.4", available)
	if !r.Laravel || r.PHP != "8.4" || len(r.Extensions) != 8 {
		t.Fatalf("report: %+v", r)
	}
	byName := map[string]Extension{}
	for _, e := range r.Extensions {
		byName[e.Name] = e
	}
	if e := byName["pdo_pgsql"]; e.Declared() || e.Hint != "DB_CONNECTION=pgsql in .env" || e.Available {
		t.Errorf("pdo_pgsql: %+v", e)
	}
	if e := byName["intl"]; !e.Declared() || e.Hint != "" || !e.Available {
		t.Errorf("intl: %+v", e)
	}
	if got := r.Missing(); !reflect.DeepEqual(got, []string{"mongodb"}) {
		t.Errorf("missing: %v (a hint isn't a requirement)", got)
	}

	// Projects that aren't Laravel apps get no hints.
	plain := project(t, map[string]string{"composer.json": `{"require": {"ext-gd": "*"}}`, ".env": "DB_CONNECTION=pgsql\n"})
	if r := Check(plain, "8.4", map[string]bool{"gd": true}); r.Laravel || len(r.Extensions) != 1 || len(r.Missing()) != 0 {
		t.Errorf("plain project: %+v", r)
	}
}

func TestNormalizeAndHas(t *testing.T) {
	if Normalize("Zend OPcache") != "zend-opcache" || Normalize("ext-PDO_mysql") != "pdo_mysql" {
		t.Fatal("Normalize")
	}
	if !Has(map[string]bool{"zend-opcache": true}, "ext-opcache") || Has(nil, "intl") {
		t.Fatal("Has")
	}
}
