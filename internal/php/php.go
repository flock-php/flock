// Package php manages installed PHP versions: their binaries, php.ini and FastCGI pools.
//
// Layout under the Flock root:
//
//	php/<minor>/bin/php, php-fpm   binaries (one static build per minor version)
//	php/<minor>/php.ini            user-editable, created once
//	php/<minor>/php-fpm.conf       regenerated on every start
//	run/php<minor>.sock            FPM socket the web server connects to
//
// Windows has no PHP-FPM: there bin/ holds the official windows.php.net build (php.exe,
// php-cgi.exe and their DLLs), ext/ its extension DLLs, conf.d/00-flock.ini loads them,
// and every version runs a few php-cgi.exe workers on loopback ports instead of an FPM pool.
package php

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"flock/internal/supervisor"
)

var minorPattern = regexp.MustCompile(`^\d+\.\d+$`)

// Dir is where a PHP minor version lives.
func Dir(root, minor string) string { return filepath.Join(root, "php", minor) }

// Binary is the CLI binary of a version.
func Binary(root, minor string) string { return filepath.Join(Dir(root, minor), "bin", exe("php")) }

// FPMBinary is the FPM binary of a version.
func FPMBinary(root, minor string) string {
	return filepath.Join(Dir(root, minor), "bin", exe("php-fpm"))
}

// CGIBinary is the FastCGI binary used on Windows instead of FPM.
func CGIBinary(root, minor string) string {
	return filepath.Join(Dir(root, minor), "bin", exe("php-cgi"))
}

// IniPath is the php.ini used by both CLI (via PHPRC) and FPM.
func IniPath(root, minor string) string { return filepath.Join(Dir(root, minor), "php.ini") }

// Socket is the FPM socket for a version. Kept short: AF_UNIX paths are limited to ~104 bytes on macOS.
func Socket(root, minor string) string {
	return filepath.Join(root, "run", "php"+strings.ReplaceAll(minor, ".", "")+".sock")
}

// CABundle is the CA bundle (system roots + Flock root) PHP uses for openssl/curl.
func CABundle(root string) string { return filepath.Join(root, "certs", "cacert.pem") }

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Installed lists installed minor versions, newest first.
func Installed(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "php"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var versions []string
	for _, e := range entries {
		if e.IsDir() && minorPattern.MatchString(e.Name()) {
			if _, err := os.Stat(Binary(root, e.Name())); err == nil {
				versions = append(versions, e.Name())
			}
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[j], versions[i]) })

	return versions, nil
}

// InstallFrom installs a PHP build from a directory or .tar.gz containing `php` and `php-fpm`
// (on Windows: a directory or .zip with php.exe, php-cgi.exe, their DLLs and ext/).
// It returns the installed minor version, detected by running the binary.
func InstallFrom(root, src string) (string, error) {
	staging, err := os.MkdirTemp(root, ".php-install-")
	if err != nil {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", err
		}
		if staging, err = os.MkdirTemp(root, ".php-install-"); err != nil {
			return "", err
		}
	}
	defer os.RemoveAll(staging)

	bin := filepath.Join(staging, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", err
	}

	ext := filepath.Join(staging, "ext")
	if err := os.MkdirAll(ext, 0o755); err != nil {
		return "", err
	}

	switch {
	case strings.HasSuffix(src, ".tar.gz") || strings.HasSuffix(src, ".tgz"):
		err = extractBinaries(src, bin, ext)
	case strings.HasSuffix(strings.ToLower(src), ".zip"):
		unpacked := filepath.Join(staging, "zip")
		if err = ExtractZip(src, unpacked); err == nil {
			err = copyBinaries(unpacked, bin, ext)
		}
	default:
		err = copyBinaries(src, bin, ext)
	}
	if err != nil {
		return "", err
	}

	out, err := exec.Command(filepath.Join(bin, exe("php")), "-n", "-r", "echo PHP_MAJOR_VERSION.'.'.PHP_MINOR_VERSION;").Output()
	if err != nil {
		return "", fmt.Errorf("run installed php: %w", err)
	}
	minor := strings.TrimSpace(string(out))
	if !minorPattern.MatchString(minor) {
		return "", fmt.Errorf("unexpected php version %q", minor)
	}

	target := Dir(root, minor)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	// Replace only binaries and shared extensions so an existing php.ini survives upgrades.
	for name, dir := range map[string]string{"bin": bin, "ext": ext} {
		if err := os.RemoveAll(filepath.Join(target, name)); err != nil {
			return "", err
		}
		if err := os.Rename(dir, filepath.Join(target, name)); err != nil {
			return "", err
		}
	}

	if runtime.GOOS == "windows" {
		if err := writeExtensionsIni(root, minor); err != nil {
			return "", err
		}
	}

	return minor, nil
}

