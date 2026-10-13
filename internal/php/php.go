// Package php manages installed PHP versions: their binaries, php.ini and PHP-FPM pools.
//
// Layout under the Flock root:
//
//	php/<minor>/bin/php, php-fpm   binaries (one static build per minor version)
//	php/<minor>/php.ini            user-editable, created once
//	php/<minor>/php-fpm.conf       regenerated on every start
//	run/php<minor>.sock            FPM socket the web server connects to
package php

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

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

// InstallFrom installs a PHP build from a directory or .tar.gz containing `php` and `php-fpm`.
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

	if strings.HasSuffix(src, ".tar.gz") || strings.HasSuffix(src, ".tgz") {
		err = extractBinaries(src, bin, ext)
	} else {
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

	return minor, nil
}

func copyBinaries(src, dst, extDst string) error {
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

// ExtensionPath is where a shared extension of a version lives, e.g. xdebug.so.
func ExtensionPath(root, minor, name string) string {
	return filepath.Join(Dir(root, minor), "ext", name+sharedSuffix())
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

; Static builds don't know the OS trust store; this bundle is the system roots plus Flock's CA.
openssl.cafile = "%s"
curl.cainfo = "%s"
`, ca, ca)
}

// EnsureIni creates php.ini for a version when it does not exist yet.
func EnsureIni(root, minor string) error {
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
		// FLOCK_XDEBUG changes the spec when Xdebug is toggled, so the supervisor restarts FPM.
		Env:        append(append(Env(root, minor), fmt.Sprintf("FLOCK_XDEBUG=%t", XdebugEnabled(root, minor))), extraEnv...),
		LogFile:    logFile,
		StopSignal: syscall.SIGQUIT, // graceful: finish in-flight requests
	}, nil
}

// Env pins php.ini and the scan dir so a static build never picks up another PHP's
// config from its compiled-in default paths (e.g. /usr/local/etc/php).
func Env(root, minor string) []string {
	return []string{
		"PHPRC=" + IniPath(root, minor),
		"PHP_INI_SCAN_DIR=" + filepath.Join(Dir(root, minor), "conf.d"),
	}
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
