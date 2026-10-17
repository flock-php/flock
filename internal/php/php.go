// Package php manages installed PHP versions: their binaries, php.ini and FastCGI pools, and
// installs them from the binaries manifest (see Manager).
//
// Layout under the Flock root:
//
//	php/<minor>/bin/php, php-fpm   binaries (the macOS package, built by Flock)
//	php/<minor>/ext/               shared extensions
//	php/<minor>/flock.json         what was installed (version, builds)
//	php/<minor>/php.ini            user-editable, created once
//	php/<minor>/conf.d/            Flock's ini files: extension_dir and the extensions to load
//	php/<minor>/conf.d-cli/        loaded only by command-line PHP (pcov)
//	php/<minor>/conf.d-xdebug/     loaded only for debugging (Xdebug)
//	php/<minor>/php-fpm.conf       regenerated on every start
//	run/php<minor>.sock            FPM socket the web server connects to
//
// Windows has no PHP-FPM: there bin/ holds the official windows.php.net build (php.exe,
// php-cgi.exe and their DLLs) with Flock's extension pack added, and every version runs a few
// php-cgi.exe workers on loopback ports instead of an FPM pool.
package php

import (
	"encoding/json"
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

	"flock/internal/archive"
	"flock/internal/fsutil"
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

// InstallFrom installs a PHP package from a directory, .tar.gz or .zip, laid out as the binaries
// manifest publishes it: bin/php, bin/php-fpm and ext/ on macOS; the official build (php.exe,
// php-cgi.exe, ext/) on Windows. It returns the installed minor version, detected by running it.
func InstallFrom(root, src string) (string, error) {
	base := filepath.Join(root, "php")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(base, ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)

	// A copy, since installing moves the package's files into place.
	dir := filepath.Join(staging, "package")
	if info, err := os.Stat(src); err != nil {
		return "", err
	} else if info.IsDir() {
		err = fsutil.CopyTree(src, dir)
	} else if archive.IsArchive(src) {
		err = archive.Extract(src, dir)
	} else {
		err = fmt.Errorf("%s is not a directory, .tar.gz or .zip", src)
	}
	if err != nil {
		return "", err
	}
	version, err := probe(packageBinary(dir))
	if err != nil {
		return "", err
	}
	minor := MinorOf(version)
	if minor == "" {
		return "", fmt.Errorf("unexpected PHP version %q", version)
	}
	return minor, place(root, minor, dir, Info{Version: version}, nil)
}

// packageBinary is the php binary of an unpacked package.
func packageBinary(dir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, "php.exe")
	}
	return filepath.Join(dir, "bin", "php")
}

