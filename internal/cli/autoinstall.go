package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"flock/internal/api"
	"flock/internal/node"
	"flock/internal/phpbuild"
	"flock/internal/postgres"
)

// phpMissingError reports that the PHP version a project (or the default) needs isn't installed.
type phpMissingError struct{ minor string }

func (e *phpMissingError) Error() string {
	return fmt.Sprintf("PHP %s is not installed (install it with `flock php install %s`)", e.minor, e.minor)
}

// interactive reports whether a person is at the terminal. Shims run by IDEs, Composer scripts or
// pipes never prompt: a question nobody sees would hang them.
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// confirm asks a yes/no question on stderr (keeping stdout clean); Enter picks the default.
func confirm(question string, defaultYes bool) bool {
	choices := "[y/N]"
	if defaultYes {
		choices = "[Y/n]"
	}
	fmt.Fprintf(os.Stderr, "%s %s ", question, choices)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "":
		return defaultYes
	}
	return false
}

// errRedisMissing and errPostgresClientMissing: a redis-cli or psql/pg_dump/... shim found nothing installed.
var (
	errRedisMissing          = errors.New("neither Redis nor Valkey is installed — run `flock services install redis` or `flock valkey install`")
	errPostgresClientMissing = errors.New("PostgreSQL client tools are not installed — run `flock postgres install-client`")
)

