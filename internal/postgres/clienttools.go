package postgres

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"

	"flock/internal/fsutil"
)

// ClientTools are the client programs Flock exposes as shims.
var ClientTools = []string{"psql", "pg_dump", "pg_dumpall", "pg_restore", "createdb", "dropdb"}

var clientVersionPattern = regexp.MustCompile(`\(PostgreSQL\) (\d+(?:\.\d+)?)`)

// ClientDir is where a client tools version is installed (bin/ and lib/).
func ClientDir(root, version string) string {
	return filepath.Join(root, "services", "postgres-client", version)
}

// ClientTool is the path of an installed client tool ("psql").
func ClientTool(root, version, tool string) string {
	return filepath.Join(ClientDir(root, version), "bin", exe(tool))
}

// InstallClientFrom installs client tools built by build/postgres-client/build.sh and returns their version.
func InstallClientFrom(root, src string) (string, error) {
	psql := filepath.Join(src, "bin", exe("psql"))
	out, err := exec.Command(psql, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("run %s: %w", psql, err)
	}
	m := clientVersionPattern.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("unexpected psql version output %q", out)
	}
	version := string(m[1])

	return version, fsutil.InstallTree(src, ClientDir(root, version))
}

// InstalledClients lists installed client tool versions, newest first.
func InstalledClients(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "services", "postgres-client"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var versions []string
	for _, e := range entries {
		if _, err := os.Stat(ClientTool(root, e.Name(), "psql")); err == nil {
			versions = append(versions, e.Name())
		}
	}
	sort.Slice(versions, func(i, j int) bool { return Newer(versions[i], versions[j]) })
	return versions, nil
}
