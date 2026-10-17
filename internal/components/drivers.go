package components

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"flock/internal/config"
	"flock/internal/valkey"
)

// Mutate edits config.json and applies it (the daemon's Mutate, or a CLI equivalent).
type Mutate func(change func(*config.Config) error) error

// DefaultWorkspace is where macOS builds happen: ~/Library/Caches/dev.flock/components-build, a
// cache without spaces (PostgreSQL's configure can't handle them; Application Support has one).
func DefaultWorkspace() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "dev.flock", "components-build"), nil
}

// Drivers returns the components of this platform: Valkey, built from source on macOS. Windows has
// none (the catalog offers Redis there).
func Drivers(root string, mutate Mutate) []*Driver {
	if runtime.GOOS == "windows" {
		return nil
	}

	src := DefaultSources()
	valkeyDriver := &Driver{
		ID:          Valkey,
		Name:        "Valkey",
		Description: "In-memory store for cache, queues and sessions",
		Method:      MethodBuild,
		Estimate:    "about 1 minute",
		Installed:   func() string { return InstalledValkey(root) },
		Latest:      src.LatestValkey,
		Install: func(ctx context.Context, version string, r *Reporter) error {
			ws, err := DefaultWorkspace()
			if err != nil {
				return err
			}
			dir, cleanup, err := BuildValkey(ctx, src, ws, version, r)
			if err != nil {
				return err
			}
			defer cleanup()
			r.Phase(PhaseInstalling, -1)
			v, err := valkey.InstallFrom(root, dir)
			if err != nil {
				return err
			}
			return ApplyValkey(root, mutate, v)
		},
		Uninstall: func() error { return UninstallValkey(root, mutate) },
	}
	return []*Driver{valkeyDriver}
}

// InstalledValkey is the configured Valkey version, when its binaries are there.
func InstalledValkey(root string) string {
	cfg, err := config.Load(root)
	if err != nil || cfg.Services.Valkey == "" {
		return ""
	}
	if _, err := os.Stat(valkey.Server(root, cfg.Services.Valkey)); err != nil {
		return ""
	}
	return cfg.Services.Valkey
}

// ApplyValkey makes version the Valkey service, (re)starts it and removes the other versions.
func ApplyValkey(root string, mutate Mutate, version string) error {
	err := mutate(func(cfg *config.Config) error {
		cfg.Services.Valkey = version
		cfg.Services.SetRunning(Valkey, true)
		return nil
	})
	if err != nil {
		return err
	}
	return removeValkeyVersions(root, version)
}

// UninstallValkey stops Valkey, removes it from the config and deletes its binaries. Its data stays.
func UninstallValkey(root string, mutate Mutate) error {
	err := mutate(func(cfg *config.Config) error {
		cfg.Services.Valkey = ""
		cfg.Services.SetRunning(Valkey, true) // forget that it was switched off
		return nil
	})
	if err != nil {
		return err
	}
	return removeValkeyVersions(root, "")
}

func removeValkeyVersions(root, keep string) error {
	versions, err := valkey.Installed(root)
	if err != nil {
		return err
	}
	var errs []error
	for _, v := range versions {
		if v != keep {
			errs = append(errs, os.RemoveAll(valkey.Dir(root, v)))
		}
	}
	return errors.Join(errs...)
}
