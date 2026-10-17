// Package postgres installs, initialises and runs PostgreSQL for Flock.
//
// Binaries come from the binaries manifest (package "postgres"): bin/ (the server and the client
// tools: psql, pg_dump, ...), lib/ and share/, relocatable so they run from any directory. Each
// build is installed under services/postgres/<version>-build<N>, next to the others of its major
// until an update has started; databases live in services/postgres/data/<major>.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"

	"flock/internal/binaries"
)

// Package is PostgreSQL's name in the binaries manifest.
const Package = "postgres"

// Installer downloads PostgreSQL builds into <Root>/services/postgres/<id>.
type Installer struct {
	Root string
	HTTP *http.Client
}

// NewInstaller creates an installer under root.
func NewInstaller(root string) *Installer {
	return &Installer{Root: root, HTTP: &http.Client{}}
}

var idPattern = regexp.MustCompile(`^((\d+)\.\d+)-build(\d+)$`)

// ID names an installed build: "18.6-build1". It is what config.json keeps as the active server.
func ID(version string, build int) string { return version + "-build" + strconv.Itoa(build) }

// ParseID splits an install ID into its version and build; ok is false for anything else.
func ParseID(id string) (version string, build int, ok bool) {
	m := idPattern.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	build, _ = strconv.Atoi(m[3])
	return m[1], build, true
}

// VersionOf is the PostgreSQL version of an install ID ("18.6-build1" → "18.6"), "" if it isn't one.
func VersionOf(id string) string {
	v, _, _ := ParseID(id)
	return v
}

// Dir is the install directory of an ID.
func Dir(root, id string) string { return filepath.Join(root, "services", "postgres", id) }

// BinDir holds the executables of an ID.
func BinDir(root, id string) string { return filepath.Join(Dir(root, id), "bin") }

// Installed reports whether an ID is installed.
func Installed(root, id string) bool {
	_, err := os.Stat(filepath.Join(BinDir(root, id), exe("postgres")))
	return err == nil
}

// Dir is the install directory of an ID.
func (i *Installer) Dir(id string) string { return Dir(i.Root, id) }

// BinDir holds the executables of an ID.
func (i *Installer) BinDir(id string) string { return BinDir(i.Root, id) }

// Installed reports whether an ID is installed.
func (i *Installer) Installed(id string) bool { return Installed(i.Root, id) }

// Install downloads, verifies and unpacks a build from the manifest and returns its ID. It is a
// no-op when that build is installed already.
func (i *Installer) Install(ctx context.Context, v binaries.Version) (string, error) {
	id := ID(v.Version, v.Build)
	if i.Installed(id) {
		return id, nil
	}
	parent := filepath.Dir(i.Dir(id))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, "."+id+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)

	dir := filepath.Join(staging, "package")
	if err := binaries.Install(ctx, i.HTTP, v.Entry, dir, nil); err != nil {
		return "", fmt.Errorf("PostgreSQL %s: %w", v.Version, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", exe("postgres"))); err != nil {
		return "", errors.New("the PostgreSQL package has no bin/" + exe("postgres"))
	}
	return id, os.Rename(dir, i.Dir(id))
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
