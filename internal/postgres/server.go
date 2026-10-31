package postgres

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"flock/internal/supervisor"
)

// InitDB creates a cluster in dataDir unless one exists. The superuser is "postgres"
// with trust auth, matching Laravel's defaults (DB_USERNAME=postgres, empty password).
func InitDB(binDir, dataDir string) error {
	if _, err := os.Stat(filepath.Join(dataDir, "PG_VERSION")); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dataDir), 0o700); err != nil {
		return err
	}

	cmd := exec.Command(filepath.Join(binDir, exe("initdb")),
		"-D", dataDir, "-U", "postgres", "--auth=trust", "-E", "UTF8", "--locale=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initdb: %w\n%s", err, out)
	}

	return nil
}

// Spec runs postgres on 127.0.0.1:port. On unix the socket goes to runDir/.s.PGSQL.<port>;
// AF_UNIX paths are limited to ~104 bytes on macOS, so keep runDir short. preload goes into
// shared_preload_libraries.
func Spec(binDir, dataDir, runDir, logFile string, port int, preload ...string) supervisor.Spec {
	args := []string{"-D", dataDir, "-p", strconv.Itoa(port), "-c", "listen_addresses=127.0.0.1"}
	if len(preload) > 0 {
		args = append(args, "-c", "shared_preload_libraries="+strings.Join(preload, ","))
	}
	if slices.Contains(preload, "timescaledb") {
		// It reports usage to Timescale by default.
		args = append(args, "-c", "timescaledb.telemetry_level=off")
	}

	return supervisor.Spec{
		Name:       "postgres",
		Path:       filepath.Join(binDir, exe("postgres")),
		Args:       append(args, socketArgs(runDir)...),
		LogFile:    logFile,
		StopSignal: stopSignal,
		Stop:       stopFunc(binDir, dataDir),
	}
}

// StopFast requests postgres' "fast" shutdown (roll back open transactions, checkpoint, exit)
// through pg_ctl, which signals the postmaster of dataDir: with SIGINT on unix, over its named
// pipe on Windows, which has no signals. It does not wait (-W); the caller waits for the exit.
func StopFast(binDir, dataDir string) error {
	cmd := exec.Command(filepath.Join(binDir, exe("pg_ctl")), "stop", "-D", dataDir, "-m", "fast", "-W")
	hideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_ctl stop: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
