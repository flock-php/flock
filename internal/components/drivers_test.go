package components

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"flock/internal/config"
	"flock/internal/postgres"
	"flock/internal/valkey"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func configMutate(root string) Mutate {
	return func(change func(*config.Config) error) error {
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		if err := change(cfg); err != nil {
			return err
		}
		return cfg.Save(root)
	}
}

func TestValkeyApplyAndUninstallKeepData(t *testing.T) {
	root := t.TempDir()
	mutate := configMutate(root)
	touch(t, valkey.Server(root, "9.0.6"))
	touch(t, valkey.Server(root, "9.1.2"))
	touch(t, filepath.Join(valkey.DataDir(root), "dump.rdb"))
	_ = mutate(func(cfg *config.Config) error { cfg.Services.SetRunning(Valkey, false); return nil })

	if err := ApplyValkey(root, mutate, "9.1.2"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(root)
	if cfg.Services.Valkey != "9.1.2" || !cfg.Services.Running(Valkey) || InstalledValkey(root) != "9.1.2" {
		t.Fatalf("got %+v", cfg.Services)
	}
	if _, err := os.Stat(valkey.Dir(root, "9.0.6")); !os.IsNotExist(err) {
		t.Fatal("old version kept")
	}

	if err := UninstallValkey(root, mutate); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(root)
	if cfg.Services.Valkey != "" || InstalledValkey(root) != "" {
		t.Fatalf("got %+v", cfg.Services)
	}
	if _, err := os.Stat(filepath.Join(valkey.DataDir(root), "dump.rdb")); err != nil {
		t.Fatal("data deleted")
	}
}

func TestClientApplyAndUninstall(t *testing.T) {
	root := t.TempDir()
	mutate := configMutate(root)
	touch(t, postgres.ClientTool(root, "18.9", "psql"))
	touch(t, postgres.ClientTool(root, "18.10", "psql"))

	if InstalledClient(root) != "18.10" {
		t.Fatalf("newest must win numerically, got %s", InstalledClient(root))
	}
	if err := ApplyClient(root, mutate, "18.10"); err != nil {
		t.Fatal(err)
	}
	if versions, _ := postgres.InstalledClients(root); len(versions) != 1 {
		t.Fatalf("got %v", versions)
	}
	if cfg, _ := config.Load(root); cfg.Services.PostgresClient != "18.10" {
		t.Fatal("config not updated")
	}
	if err := UninstallClient(root, mutate); err != nil || InstalledClient(root) != "" {
		t.Fatalf("uninstall: %v", err)
	}
}

func TestDriversDescribeThePlatform(t *testing.T) {
	drivers := Drivers(t.TempDir(), configMutate(t.TempDir()))
	want := MethodBuild
	if runtime.GOOS == "windows" {
		want = MethodDownload
		if len(drivers) != 1 || drivers[0].ID != PostgresClient {
			t.Fatalf("got %v", drivers)
		}
	} else if len(drivers) != 2 || drivers[0].ID != Valkey || drivers[1].ID != PostgresClient {
		t.Fatalf("got %v", drivers)
	}
	for _, d := range drivers {
		if d.Method != want || d.Estimate == "" || d.Name == "" || d.Latest == nil || d.Install == nil {
			t.Fatalf("got %+v", d)
		}
	}
}
