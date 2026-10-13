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
	"flock/internal/php"
)

const composerURL = "https://getcomposer.org/download/latest-stable/composer.phar"

func newInstallCommand() *cobra.Command {
	var skipPrivileged bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Set Flock up: shims on PATH, background daemon, .test DNS and trusted HTTPS (asks for your password once)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if err := s.cfg.Save(s.root); err != nil {
				return err
			}

			bin, err := installBinary(s.root)
			if err != nil {
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

			ca, err := certs.Ensure(filepath.Join(s.root, "certs"), s.cfg.TLD)
			if err != nil {
				return err
			}
			cmd.Printf("✓ local certificate authority (%s)\n", ca.Fingerprint())

			if err := addToShellPath(filepath.Join(s.root, "bin")); err != nil {
				cmd.Printf("! could not update your shell profile: %v\n", err)
			}

			if err := installService(s.root, bin); err != nil {
				return fmt.Errorf("install background service: %w", err)
			}
			cmd.Println("✓ daemon runs in the background and starts at login")

			if skipPrivileged {
				return nil
			}
			return ensurePrivileged(cmd, s, bin, ca)
		},
	}

	cmd.Flags().BoolVar(&skipPrivileged, "skip-privileged", false, "skip the steps that need an administrator password")
	return cmd
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

	dst := filepath.Join(root, "bin", exeName("flock"))
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
