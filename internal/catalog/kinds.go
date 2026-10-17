package catalog

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"flock/internal/supervisor"
)

// Kinds returns the catalog for goos. Every service is listed everywhere; where the manifest has
// no build of one, its Unavailable says why.
func Kinds(goos string) []*Kind {
	return []*Kind{
		mysql(goos),
		mariadb(goos),
		mongodb(goos),
		valkey(goos),
		redis(goos),
		meilisearch(goos),
		typesense(goos),
		rustfs(goos),
	}
}

func exe(goos, name string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// ---------------------------------------------------------------------------------------------
// MySQL: Oracle's builds, as published.

func mysql(goos string) *Kind {
	return &Kind{
		ID: "mysql", Name: "MySQL", Category: "Database", DefaultPort: 3306, Line: majorMinor,
		Description: "The world's most popular open-source database. User root, no password.",
		Env: func(port int) string {
			return fmt.Sprintf("DB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=laravel\nDB_USERNAME=root\nDB_PASSWORD=", port)
		},
		URL: func(port int) string { return fmt.Sprintf("mysql://root@127.0.0.1:%d", port) },
		Prepare: func(dir string) error {
			// The debug server, Japanese full-text dictionaries, static libraries and headers are
			// more than half of it.
			trim(dir, "docs", "man", "include", "mysql-test", "support-files", "lib/plugin/debug", "lib/mecab",
				"bin/mysqld-debug", "bin/mysqld-debug.exe")
			return errors.Join(removeMatching(dir, "*.a"), removeMatching(dir, "*.lib"), removeMatching(dir, "*.pdb"))
		},
		Spec: func(p Paths, port int) (supervisor.Spec, error) {
			return sqlSpec(goos, p, port, "mysqld", "mysqladmin", func() error {
				return run(filepath.Join(p.Dir, "bin", exe(goos, "mysqld")), "--no-defaults", "--initialize-insecure",
					"--basedir="+p.Dir, "--datadir="+p.Data)
			}, "--mysqlx=OFF")
		},
	}
}

// sqlSpec runs MySQL or MariaDB on 127.0.0.1:port, initialising the data directory the first
// time with initialise. On Windows it stops through the admin tool: there are no signals.
func sqlSpec(goos string, p Paths, port int, server, admin string, initialise func() error, extra ...string) (supervisor.Spec, error) {
	if !hasData(p.Data) {
		if err := os.Remove(p.Data); err != nil && !errors.Is(err, os.ErrNotExist) {
			return supervisor.Spec{}, err
		}
		if err := initialise(); err != nil {
			_ = os.RemoveAll(p.Data)
			return supervisor.Spec{}, fmt.Errorf("initialise the data directory: %w", err)
		}
	}
	args := []string{"--no-defaults", "--basedir=" + p.Dir, "--datadir=" + p.Data,
		"--bind-address=127.0.0.1", fmt.Sprintf("--port=%d", port)}
	if goos != "windows" {
		run := filepath.Join(p.Root, "run")
		if err := os.MkdirAll(run, 0o700); err != nil {
			return supervisor.Spec{}, err
		}
		args = append(args, "--socket="+filepath.Join(run, server+".sock"))
	}
	spec := supervisor.Spec{
		Path:       filepath.Join(p.Dir, "bin", exe(goos, server)),
		Args:       append(args, extra...),
		Dir:        p.Dir,
		StopSignal: syscall.SIGTERM,
	}
	if goos == "windows" {
		tool := filepath.Join(p.Dir, "bin", exe(goos, admin))
		spec.Stop = func() error {
			return exec.Command(tool, "-uroot", "-h127.0.0.1", fmt.Sprintf("-P%d", port), "shutdown").Start()
		}
	}
	return spec, nil
}

// ---------------------------------------------------------------------------------------------
// MariaDB: built by Flock for macOS (MariaDB publishes none), MariaDB's own zip on Windows.

func mariadb(goos string) *Kind {
	return &Kind{
		ID: "mariadb", Name: "MariaDB", Category: "Database", DefaultPort: 3306, Line: majorMinor,
		Description: "The community fork of MySQL, compatible with its clients. User root, no password.",
		Env: func(port int) string {
			return fmt.Sprintf("DB_CONNECTION=mariadb\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=laravel\nDB_USERNAME=root\nDB_PASSWORD=", port)
		},
		URL: func(port int) string { return fmt.Sprintf("mysql://root@127.0.0.1:%d", port) },
		Prepare: func(dir string) error {
			trim(dir, "include", "lib/debug", "share/doc")
			return nil
		},
		Spec: func(p Paths, port int) (supervisor.Spec, error) {
			return sqlSpec(goos, p, port, "mariadbd", "mariadb-admin", func() error {
				if goos == "windows" {
					return run(filepath.Join(p.Dir, "bin", "mariadb-install-db.exe"), "--datadir="+p.Data)
				}
				return run(filepath.Join(p.Dir, "scripts", "mariadb-install-db"), "--no-defaults", "--basedir="+p.Dir,
					"--datadir="+p.Data, "--auth-root-authentication-method=normal", "--skip-test-db")
			})
		},
	}
}

// ---------------------------------------------------------------------------------------------
// MongoDB: MongoDB Inc.'s builds, as published.

func mongodb(goos string) *Kind {
	return &Kind{
		ID: "mongodb", Name: "MongoDB", Category: "Database", DefaultPort: 27017, Line: majorMinor,
		Description: "The document database. Laravel uses it through the mongodb/laravel-mongodb package and PHP's mongodb extension.",
		Env: func(port int) string {
			return fmt.Sprintf("DB_CONNECTION=mongodb\nMONGODB_URI=mongodb://127.0.0.1:%d\nMONGODB_DATABASE=laravel", port)
		},
		URL: func(port int) string { return fmt.Sprintf("mongodb://127.0.0.1:%d", port) },
		Prepare: func(dir string) error {
			// The Windows zip is mostly debug symbols; mongos (the sharding router) isn't needed.
			trim(dir, "bin/mongos", "bin/mongos.exe", "bin/install_compass", "bin/Install-Compass.ps1")
			return removeMatching(dir, "*.pdb")
		},
		Spec: func(p Paths, port int) (supervisor.Spec, error) {
			args := []string{"--dbpath", p.Data, "--bind_ip", "127.0.0.1", "--port", fmt.Sprint(port)}
			if goos != "windows" {
				args = append(args, "--nounixsocket")
			}
			return supervisor.Spec{Path: filepath.Join(p.Dir, "bin", exe(goos, "mongod")), Args: args, Dir: p.Data,
				StopSignal: syscall.SIGTERM}, nil
		},
	}
}

// ---------------------------------------------------------------------------------------------
// Valkey and Redis: the same protocol, built by Flock (Valkey for Windows with MSYS2, whose
// fork() it needs to save snapshots).

func valkey(goos string) *Kind {
	return &Kind{
		ID: "valkey", Name: "Valkey", Category: "Cache & Queue", DefaultPort: 6379, Line: majorMinor,
		Description: "The open-source Redis: an in-memory store for your cache, queues and sessions. Laravel's Redis settings work unchanged.",
		Env:         RedisEnv,
		URL:         func(port int) string { return fmt.Sprintf("redis://127.0.0.1:%d", port) },
		Prepare:     needs(exe(goos, "valkey-server"), exe(goos, "valkey-cli")),
		Spec:        func(p Paths, port int) (supervisor.Spec, error) { return redisSpec(goos, p, port, "valkey-server") },
	}
}

func redis(goos string) *Kind {
	return &Kind{
		ID: "redis", Name: "Redis", Category: "Cache & Queue", DefaultPort: 6379, Line: majorMinor,
		Description: "The in-memory store for your cache, queues and sessions, from Redis Ltd.",
		Unavailable: "Flock offers Valkey on Windows instead: the open-source Redis, which works with Laravel's Redis settings unchanged.",
		Env:         RedisEnv,
		URL:         func(port int) string { return fmt.Sprintf("redis://127.0.0.1:%d", port) },
		Prepare:     needs(exe(goos, "redis-server"), exe(goos, "redis-cli")),
		Spec:        func(p Paths, port int) (supervisor.Spec, error) { return redisSpec(goos, p, port, "redis-server") },
	}
}

// redisSpec runs a Redis-protocol server on 127.0.0.1:port with its snapshots in the line's data.
func redisSpec(goos string, p Paths, port int, server string) (supervisor.Spec, error) {
	data := p.Data
	if goos == "windows" {
		data = filepath.ToSlash(data) // the MSYS2 runtime reads C:/… paths
	}
	spec := supervisor.Spec{
		Path: filepath.Join(p.Dir, exe(goos, server)),
		Args: []string{"--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--dir", data, "--daemonize", "no",
			"--protected-mode", "yes", "--logfile", ""},
		StopSignal: syscall.SIGTERM, // saves a snapshot
	}
	if goos == "windows" {
		// No SIGTERM there; killing it would lose everything written since the last snapshot.
		spec.Stop = func() error { return Shutdown(port) }
	}
	return spec, nil
}

// Shutdown sends SHUTDOWN to the server on 127.0.0.1:port, which saves a snapshot and exits. It
// returns once the server has accepted the command: it closes the connection when it exits, so
// no answer (within a short wait for an error reply) means it is shutting down.
func Shutdown(port int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("*1\r\n$8\r\nSHUTDOWN\r\n")); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	switch {
	case strings.HasPrefix(line, "-"):
		return fmt.Errorf("SHUTDOWN: %s", strings.TrimSpace(line[1:]))
	case err != nil:
		return nil // closed (or still saving): the server is going away
	}
	return fmt.Errorf("SHUTDOWN: unexpected reply %q", strings.TrimSpace(line))
}

