package node

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// stripTop removes the archive's top-level directory and rejects paths escaping dest.
// It returns "" for entries that are the top-level directory itself.
func stripTop(dest, name string) (string, error) {
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	_, rest, found := strings.Cut(name, "/")
	if !found || rest == "" {
		return "", nil
	}

	target := filepath.Join(dest, filepath.FromSlash(rest))
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes destination", name)
	}

	return target, nil
}

func extractTarGz(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		target, err := stripTop(dest, hdr.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, hdr.FileInfo().Mode().Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("absolute symlink %q in archive", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func extractZip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}

	for _, f := range zr.File {
		target, err := stripTop(dest, f.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
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
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
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

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}

	return f.Close()
}
