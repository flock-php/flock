package catalog

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// validDatabase keeps database names to what needs no quoting rules beyond backticks.
var validDatabase = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// CreateDatabase creates database name in the installed version of MySQL or MariaDB (id) on port,
// as root with no password; an existing database is not an error.
func CreateDatabase(ctx context.Context, root, id, version string, port int, name string) error {
	if !validDatabase.MatchString(name) {
		return fmt.Errorf("%w: database names may only use letters, digits, - and _", ErrInvalid)
	}

	var clients []string
	switch id {
	case "mysql":
		clients = []string{"mysql"}
	case "mariadb":
		clients = []string{"mariadb", "mysql"}
	default:
		return fmt.Errorf("%w: %s has no databases to create", ErrInvalid, id)
	}

	client := ""
	for _, c := range clients {
		path := filepath.Join(Dir(root, id, version), "bin", exe(runtime.GOOS, c))
		if _, err := os.Stat(path); err == nil {
			client = path
			break
		}
	}
	if client == "" {
		return fmt.Errorf("the %s client isn't part of the installed %s %s", clients[0], id, version)
	}

	out, err := exec.CommandContext(ctx, client, "--no-defaults", "-uroot", "-h127.0.0.1", fmt.Sprintf("-P%d", port),
		"-e", "CREATE DATABASE IF NOT EXISTS `"+name+"`").CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}