func copyBinaries(src, dst, extDst string) error {
	if runtime.GOOS == "windows" {
		return copyWindowsBuild(src, dst, extDst)
	}
	if sos, _ := filepath.Glob(filepath.Join(src, "ext", "*"+sharedSuffix())); len(sos) > 0 {
		for _, so := range sos {
			if err := copyFile(so, filepath.Join(extDst, filepath.Base(so)), 0o755); err != nil {
				return err
			}
		}
	}

	for _, name := range []string{exe("php"), exe("php-fpm")} {
		from := filepath.Join(src, name)
		if _, err := os.Stat(from); err != nil {
			if name == exe("php-fpm") && runtime.GOOS == "windows" {
				continue
			}
			return fmt.Errorf("%s not found in %s", name, src)
		}
		if err := copyFile(from, filepath.Join(dst, name), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func extractBinaries(archive, dst, extDst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)

	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Base(hdr.Name)
		target := filepath.Join(dst, name)
		switch {
		case hdr.Typeflag != tar.TypeReg:
			continue
		case filepath.Base(filepath.Dir(hdr.Name)) == "ext" && strings.HasSuffix(name, sharedSuffix()):
			target = filepath.Join(extDst, name)
		case name != exe("php") && name != exe("php-fpm"):
			continue
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
		found[name] = true
	}

	if !found[exe("php")] {
		return fmt.Errorf("%s: no php binary in archive", archive)
	}
	return nil
}

// copyWindowsBuild copies an official Windows build: every top-level file (php.exe, php-cgi.exe
// and the DLLs they and the extensions load from their own directory) plus ext/.
func copyWindowsBuild(src, dst, extDst string) error {
	for _, name := range []string{"php.exe", "php-cgi.exe"} {
		if _, err := os.Stat(filepath.Join(src, name)); err != nil {
			return fmt.Errorf("%s not found in %s", name, src)
		}
	}

	// OpenSSL key generation (openssl_pkey_new, e.g. `php artisan passport:keys`) needs a config file.
	if cnf := filepath.Join(src, "extras", "ssl", "openssl.cnf"); fileExists(cnf) {
		if err := copyFile(cnf, filepath.Join(dst, "openssl.cnf"), 0o644); err != nil {
			return err
		}
	}

	for from, to := range map[string]string{src: dst, filepath.Join(src, "ext"): extDst} {
		entries, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			if err := copyFile(filepath.Join(from, e.Name()), filepath.Join(to, e.Name()), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExtractZip unpacks a .zip into dest, rejecting entries that would escape it.
func ExtractZip(archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	clean := filepath.Clean(dest) + string(filepath.Separator)
	for _, f := range zr.File {
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, clean) {
			return fmt.Errorf("illegal path in archive: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func sharedSuffix() string {
	if runtime.GOOS == "windows" {
		return ".dll"
	}
	return ".so"
}

// ExtensionPath is where a shared extension of a version lives, e.g. xdebug.so
// (php_xdebug.dll on Windows, following the official builds' naming).
func ExtensionPath(root, minor, name string) string {
	if runtime.GOOS == "windows" {
		name = "php_" + name
	}
	return filepath.Join(Dir(root, minor), "ext", name+sharedSuffix())
}

// windowsExtensions are loaded from ext/ when the build ships them (8.5 compiles opcache in).
// The official builds keep almost everything shared; this is what Laravel apps commonly need.
var windowsExtensions = []string{
	"bz2", "curl", "exif", "fileinfo", "ftp", "gd", "gettext", "gmp", "intl", "mbstring", "mysqli",
	"openssl", "pdo_mysql", "pdo_pgsql", "pdo_sqlite", "pgsql", "sockets", "sodium", "sqlite3", "xsl", "zip",
	"igbinary", "redis",
}

// writeExtensionsIni (re)writes conf.d/00-flock.ini, which points PHP at ext/ and loads the
// extensions present there. It is Flock's file; php.ini stays the user's.
func writeExtensionsIni(root, minor string) error {
	dir := filepath.Join(Dir(root, minor), "conf.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "; Managed by Flock — regenerated on install, edit php.ini instead.\nextension_dir = \"%s\"\n", filepath.Join(Dir(root, minor), "ext"))
	if _, err := os.Stat(ExtensionPath(root, minor, "opcache")); err == nil {
		b.WriteString("zend_extension = opcache\n")
	}
	for _, name := range windowsExtensions {
		if _, err := os.Stat(ExtensionPath(root, minor, name)); err == nil {
			fmt.Fprintf(&b, "extension = %s\n", name)
		}
	}
	return os.WriteFile(filepath.Join(dir, "00-flock.ini"), []byte(b.String()), 0o644)
}

func xdebugIni(root, minor string) string {
	return filepath.Join(Dir(root, minor), "conf.d", "xdebug.ini")
}

// XdebugEnabled reports whether Xdebug is loaded for a version.
func XdebugEnabled(root, minor string) bool {
	_, err := os.Stat(xdebugIni(root, minor))
	return err == nil
}

// SetXdebug loads or unloads Xdebug for a version (CLI and FPM) via conf.d/xdebug.ini.
// It is off by default: merely loading Xdebug slows every request down noticeably.
// With mode=debug,develop and start_with_request=trigger, a debugger session starts only
// when the request carries XDEBUG_TRIGGER (browser extension, ?XDEBUG_TRIGGER=1 or the env var).
func SetXdebug(root, minor string, on bool) error {
	ini := xdebugIni(root, minor)
	if !on {
		if err := os.Remove(ini); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	so := ExtensionPath(root, minor, "xdebug")
	if _, err := os.Stat(so); err != nil {
		return fmt.Errorf("PHP %s was installed without xdebug — reinstall it from a newer build", minor)
	}
	if err := os.MkdirAll(filepath.Dir(ini), 0o755); err != nil {
		return err
	}

	body := fmt.Sprintf(`; Managed by Flock (flock xdebug on|off)
zend_extension = "%s"
xdebug.mode = debug,develop
xdebug.start_with_request = trigger
xdebug.client_host = 127.0.0.1
xdebug.client_port = 9003
`, so)
	return os.WriteFile(ini, []byte(body), 0o644)
}

// DefaultIni is written once per version; afterwards the file belongs to the user.
func DefaultIni(root string) string {
	ca := CABundle(root)
	return fmt.Sprintf(`; Flock php.ini — edit freely, Flock will not overwrite it.
memory_limit = 512M
max_execution_time = 60
upload_max_filesize = 128M
post_max_size = 128M
display_errors = On
display_startup_errors = On
error_reporting = E_ALL
log_errors = On
date.timezone = UTC
variables_order = "EGPCS"

opcache.enable = 1
opcache.enable_cli = 0
opcache.validate_timestamps = 1
opcache.revalidate_freq = 0
opcache.memory_consumption = 256
opcache.max_accelerated_files = 50000

; PHP's OpenSSL doesn't use the OS trust store; this bundle is the system roots plus Flock's CA.
openssl.cafile = "%s"
curl.cainfo = "%s"
`, ca, ca)
}

// EnsureIni creates php.ini for a version when it does not exist yet.
func EnsureIni(root, minor string) error {
	if runtime.GOOS == "windows" {
		if err := writeExtensionsIni(root, minor); err != nil {
			return err
		}
	}
	path := IniPath(root, minor)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(DefaultIni(root)), 0o644)
}

// FPMSpec writes the FPM config for a version and returns the process to supervise.
// extraEnv is passed to the workers (e.g. the dump server address).
func FPMSpec(root, minor string, extraEnv ...string) (supervisor.Spec, error) {
	if err := EnsureIni(root, minor); err != nil {
		return supervisor.Spec{}, err
	}
	if err := os.MkdirAll(filepath.Join(root, "run"), 0o700); err != nil {
		return supervisor.Spec{}, err
	}

	conf := filepath.Join(Dir(root, minor), "php-fpm.conf")
	logFile := filepath.Join(root, "log", "php"+minor+"-fpm.log")
	body := fmt.Sprintf(`; Generated by Flock on every start — edit php.ini instead.
[global]
error_log = "%s"
daemonize = no

[flock]
listen = "%s"
listen.mode = 0600
pm = dynamic
pm.max_children = 16
pm.start_servers = 2
pm.min_spare_servers = 1
pm.max_spare_servers = 4
pm.max_requests = 500
catch_workers_output = yes
decorate_workers_output = no
clear_env = no
`, logFile, Socket(root, minor))

	if err := os.WriteFile(conf, []byte(body), 0o644); err != nil {
		return supervisor.Spec{}, err
	}

	return supervisor.Spec{
		Name: "php" + minor + "-fpm",
		Path: FPMBinary(root, minor),
		Args: []string{"--nodaemonize", "--fpm-config", conf, "-c", IniPath(root, minor)},
		// FLOCK_XDEBUG and FLOCK_PHP_VERSION change the spec when Xdebug is toggled or a patch
		// update is installed, so the supervisor restarts FPM with the new setup.
		Env: append(append(Env(root, minor),
			fmt.Sprintf("FLOCK_XDEBUG=%t", XdebugEnabled(root, minor)),
			"FLOCK_PHP_VERSION="+FullVersion(root, minor)), extraEnv...),
		LogFile:    logFile,
		StopSignal: syscall.SIGQUIT, // graceful: finish in-flight requests
	}, nil
}

// Pool is a version's FastCGI pool: the processes to supervise and the addresses
// ("unix/<socket>" or "127.0.0.1:<port>") the web server sends requests to.
type Pool struct {
	Specs     []supervisor.Spec
	Upstreams []string
}

// PoolFor returns the pool of a version: one PHP-FPM master, or php-cgi workers on Windows.
func PoolFor(root, minor string, extraEnv ...string) (Pool, error) {
	if runtime.GOOS == "windows" {
		return CGIPool(root, minor, extraEnv...)
	}
	spec, err := FPMSpec(root, minor, extraEnv...)
	if err != nil {
		return Pool{}, err
	}
	return Pool{Specs: []supervisor.Spec{spec}, Upstreams: []string{"unix/" + Socket(root, minor)}}, nil
}

// CGIWorkers is how many php-cgi processes serve a version on Windows. Each handles one request
// at a time, so a paused Xdebug session or a slow request only blocks its own worker.
const CGIWorkers = 6

// CGIPort is the loopback port of a php-cgi worker: 19000 + major*100 + minor*10 + worker,
// e.g. 19840-19845 for PHP 8.4.
func CGIPort(minor string, worker int) int {
	major, rest, _ := strings.Cut(minor, ".")
	ma, _ := strconv.Atoi(major)
	mi, _ := strconv.Atoi(rest)
	return 19000 + ma*100 + mi*10 + worker
}

// CGIPool runs CGIWorkers php-cgi processes for a version (Windows has no PHP-FPM).
func CGIPool(root, minor string, extraEnv ...string) (Pool, error) {
	if err := EnsureIni(root, minor); err != nil {
		return Pool{}, err
	}

	env := append(append(Env(root, minor),
		fmt.Sprintf("FLOCK_XDEBUG=%t", XdebugEnabled(root, minor)),
		"FLOCK_PHP_VERSION="+FullVersion(root, minor),
		// php-cgi exits after 500 requests by default; the restart would briefly take a worker away.
		"PHP_FCGI_MAX_REQUESTS=0",
		"PHP_FCGI_CHILDREN=0"), extraEnv...)

	var pool Pool
	for i := range CGIWorkers {
		addr := fmt.Sprintf("127.0.0.1:%d", CGIPort(minor, i))
		pool.Specs = append(pool.Specs, supervisor.Spec{
			Name:    fmt.Sprintf("php%s-cgi-%d", minor, i+1),
			Path:    CGIBinary(root, minor),
			Args:    []string{"-b", addr, "-c", IniPath(root, minor)},
			Env:     env,
			LogFile: filepath.Join(root, "log", "php"+minor+"-cgi.log"),
		})
		pool.Upstreams = append(pool.Upstreams, addr)
	}
	return pool, nil
}

// PoolLog is the log name (`flock logs <name>`) of a version's pool.
func PoolLog(minor string) string {
	if runtime.GOOS == "windows" {
		return "php" + minor + "-cgi"
	}
	return "php" + minor + "-fpm"
}

// WaitReady polls until every upstream accepts connections, or the timeout passes.
func WaitReady(upstreams []string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for _, upstream := range upstreams {
		network, addr := "tcp", upstream
		if path, ok := strings.CutPrefix(upstream, "unix/"); ok {
			network, addr = "unix", path
		}
		for {
			if conn, err := net.DialTimeout(network, addr, 100*time.Millisecond); err == nil {
				_ = conn.Close()
				break
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	return true
}

// Env pins php.ini and the scan dir so a static build never picks up another PHP's
// config from its compiled-in default paths (e.g. /usr/local/etc/php).
func Env(root, minor string) []string {
	env := []string{
		"PHPRC=" + IniPath(root, minor),
		"PHP_INI_SCAN_DIR=" + filepath.Join(Dir(root, minor), "conf.d"),
	}
	if cnf := filepath.Join(Dir(root, minor), "bin", "openssl.cnf"); runtime.GOOS == "windows" && fileExists(cnf) {
		env = append(env, "OPENSSL_CONF="+cnf)
	}
	return env
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var fullVersions sync.Map // binary path + mtime → version

// FullVersion returns the exact version of an installed minor ("8.5.11"), cached per binary.
func FullVersion(root, minor string) string {
	bin := Binary(root, minor)
	info, err := os.Stat(bin)
	if err != nil {
		return ""
	}
	key := bin + "@" + info.ModTime().String()
	if v, ok := fullVersions.Load(key); ok {
		return v.(string)
	}
	out, err := exec.Command(bin, "-n", "-r", "echo PHP_VERSION;").Output()
	if err != nil {
		return ""
	}
	version := strings.TrimSpace(string(out))
	fullVersions.Store(key, version)
	return version
}

// Uninstall removes a PHP version including its php.ini.
func Uninstall(root, minor string) error {
	if !minorPattern.MatchString(minor) {
		return fmt.Errorf("invalid version %q", minor)
	}
	return os.RemoveAll(Dir(root, minor))
}

// ComposerPhar is where the Composer phar lives.
func ComposerPhar(root string) string { return filepath.Join(root, "tools", "composer.phar") }

// versionLess compares "8.4" < "8.10" numerically.
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, _ := strconv.Atoi(pa[i])
		nb, _ := strconv.Atoi(pb[i])
		if na != nb {
			return na < nb
		}
	}
	return len(pa) < len(pb)
}
