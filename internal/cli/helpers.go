package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"flock/internal/api"
	"flock/internal/binaries"
	"flock/internal/config"
	"flock/internal/dns"
	"flock/internal/paths"
)

// parseTLDList reads a privileged-setup --tlds comma list into the suffixes it names: trimmed,
// lower-case, deduplicated. It is the root-run argv guard: every entry must pass dns.ValidTLD,
// the allowlist of what a resolved Domains= word, a resolver file name and a NRPT namespace can
// carry (one DNS label of [a-z0-9-], no edge hyphens, ≤63) — anything else is refused before a
// root process writes it. An empty list provisions nothing.
func parseTLDList(list string) ([]string, error) {
	var out []string
	for _, s := range strings.Split(list, ",") {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !dns.ValidTLD(s) {
			return nil, fmt.Errorf("invalid --tlds entry %q", s)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--tlds must name at least one suffix")
	}
	return out, nil
}

// parseCSVList reads a privileged-setup list flag (--ca, --roots, --untrust) into its
// trimmed, non-empty entries; an empty string gives no entries. The entries themselves are
// validated by the root-run command that consumes them, the way --tlds entries are.
func parseCSVList(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// suffixRootCerts lists the per-suffix root certificates Flock stores on disk under root —
// every certs/roots/<suffix>/ca.crt, sorted. The global root (certs/ca.crt) is not one of
// them: it belongs to the store, not to one suffix, and callers pass it separately. This is
// the whole disk set, the uninstall scope (tear every root down, active suffixes and stale
// ones alike); provisioning uses activeSuffixRootCerts — the disk set intersected with
// ActiveSuffixes, so a root whose suffix left the set is never handed back to the OS trust.
func suffixRootCerts(root string) []string {
	paths, _ := filepath.Glob(filepath.Join(root, "certs", "roots", "*", "ca.crt"))
	slices.Sort(paths)
	return paths
}

// untrustStoreRoots hands the OS untrust function every root certificate in Flock's store:
// the global CA first (certs/ca.crt — never in suffixRootCerts, so every root is untrusted
// exactly once), then every per-suffix root on disk, sorted: the whole disk set, active
// suffixes and stale ones alike, the uninstall scope macOS and Windows already loop. Best
// effort per certificate — one stubborn keychain entry or locked database doesn't stop the
// rest — and every result is printed to out where the user can act on it.
func untrustStoreRoots(root string, untrust func(string) error, out io.Writer) {
	if err := untrust(filepath.Join(root, "certs", "ca.crt")); err != nil {
		fmt.Fprintf(out, "! could not remove the global root's trust: %v\n", err)
	} else {
		fmt.Fprintln(out, "✓ the global root is no longer trusted")
	}
	for _, cert := range suffixRootCerts(root) {
		suffix := filepath.Base(filepath.Dir(cert))
		if err := untrust(cert); err != nil {
			fmt.Fprintf(out, "! could not remove the .%s root's trust: %v\n", suffix, err)
			continue
		}
		fmt.Fprintf(out, "✓ the .%s root is no longer trusted\n", suffix)
	}
}

// validGlobalCAPath is the root-run argv guard for a privileged-setup --ca: the certificate
// that goes into the OS trust store must be Flock's global root in its store layout — an
// absolute path, named ca.crt, in a directory named certs. Per-suffix roots ride --roots,
// which carries its own guard; anything not shaped like the store's global CA is refused
// before certutil sees it, the way every other argument of the elevated process is.
func validGlobalCAPath(caPath string) bool {
	return filepath.IsAbs(caPath) && filepath.Base(caPath) == "ca.crt" &&
		filepath.Base(filepath.Dir(caPath)) == "certs"
}

// state is the loaded config plus where it lives.
type state struct {
	root string
	cfg  *config.Config
	// lastWarnings are the status warnings the last commit printed, remembered so the
	// provisioning sync reports only what changed after the machine caught up (see
	// syncSuffixes).
	lastWarnings []string
}

func load() (*state, error) {
	root, err := paths.Root()
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}

	return &state{root: root, cfg: cfg}, nil
}

// binaries reads the package manifest of the configured channel (cached in the data directory).
func (s *state) binaries() *binaries.Catalog {
	return binaries.NewCatalog(binaries.DefaultSource(s.root, Version), func() binaries.Channel {
		ch, err := binaries.ParseChannel(s.cfg.Channel)
		if err != nil {
			return binaries.Stable
		}
		return ch
	})
}

func (s *state) client() *api.Client {
	return api.NewClient(paths.Socket(s.root))
}

// commit saves the config and asks a running daemon to apply it. The warnings it prints are
// remembered in lastWarnings, so the provisioning sync can report only the ones the user
// has not heard yet.
func (s *state) commit(ctx context.Context, out io.Writer) error {
	if err := s.cfg.Save(s.root); err != nil {
		return err
	}

	status, err := s.client().Reload(ctx)
	if errors.Is(err, api.ErrDaemonNotRunning) {
		s.lastWarnings = nil
		fmt.Fprintln(out, "saved — the daemon is not running, changes apply when it starts")
		return nil
	}
	if err != nil {
		return fmt.Errorf("daemon reload: %w", err)
	}

	for _, w := range status.Warnings {
		fmt.Fprintln(out, "warning: "+w)
	}
	s.lastWarnings = status.Warnings
	return nil
}
