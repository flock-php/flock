package postgres

import (
	"io/fs"
	"os"
	"path/filepath"
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

// HasCluster reports whether dataDir holds a cluster.
func HasCluster(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "PG_VERSION"))
	return err == nil
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