// place installs an unpacked package as minor: bin/ and ext/ are replaced, so php.ini and Flock's
// ini files survive. release, when set, is called first so the running version can be stopped.
func place(root, minor, dir string, info Info, release func(minor string)) error {
	version, err := probe(packageBinary(dir))
	if err != nil {
		return err
	}
	if version != info.Version {
		return fmt.Errorf("the package for PHP %s contains PHP %s", info.Version, version)
	}

	target := Dir(root, minor)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".place-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	// The parts of the new install, ready to be moved into place.
	parts := map[string]string{"bin": filepath.Join(dir, "bin"), "ext": filepath.Join(dir, "ext"), "LICENSES": filepath.Join(dir, "LICENSES")}
	if runtime.GOOS == "windows" {
		bin, ext := filepath.Join(staging, "bin"), filepath.Join(staging, "ext")
		for _, d := range []string{bin, ext} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		if err := copyWindowsBuild(dir, bin, ext); err != nil {
			return err
		}
		parts["bin"], parts["ext"] = bin, ext
	} else {
		if !fileExists(filepath.Join(dir, "bin", "php-fpm")) {
			return errors.New("the package has no bin/php-fpm")
		}
		if err := os.MkdirAll(parts["ext"], 0o755); err != nil {
			return err
		}
	}

	if release != nil {
		release(minor)
	}
	for name, from := range parts {
		to := filepath.Join(target, name)
		if err := os.RemoveAll(to); err != nil {
			return err
		}
		if !fileExists(from) {
			continue
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(infoFile(root, minor), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return EnsureIni(root, minor)
}

// MergeExtensionPack adds an extension pack (Windows) to an unpacked PHP or FrankenPHP package:
// its ext/ DLLs go to the package's ext/, the libraries they load (ImageMagick's) next to the
// executables, and its licenses to LICENSES/.
func MergeExtensionPack(dir, pack string) error {
	moves := map[string]string{filepath.Join(pack, "ext"): filepath.Join(dir, "ext"), pack: dir, filepath.Join(pack, "LICENSES"): filepath.Join(dir, "LICENSES")}
	for from, to := range moves {
		entries, err := os.ReadDir(from)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.MkdirAll(to, 0o755); err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if !e.Type().IsRegular() || (from == pack && !strings.EqualFold(filepath.Ext(name), ".dll")) {
				continue
			}
			if err := copyFile(filepath.Join(from, name), filepath.Join(to, name), 0o755); err != nil {
				return err
			}
		}
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
		if errors.Is(err, os.ErrNotExist) && from != src {
			continue
		}
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

// extensions are loaded from ext/ when the package ships them, in this order (redis and memcached
// use igbinary). The official Windows builds keep almost everything shared; the macOS builds
// compile most of these in and ship only the larger ones (imagick, mongodb, memcached) shared.
// Xdebug and pcov aren't here: Xdebug loads only for debugging, pcov only for the CLI.
var extensions = []string{
	"bz2", "curl", "exif", "fileinfo", "ftp", "gd", "gettext", "gmp", "intl", "ldap", "mbstring", "mysqli",
	"openssl", "pdo_mysql", "pdo_pgsql", "pdo_sqlite", "pgsql", "shmop", "soap", "sockets", "sodium", "sqlite3", "xsl", "zip",
	"apcu", "igbinary", "redis", "imagick", "mongodb", "memcached",
}

// extensionFile is a shared extension's file in extDir: imagick.so, or php_imagick.dll on Windows.
func extensionFile(extDir, name string) string {
	if runtime.GOOS == "windows" {
		name = "php_" + name
	}
	return filepath.Join(extDir, name+sharedSuffix())
}

// writeExtensionsIni (re)writes conf.d/00-flock.ini, which points PHP at ext/ and loads the
// extensions present there. It is Flock's file; php.ini stays the user's.
func writeExtensionsIni(root, minor string) error {
	dir := filepath.Join(Dir(root, minor), "conf.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "00-flock.ini"), []byte(ExtensionsIni(filepath.Join(Dir(root, minor), "ext"))), 0o644)
}

// ExtensionsIni is the ini that points a build at its ext/ directory and loads the extensions
// present there (FrankenPHP's builds use the same).
func ExtensionsIni(extDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "; Managed by Flock — regenerated on install, edit php.ini instead.\nextension_dir = \"%s\"\n", extDir)
	// opcache is compiled in from PHP 8.5 on, and into the macOS builds from PHP 8.0 on.
	if fileExists(extensionFile(extDir, "opcache")) {
		b.WriteString("zend_extension = opcache\n")
	}
	for _, name := range extensions {
		if fileExists(extensionFile(extDir, name)) {
			fmt.Fprintf(&b, "extension = %s\n", name)
		}
	}
	return b.String()
}

// CLIDir is a version's extra ini scan dir for command-line PHP (the shims, `flock debug`): it
// loads pcov, so test suites can measure coverage while web requests don't pay for it.
func CLIDir(root, minor string) string { return filepath.Join(Dir(root, minor), "conf.d-cli") }

// writeCLIIni (re)writes conf.d-cli/pcov.ini when the build ships pcov.
func writeCLIIni(root, minor string) error {
	dir := CLIDir(root, minor)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "pcov.ini")
	if !fileExists(ExtensionPath(root, minor, "pcov")) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	body := fmt.Sprintf("; Managed by Flock — loaded only by command-line PHP, for code coverage.\nextension = \"%s\"\npcov.enabled = 1\n", ExtensionPath(root, minor, "pcov"))
	return os.WriteFile(path, []byte(body), 0o644)
}

// XdebugDir is a version's extra ini scan dir that loads Xdebug. Only the debug pool and
// `flock debug` scan it, so ordinary requests and CLI runs never load the extension.
func XdebugDir(root, minor string) string { return filepath.Join(Dir(root, minor), "conf.d-xdebug") }

// XdebugAvailable reports whether a version's build ships Xdebug.
func XdebugAvailable(root, minor string) bool {
	return fileExists(ExtensionPath(root, minor, "xdebug"))
}

// writeXdebugIni (re)writes conf.d-xdebug/xdebug.ini when the build ships Xdebug. Whatever
// reaches the debug pool asked to be debugged, so a session starts with every request.
func writeXdebugIni(root, minor string) error {
	if !XdebugAvailable(root, minor) {
		return nil
	}
	dir := XdebugDir(root, minor)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(`; Managed by Flock — loaded only for requests that ask for Xdebug, and by flock debug.
zend_extension = "%s"
xdebug.mode = debug,develop
xdebug.start_with_request = yes
xdebug.client_host = 127.0.0.1
xdebug.client_port = 9003
; An IDE that isn't listening is normal here: don't warn about it on every request.
xdebug.log_level = 0
`, ExtensionPath(root, minor, "xdebug"))
	return os.WriteFile(filepath.Join(dir, "xdebug.ini"), []byte(body), 0o644)
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

// EnsureIni creates php.ini for a version when it does not exist yet, and (re)writes the ini
// files Flock manages.
func EnsureIni(root, minor string) error {
	for _, write := range []func(root, minor string) error{writeExtensionsIni, writeCLIIni, writeXdebugIni} {
		if err := write(root, minor); err != nil {
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
	return fpmSpec(root, minor, false, extraEnv)
}

// fpmSpec builds the normal FPM pool, or with debug the one that loads Xdebug. The debug pool
// starts workers on demand, so it is idle unless someone debugs.
func fpmSpec(root, minor string, debug bool, extraEnv []string) (supervisor.Spec, error) {
	if err := EnsureIni(root, minor); err != nil {
		return supervisor.Spec{}, err
	}
	if err := os.MkdirAll(filepath.Join(root, "run"), 0o700); err != nil {
		return supervisor.Spec{}, err
	}

	name, conf, socket, env := "php"+minor+"-fpm", "php-fpm.conf", Socket(root, minor), Env(root, minor)
	pm := "pm = dynamic\npm.max_children = 16\npm.start_servers = 2\npm.min_spare_servers = 1\npm.max_spare_servers = 4"
	if debug {
		name, conf, socket, env = "php"+minor+"-fpm-xdebug", "php-fpm-xdebug.conf", DebugSocket(root, minor), DebugEnv(root, minor)
		pm = "pm = ondemand\npm.max_children = 8\npm.process_idle_timeout = 60s"
	}

	conf = filepath.Join(Dir(root, minor), conf)
	logFile := filepath.Join(root, "log", name+".log")
	body := fmt.Sprintf(`; Generated by Flock on every start — edit php.ini instead.
[global]
error_log = "%s"
daemonize = no

[flock]
listen = "%s"
listen.mode = 0600
%s
pm.max_requests = 500
catch_workers_output = yes
decorate_workers_output = no
clear_env = no
`, logFile, socket, pm)

	if err := os.WriteFile(conf, []byte(body), 0o644); err != nil {
		return supervisor.Spec{}, err
	}

	return supervisor.Spec{
		Name: name,
		Path: FPMBinary(root, minor),
		Args: []string{"--nodaemonize", "--fpm-config", conf, "-c", IniPath(root, minor)},
		// FLOCK_PHP_VERSION changes the spec when a patch update is installed, so the supervisor
		// restarts FPM with the new binary.
		Env:        append(append(env, "FLOCK_PHP_VERSION="+FullVersion(root, minor)), extraEnv...),
		LogFile:    logFile,
		StopSignal: syscall.SIGQUIT, // graceful: finish in-flight requests
	}, nil
}

// DebugSocket is the socket of a version's Xdebug FPM pool.
func DebugSocket(root, minor string) string {
	return filepath.Join(root, "run", "php"+strings.ReplaceAll(minor, ".", "")+"-xdebug.sock")
}

// Pool is a version's FastCGI pool: the processes to supervise and the addresses
// ("unix/<socket>" or "127.0.0.1:<port>") the web server sends requests to.
type Pool struct {
	Specs     []supervisor.Spec
	Upstreams []string
}

// PoolFor returns the pool of a version: one PHP-FPM master, or php-cgi workers on Windows.
func PoolFor(root, minor string, extraEnv ...string) (Pool, error) {
	return poolFor(root, minor, false, extraEnv)
}

// DebugPoolFor returns a version's second pool, which loads Xdebug. The web server sends it
// only the requests that ask to be debugged, so every other request stays fast.
func DebugPoolFor(root, minor string, extraEnv ...string) (Pool, error) {
	if !XdebugAvailable(root, minor) {
		return Pool{}, fmt.Errorf("PHP %s was installed without Xdebug — reinstall it from a newer build", minor)
	}
	return poolFor(root, minor, true, extraEnv)
}

func poolFor(root, minor string, debug bool, extraEnv []string) (Pool, error) {
	if runtime.GOOS == "windows" {
		return cgiPool(root, minor, debug, extraEnv)
	}
	spec, err := fpmSpec(root, minor, debug, extraEnv)
	if err != nil {
		return Pool{}, err
	}
	socket := Socket(root, minor)
	if debug {
		socket = DebugSocket(root, minor)
	}
	return Pool{Specs: []supervisor.Spec{spec}, Upstreams: []string{"unix/" + socket}}, nil
}

// CGIWorkers is how many php-cgi processes serve a version on Windows. Each handles one request
// at a time, so a slow request only blocks its own worker.
const CGIWorkers = 6

// DebugCGIWorkers serve a version's Xdebug requests on Windows. A request paused at a
// breakpoint holds its worker, so there are a few.
const DebugCGIWorkers = 3

// CGIPort is the loopback port of a php-cgi worker: 19000 + major*100 + minor*10 + worker,
// e.g. 19840-19845 for PHP 8.4.
func CGIPort(minor string, worker int) int {
	major, rest, _ := strings.Cut(minor, ".")
	ma, _ := strconv.Atoi(major)
	mi, _ := strconv.Atoi(rest)
	return 19000 + ma*100 + mi*10 + worker
}

// DebugCGIPort is the port of an Xdebug php-cgi worker: CGIPort + 1000, e.g. 20840-20842 for PHP 8.4.
func DebugCGIPort(minor string, worker int) int { return CGIPort(minor, worker) + 1000 }

// CGIPool runs CGIWorkers php-cgi processes for a version (Windows has no PHP-FPM).
func CGIPool(root, minor string, extraEnv ...string) (Pool, error) {
	return cgiPool(root, minor, false, extraEnv)
}

func cgiPool(root, minor string, debug bool, extraEnv []string) (Pool, error) {
	if err := EnsureIni(root, minor); err != nil {
		return Pool{}, err
	}

	name, workers, port, env := "php"+minor+"-cgi", CGIWorkers, CGIPort, Env(root, minor)
	if debug {
		name, workers, port, env = "php"+minor+"-cgi-xdebug", DebugCGIWorkers, DebugCGIPort, DebugEnv(root, minor)
	}
	env = append(append(env,
		"FLOCK_PHP_VERSION="+FullVersion(root, minor),
		// php-cgi exits after 500 requests by default; the restart would briefly take a worker away.
		"PHP_FCGI_MAX_REQUESTS=0",
		"PHP_FCGI_CHILDREN=0"), extraEnv...)

	var pool Pool
	for i := range workers {
		addr := fmt.Sprintf("127.0.0.1:%d", port(minor, i))
		pool.Specs = append(pool.Specs, supervisor.Spec{
			Name:    fmt.Sprintf("%s-%d", name, i+1),
			Path:    CGIBinary(root, minor),
			Args:    []string{"-b", addr, "-c", IniPath(root, minor)},
			Env:     env,
			LogFile: filepath.Join(root, "log", name+".log"),
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

// DebugPoolLog is the log name of a version's Xdebug pool.
func DebugPoolLog(minor string) string { return PoolLog(minor) + "-xdebug" }

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
	return env(root, minor, filepath.Join(Dir(root, minor), "conf.d"))
}

// CLIEnv is Env for command-line PHP: it also scans CLIDir, so PHP loads pcov.
func CLIEnv(root, minor string) []string {
	return env(root, minor, filepath.Join(Dir(root, minor), "conf.d")+string(os.PathListSeparator)+CLIDir(root, minor))
}

// DebugEnv is Env that also scans XdebugDir, so PHP loads Xdebug.
func DebugEnv(root, minor string) []string {
	return env(root, minor, filepath.Join(Dir(root, minor), "conf.d")+string(os.PathListSeparator)+XdebugDir(root, minor))
}

func env(root, minor, scanDir string) []string {
	env := []string{
		"PHPRC=" + IniPath(root, minor),
		"PHP_INI_SCAN_DIR=" + scanDir,
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

var loadedExtensions sync.Map // binary + ini files and their mtimes → extensions

// Extensions lists what command-line PHP of an installed minor loads (lower case, spaces as
// dashes: "intl", "zend-opcache"), plus Xdebug when the build ships it. It is cached until the
// binary or Flock's ini files change.
func Extensions(root, minor string) (map[string]bool, error) {
	key := ""
	for _, path := range []string{Binary(root, minor), filepath.Join(Dir(root, minor), "conf.d", "00-flock.ini"), filepath.Join(CLIDir(root, minor), "pcov.ini"), IniPath(root, minor)} {
		if info, err := os.Stat(path); err == nil {
			key += path + "@" + info.ModTime().String() + ";"
		} else if path == Binary(root, minor) {
			return nil, fmt.Errorf("PHP %s is not installed", minor)
		}
	}
	if v, ok := loadedExtensions.Load(key); ok {
		return v.(map[string]bool), nil
	}
	cmd := exec.Command(Binary(root, minor), "-r", `echo implode("\n", array_merge(get_loaded_extensions(), get_loaded_extensions(true)));`)
	cmd.Env = append(os.Environ(), CLIEnv(root, minor)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list the extensions of PHP %s: %w", minor, err)
	}
	exts := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(line)), " ", "-"); name != "" {
			exts[name] = true
		}
	}
	if XdebugAvailable(root, minor) {
		exts["xdebug"] = true
	}
	loadedExtensions.Store(key, exts)
	return exts, nil
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
