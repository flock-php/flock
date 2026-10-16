package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/phpbuild"
)

// Flock's data dir, entry by entry. Only these names are ever deleted, so a FLOCK_HOME pointing
// somewhere unexpected can't take other files with it.
var (
	// rebuildable: programs, downloads, certificates, logs and runtime files.
	rebuildable = []string{"bin", "cache", "caddy", "certs", "log", "mail", "dumps", "tools", "php", "node", "node-global", "run",
		"api.json", "flockd.sock", "hosts.txt", "hosts.txt.tmp", "hosts-helper-blocked", "config.json.tmp"}
	// userData survives a plain uninstall: sites, parked folders and settings, and in services/
	// each service's data/ (the PostgreSQL, Valkey, MySQL, Redis, ... databases; per version line).
	userData = []string{"config.json", "services"}
)

func uninstallFlags(cmd *cobra.Command, purge *bool) {
	cmd.Flags().BoolVar(purge, "purge", false, "also delete your sites' settings and databases, without asking")
}

// decidePurge settles, before anything is removed, whether the user's data goes too: --purge says
// so; otherwise someone at the terminal is asked (default no), and scripts and uninstallers keep it.
func decidePurge(root string, purge bool) bool {
	if purge || !interactive() || !hasUserData(root) {
		return purge
	}
	return confirm("Also delete your sites' settings and your databases (PostgreSQL, MySQL, Redis, ...)?", false)
}

// hasUserData reports whether a plain uninstall would keep anything.
func hasUserData(root string) bool {
	for _, name := range userData {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	return false
}

// finishUninstall deletes Flock's files once the daemon, PATH entry and system integration are
// gone, and says what it kept.
func finishUninstall(cmd *cobra.Command, root string, purge bool) error {
	kept, err := cleanUp(root, purge)
	if err != nil {
		return fmt.Errorf("remove Flock's files: %w", err)
	}
	cmd.Println("✓ removed Flock's programs, PHP and Node.js versions, certificates, logs and caches")
	if len(kept) == 0 {
		if purge {
			cmd.Println("✓ removed your sites' settings and databases")
		}
		return nil
	}
	cmd.Println("kept your sites' settings and databases (reinstalling picks them up again):")
	for _, path := range kept {
		cmd.Printf("  %s\n", path)
	}
	cmd.Printf("to remove them as well, delete %s\n", root)
	return nil
}

// cleanUp deletes what Flock can download or create again, and with purge the user's data too.
// It returns the paths it kept.
func cleanUp(root string, purge bool) ([]string, error) {
	var kept []string
	var errs []error

	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		switch {
		case slices.Contains(rebuildable, e.Name()), purge && slices.Contains(userData, e.Name()):
			errs = append(errs, remove(path))
		case e.Name() == "services" && e.IsDir():
			k, err := cleanServices(path)
			kept = append(kept, k...)
			errs = append(errs, err)
		case e.Name() == "config.json":
			kept = append(kept, path)
		}
	}
	if purge {
		// Only empty now, unless something Flock doesn't know is in there.
		_ = os.Remove(root)
	}

	for _, dir := range outsideDirs() {
		errs = append(errs, remove(dir))
	}
	return kept, errors.Join(errs...)
}

// outsideDirs are Flock's caches and the app's own files, outside the data dir.
var outsideDirs = func() []string {
	dirs := appDirs()
	if ws, err := phpbuild.DefaultWorkspace(); err == nil {
		dirs = append(dirs, filepath.Dir(ws))
	}
	return dirs
}

// cleanServices deletes the installed service binaries and keeps each service's data/.
func cleanServices(dir string) ([]string, error) {
	services, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var kept []string
	var errs []error
	for _, service := range services {
		serviceDir := filepath.Join(dir, service.Name())
		entries, err := os.ReadDir(serviceDir)
		if err != nil {
			errs = append(errs, remove(serviceDir))
			continue
		}
		hasData := false
		for _, e := range entries {
			// data.migrating is a PostgreSQL cluster halfway through its move into data/<major>.
			if (e.Name() == "data" || e.Name() == "data.migrating") && e.IsDir() {
				hasData = true
				kept = append(kept, filepath.Join(serviceDir, e.Name()))
				continue
			}
			errs = append(errs, remove(filepath.Join(serviceDir, e.Name())))
		}
		if !hasData {
			errs = append(errs, remove(serviceDir))
		}
	}
	if len(kept) == 0 {
		errs = append(errs, remove(dir))
	}
	return kept, errors.Join(errs...)
}

// remove deletes path. Processes the daemon ran may still be exiting, so it retries for a moment;
// files that stay in use (on Windows, the running flock.exe and its shims) go once flock has exited.
func remove(path string) error {
	var err error
	for range 10 {
		if err = os.RemoveAll(path); err == nil {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	if removeAfterExit(path) {
		return nil
	}
	return err
}
