package catalog

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"

	"flock/internal/components"
	"flock/internal/supervisor"
	"flock/internal/valkey"
)

// Kinds returns the catalog for goos/goarch. Every service is listed everywhere; one Flock can't
// run there has no Method and says why in Unavailable.
func Kinds(goos, goarch string) []*Kind {
	return []*Kind{
		mysql(goos, goarch),
		mariadb(goos, goarch),
		mongodb(goos, goarch),
		redis(goos, goarch),
		meilisearch(goos, goarch),
		typesense(goos, goarch),
		rustfs(goos, goarch),
	}
}

func exe(goos, name string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

func unavailable(k *Kind, why string) *Kind {
	k.Method, k.Unavailable, k.Estimate = "", why, ""
	return k
}

// macArch is the architecture in macOS download names: arm64 or x86_64.
func macArch(goarch string) string {
	if goarch == "arm64" {
		return "arm64"
	}
	return "x86_64"
}

// ---------------------------------------------------------------------------------------------
// MySQL: Oracle's builds for macOS and Windows. They publish MD5 checksums only.

// mysqlBaselines are releases known to exist; newer patches are found by probing the CDN.
var mysqlBaselines = map[string]string{"9.7": "9.7.2", "8.4": "8.4.11", "8.0": "8.0.46"}

func mysql(goos, goarch string) *Kind {
	k := &Kind{
		ID: "mysql", Name: "MySQL", Category: "Database", DefaultPort: 3306,
		Description: "The world's most popular open-source database. User root, no password.",
		Lines:       []Line{{"9.7", "9.7 LTS"}, {"8.4", "8.4 LTS"}, {"8.0", "8.0 (end of life)"}},
		Method:      components.MethodDownload,
		Env: func(port int) string {
			return fmt.Sprintf("DB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=laravel\nDB_USERNAME=root\nDB_PASSWORD=", port)
		},
		URL: func(port int) string { return fmt.Sprintf("mysql://root@127.0.0.1:%d", port) },
	}
	var file func(v string) string
	switch {
	case goos == "darwin":
		k.Estimate = "about 170 MB download"
		file = func(v string) string { return fmt.Sprintf("mysql-%s-macos15-%s.tar.gz", v, macArch(goarch)) }
	case goos == "windows" && goarch == "amd64":
		k.Estimate = "about 330 MB download"
		file = func(v string) string { return fmt.Sprintf("mysql-%s-winx64.zip", v) }
	default:
		return unavailable(k, "Flock has no MySQL build for this platform.")
	}
	cdn := func(v string) string { return "https://cdn.mysql.com/Downloads/MySQL-" + lineOf2(v) + "/" + file(v) }

	k.Latest = func(ctx context.Context) (map[string]string, error) {
		return probeLines(ctx, mysqlBaselines, cdn)
	}
	k.Install = func(ctx context.Context, dir, version string, r *components.Reporter) error {
		url, sum := cdn(version), ""
		if data, err := get(ctx, url+".md5"); err == nil {
			sum, _ = md5For(data, file(version))
		} else if !exists(ctx, url) {
			// Older patches move to the archive, which has no checksum files.
			url = "https://downloads.mysql.com/archives/get/p/23/file/" + file(version)
		}
		if err := fetchArchiveMD5(ctx, url, sum, dir, r); err != nil {
			return err
		}
		// The debug server, Japanese full-text dictionaries, static libraries and headers are
		// more than half of it.
		trim(dir, "docs", "man", "include", "mysql-test", "support-files", "lib/plugin/debug", "lib/mecab",
			"bin/mysqld-debug", "bin/mysqld-debug.exe")
		return errors.Join(removeMatching(dir, "*.a"), removeMatching(dir, "*.lib"), removeMatching(dir, "*.pdb"))
	}
	k.Spec = func(p Paths, port int) (supervisor.Spec, error) {
		return sqlSpec(goos, p, port, "mysqld", "mysqladmin", func() error {
			return run(filepath.Join(p.Dir, "bin", exe(goos, "mysqld")), "--no-defaults", "--initialize-insecure",
				"--basedir="+p.Dir, "--datadir="+p.Data)
		}, "--mysqlx=OFF")
	}
	return k
}

// lineOf2 is the major.minor of a version: "8.4" for "8.4.11".
func lineOf2(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
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
// MariaDB: built from source on macOS (no official builds), downloaded on Windows.

const mariadbAPI = "https://downloads.mariadb.org/rest-api/mariadb/"

func mariadb(goos, goarch string) *Kind {
	k := &Kind{
		ID: "mariadb", Name: "MariaDB", Category: "Database", DefaultPort: 3306,
		Description: "The community fork of MySQL, compatible with its clients. User root, no password.",
		Lines:       []Line{{"12.3", "12.3 LTS"}, {"11.8", "11.8 LTS"}, {"11.4", "11.4 LTS"}, {"10.11", "10.11 LTS"}},
		Env: func(port int) string {
			return fmt.Sprintf("DB_CONNECTION=mariadb\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=laravel\nDB_USERNAME=root\nDB_PASSWORD=", port)
		},
		URL: func(port int) string { return fmt.Sprintf("mysql://root@127.0.0.1:%d", port) },
	}
	switch {
	case goos == "darwin":
		k.Method, k.Estimate = components.MethodBuild, "about 5 minutes"
		k.Install = buildMariaDB
	case goos == "windows" && goarch == "amd64":
		k.Method, k.Estimate = components.MethodDownload, "about 100 MB download"
		k.Install = func(ctx context.Context, dir, version string, r *components.Reporter) error {
			file := "mariadb-" + version + "-winx64.zip"
			sha, err := mariadbSHA256(ctx, version, file)
			if err != nil {
				return err
			}
			url := "https://archive.mariadb.org/mariadb-" + version + "/winx64-packages/" + file
			if err := fetchArchive(ctx, url, sha, dir, "", r); err != nil {
				return err
			}
			trim(dir, "include", "lib/debug", "share/doc")
			return nil
		}
	default:
		return unavailable(k, "Flock has no MariaDB build for this platform.")
	}

	k.Latest = func(ctx context.Context) (map[string]string, error) {
		latest := map[string]string{}
		var errs []error
		for _, l := range k.Lines {
			v, err := mariadbLatest(ctx, l.ID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			latest[l.ID] = v
		}
		return latest, errors.Join(errs...)
	}
	k.Spec = func(p Paths, port int) (supervisor.Spec, error) {
		return sqlSpec(goos, p, port, "mariadbd", "mariadb-admin", func() error {
			if goos == "windows" {
				return run(filepath.Join(p.Dir, "bin", "mariadb-install-db.exe"), "--datadir="+p.Data)
			}
			return run(filepath.Join(p.Dir, "scripts", "mariadb-install-db"), "--no-defaults", "--basedir="+p.Dir,
				"--datadir="+p.Data, "--auth-root-authentication-method=normal", "--skip-test-db")
		})
	}
	return k
}

// mariadbRelease is the part of downloads.mariadb.org's /<line>/latest/ (releases) and
// /<version>/ (release_data) answers Flock reads.
type mariadbRelease struct {
	Releases    map[string]mariadbFiles `json:"releases"`
	ReleaseData map[string]mariadbFiles `json:"release_data"`
}

type mariadbFiles struct {
	Files []struct {
		Name     string `json:"file_name"`
		Checksum struct {
			SHA256 string `json:"sha256sum"`
		} `json:"checksum"`
	} `json:"files"`
}

func mariadbLatest(ctx context.Context, line string) (string, error) {
	data, err := get(ctx, mariadbAPI+line+"/latest/")
	if err != nil {
		return "", err
	}
	var rel mariadbRelease
	if err := json.Unmarshal(data, &rel); err != nil {
		return "", fmt.Errorf("parse MariaDB releases: %w", err)
	}
	for v := range rel.Releases {
		if strings.HasPrefix(v, line+".") {
			return v, nil
		}
	}
	return "", fmt.Errorf("downloads.mariadb.org lists no MariaDB %s release", line)
}

// mariadbSHA256 is the published checksum of a release's file.
func mariadbSHA256(ctx context.Context, version, file string) (string, error) {
	data, err := get(ctx, mariadbAPI+version+"/")
	if err != nil {
		return "", err
	}
	var rel mariadbRelease
	if err := json.Unmarshal(data, &rel); err != nil {
		return "", fmt.Errorf("parse MariaDB release: %w", err)
	}
	for _, f := range rel.ReleaseData[version].Files {
		if f.Name == file && len(f.Checksum.SHA256) == 64 {
			return f.Checksum.SHA256, nil
		}
	}
	return "", fmt.Errorf("downloads.mariadb.org has no checksum for %s", file)
}

// ---------------------------------------------------------------------------------------------
// MongoDB: MongoDB Inc.'s builds, with .sha256 files next to them.

var mongodbBaselines = map[string]string{"9.0": "9.0.4", "8.0": "8.0.34", "7.0": "7.0.45"}

func mongodb(goos, goarch string) *Kind {
	k := &Kind{
		ID: "mongodb", Name: "MongoDB", Category: "Database", DefaultPort: 27017,
		Description: "The document database. Laravel uses it through the mongodb/laravel-mongodb package and PHP's mongodb extension.",
		Lines:       []Line{{"9.0", "9.0"}, {"8.0", "8.0"}, {"7.0", "7.0"}},
		Method:      components.MethodDownload,
		Env: func(port int) string {
			return fmt.Sprintf("DB_CONNECTION=mongodb\nMONGODB_URI=mongodb://127.0.0.1:%d\nMONGODB_DATABASE=laravel", port)
		},
		URL: func(port int) string { return fmt.Sprintf("mongodb://127.0.0.1:%d", port) },
	}
	var url func(v string) string
	switch {
	case goos == "darwin":
		k.Estimate = "about 80 MB download"
		url = func(v string) string {
			return fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-macos-%s-%s.tgz", macArch(goarch), v)
		}
	case goos == "windows" && goarch == "amd64":
		k.Estimate = "about 800 MB download"
		url = func(v string) string {
			return fmt.Sprintf("https://fastdl.mongodb.org/windows/mongodb-windows-x86_64-%s.zip", v)
		}
	default:
		return unavailable(k, "Flock has no MongoDB build for this platform.")
	}
	k.Latest = func(ctx context.Context) (map[string]string, error) {
		return probeLines(ctx, mongodbBaselines, url)
	}
	k.Install = func(ctx context.Context, dir, version string, r *components.Reporter) error {
		data, err := get(ctx, url(version)+".sha256")
		if err != nil {
			return fmt.Errorf("checksum of MongoDB %s: %w", version, err)
		}
		sha, err := sumFor(data, filepath.Base(url(version)))
		if err != nil {
			return err
		}
		if err := fetchArchive(ctx, url(version), sha, dir, "", r); err != nil {
			return err
		}
		// The Windows zip is mostly debug symbols; mongos (the sharding router) isn't needed.
		trim(dir, "bin/mongos", "bin/mongos.exe", "bin/install_compass", "bin/Install-Compass.ps1")
		return removeMatching(dir, "*.pdb")
	}
	k.Spec = func(p Paths, port int) (supervisor.Spec, error) {
		args := []string{"--dbpath", p.Data, "--bind_ip", "127.0.0.1", "--port", fmt.Sprint(port)}
		if goos != "windows" {
			args = append(args, "--nounixsocket")
		}
		return supervisor.Spec{Path: filepath.Join(p.Dir, "bin", exe(goos, "mongod")), Args: args, Dir: p.Data,
			StopSignal: syscall.SIGTERM}, nil
	}
	return k
}

// ---------------------------------------------------------------------------------------------
// Redis: built from source on macOS like Valkey; on Windows the MSYS2 builds of
// github.com/redis-windows/redis-windows (built from the unmodified upstream tags).

// windowsRedisLine is the line of the Redis that older Flock versions installed on Windows.
const windowsRedisLine = "7.2"

func redis(goos, goarch string) *Kind {
	k := &Kind{
		ID: "redis", Name: "Redis", Category: "Cache & Queue", DefaultPort: 6379,
		Description: "The in-memory store for your cache, queues and sessions, from Redis Ltd.",
		Lines:       []Line{{"8.10", "8.10"}, {"8.4", "8.4"}, {"7.4", "7.4"}, {"7.2", "7.2"}},
		Env:         RedisEnv,
		URL:         func(port int) string { return fmt.Sprintf("redis://127.0.0.1:%d", port) },
	}
	switch {
	case goos == "darwin":
		k.Method, k.Estimate = components.MethodBuild, "about 1 minute"
		k.Latest = func(ctx context.Context) (map[string]string, error) {
			data, err := get(ctx, "https://download.redis.io/releases/")
			if err != nil {
				return nil, err
			}
			return newestByLine(k, redisReleases.FindAllStringSubmatch(string(data), -1)), nil
		}
		k.Install = buildRedis
	case goos == "windows" && goarch == "amd64":
		k.Method, k.Estimate = components.MethodDownload, "about 10 MB download"
		k.Latest = func(ctx context.Context) (map[string]string, error) {
			releases, err := githubReleases(ctx, "redis-windows/redis-windows")
			if err != nil {
				return nil, err
			}
			var tags [][]string
			for _, r := range releases {
				if !r.Draft && !r.Prerelease {
					tags = append(tags, []string{r.Tag, r.Tag})
				}
			}
			return newestByLine(k, tags), nil
		}
		k.Install = func(ctx context.Context, dir, version string, r *components.Reporter) error {
			url, sha, err := githubAsset(ctx, "redis-windows/redis-windows", version, "Redis-"+version+"-Windows-x64-msys2.zip")
			if err != nil {
				return err
			}
			if err := fetchArchive(ctx, url, sha, dir, "", r); err != nil {
				return err
			}
			for _, need := range []string{"redis-server.exe", "redis-cli.exe"} {
				if _, err := os.Stat(filepath.Join(dir, need)); err != nil {
					return fmt.Errorf("%s not in the archive", need)
				}
			}
			return nil
		}
	default:
		return unavailable(k, "Flock has no Redis build for this platform.")
	}
	k.Spec = func(p Paths, port int) (supervisor.Spec, error) {
		return supervisor.Spec{
			Path: filepath.Join(p.Dir, exe(goos, "redis-server")),
			Args: []string{"--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--dir", p.Data, "--daemonize", "no",
				"--protected-mode", "yes", "--logfile", ""},
			StopSignal: syscall.SIGTERM, // saves a snapshot
			Stop:       valkey.StopFunc(port),
		}, nil
	}
	return k
}

var redisReleases = regexp.MustCompile(`redis-(\d+\.\d+\.\d+)\.tar\.gz`)

// RedisCLI is redis-cli of an installed Redis version.
func RedisCLI(root, version string) string {
	return filepath.Join(Dir(root, "redis", version), exe(runtime.GOOS, "redis-cli"))
}

// RedisEnv is the .env snippet for a Redis-protocol server (Valkey, Redis) on port. Flock's macOS
// PHP builds include the phpredis extension, the windows.php.net builds don't, so Windows
// projects use predis (a Composer package).
func RedisEnv(port int) string {
	client := "phpredis"
	if runtime.GOOS == "windows" {
		client = "predis"
	}
	return fmt.Sprintf("REDIS_CLIENT=%s\nREDIS_HOST=127.0.0.1\nREDIS_PASSWORD=null\nREDIS_PORT=%d", client, port)
}

// ---------------------------------------------------------------------------------------------
// Meilisearch: single binaries from GitHub releases.

func meilisearch(goos, goarch string) *Kind {
	k := &Kind{
		ID: "meilisearch", Name: "Meilisearch", Category: "Search", DefaultPort: 7700,
		Description: "Fast full-text search for Laravel Scout, with a search preview in the browser. No API key needed locally.",
		Lines:       []Line{{"1", "1.x"}},
		Method:      components.MethodDownload, Estimate: "about 350 MB download",
		Env: func(port int) string {
			return fmt.Sprintf("SCOUT_DRIVER=meilisearch\nMEILISEARCH_HOST=http://127.0.0.1:%d\nMEILISEARCH_KEY=", port)
		},
		URL: func(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) },
	}
	var asset string
	switch {
	case goos == "darwin" && goarch == "arm64":
		asset = "meilisearch-macos-apple-silicon"
	case goos == "darwin":
		asset = "meilisearch-macos-amd64"
	case goos == "windows" && goarch == "amd64":
		asset = "meilisearch-windows-amd64.exe"
	default:
		return unavailable(k, "Flock has no Meilisearch build for this platform.")
	}
	const repo = "meilisearch/meilisearch"
	k.Latest = func(ctx context.Context) (map[string]string, error) { return githubLatest(ctx, k, repo, asset) }
	k.Install = func(ctx context.Context, dir, version string, r *components.Reporter) error {
		url, sha, err := githubAsset(ctx, repo, "v"+version, asset)
		if err != nil {
			return err
		}
		return fetchArchive(ctx, url, sha, dir, exe(goos, "meilisearch"), r)
	}
	k.Spec = func(p Paths, port int) (supervisor.Spec, error) {
		return supervisor.Spec{
			Path: filepath.Join(p.Dir, exe(goos, "meilisearch")),
			Args: []string{"--db-path", filepath.Join(p.Data, "data.ms"), "--http-addr", fmt.Sprintf("127.0.0.1:%d", port),
				"--env", "development", "--no-analytics", "--upgrade-db", // updates keep the indexes
				"--dump-dir", filepath.Join(p.Data, "dumps"), "--snapshot-dir", filepath.Join(p.Data, "snapshots")},
			Dir:        p.Data, // it reads ./config.toml
			StopSignal: syscall.SIGTERM,
		}, nil
	}
	return k
}

// ---------------------------------------------------------------------------------------------
// Typesense: tarballs from dl.typesense.org (macOS and Linux only). Typesense publishes no
// checksums; the tarball holds an MD5 of the binary, which Flock checks.

// TypesenseKey is the API key of Flock's Typesense.
const TypesenseKey = "flock"

func typesense(goos, goarch string) *Kind {
	k := &Kind{
		ID: "typesense", Name: "Typesense", Category: "Search", DefaultPort: 8108,
		Description: "Typo-tolerant search for Laravel Scout. API key \"" + TypesenseKey + "\".",
		Lines:       []Line{{"30", "30"}, {"29", "29"}},
		Method:      components.MethodDownload, Estimate: "about 40 MB download",
		Env: func(port int) string {
			return fmt.Sprintf("SCOUT_DRIVER=typesense\nTYPESENSE_HOST=127.0.0.1\nTYPESENSE_PORT=%d\nTYPESENSE_PROTOCOL=http\nTYPESENSE_API_KEY=%s", port, TypesenseKey)
		},
		URL: func(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) },
	}
	if goos != "darwin" {
		return unavailable(k, "Typesense has no Windows build — it runs only on macOS and Linux.")
	}
	arch := "amd64"
	if goarch == "arm64" {
		arch = "arm64"
	}
	k.Latest = func(ctx context.Context) (map[string]string, error) {
		releases, err := githubReleases(ctx, "typesense/typesense")
		if err != nil {
			return nil, err
		}
		var tags [][]string
		for _, r := range releases {
			if !r.Draft && !r.Prerelease {
				tags = append(tags, []string{r.Tag, strings.TrimPrefix(r.Tag, "v")})
			}
		}
		return newestByLine(k, tags), nil
	}
	k.Install = func(ctx context.Context, dir, version string, r *components.Reporter) error {
		url := fmt.Sprintf("https://dl.typesense.org/releases/%s/typesense-server-%s-darwin-%s.tar.gz", version, version, arch)
		if err := fetchArchive(ctx, url, "", dir, "", r); err != nil {
			return err
		}
		return checkTypesenseMD5(dir)
	}
	k.Spec = func(p Paths, port int) (supervisor.Spec, error) {
		return supervisor.Spec{
			Path: filepath.Join(p.Dir, "typesense-server"),
			Args: []string{"--data-dir=" + p.Data, "--api-key=" + TypesenseKey, "--api-address=127.0.0.1", fmt.Sprintf("--api-port=%d", port),
				// It binds its peering port on the LAN address unless told otherwise.
				"--peering-address=127.0.0.1", fmt.Sprintf("--peering-port=%d", port-1), "--enable-cors"},
			StopSignal: syscall.SIGTERM,
		}, nil
	}
	return k
}

// checkTypesenseMD5 compares typesense-server with the MD5 the tarball ships next to it.
func checkTypesenseMD5(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "typesense-server.md5.txt"))
	if err != nil {
		return fmt.Errorf("the Typesense archive has no checksum: %w", err)
	}
	want := strings.Fields(string(data))
	if len(want) == 0 {
		return errors.New("the Typesense archive's checksum is empty")
	}
	return verifyMD5(filepath.Join(dir, "typesense-server"), want[0])
}

