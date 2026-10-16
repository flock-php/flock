// Package archive unpacks the .zip and .tar.gz files the installers download.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// IsArchive reports whether file is a .zip, .tar.gz or .tgz, by name.
func IsArchive(file string) bool {
	base := strings.ToLower(filepath.Base(file))
	return strings.HasSuffix(base, ".zip") || strings.HasSuffix(base, ".tar.gz") || strings.HasSuffix(base, ".tgz")
}

// Extract unpacks a .zip, .tar.gz or .tgz into dir and drops the archive's single top-level
// directory when it has one. Entries that would leave dir are refused.
func Extract(file, dir string) error {
	base := strings.ToLower(filepath.Base(file))
	var err error
	switch {
	case strings.HasSuffix(base, ".zip"):
		err = unzip(file, dir)
	case strings.HasSuffix(base, ".tar.gz"), strings.HasSuffix(base, ".tgz"):
		err = untar(file, dir)
	default:
		return fmt.Errorf("%s is not a .zip or .tar.gz", filepath.Base(file))
	}
	if err != nil {
		return fmt.Errorf("unpack %s: %w", filepath.Base(file), err)
	}
	return Flatten(dir)
}

// Flatten moves the contents of dir's only entry up when that is a directory.
func Flatten(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return err
	}
	inner := filepath.Join(dir, entries[0].Name())
	tmp := dir + ".flatten"
	if err := os.Rename(inner, tmp); err != nil {
		return err
	}
	if err := os.Remove(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

// safeJoin joins an archive entry's name to dir, refusing names that leave it.
func safeJoin(dir, name string) (string, error) {
	target := filepath.Join(dir, filepath.FromSlash(name))
	if target != dir && !strings.HasPrefix(target, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q leaves the target directory", name)
	}
	return target, nil
}

func untar(file, dir string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			err = writeFile(target, tr, os.FileMode(h.Mode).Perm()|0o600)
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("archive entry %q links outside the archive", h.Name)
			}
			if _, err = safeJoin(dir, path.Join(path.Dir(h.Name), h.Linkname)); err != nil {
				return err
			}
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
				err = os.Symlink(h.Linkname, target)
			}
		case tar.TypeLink:
			var src string
			if src, err = safeJoin(dir, h.Linkname); err == nil {
				if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
					err = os.Link(src, target)
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func unzip(file, dir string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm() | 0o600
		if mode&0o111 == 0 && strings.HasSuffix(strings.ToLower(f.Name), ".exe") {
			mode |= 0o755
		}
		err = writeFile(target, rc, mode)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