// RedisEnv is the .env snippet for a Redis-protocol server (Valkey, Redis) on port. Flock's PHP
// has the phpredis extension on both platforms.
func RedisEnv(port int) string {
	return fmt.Sprintf("REDIS_CLIENT=phpredis\nREDIS_HOST=127.0.0.1\nREDIS_PASSWORD=null\nREDIS_PORT=%d", port)
}

// CLI is the command-line client (redis-cli, valkey-cli) of an installed Redis or Valkey version.
func CLI(root, id, version string) string {
	return filepath.Join(Dir(root, id, version), exe(runtime.GOOS, id+"-cli"))
}

// ---------------------------------------------------------------------------------------------
// Meilisearch: the single binary from its GitHub releases.

func meilisearch(goos string) *Kind {
	return &Kind{
		ID: "meilisearch", Name: "Meilisearch", Category: "Search", DefaultPort: 7700, Line: major,
		Description: "Fast full-text search for Laravel Scout, with a search preview in the browser. No API key needed locally.",
		Env: func(port int) string {
			return fmt.Sprintf("SCOUT_DRIVER=meilisearch\nMEILISEARCH_HOST=http://127.0.0.1:%d\nMEILISEARCH_KEY=", port)
		},
		URL:     func(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) },
		Prepare: needs(exe(goos, "meilisearch")),
		Spec: func(p Paths, port int) (supervisor.Spec, error) {
			return supervisor.Spec{
				Path: filepath.Join(p.Dir, exe(goos, "meilisearch")),
				Args: []string{"--db-path", filepath.Join(p.Data, "data.ms"), "--http-addr", fmt.Sprintf("127.0.0.1:%d", port),
					"--env", "development", "--no-analytics", "--upgrade-db", // updates keep the indexes
					"--dump-dir", filepath.Join(p.Data, "dumps"), "--snapshot-dir", filepath.Join(p.Data, "snapshots")},
				Dir:        p.Data, // it reads ./config.toml
				StopSignal: syscall.SIGTERM,
			}, nil
		},
	}
}

