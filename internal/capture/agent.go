package capture

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"flock/internal/frankenphp"
	"flock/internal/php"
)

//go:embed agent/agent.php
var agentSource []byte

// Extension is the PHP extension the agent needs (its hook()). Flock ships it in ext/ but loads
// it only through the agent's ini.
const Extension = "opentelemetry"

// iniName sorts after Flock's 00-flock.ini, so it overrides php.ini's auto_prepend_file.
const iniName = "90-flock-agent.ini"

// Settings are what the agent is told through its ini.
type Settings struct {
	On          bool
	Addr        string   // the collector, e.g. "127.0.0.1:9913"
	Off         []string // kinds not captured (config.Config.CaptureOff)
	DumpsInPage bool
}

// kinds is the flock.capture value: dumps and every kind not switched off.
func (s Settings) kinds() string {
	kinds := []string{"dump"}
	for _, k := range Kinds {
		if !slices.Contains(s.Off, k) {
			kinds = append(kinds, k)
		}
	}
	return strings.Join(kinds, ",")
}

// Env is added to the PHP processes' environment while capture is on: a value that changes with
// the settings and with the builds the agent loads into (a rebuilt package can add the extension
// without changing the PHP version), so the supervisor restarts the pools when their ini changes.
func (s Settings) Env(root string) []string {
	if !s.On {
		return nil
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%t|%x", s.Addr, s.kinds(), s.DumpsInPage, sha256.Sum256(agentSource))
	installed, _ := php.Installed(root)
	for _, minor := range installed {
		for _, t := range targets(root, minor) {
			if _, err := os.Stat(t.extension); err == nil {
				fmt.Fprintf(h, "|%s", t.extension)
			}
		}
	}
	return []string{"FLOCK_CAPTURE=" + hex.EncodeToString(h.Sum(nil)[:6])}
}

// AgentPath is where the agent is extracted to.
func AgentPath(root string) string { return filepath.Join(root, "tools", "agent", "agent.php") }

// extract writes the agent when it changed.
func extract(root string) error {
	path := AgentPath(root)
	if data, err := os.ReadFile(path); err == nil && bytes.Equal(data, agentSource) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, agentSource, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// target is one PHP build the agent can be loaded into: its conf.d and its copy of the extension.
type target struct{ confDir, extension string }

func targets(root, minor string) []target {
	ts := []target{{
		confDir:   filepath.Join(php.Dir(root, minor), "conf.d"),
		extension: php.ExtensionPath(root, minor, Extension),
	}}
	if frankenphp.Binary(root, minor) != "" {
		ts = append(ts, target{
			confDir:   filepath.Join(frankenphp.Dir(root, minor), "conf.d"),
			extension: php.ExtensionFile(filepath.Join(frankenphp.Dir(root, minor), "ext"), Extension),
		})
	}
	return ts
}

// Supported reports whether a PHP version's build ships the extension, so the agent runs there.
func Supported(root, minor string) bool {
	_, err := os.Stat(php.ExtensionPath(root, minor, Extension))
	return err == nil
}

// Sync writes the agent's ini into every installed PHP and FrankenPHP build that ships the
// extension while capture is on, and removes it everywhere else. It returns the installed PHP
// versions without the extension, where only the dump server captures (dumps only).
func Sync(root string, s Settings) (unsupported []string, err error) {
	installed, _ := php.Installed(root)
	unsupported = []string{}
	if s.On {
		if err := extract(root); err != nil {
			return unsupported, fmt.Errorf("agent: %w", err)
		}
	}

	var errs []error
	for _, minor := range installed {
		if !Supported(root, minor) {
			unsupported = append(unsupported, minor)
		}
		user, _ := os.ReadFile(php.IniPath(root, minor))
		for _, t := range targets(root, minor) {
			path := filepath.Join(t.confDir, iniName)
			if _, err := os.Stat(t.extension); !s.On || err != nil {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
				continue
			}
			if err := writeIfChanged(path, ini(root, t.extension, string(user), s)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return unsupported, errors.Join(errs...)
}

// ini loads the extension and the agent. A php.ini auto_prepend_file still runs: the agent
// requires it (flock.user_prepend), since this file takes the setting over.
func ini(root, extension, userIni string, s Settings) string {
	// Absolute: FPM and CGI change into each script's directory, and PHP resolves a relative
	// auto_prepend_file from there.
	agent, _ := filepath.Abs(AgentPath(root))
	extension, _ = filepath.Abs(extension)
	var b strings.Builder
	fmt.Fprintf(&b, `; Managed by Flock — loaded while capture is on (flock dumps on|off).
extension = "%s"
auto_prepend_file = "%s"
flock.agent = "%s"
flock.capture = "%s"
flock.dumps_in_page = %d
`, extension, agent, s.Addr, s.kinds(), map[bool]int{false: 0, true: 1}[s.DumpsInPage])
	if prepend, ok := php.IniValue(userIni, "auto_prepend_file"); ok && prepend != "" && prepend != agent {
		fmt.Fprintf(&b, "flock.user_prepend = \"%s\"\n", prepend)
	}
	return b.String()
}

func writeIfChanged(path, body string) error {
	if data, err := os.ReadFile(path); err == nil && string(data) == body {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}
