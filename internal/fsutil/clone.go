package fsutil

import (
	"os"
	"path/filepath"
)

// CanClone reports whether files can be cloned copy-on-write from the folder of src into dir:
// both on one APFS volume (macOS), or one btrfs, XFS or bcachefs filesystem (Linux). It clones
// src, a file, into dir and removes the clone again.
func CanClone(src, dir string) bool {
	probe := filepath.Join(dir, ".flock-clone-probe")
	_ = os.Remove(probe)
	ok := cloneFile(src, probe) == nil
	_ = os.Remove(probe)
	return ok
}

// CloneTree copies src into dst (which must not exist), cloning files copy-on-write where the
// filesystem can and copying them otherwise; modes and symlinks are kept. A clone shares its
// blocks with the original until either changes, so even a large vendor/ costs no time or space.
func CloneTree(src, dst string) error {
	if cloneDir(src, dst) == nil {
		return nil
	}
	_ = os.RemoveAll(dst) // whatever a failed clone left
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		default:
			if cloneFile(path, target) == nil {
				return nil
			}
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}

// CloneFile copies the file src to dst (which must not exist), copy-on-write where it can.
func CloneFile(src, dst string) error {
	if cloneFile(src, dst) == nil {
		return nil
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return copyFile(src, dst, info.Mode().Perm())
}