// ---------------------------------------------------------------------------------------------
// Typesense: macOS (and Linux) only upstream.

// TypesenseKey is the API key of Flock's Typesense.
const TypesenseKey = "flock"

func typesense(goos string) *Kind {
	return &Kind{
		ID: "typesense", Name: "Typesense", Category: "Search", DefaultPort: 8108, Line: major,
		Description: "Typo-tolerant search for Laravel Scout. API key \"" + TypesenseKey + "\".",
		Unavailable: "Typesense has no Windows build — it runs only on macOS and Linux.",
		Env: func(port int) string {
			return fmt.Sprintf("SCOUT_DRIVER=typesense\nTYPESENSE_HOST=127.0.0.1\nTYPESENSE_PORT=%d\nTYPESENSE_PROTOCOL=http\nTYPESENSE_API_KEY=%s", port, TypesenseKey)
		},
		URL:     func(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) },
		Prepare: needs("typesense-server"),
		Spec: func(p Paths, port int) (supervisor.Spec, error) {
			return supervisor.Spec{
				Path: filepath.Join(p.Dir, "typesense-server"),
				Args: []string{"--data-dir=" + p.Data, "--api-key=" + TypesenseKey, "--api-address=127.0.0.1", fmt.Sprintf("--api-port=%d", port),
					// It binds its peering port on the LAN address unless told otherwise.
					"--peering-address=127.0.0.1", fmt.Sprintf("--peering-port=%d", port-1), "--enable-cors"},
				StopSignal: syscall.SIGTERM,
			}, nil
		},
	}
}