// ---------------------------------------------------------------------------------------------
// RustFS: S3-compatible object storage, single binaries from GitHub releases.

// RustFS credentials (its defaults).
const (
	RustFSAccessKey = "rustfsadmin"
	RustFSSecretKey = "rustfsadmin"
)

func rustfs(goos, goarch string) *Kind {
	k := &Kind{
		ID: "rustfs", Name: "RustFS", Category: "Storage", DefaultPort: 9000,
		Description: "S3-compatible object storage for your filesystem disks, with a web console on the next port. Create the bucket \"laravel\" in the console.",
		Lines:       []Line{{"1", "1.x"}},
		Method:      components.MethodDownload, Estimate: "about 40 MB download",
		Env: func(port int) string {
			return fmt.Sprintf("FILESYSTEM_DISK=s3\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_DEFAULT_REGION=us-east-1\nAWS_BUCKET=laravel\n"+
				"AWS_ENDPOINT=http://127.0.0.1:%d\nAWS_URL=http://127.0.0.1:%d/laravel\nAWS_USE_PATH_STYLE_ENDPOINT=true", RustFSAccessKey, RustFSSecretKey, port, port)
		},
		URL: func(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/rustfs/console/", port+1) },
	}
	var asset string
	switch {
	case goos == "darwin" && goarch == "arm64":
		asset = "rustfs-macos-aarch64-v%s.zip"
	case goos == "darwin":
		return unavailable(k, "RustFS has no build for Intel Macs.")
	case goos == "windows" && goarch == "amd64":
		asset = "rustfs-windows-x86_64-v%s.zip"
	default:
		return unavailable(k, "Flock has no RustFS build for this platform.")
	}
	const repo = "rustfs/rustfs"
	k.Latest = func(ctx context.Context) (map[string]string, error) { return githubLatest(ctx, k, repo, "") }
	k.Install = func(ctx context.Context, dir, version string, r *components.Reporter) error {
		url, sha, err := githubAsset(ctx, repo, version, fmt.Sprintf(asset, version))
		if err != nil {
			return err
		}
		return fetchArchive(ctx, url, sha, dir, "", r)
	}
	k.Spec = func(p Paths, port int) (supervisor.Spec, error) {
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
	}
	return k
}

// ---------------------------------------------------------------------------------------------
// Version lookups

var version3 = regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`)

// newestByLine returns the newest stable version of each of k's lines among matches, whose
// second element is a version ("8.4.7"; tags like "8.6-rc1" are skipped).
func newestByLine(k *Kind, matches [][]string) map[string]string {
	latest := map[string]string{}
	for _, m := range matches {
		v := strings.TrimPrefix(m[1], "v")
		if !version3.MatchString(v) {
			continue
		}
		line := k.LineOf(v)
		if line == "" {
			continue
		}
		if cur, ok := latest[line]; !ok || components.Newer(v, cur) {
			latest[line] = v
		}
	}
	return latest
}

// githubLatest finds the newest stable release of each line of k in repo; with asset, only
// releases that have it ("%s" in its name is the version).
func githubLatest(ctx context.Context, k *Kind, repo, asset string) (map[string]string, error) {
	releases, err := githubReleases(ctx, repo)
	if err != nil {
		return nil, err
	}
	var tags [][]string
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		if asset != "" && !hasAsset(r, asset) {
			continue
		}
		tags = append(tags, []string{r.Tag, r.Tag})
	}
	latest := newestByLine(k, tags)
	if len(latest) == 0 {
		return nil, fmt.Errorf("found no release of %s on GitHub", k.Name)
	}
	return latest, nil
}

func hasAsset(r githubRelease, name string) bool {
	for _, a := range r.Assets {
		if a.Name == name {
			return true
		}
	}
	return false
}

// probeLines finds the newest patch of each line by asking whether url(next patch) exists,
// starting at a release known to exist. Two missing patches in a row end the search.
func probeLines(ctx context.Context, baselines map[string]string, url func(v string) string) (map[string]string, error) {
	latest := map[string]string{}
	for line, base := range baselines {
		v, err := probe(ctx, base, url)
		if err != nil {
			return nil, err
		}
		latest[line] = v
	}
	return latest, nil
}

func probe(ctx context.Context, base string, url func(v string) string) (string, error) {
	parts := strings.Split(base, ".")
	var major, minor, patch int
	if _, err := fmt.Sscanf(strings.Join(parts, " "), "%d %d %d", &major, &minor, &patch); err != nil {
		return "", fmt.Errorf("bad baseline %q", base)
	}
	newest := base
	for misses, p := 0, patch+1; misses < 2 && p < patch+200; p++ {
		v := fmt.Sprintf("%d.%d.%d", major, minor, p)
		if exists(ctx, url(v)) {
			newest, misses = v, 0
		} else {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			misses++
		}
	}
	return newest, nil
}

// exists reports whether url answers a HEAD request with 200.
func exists(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ---------------------------------------------------------------------------------------------
// Helpers

// fetchArchiveMD5 is fetchArchive for vendors that publish only MD5 checksums (sum may be "").
func fetchArchiveMD5(ctx context.Context, url, sum, dir string, r *components.Reporter) error {
	if sum == "" {
		return fetchArchive(ctx, url, "", dir, "", r)
	}
	// Download, verify, then unpack: fetchArchive verifies SHA-256 only.
	work := dir + ".download"
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)
	file := filepath.Join(work, filepath.Base(url))
	fmt.Fprintf(r.Log, "==> Downloading %s\n", url)
	r.Phase(components.PhaseDownloading, 0)
	if err := downloadFile(ctx, url, file, r); err != nil {
		return err
	}
	if err := verifyMD5(file, sum); err != nil {
		return err
	}
	fmt.Fprintf(r.Log, "==> Verified MD5 %s\n", sum)
	r.Phase(components.PhaseInstalling, -1)
	fmt.Fprintf(r.Log, "==> Unpacking %s\n", filepath.Base(file))
	return unpack(file, dir, "")
}

// md5For finds the MD5 of file in an md5sum-style list.
func md5For(data []byte, file string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 1 && len(f[0]) == 32 && (len(f) == 1 || strings.TrimPrefix(filepath.Base(f[len(f)-1]), "*") == file) {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", file)
}

func verifyMD5(file, want string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got MD5 %s, want %s", filepath.Base(file), got, want)
	}
	return nil
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
