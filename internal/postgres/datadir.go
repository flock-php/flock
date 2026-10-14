package postgres

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A cluster created by one major version can't be opened by another, so every major has its own
// data directory: <root>/services/postgres/data/<major>. Minor updates keep using it.

// DataRoot holds the clusters of every major version.
func DataRoot(root string) string {
	return filepath.Join(root, "services", "postgres", "data")
}

// DataDir is the cluster of a major version ("18").
func DataDir(root, major string) string {
	return filepath.Join(DataRoot(root), major)
}

// migratingDir is where MigrateLegacyDataDir parks the old cluster between its two renames.
func migratingDir(root string) string {
	return filepath.Join(root, "services", "postgres", "data.migrating")
}

// ClusterMajor reads the major version of the cluster in dataDir from its PG_VERSION file.
func ClusterMajor(dataDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "PG_VERSION"))
	if err != nil {
		return "", err
	}
	major := strings.TrimSpace(string(data))
	if _, err := strconv.Atoi(major); err != nil {
		return "", fmt.Errorf("unexpected PG_VERSION %q in %s", major, dataDir)
	}
	return major, nil
}

// HasCluster reports whether dataDir holds a cluster.
func HasCluster(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "PG_VERSION"))
	return err == nil
}

// MigrateLegacyDataDir moves the single cluster older Flock versions kept in services/postgres/data
// (shared by every version) to data/<major>, reading the major from its PG_VERSION. A postgres
// still running from it is stopped first (fast shutdown with an installed version of that major).
// It is idempotent and never deletes data: when data/<major> exists already, or the move can't
// finish, it returns an error and leaves everything in place.
func MigrateLegacyDataDir(root string) error {
	legacy, staging := DataRoot(root), migratingDir(root)

	if HasCluster(legacy) {
		major, err := ClusterMajor(legacy)
		if err != nil {
			return err
		}
		if err := stopLegacy(root, legacy, major); err != nil {
			return err
		}
		if _, err := os.Stat(staging); err == nil {
			return fmt.Errorf("can't move the PostgreSQL data in %s: %s exists already — move one of them away", legacy, staging)
		}
		if err := os.Rename(legacy, staging); err != nil {
			return fmt.Errorf("move the PostgreSQL %s data out of the way: %w", major, err)
		}
	}

	if _, err := os.Stat(staging); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	major, err := ClusterMajor(staging)
	if err != nil {
		return fmt.Errorf("unfinished PostgreSQL data move in %s: %w", staging, err)
	}
	target := DataDir(root, major)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("can't move the PostgreSQL %s data from %s: %s exists already — move one of them away", major, staging, target)
	}
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		return fmt.Errorf("move the PostgreSQL %s data to %s: %w", major, target, err)
	}
	return nil
}

// stopLegacy stops a postgres that runs from the legacy cluster (e.g. left behind by a daemon that
// was killed) and waits until it has exited.
func stopLegacy(root, dataDir, major string) error {
	pid := postmasterPID(dataDir)
	if pid <= 0 || !processAlive(pid) {
		return nil
	}

	versions, _ := InstalledVersions(root)
	stopped := false
	for _, v := range versions {
		if MajorOf(v) == major {
			stopped = StopFast(filepath.Join(root, "services", "postgres", v, "bin"), dataDir) == nil
			break
		}
	}
	if stopped {
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			if !processAlive(pid) {
				return nil
			}
		}
	}
	return fmt.Errorf("PostgreSQL (pid %d) still runs from %s, which Flock needs to move to %s — stop it and try again", pid, dataDir, DataDir(root, major))
}

// postmasterPID is the pid in dataDir/postmaster.pid, or 0.
func postmasterPID(dataDir string) int {
	data, err := os.ReadFile(filepath.Join(dataDir, "postmaster.pid"))
	if err != nil {
		return 0
	}
	first, _, _ := strings.Cut(string(data), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		return 0
	}
	return pid
}

// DirSize is the total size of the files under dir (0 when it doesn't exist).
func DirSize(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}