// ---------------------------------------------------------------------------------------------
// RustFS: S3-compatible object storage, the single binary from its GitHub releases.

// RustFS credentials (its defaults).
const (
	RustFSAccessKey = "rustfsadmin"
	RustFSSecretKey = "rustfsadmin"
)

func rustfs(goos string) *Kind {
	return &Kind{
		ID: "rustfs", Name: "RustFS", Category: "Storage", DefaultPort: 9000, Line: major,
		Description: "S3-compatible object storage for your filesystem disks, with a web console on the next port. Create the bucket \"laravel\" in the console.",
		Env: func(port int) string {
			return fmt.Sprintf("FILESYSTEM_DISK=s3\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_DEFAULT_REGION=us-east-1\nAWS_BUCKET=laravel\n"+
				"AWS_ENDPOINT=http://127.0.0.1:%d\nAWS_URL=http://127.0.0.1:%d/laravel\nAWS_USE_PATH_STYLE_ENDPOINT=true", RustFSAccessKey, RustFSSecretKey, port, port)
		},
		URL:     func(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/rustfs/console/", port+1) },
		Prepare: needs(exe(goos, "rustfs")),
		Spec: func(p Paths, port int) (supervisor.Spec, error) {
			spec := supervisor.Spec{
				Path: filepath.Join(p.Dir, exe(goos, "rustfs")),
				Args: []string{"server", "--address", fmt.Sprintf("127.0.0.1:%d", port),
					"--console-enable", "--console-address", fmt.Sprintf("127.0.0.1:%d", port+1),
					"--access-key", RustFSAccessKey, "--secret-key", RustFSSecretKey, p.Data},
				StopSignal: syscall.SIGTERM,
			}
			if goos == "darwin" {
				// The macOS build links Homebrew's liblzma by its path; without Homebrew's xz, dyld
				// falls back to the one macOS has.
				spec.Env = []string{"DYLD_FALLBACK_LIBRARY_PATH=/usr/lib"}
			}
			return spec, nil
		},
	}
}

// ---------------------------------------------------------------------------------------------
// Helpers

// needs is a Prepare that checks the package has the given files at its top.
func needs(files ...string) func(dir string) error {
	return func(dir string) error {
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				return fmt.Errorf("the package has no %s", f)
			}
		}
		return nil
	}
}

// trim removes parts of an install Flock doesn't need (tests, docs, headers).
func trim(dir string, parts ...string) {
	for _, p := range parts {
		_ = os.RemoveAll(filepath.Join(dir, filepath.FromSlash(p)))
	}
}

// removeMatching deletes the files under dir whose name matches pattern.
func removeMatching(dir, pattern string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ok, _ := filepath.Match(pattern, d.Name()); ok && !d.IsDir() {
			return os.Remove(path)
		}
		return nil
	})
}

// run runs a setup command (initialising a database), returning its output on failure.
func run(path string, args ...string) error {
	out, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 2000 {
			tail = "…" + tail[len(tail)-2000:]
		}
		return fmt.Errorf("%s: %v\n%s", filepath.Base(path), err, tail)
	}
	return nil
}
