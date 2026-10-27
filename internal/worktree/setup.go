package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"flock/internal/fsutil"
)

// Job is the setup of one worktree.
type Job struct {
	Site         string // the parent site's folder
	Path         string // the worktree's folder
	Parent       string // the parent site's name
	Branch       string
	Domain       string // the worktree's domain, feature-auth.acme.test
	ParentDomain string
	Secured      bool
	Settings     Settings
	// Flock is the flock binary: installs run through `flock exec`, so they use the worktree's
	// PHP and Node.js. BinDirs go first on PATH for the hooks (Flock's shims, global npm tools).
	Flock   string
	BinDirs []string
	// AsRoot is set when the daemon runs as root: it then leaves the worktree alone.
	AsRoot bool
}

// Steps of a setup, as Status.Step names them.
const (
	StepCopy     = "copying files"
	StepEnv      = "writing .env"
	StepComposer = "composer install"
	StepKey      = "generating the app key"
	StepJS       = "installing JS packages"
	StepHooks    = "running hooks"
)

// Vars are the job's placeholders (see Vars.Expand).
func (j Job) Vars() Vars {
	scheme := "http"
	if j.Secured {
		scheme = "https"
	}
	return Vars{
		"domain": j.Domain, "url": scheme + "://" + j.Domain, "scheme": scheme, "branch": slugOf(j.Domain, j.ParentDomain),
		"parent": j.Parent, "parent_domain": j.ParentDomain, "folder": filepath.Base(j.Site),
	}
}

func slugOf(domain, parentDomain string) string {
	return strings.TrimSuffix(domain, "."+parentDomain)
}

// Run sets the worktree up, writing what it does to log and naming each step as it starts it.
// Every step first checks whether it is needed, so running it again is safe.
func (j Job) Run(ctx context.Context, log io.Writer, step func(string)) error {
	if j.AsRoot {
		// Files it wrote would belong to root, and installs and hooks are the project's own code.
		fmt.Fprintln(log, "Flock runs as root here, so it doesn't set worktrees up: copy .env and run the installs in the worktree yourself.")
		return nil
	}

	step(StepCopy)
	created, err := j.copyIn(log)
	if err != nil {
		return fmt.Errorf("copying files: %w", err)
	}

	step(StepEnv)
	if err := j.writeEnv(log); err != nil {
		return fmt.Errorf("writing .env: %w", err)
	}

	if exists(filepath.Join(j.Path, "composer.json")) {
		if j.sameLock("composer.lock") && exists(filepath.Join(j.Path, "vendor")) {
			fmt.Fprintln(log, "composer.lock is the site's, vendor/ is up to date")
		} else {
			step(StepComposer)
			if err := j.run(ctx, log, j.Flock, "exec", "composer", "install", "--no-interaction"); err != nil {
				return fmt.Errorf("composer install failed: %w", err)
			}
		}
	}

	if created && exists(filepath.Join(j.Path, "artisan")) && exists(filepath.Join(j.Path, "vendor")) {
		if env, err := ReadDotenv(filepath.Join(j.Path, ".env")); err == nil && env.Get("APP_KEY") == "" {
			step(StepKey)
			if err := j.run(ctx, log, j.Flock, "exec", "php", "artisan", "key:generate", "--force", "--no-interaction"); err != nil {
				return fmt.Errorf("php artisan key:generate failed: %w", err)
			}
		}
	}

	if err := j.installJS(ctx, log, step); err != nil {
		return err
	}

	if len(j.Settings.AfterCreate) > 0 {
		step(StepHooks)
		if err := j.RunHooks(ctx, log, "after_create", j.Settings.AfterCreate); err != nil {
			return err
		}
	}
	return nil
}

// copyIn copies what git doesn't track from the site into the worktree, where the worktree lacks
// it: vendor/ and node_modules/ only copy-on-write (a plain copy is hardly faster than installing,
// and doubles the space), .env, and the paths under `copy:`. created reports a new .env.
func (j Job) copyIn(log io.Writer) (created bool, err error) {
	probe := ""
	for _, name := range []string{"composer.json", "package.json", ".git"} {
		if info, err := os.Stat(filepath.Join(j.Site, name)); err == nil && !info.IsDir() {
			probe = filepath.Join(j.Site, name)
			break
		}
	}
	cow := probe != "" && fsutil.CanClone(probe, j.Path)

	for _, dir := range []string{"vendor", "node_modules"} {
		src, dst := filepath.Join(j.Site, dir), filepath.Join(j.Path, dir)
		if !exists(src) || exists(dst) {
			continue
		}
		if !cow {
			fmt.Fprintf(log, "%s/: not copied, this disk can't copy-on-write; it gets installed instead\n", dir)
			continue
		}
		if err := fsutil.CloneTree(src, dst); err != nil {
			return false, err
		}
		fmt.Fprintf(log, "%s/: copied from the site (copy-on-write)\n", dir)
	}

	env := filepath.Join(j.Path, ".env")
	if !exists(env) {
		src := filepath.Join(j.Site, ".env")
		if !exists(src) {
			src = filepath.Join(j.Path, ".env.example")
		}
		if exists(src) {
			if err := fsutil.CloneFile(src, env); err != nil {
				return false, err
			}
			created = true
			fmt.Fprintf(log, ".env: copied from %s\n", src)
		}
	}

	for _, rel := range j.Settings.Copy {
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			fmt.Fprintf(log, "copy: %s is outside the project, skipped\n", rel)
			continue
		}
		src, dst := filepath.Join(j.Site, clean), filepath.Join(j.Path, clean)
		info, err := os.Stat(src)
		if err != nil || exists(dst) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return created, err
		}
		if info.IsDir() {
			err = fsutil.CloneTree(src, dst)
		} else {
			err = fsutil.CloneFile(src, dst)
		}
		if err != nil {
			return created, err
		}
		fmt.Fprintf(log, "%s: copied from the site\n", rel)
	}
	return created, nil
}

