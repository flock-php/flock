package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MigrateLegacy moves the Redis that older versions installed on Windows (as the "valkey"
// service, with redis-server.exe renamed to valkey-server.exe) to where the catalog keeps Redis:
// services/redis/<version>/redis-server.exe, with its data. config.Load already records it as
// the catalog's Redis.
func MigrateLegacy(root string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	return migrateWindowsRedis(root)
}

func migrateWindowsRedis(root string) error {
	old := filepath.Join(root, "services", "valkey")
	entries, err := os.ReadDir(old)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		src := filepath.Join(old, e.Name())
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.Name() == "data" {
			if err := moveIfMissing(src, DataDir(root, "redis", windowsRedisLine)); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(src, "valkey-server.exe")); err != nil {
			continue
		}
		for _, bin := range []string{"server", "cli"} {
			if err := os.Rename(filepath.Join(src, "valkey-"+bin+".exe"), filepath.Join(src, "redis-"+bin+".exe")); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := moveIfMissing(src, Dir(root, "redis", e.Name())); err != nil {
			return err
		}
	}
	if rest, _ := os.ReadDir(old); len(rest) == 0 {
		_ = os.Remove(old)
	}
	return nil
}

// moveIfMissing renames src to dst unless dst exists.
func moveIfMissing(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}
