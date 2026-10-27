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
	return ExecSQL(ctx, root, id, version, port, "CREATE DATABASE IF NOT EXISTS `"+name+"`")
}

// ExecSQL runs sql as root with no password in the installed version of MySQL or MariaDB (id) on
// port. The error is the client's message.
func ExecSQL(ctx context.Context, root, id, version string, port int, sql string) error {
	client, err := Tool(root, id, version, "client")
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, client, append(Connect(port), "-e", sql)...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// Connect are the client arguments that connect to the server on port as root with no password,
// ignoring the user's option files.
func Connect(port int) []string {
	return []string{"--no-defaults", "-uroot", "-h127.0.0.1", fmt.Sprintf("-P%d", port)}
}

// Tool finds a program of the installed version of MySQL or MariaDB (id): "client" (mysql, or
// mariadb) or "dump" (mysqldump, or mariadb-dump).
func Tool(root, id, version, tool string) (string, error) {
	names := map[string][]string{"client": {"mysql"}, "dump": {"mysqldump"}}[tool]
	switch id {
	case "mysql":
	case "mariadb":
		names = map[string][]string{"client": {"mariadb", "mysql"}, "dump": {"mariadb-dump", "mysqldump"}}[tool]
	default:
		return "", fmt.Errorf("%w: %s has no databases", ErrInvalid, id)
	}
	for _, name := range names {
		path := filepath.Join(Dir(root, id, version), "bin", exe(runtime.GOOS, name))
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s isn't part of the installed %s %s", names[0], id, version)
}
