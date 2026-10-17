package components

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"flock/internal/config"
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

func TestDriversDescribeThePlatform(t *testing.T) {
	drivers := Drivers(t.TempDir(), configMutate(t.TempDir()))
	if runtime.GOOS == "windows" {
		if len(drivers) != 0 {
			t.Fatalf("got %v", drivers)
		}
		return
	}
	if len(drivers) != 1 || drivers[0].ID != Valkey {
		t.Fatalf("got %v", drivers)
	}
	if d := drivers[0]; d.Method != MethodBuild || d.Estimate == "" || d.Name == "" || d.Latest == nil || d.Install == nil {
		t.Fatalf("got %+v", d)
	}
}