// writeEnv points the worktree's .env at the worktree: APP_URL, the session and Sanctum domains
// where they named the site, a SQLite database by the site's path (the worktree shares it), then
// the `env:` values. Only keys that are there change, except those under `env:`.
func (j Job) writeEnv(log io.Writer) error {
	path := filepath.Join(j.Path, ".env")
	env, err := ReadDotenv(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	vars := j.Vars()
	set := func(key, value string) {
		if env.Get(key) != value {
			env.Set(key, value)
			fmt.Fprintf(log, ".env: %s=%s\n", key, value)
		}
	}

	if env.Has("APP_URL") {
		set("APP_URL", vars["url"])
	}
	if env.Get("SESSION_DOMAIN") == j.ParentDomain {
		set("SESSION_DOMAIN", j.Domain)
	}
	if v := env.Get("SANCTUM_STATEFUL_DOMAINS"); v != "" {
		parts := strings.Split(v, ",")
		for i, p := range parts {
			host, port, _ := strings.Cut(strings.TrimSpace(p), ":")
			if host == j.ParentDomain {
				parts[i] = j.Domain
				if port != "" {
					parts[i] += ":" + port
				}
			}
		}
		set("SANCTUM_STATEFUL_DOMAINS", strings.Join(parts, ","))
	}
	if env.Get("DB_CONNECTION") == "sqlite" {
		db := env.Get("DB_DATABASE")
		switch {
		case db == "":
			db = filepath.Join(j.Site, "database", "database.sqlite")
		case !filepath.IsAbs(db) && db != ":memory:":
			db = filepath.Join(j.Site, db)
		}
		set("DB_DATABASE", db)
	}
	for _, key := range j.Settings.EnvKeys {
		set(key, vars.Expand(j.Settings.Env[key]))
	}
	return env.Write(path)
}

// installJS installs the JS packages with the package manager the lock file belongs to, unless
// the lock file is the site's and node_modules/ was copied.
func (j Job) installJS(ctx context.Context, log io.Writer, step func(string)) error {
	if !exists(filepath.Join(j.Path, "package.json")) {
		return nil
	}
	managers := []struct{ lock, tool string }{
		{"package-lock.json", "npm"}, {"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"},
	}
	lock, tool := "", "npm"
	for _, m := range managers {
		if exists(filepath.Join(j.Path, m.lock)) {
			lock, tool = m.lock, m.tool
			break
		}
	}
	modules := exists(filepath.Join(j.Path, "node_modules"))
	if modules && (lock == "" || j.sameLock(lock)) {
		fmt.Fprintln(log, "node_modules/ is up to date")
		return nil
	}

	// `npm install`, not `npm ci`: ci deletes node_modules/ first, and with it what was copied.
	step(StepJS)
	var err error
	if tool == "npm" {
		err = j.run(ctx, log, j.Flock, "exec", "npm", "install")
	} else {
		var path string
		if path, err = j.lookPath(tool); err != nil {
			fmt.Fprintf(log, "%s isn't installed: run `%s install` in the worktree yourself\n", tool, tool)
			return nil
		}
		err = j.run(ctx, log, path, "install")
	}
	if err != nil {
		return fmt.Errorf("%s install failed: %w", tool, err)
	}
	return nil
}

// RunHooks runs a hook's commands one by one in the worktree, through the shell, with Flock's
// tools on PATH and the FLOCK_WORKTREE_* variables. The first that fails stops the hook.
func (j Job) RunHooks(ctx context.Context, log io.Writer, name string, commands []string) error {
	vars := j.Vars()
	for _, command := range commands {
		command = vars.Expand(command)
		var args []string
		if runtime.GOOS == "windows" {
			args = []string{"cmd", "/C", command}
		} else {
			args = []string{"/bin/sh", "-c", command}
		}
		fmt.Fprintf(log, "$ %s\n", command)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = j.Path, log, log
		cmd.Env = append(j.env(),
			"FLOCK_WORKTREE_PATH="+j.Path, "FLOCK_WORKTREE_BRANCH="+j.Branch, "FLOCK_WORKTREE_DOMAIN="+j.Domain,
			"FLOCK_WORKTREE_URL="+vars["url"], "FLOCK_WORKTREE_PARENT="+j.Parent)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s hook: `%s` failed: %w", name, command, err)
		}
	}
	return nil
}

// env is the daemon's environment with BinDirs first on PATH.
func (j Job) env() []string {
	env := os.Environ()
	path := strings.Join(append(append([]string{}, j.BinDirs...), os.Getenv("PATH")), string(os.PathListSeparator))
	for i, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); strings.EqualFold(name, "PATH") {
			env[i] = name + "=" + path
			return env
		}
	}
	return append(env, "PATH="+path)
}

func (j Job) lookPath(tool string) (string, error) {
	for _, dir := range append(append([]string{}, j.BinDirs...), filepath.SplitList(os.Getenv("PATH"))...) {
		for _, name := range []string{tool, tool + ".cmd", tool + ".exe"} {
			if path := filepath.Join(dir, name); exists(path) {
				return path, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

func (j Job) run(ctx context.Context, log io.Writer, name string, args ...string) error {
	fmt.Fprintf(log, "$ %s %s\n", filepath.Base(name), strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.Env = j.Path, log, log, j.env()
	return cmd.Run()
}

// sameLock reports whether the worktree's lock file is the site's, byte for byte.
func (j Job) sameLock(name string) bool {
	a, errA := os.ReadFile(filepath.Join(j.Path, name))
	b, errB := os.ReadFile(filepath.Join(j.Site, name))
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
