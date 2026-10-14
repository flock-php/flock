package components

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"flock/internal/config"
	"flock/internal/postgres"
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

// Drivers returns the components of this platform: built from source on macOS, downloaded on
// Windows. Valkey has no Windows build, so there it isn't offered (the catalog has Redis).
func Drivers(root string, mutate Mutate) []*Driver {
	client := &Driver{
		ID:          PostgresClient,
		Name:        "PostgreSQL command-line tools",
		Description: "psql, pg_dump, pg_restore, pg_dumpall, createdb, dropdb",
		Installed:   func() string { return InstalledClient(root) },
		Install: func(ctx context.Context, version string, r *Reporter) error {
			return fmt.Errorf("no way to install the PostgreSQL tools on %s", runtime.GOOS)
		},
		Uninstall: func() error { return UninstallClient(root, mutate) },
	}

	if b, ok := postgres.PrebuiltClient(runtime.GOOS, runtime.GOARCH); ok {
		client.Method, client.Estimate = MethodDownload, fmt.Sprintf("%d MB download", b.Size>>20)
		client.Latest = func(context.Context) (string, error) { return b.Version, nil }
		client.Install = func(ctx context.Context, version string, r *Reporter) error {
			if version != b.Version {
				return fmt.Errorf("Flock can only download the PostgreSQL %s tools", b.Version)
			}
			r.Phase(PhaseDownloading, 0)
			v, err := postgres.DownloadClient(ctx, root, b, nil, r.Download())
			if err != nil {
				return err
			}
			r.Phase(PhaseInstalling, -1)
			return ApplyClient(root, mutate, v)
		}
	} else {
		src := DefaultSources()
		client.Method, client.Estimate = MethodBuild, "about 2 minutes"
		client.Latest = func(ctx context.Context) (string, error) { return src.LatestPostgres(ctx, ClientMajor) }
		client.Install = func(ctx context.Context, version string, r *Reporter) error {
			ws, err := DefaultWorkspace()
			if err != nil {
				return err
			}
			dir, cleanup, err := BuildPostgresClient(ctx, src, ws, version, r)
			if err != nil {
				return err
			}
			defer cleanup()
			r.Phase(PhaseInstalling, -1)
			v, err := postgres.InstallClientFrom(root, dir)
			if err != nil {
				return err
			}
			return ApplyClient(root, mutate, v)
		}
	}

	if runtime.GOOS == "windows" {
		return []*Driver{client}
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
	return []*Driver{valkeyDriver, client}
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

// InstalledClient is the newest installed version of the client tools.
func InstalledClient(root string) string {
	versions, _ := postgres.InstalledClients(root)
	if len(versions) == 0 {
		return ""
	}
	return versions[0]
}

// ApplyClient records version as the client tools and removes the other versions.
func ApplyClient(root string, mutate Mutate, version string) error {
	err := mutate(func(cfg *config.Config) error {
		cfg.Services.PostgresClient = version
		return nil
	})
	if err != nil {
		return err
	}
	versions, _ := postgres.InstalledClients(root)
	var errs []error
	for _, v := range versions {
		if v != version {
			errs = append(errs, os.RemoveAll(postgres.ClientDir(root, v)))
		}
	}
	return errors.Join(errs...)
}

// UninstallClient removes the client tools.
func UninstallClient(root string, mutate Mutate) error {
	err := mutate(func(cfg *config.Config) error {
		cfg.Services.PostgresClient = ""
		return nil
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(root, "services", "postgres-client"))
}
