package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/certs"
	"flock/internal/node"
	"flock/internal/paths"
	"flock/internal/php"
)

const composerURL = "https://getcomposer.org/download/latest-stable/composer.phar"

// serviceNote is what `flock install` reports once installService is done.
var serviceNote = "daemon runs in the background and starts at login"

func newInstallCommand() *cobra.Command {
	var skipPrivileged, privilegedOnly, ignoreConflicts bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Set Flock up: shims on PATH, background daemon, .test DNS and trusted HTTPS (asks for your password once)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			// Another local environment on Flock's ports would leave it half working. Only the first
			// install checks: an update runs this command too, and the daemon reports busy ports.
			if !serviceInstalled() && !privilegedOnly && !ignoreConflicts {
				if found := findConflicts(s); len(found) > 0 {
					return conflictsError(found)
				}
			}

			// With privilegedFirst, the steps that need an administrator come before anything is
			// replaced: declining them leaves the installed version as it was.
			var ca *certs.CA
			if privilegedFirst {
				if ca, err = ensureCA(cmd, s); err != nil {
					return err
				}
				if !skipPrivileged {
					if err := ensurePrivileged(cmd, s, "", ca); err != nil {
						return err
					}
				}
				if privilegedOnly {
					return nil
				}
			}

			if err := s.cfg.Save(s.root); err != nil {
				return err
			}

			bin, err := installBinary(s.root)
			if err != nil {
				return fmt.Errorf("install flock binary: %w", err)
			}
			if err := installHelperBinary(bin); err != nil {
				return fmt.Errorf("install flock binary: %w", err)
			}
			cmd.Printf("✓ flock binary → %s\n", bin)

			if err := writeShims(s.root, bin); err != nil {
				return fmt.Errorf("write shims: %w", err)
			}
			cmd.Printf("✓ shims for %s\n", strings.Join(tools, ", "))

			if err := ensureComposer(cmd.Context(), s.root); err != nil {
				return fmt.Errorf("download composer: %w", err)
			}
			cmd.Println("✓ composer")

			if !privilegedFirst {
				if ca, err = ensureCA(cmd, s); err != nil {
					return err
				}
			}

			// Flock's shims first, then the commands of the default Node.js version's global packages.
			if err := addToShellPath(filepath.Join(s.root, "bin"), node.GlobalBin(node.CurrentGlobalDir(s.root))); err != nil {
				cmd.Printf("! could not update your shell profile: %v\n", err)
			}

			// Until the first install gets through its privileged steps, Flock isn't set up: the apps
			// take a registered daemon for a finished setup (Installation.isSetUp, IsSetUp).
			firstInstall := !serviceInstalled()
			if err := installService(s.root, bin); err != nil {
				return fmt.Errorf("install background service: %w", err)
			}
			cmd.Println("✓ " + serviceNote)

			if skipPrivileged || privilegedFirst {
				return nil
			}
			if err := ensurePrivileged(cmd, s, bin, ca); err != nil {
				if firstInstall {
					_ = uninstallService()
				}
				return err
			}
			// The daemon started before the privileged steps and still reports what was missing
			// then (e.g. the hosts helper); have it check again now instead of on its next retry.
			_, _ = s.client().Reload(cmd.Context())
			return nil
		},
	}

	cmd.Flags().BoolVar(&skipPrivileged, "skip-privileged", false, "skip the steps that need an administrator password")
	cmd.Flags().BoolVar(&ignoreConflicts, "ignore-conflicts", false, "set up even when other programs use Flock's ports (see `flock conflicts`)")
	if privilegedFirst {
		// The installer checks these before it replaces the app (see windows/installer/Flock.iss).
		cmd.Flags().BoolVar(&privilegedOnly, "privileged-only", false, "only the steps that need an administrator")
		_ = cmd.Flags().MarkHidden("privileged-only")
	}
	return cmd
}

// ensureCA creates Flock's certificate authority unless it exists.
func ensureCA(cmd *cobra.Command, s *state) (*certs.CA, error) {
	ca, err := certs.Ensure(filepath.Join(s.root, "certs"), s.cfg.TLD)
	if err != nil {
		return nil, err
	}
	cmd.Printf("✓ local certificate authority (%s)\n", ca.Fingerprint())
	return ca, nil
}

// installBinary copies the running flock executable to <root>/bin so the shims and the service
// keep working when the original moves (e.g. a rebuilt dev binary).
func installBinary(root string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return "", err
	}

	dst := filepath.Join(root, "bin", paths.CLI())
	if self == dst {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}

	data, err := os.ReadFile(self)
	if err != nil {
		return "", err
	}
	// Write then rename: replacing a running binary in place would crash it.
	return dst, replaceExecutable(dst, data)
}

// replaceExecutable writes data to path via a temporary file and a rename. Windows refuses to
// replace an executable that is running (e.g. a shim or the daemon), but lets it be renamed, so
// the running one is moved aside first; leftovers are removed on the next install.
func replaceExecutable(path string, data []byte) error {
	if old, _ := filepath.Glob(path + ".old*"); len(old) > 0 {
		for _, o := range old {
			_ = os.Remove(o)
		}
	}

	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	err := os.Rename(tmp, path)
	if err == nil {
		return nil
	}
	aside := fmt.Sprintf("%s.old%d", path, time.Now().UnixNano())
	if os.Rename(path, aside) != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func ensureComposer(ctx context.Context, root string) error {
	path := php.ComposerPhar(root)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, composerURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", composerURL, resp.Status)
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
