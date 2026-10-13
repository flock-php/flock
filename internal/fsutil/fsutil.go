// Package fsutil has small filesystem helpers shared by the installers.
package fsutil

import (
	"io"
	"os"
	"path/filepath"
)

// CopyTree copies src into dst (which must not exist), preserving file modes and symlinks.
func CopyTree(src, dst string) error {
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
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}

// InstallTree atomically replaces dst with a copy of src.
func InstallTree(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(dst), ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	tree := filepath.Join(staging, "tree")
	if err := CopyTree(src, tree); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return os.Rename(tree, dst)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
