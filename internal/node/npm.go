package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Global packages (`npm install -g`) and an updated npm live outside the version directories, in
// one prefix per major: <root>/node-global/<major>. They survive patch updates of Node, which
// replace the version directory; majors stay apart because native modules are built per major.
// <root>/node-global/current links to the default major's prefix, and its bin directory is on the
// user's PATH (after Flock's shims), so global CLIs like `vue` work in any terminal.

// GlobalDir is the global prefix of a major.
func GlobalDir(root, major string) string { return filepath.Join(root, "node-global", major) }

// CurrentGlobalDir links to the default major's global prefix.
func CurrentGlobalDir(root string) string { return filepath.Join(root, "node-global", "current") }

// GlobalBin is where npm puts the commands of global packages in a prefix.
func GlobalBin(prefix string) string {
	if runtime.GOOS == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

// modulesDir is where packages live under a prefix (or a version directory, for the bundled ones).
func modulesDir(prefix string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(prefix, "node_modules")
	}
	return filepath.Join(prefix, "lib", "node_modules")
}

// VersionDir is the directory of an installed version.
func VersionDir(root, version string) string { return filepath.Join(Dir(root), version) }

// Npm returns the npm a version uses: one installed into its major's global prefix (by updating
// npm), or else the one bundled with Node. It returns npm's version and its npm-cli.js.
func Npm(root, version string) (npmVersion, cli string) {
	for _, dir := range []string{GlobalDir(root, MajorOf(version)), VersionDir(root, version)} {
		pkg := filepath.Join(modulesDir(dir), "npm")
		if v := packageVersion(pkg); v != "" {
			return v, filepath.Join(pkg, "bin", "npm-cli.js")
		}
	}
	return "", ""
}

func packageVersion(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(data, &pkg)
	return pkg.Version
}

// NpmEnv is the environment npm runs with for a version: its major's global prefix, and that
// prefix's commands (an updated npm, global CLIs) ahead of Node's own on PATH.
func NpmEnv(root, version, binDir string) []string {
	prefix := GlobalDir(root, MajorOf(version))
	return []string{
		"NPM_CONFIG_PREFIX=" + prefix,
		"PATH=" + GlobalBin(prefix) + string(os.PathListSeparator) + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

// runNpm runs npm (the one Npm finds) with a version's Node and global prefix.
func runNpm(ctx context.Context, root, version string, args ...string) ([]byte, error) {
	binDir, err := BinDir(root, version)
	if err != nil {
		return nil, err
	}
	_, cli := Npm(root, version)
	if cli == "" {
		return nil, fmt.Errorf("Node.js %s has no npm", version)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(binDir, nodeExe()), append([]string{cli}, args...)...)
	cmd.Env = append(os.Environ(), NpmEnv(root, version, binDir)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("npm %s: %v\n%s", strings.Join(args, " "), err, lastLines(out, 8))
	}
	return out, nil
}

func nodeExe() string {
	if runtime.GOOS == "windows" {
		return "node.exe"
	}
	return "node"
}

func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// UpdateNpm installs npm@npmVersion into the global prefix of version's major.
func UpdateNpm(ctx context.Context, root, version, npmVersion string) error {
	_, err := runNpm(ctx, root, version, "install", "--global", "--no-fund", "--no-audit", "npm@"+npmVersion)
	return err
}

// LinkCurrent points <root>/node-global/current at the default major's global prefix.
func LinkCurrent(root, major string) error {
	if major == "" {
		return nil
	}
	target := GlobalDir(root, major)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	link := CurrentGlobalDir(root)
	if dest, err := os.Readlink(link); err == nil && filepath.Clean(dest) == filepath.Clean(target) {
		return nil
	}
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return symlinkDir(target, link)
}

// npmRegistry is npm's own package document (abbreviated: versions with their engines).
const npmRegistry = "https://registry.npmjs.org/npm"

type npmPackument struct {
	DistTags map[string]string `json:"dist-tags"`
	Versions map[string]struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
		Deprecated json.RawMessage `json:"deprecated"`
	} `json:"versions"`
}

// latestNpmFor returns the newest stable npm whose engines allow nodeVersion.
func (p npmPackument) latestNpmFor(nodeVersion string) string {
	best := ""
	for v, meta := range p.Versions {
		if strings.Contains(v, "-") || len(meta.Deprecated) > 0 && string(meta.Deprecated) != "false" {
			continue // pre-releases and deprecated versions
		}
		if meta.Engines.Node != "" && !Satisfies(nodeVersion, meta.Engines.Node) {
			continue
		}
		if best == "" || Newer(v, best) {
			best = v
		}
	}
	return best
}

func (i *Installer) npmPackument(ctx context.Context) (npmPackument, error) {
	url := i.NpmRegistryURL
	if url == "" {
		url = npmRegistry
	}
	var p npmPackument
	data, err := i.getAccept(ctx, url, "application/vnd.npm.install-v1+json")
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("parse npm's registry data: %w", err)
	}
	return p, nil
}

// Npm returns the cached npm registry data, fetched like Lines.
func (c *Catalog) Npm(ctx context.Context, refresh bool) (func(nodeVersion string) string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	age := time.Since(c.npmCheckedAt)
	if refresh || c.npmCheckedAt.IsZero() || age > c.MaxAge || (c.npmErr != nil && age > time.Minute) {
		p, err := c.Installer.npmPackument(ctx)
		c.npmCheckedAt, c.npmErr = time.Now(), err
		if err == nil {
			c.npm = &p
		}
	}
	if c.npm == nil {
		return func(string) string { return "" }, c.npmErr
	}
	p := *c.npm
	return p.latestNpmFor, c.npmErr
}

// RunNpm runs npm in dir with a version's Node and global prefix (as the shims would), writing
// its output to out.
func RunNpm(ctx context.Context, root, version, dir string, out io.Writer, args ...string) error {
	binDir, err := BinDir(root, version)
	if err != nil {
		return err
	}
	_, cli := Npm(root, version)
	if cli == "" {
		return fmt.Errorf("Node.js %s has no npm", version)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(binDir, nodeExe()), append([]string{cli}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), NpmEnv(root, version, binDir)...)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}