// offerInstall asks whether to install the version a shim found missing (err) and installs it.
// It reports whether the version (or service) is installed now; when err is about something else, the user
// declines or nothing can be installed, it returns false and the shim reports err as before.
func offerInstall(ctx context.Context, s *state, err error) (bool, error) {
	var phpErr *phpMissingError
	var nodeErr *node.NotInstalledError
	var question string
	var install func(ctx context.Context) error

	switch {
	case errors.As(err, &phpErr):
		minor := phpErr.minor
		v, err := phpRelease(ctx, s.client(), minor)
		if errors.Is(err, api.ErrDaemonNotRunning) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if question, err = phpQuestion(v); err != nil {
			return false, err
		}
		install = func(ctx context.Context) error { return installPHP(ctx, s, minor, os.Stderr) }

	case errors.As(err, &nodeErr):
		question = fmt.Sprintf("Node.js %s isn't installed. Download and install it now?", nodeErr.Spec)
		install = func(ctx context.Context) error { return installNode(ctx, s, nodeErr.Spec, os.Stderr) }

	// Valkey, built from source, on macOS; on Windows Redis is installed through the daemon.
	case errors.Is(err, errRedisMissing):
		if runtime.GOOS != "darwin" {
			return false, nil
		}
		question = fmt.Sprintf("Redis isn't installed. Build Valkey (the open-source Redis) from source and start it on 127.0.0.1:%d? It takes about a minute.", s.cfg.Ports.Valkey)
		install = func(ctx context.Context) error { return installValkey(ctx, s, "", os.Stderr, os.Stderr) }

	case errors.Is(err, errPostgresClientMissing):
		if b, ok := postgres.PrebuiltClient(runtime.GOOS, runtime.GOARCH); ok {
			question = fmt.Sprintf("The PostgreSQL client tools aren't installed. Download PostgreSQL %s (%d MB, Flock keeps only the client tools)?", b.Version, b.Size>>20)
		} else if runtime.GOOS == "darwin" {
			question = "The PostgreSQL client tools aren't installed. Build them from source now? It takes about two minutes."
		} else {
			return false, nil
		}
		install = func(ctx context.Context) error { return installPostgresClient(ctx, s, "", os.Stderr, os.Stderr) }

	default:
		return false, nil
	}

	if !confirm(question, true) {
		return false, nil
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	return true, install(ctx)
}

// phpQuestion is the prompt for a missing PHP version: what installing it takes differs between
// a ready build, a download (Windows) and a build from source (macOS).
func phpQuestion(v api.PHPRelease) (string, error) {
	switch {
	case v.Status == "unsupported":
		return "", fmt.Errorf("PHP %s is not installed, and can't be: %s", v.Minor, v.Note)
	case v.Job != nil && (v.Job.State == phpbuild.Queued || v.Job.State == phpbuild.Running):
		return fmt.Sprintf("PHP %s isn't installed, but Flock is already preparing it. Wait for it and install it?", v.Minor), nil
	case v.Status == "ready":
		return fmt.Sprintf("PHP %s isn't installed. Install it now?", v.Minor), nil
	case runtime.GOOS == "windows":
		return fmt.Sprintf("PHP %s isn't installed. Download and install it now?", v.Minor), nil
	default:
		return fmt.Sprintf("PHP %s isn't installed. Build it now? Compiling PHP takes a few minutes.", v.Minor), nil
	}
}

// phpRelease returns the daemon's view of minor, fetching the releases first when it doesn't
// know it yet (right after the daemon started, or for a brand-new PHP version).
func phpRelease(ctx context.Context, c *api.Client, minor string) (api.PHPRelease, error) {
	versions, err := c.PHPVersions(ctx)
	if err != nil {
		return api.PHPRelease{}, err
	}
	if v, ok := versions.Find(minor); ok {
		return v, nil
	}

	if versions, err = c.CheckPHP(ctx); err != nil {
		return api.PHPRelease{}, err
	}
	if v, ok := versions.Find(minor); ok {
		return v, nil
	}
	if versions.CheckError != "" {
		return api.PHPRelease{}, fmt.Errorf("could not look up PHP releases: %s", versions.CheckError)
	}
	return api.PHPRelease{}, fmt.Errorf("there is no PHP %s release Flock can install", minor)
}

// installPHP has the daemon install the latest release of minor and waits until it is installed,
// showing the build's progress on out. When ctx ends (Ctrl+C) the build is canceled.
func installPHP(ctx context.Context, s *state, minor string, out io.Writer) error {
	c := s.client()
	if _, err := c.InstallPHP(ctx, minor); err != nil {
		return err
	}

	p := newProgress(out)
	defer p.clear()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		versions, err := c.PHPVersions(ctx)
		if ctx.Err() != nil {
			p.clear()
			if err := c.CancelPHPBuild(context.Background(), minor); err != nil {
				return fmt.Errorf("could not cancel the PHP %s build: %w", minor, err)
			}
			return fmt.Errorf("canceled — PHP %s was not installed", minor)
		}
		if err != nil {
			return err
		}

		v, _ := versions.Find(minor)
		switch {
		case v.Job == nil && v.Installed != "":
			p.clear()
			fmt.Fprintf(out, "installed PHP %s\n", v.Installed)
			return nil
		case v.Job == nil:
			return fmt.Errorf("the PHP %s install was canceled", minor)
		case v.Job.State == phpbuild.Failed:
			p.clear()
			return fmt.Errorf("installing PHP %s failed: %s (log: %s)", minor, v.Job.Error, phpbuild.LogPath(s.root, minor))
		case v.Job.State == phpbuild.Queued:
			p.set(fmt.Sprintf("PHP %s: waiting for another PHP build to finish", minor))
		default:
			p.set(v.Job.Step)
		}

		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
}

// installNode downloads a Node.js version matching spec. When ctx ends (Ctrl+C) nothing is installed.
func installNode(ctx context.Context, s *state, spec string, out io.Writer) error {
	fmt.Fprintf(out, "downloading Node.js %s…\n", spec)
	v, err := (&node.Installer{Root: s.root}).Install(ctx, spec)
	if ctx.Err() != nil {
		return fmt.Errorf("canceled — Node.js %s was not installed", spec)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "installed Node.js %s\n", v)
	return nil
}

// progress shows a changing status line: rewritten in place on a terminal, one line per change
// otherwise (e.g. in a log). It avoids escape sequences, which older Windows consoles print as is.
type progress struct {
	out   io.Writer
	fd    int
	tty   bool
	raw   string // the last line as set
	shown string // what is on screen (shortened to the terminal width)
}

func newProgress(out io.Writer) *progress {
	p := &progress{out: out}
	if f, ok := out.(*os.File); ok {
		p.fd = int(f.Fd())
		p.tty = term.IsTerminal(p.fd)
	}
	return p
}

func (p *progress) set(line string) {
	if line == "" || line == p.raw {
		return
	}
	p.raw = line
	if !p.tty {
		fmt.Fprintln(p.out, line)
		return
	}

	// A line wider than the terminal would wrap, and \r only returns to the last row.
	runes := []rune(line)
	if width, _, err := term.GetSize(p.fd); err == nil && width > 1 && len(runes) >= width {
		line = string(runes[:width-2]) + "…"
	}
	pad := max(0, len([]rune(p.shown))-len([]rune(line)))
	fmt.Fprintf(p.out, "\r%s%s", line, strings.Repeat(" ", pad))
	p.shown = line
}

// clear removes the status line from the terminal.
func (p *progress) clear() {
	if p.tty && p.shown != "" {
		fmt.Fprintf(p.out, "\r%s\r", strings.Repeat(" ", len([]rune(p.shown))))
	}
	p.raw, p.shown = "", ""
}
