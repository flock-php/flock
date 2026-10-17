// Package boost adds Flock's guideline to projects that use Laravel Boost.
//
// Boost builds the agents' guideline files (CLAUDE.md, AGENTS.md, ...) and includes a project's
// own guidelines from .ai/guidelines. Flock writes .ai/guidelines/flock.md there and runs
// `php artisan boost:update`, so agents know Flock serves the app and picks its PHP version.
// The file is committed with the project, so it says nothing specific to this machine.
package boost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// States of a project.
const (
	None     = ""         // the project doesn't use Boost
	Missing  = "missing"  // uses Boost, without Flock's guideline
	Current  = "current"  // has Flock's current guideline
	Outdated = "outdated" // has an older version of Flock's guideline
	Custom   = "custom"   // has a flock.md Flock didn't write (or that was edited); Flock leaves it alone
)

// Guideline is the current text of .ai/guidelines/flock.md.
const Guideline = `## Flock

- When Flock is running on this machine (` + "`flock status`" + `), it serves this app on a ` + "`.test`" + ` domain (` + "`https://<folder>.test`" + ` by default). Use the ` + "`site_info`" + ` tool of Flock's MCP server, or ` + "`flock sites`" + `, for the exact URL. Don't run ` + "`php artisan serve`" + `.
- Run ` + "`php`, `composer`, `node` and `npm`" + ` as usual: Flock's shims pick the versions this project uses.
- When code uses a PHP extension (imagick, intl, ...), require it in composer.json: ` + "`composer require ext-<name>:*`" + `, so every machine and server installing the project needs it too.
- If other guidelines describe a different local development server, follow them only when Flock isn't the one serving this app.
`

// previous are the texts earlier Flock versions wrote, which Write may replace.
var previous = []string{`## Flock

- When Flock is running on this machine (` + "`flock status`" + `), it serves this app on a ` + "`.test`" + ` domain (` + "`https://<folder>.test`" + ` by default). Use the ` + "`site_info`" + ` tool of Flock's MCP server, or ` + "`flock sites`" + `, for the exact URL. Don't run ` + "`php artisan serve`" + `.
- Run ` + "`php`, `composer`, `node` and `npm`" + ` as usual: Flock's shims pick the versions this project uses.
- If other guidelines describe a different local development server, follow them only when Flock isn't the one serving this app.
`}

// ErrNotBoost is returned for a project that doesn't use Boost.
var ErrNotBoost = errors.New("the project doesn't use Laravel Boost")

// ErrCustom is returned when the project has a flock.md Flock didn't write.
var ErrCustom = errors.New(".ai/guidelines/flock.md was written or edited by hand, so Flock leaves it alone")

// File is where Flock's guideline goes in a project.
func File(dir string) string { return filepath.Join(dir, ".ai", "guidelines", "flock.md") }

// Uses reports whether the project in dir uses Boost: it has boost.json, or requires laravel/boost.
func Uses(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "boost.json")); err == nil {
		return true
	}
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return false
	}
	var composer struct {
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
	}
	if json.Unmarshal(data, &composer) != nil {
		return false
	}
	_, a := composer.Require["laravel/boost"]
	_, b := composer.RequireDev["laravel/boost"]
	return a || b
}

// State reports whether the project uses Boost and has Flock's guideline.
func State(dir string) string {
	if !Uses(dir) {
		return None
	}
	data, err := os.ReadFile(File(dir))
	if err != nil {
		return Missing
	}
	text := normalize(string(data))
	switch {
	case text == normalize(Guideline):
		return Current
	case slices.ContainsFunc(previous, func(p string) bool { return text == normalize(p) }):
		return Outdated
	}
	return Custom
}

func normalize(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")) }

// Write writes Flock's guideline into the project, unless the project has its own flock.md.
func Write(dir string) error {
	switch State(dir) {
	case None:
		return ErrNotBoost
	case Custom:
		return ErrCustom
	}
	path := File(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(Guideline), 0o644)
}

// Update runs `php artisan boost:update` in the project through flock (the binary), so it uses
// the project's PHP version, and returns its output.
func Update(ctx context.Context, flock, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, flock, "exec", "php", "artisan", "boost:update", "--no-interaction", "--no-discover")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("php artisan boost:update failed: %s", lastLine(text))
	}
	return text, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Apply writes the guideline and has Boost add it to the agents' files.
func Apply(ctx context.Context, flock, dir string) (string, error) {
	if err := Write(dir); err != nil {
		return "", err
	}
	return Update(ctx, flock, dir)
}
