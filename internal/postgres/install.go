// Package postgres installs, initialises and runs PostgreSQL for Flock.
//
// Binaries come from zonky's embedded-postgres builds on Maven Central: a jar (zip)
// holding a single .txz with bin/, lib/ and share/. The macOS binaries are universal
// (x86_64 + arm64) and relocatable (rpath @loader_path/../lib), so they run from any
// directory. They only ship initdb, pg_ctl and postgres — no psql/createdb/pg_dump —
// which is why databases are created through the small wire-protocol client in client.go.
package postgres

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ulikunitz/xz"
)

// DefaultVersion is a known-good release (the integration test installs it); Flock itself
// installs the newest release of a major, see Catalog.
const DefaultVersion = "18.6.0"

// DefaultBaseURL is the Maven Central directory holding zonky's postgres binaries.
const DefaultBaseURL = "https://repo1.maven.org/maven2/io/zonky/test/postgres"

// Installer downloads PostgreSQL into <Root>/services/postgres/<version>.
type Installer struct {
	Root     string
	BaseURL  string
	Platform string // zonky platform, see Platform
	Client   *http.Client
}

// NewInstaller creates an installer for the current OS/arch.
func NewInstaller(root string) (*Installer, error) {
	platform, err := Platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}

	return &Installer{Root: root, BaseURL: DefaultBaseURL, Platform: platform, Client: http.DefaultClient}, nil
}

// Platform maps GOOS/GOARCH to zonky's artifact platform name.
func Platform(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "darwin/arm64":
		return "darwin-arm64v8", nil
	case "darwin/amd64":
		return "darwin-amd64", nil
	case "windows/amd64":
		return "windows-amd64", nil
	}

	return "", fmt.Errorf("postgres: unsupported platform %s/%s", goos, goarch)
}

// JarURL is the download URL of a zonky binaries jar.
func JarURL(baseURL, platform, version string) string {
	artifact := "embedded-postgres-binaries-" + platform
	return fmt.Sprintf("%s/%s/%s/%s-%s.jar", strings.TrimRight(baseURL, "/"), artifact, version, artifact, version)
}

// Dir is the install directory of version.
func (i *Installer) Dir(version string) string {
	return filepath.Join(i.Root, "services", "postgres", version)
}

// BinDir holds the postgres executables of version.
func (i *Installer) BinDir(version string) string {
	return filepath.Join(i.Dir(version), "bin")
}

// Installed reports whether version is already installed.
func (i *Installer) Installed(version string) bool {
	_, err := os.Stat(filepath.Join(i.BinDir(version), exe("postgres")))
	return err == nil
}

// Install downloads, verifies and extracts version. It is a no-op when already installed.
func (i *Installer) Install(ctx context.Context, version string) error {
	if i.Installed(version) {
		return nil
	}

	url := JarURL(i.BaseURL, i.Platform, version)
	jar, err := i.fetch(ctx, url)
	if err != nil {
		return err
	}

	sum, err := i.fetch(ctx, url+".sha1")
	if err != nil {
		return err
	}
	if err := verifySHA1(jar, string(sum)); err != nil {
		return fmt.Errorf("postgres %s: %w", version, err)
	}

	parent := filepath.Dir(i.Dir(version))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp(parent, "."+version+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	if err := extractJar(jar, tmp); err != nil {
		return fmt.Errorf("postgres %s: %w", version, err)
	}

	return os.Rename(tmp, i.Dir(version))
}

func (i *Installer) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := i.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	return io.ReadAll(resp.Body)
}

func verifySHA1(data []byte, published string) error {
	fields := strings.Fields(published)
	if len(fields) == 0 {
		return errors.New("empty checksum file")
	}

	got := sha1.Sum(data)
	if !strings.EqualFold(hex.EncodeToString(got[:]), fields[0]) {
		return fmt.Errorf("checksum mismatch: got %x, want %s", got, fields[0])
	}

	return nil
}

// extractJar unpacks the .txz inside the jar into dest.
func extractJar(jar []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(jar), int64(len(jar)))
	if err != nil {
		return fmt.Errorf("open jar: %w", err)
	}

	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".txz") {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		xr, err := xz.NewReader(rc)
		if err != nil {
			return fmt.Errorf("open %s: %w", f.Name, err)
		}

		return extractTar(tar.NewReader(xr), dest)
	}

	return errors.New("jar contains no .txz archive")
}

func extractTar(tr *tar.Reader, dest string) error {
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(filepath.Separator)) {
			return fmt.Errorf("illegal path in archive: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) || strings.Contains(hdr.Linkname, "..") {
				return fmt.Errorf("illegal symlink in archive: %s -> %s", hdr.Name, hdr.Linkname)
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

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}

	return f.Close()
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
