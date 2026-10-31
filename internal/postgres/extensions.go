package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"flock/internal/binaries"
)

// Extension is a PostgreSQL extension Flock offers as a package of its own, one per major
// ("postgis-pg18"). It is installed per major under services/postgres/extensions/<name>/<major>,
// and Link makes it loadable by that major's server.
//
// A package has lib/ (the modules, which go into the server's pkglibdir), extension/ (control and
// SQL files) and deps/ (the libraries the modules need). macOS finds deps/ through the modules'
// rpath, since @loader_path follows the symlink to the real module; Linux expands $ORIGIN to the
// symlink's folder instead, so the server gets deps/ on LD_LIBRARY_PATH (LibraryDirs); Windows
// gets them copied next to postgres.exe. Anything else, like PostGIS' share/proj, stays where it
// is: PROJ finds it from the real location of its library, and on Windows through PROJ_DATA
// (ServerEnv).
type Extension struct {
	Name        string   `json:"name"`        // package name without the major: "postgis"
	Title       string   `json:"title"`       // "PostGIS"
	Description string   `json:"description"` // one line for the apps
	Creates     string   `json:"creates"`     // what CREATE EXTENSION takes: "vector" for pgvector
	License     string   `json:"license"`
	Preload     bool     `json:"preload"`  // needs shared_preload_libraries: installing it restarts the server
	Requires    []string `json:"requires"` // extensions it needs (its control file's requires), installed with it
}

// Extensions are the extensions Flock offers, in the order the apps list them.
var Extensions = []Extension{
	{Name: "postgis", Title: "PostGIS", Description: "Geographic types, spatial indexes and queries", Creates: "postgis", License: "GPL-2.0"},
	{Name: "pgrouting", Title: "pgRouting", Description: "Shortest paths and routing on PostGIS networks", Creates: "pgrouting", License: "GPL-2.0", Requires: []string{"postgis"}},
	{Name: "pgvector", Title: "pgvector", Description: "Vector similarity search for embeddings", Creates: "vector", License: "PostgreSQL"},
	{Name: "timescaledb", Title: "TimescaleDB", Description: "Hypertables, compression and continuous aggregates for time series", Creates: "timescaledb", License: "Apache-2.0, Timescale License", Preload: true},
}

// FindExtension returns the extension called name.
func FindExtension(name string) (Extension, bool) {
	for _, e := range Extensions {
		if e.Name == name {
			return e, true
		}
	}
	return Extension{}, false
}

// ExtensionPackage is the manifest package of an extension for a major: "postgis-pg18".
func ExtensionPackage(name, major string) string { return name + "-pg" + major }

// ExtensionsRoot holds the installed extensions and the custom folders.
func ExtensionsRoot(root string) string {
	return filepath.Join(root, "services", "postgres", "extensions")
}

// ExtensionDir is where a build of an extension is installed for a major.
func ExtensionDir(root, name, major, id string) string {
	return filepath.Join(ExtensionsRoot(root), name, major, id)
}

// CustomDir is the folder for a major's own extensions: lib/ and extension/ (deps/ on Windows),
// like a package. Flock never changes what is in it, and keeps it when it is uninstalled.
func CustomDir(root, major string) string {
	return filepath.Join(ExtensionsRoot(root), "custom", major)
}

// customReadme explains the custom folder to whoever opens it.
const customReadme = `Your own PostgreSQL %[1]s extensions. Flock makes them available to PostgreSQL %[1]s
and keeps them across updates.

lib/        the extension's library (.so, .dylib or .dll)
extension/  its .control and .sql files
deps/       Windows and Linux: libraries it needs

With PGXS (macOS, Linux):
  PG_CONFIG=$(flock services extensions postgres pg-config --major %[1]s)
  make PG_CONFIG=$PG_CONFIG
  make install PG_CONFIG=$PG_CONFIG pkglibdir="%[2]s" datadir="%[3]s"

Then run CREATE EXTENSION in your database.
`

// EnsureCustomDir creates a major's custom folder and returns it.
func EnsureCustomDir(root, major string) (string, error) {
	if !majorPattern.MatchString(major) {
		return "", fmt.Errorf("%w %q", ErrInvalidMajor, major)
	}
	dir := CustomDir(root, major)
	subdirs := []string{"lib", "extension"}
	if runtime.GOOS != "darwin" {
		subdirs = append(subdirs, "deps")
	}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return "", err
		}
	}
	readme := filepath.Join(dir, "README.txt")
	if _, err := os.Stat(readme); errors.Is(err, os.ErrNotExist) {
		text := fmt.Sprintf(customReadme, major, filepath.Join(dir, "lib"), dir)
		if err := os.WriteFile(readme, []byte(text), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

var extensionIDPattern = regexp.MustCompile(`^(\d[0-9A-Za-z.+]*)-build(\d+)$`)

// parseExtensionID splits an extension install ID ("3.6.4-build1") into its version and build.
func parseExtensionID(id string) (version string, build int, ok bool) {
	m := extensionIDPattern.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	build, _ = strconv.Atoi(m[2])
	return m[1], build, true
}

// extensionVersion is the version of an extension install ID, "" if it isn't one.
func extensionVersion(id string) string {
	v, _, _ := parseExtensionID(id)
	return v
}

// newerExtension reports whether extension install ID a is newer than b.
func newerExtension(a, b string) bool {
	va, ba, _ := parseExtensionID(a)
	vb, bb, _ := parseExtensionID(b)
	if c := binaries.CompareVersions(va, vb); c != 0 {
		return c > 0
	}
	return ba > bb
}

// InstalledExtension returns the installed build of an extension for a major ("3.6.4-build1"), or "".
func InstalledExtension(root, name, major string) string {
	ids := installedExtensionIDs(root, name, major)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// installedExtensionIDs lists the installed builds of an extension for a major, newest first.
func installedExtensionIDs(root, name, major string) []string {
	entries, err := os.ReadDir(filepath.Join(ExtensionsRoot(root), name, major))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if _, _, ok := parseExtensionID(e.Name()); ok && isDir(filepath.Join(ExtensionsRoot(root), name, major, e.Name(), "extension")) {
			ids = append(ids, e.Name())
		}
	}
	sort.Slice(ids, func(i, j int) bool { return newerExtension(ids[i], ids[j]) })
	return ids
}

// InstalledExtensions names the extensions installed for a major, in the order of Extensions.
func InstalledExtensions(root, major string) []string {
	out := []string{}
	for _, e := range Extensions {
		if InstalledExtension(root, e.Name, major) != "" {
			out = append(out, e.Name)
		}
	}
	return out
}

// LibraryDirs are the deps/ folders of a major's installed extensions, and of its custom folder
// when that has any: on Linux the server finds the extensions' libraries through LD_LIBRARY_PATH.
func LibraryDirs(root, major string) []string {
	var dirs []string
	for _, e := range Extensions {
		if id := InstalledExtension(root, e.Name, major); id != "" {
			if dir := filepath.Join(ExtensionDir(root, e.Name, major, id), "deps"); isDir(dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	custom := filepath.Join(CustomDir(root, major), "deps")
	if entries, err := os.ReadDir(custom); err == nil && len(entries) > 0 {
		dirs = append(dirs, custom)
	}
	return dirs
}

// ServerEnv is what a major's server needs in its environment for its extensions: on Linux their
// deps/ on LD_LIBRARY_PATH; on Windows, where an extension's DLLs are copied next to postgres.exe
// and PROJ would look for its data next to bin\, PROJ_DATA on the share\proj of the package that
// has one (PostGIS). macOS needs nothing: @loader_path follows the symlinks to the packages.
func ServerEnv(root, major string) []string {
	switch runtime.GOOS {
	case "linux":
		if dirs := LibraryDirs(root, major); len(dirs) > 0 {
			return []string{"LD_LIBRARY_PATH=" + strings.Join(dirs, string(os.PathListSeparator))}
		}
	case "windows":
		for _, e := range Extensions {
			if id := InstalledExtension(root, e.Name, major); id != "" {
				if proj := filepath.Join(ExtensionDir(root, e.Name, major, id), "share", "proj"); isDir(proj) {
					return []string{"PROJ_DATA=" + proj}
				}
			}
		}
	}
	return nil
}

// PreloadLibraries are the installed extensions of a major that go into shared_preload_libraries.
func PreloadLibraries(root, major string) []string {
	var out []string
	for _, e := range Extensions {
		if e.Preload && InstalledExtension(root, e.Name, major) != "" {
			out = append(out, e.Name)
		}
	}
	return out
}

// linksFile records, in a server build, the files Link put there.
const linksFile = ".flock-extensions.json"

// linkMu serialises Link: the daemon links on every reload, the Manager after its operations.
var linkMu sync.Mutex

// Link makes the extensions installed for id's major, and the files in its custom folder, loadable
// by that server: modules go into its pkglibdir and control/SQL files into its extension
// directory, as symlinks (copies on Windows, plus deps/ next to postgres.exe). What an earlier
// Link put there and is gone now is removed. A file of the server's own is never replaced: the
// conflict is reported in warnings. changed reports whether anything was added or removed.
func Link(root, id string) (changed bool, warnings []string, err error) {
	linkMu.Lock()
	defer linkMu.Unlock()

	build := Dir(root, id)
	major := MajorOf(id)
	libDir, extDir := serverDirs(build)
	if libDir == "" || extDir == "" {
		if len(InstalledExtensions(root, major)) == 0 && CustomFiles(root, major) == "" {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("PostgreSQL %s has no extension directories", VersionOf(id))
	}

	// What should be there: path relative to the build → source file.
	want := map[string]string{}
	owner := map[string]string{}
	add := func(src, dstDir, label string) {
		entries, err := os.ReadDir(src)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			rel, err := filepath.Rel(build, filepath.Join(dstDir, e.Name()))
			if err != nil {
				continue
			}
			if other, taken := owner[rel]; taken {
				warnings = append(warnings, fmt.Sprintf("%s and %s both have %s; using %s's", other, label, e.Name(), other))
				continue
			}
			want[rel] = filepath.Join(src, e.Name())
			owner[rel] = label
		}
	}
	sources := func(dir, label string) {
		add(filepath.Join(dir, "lib"), libDir, label)
		add(filepath.Join(dir, "extension"), extDir, label)
		if runtime.GOOS == "windows" {
			add(filepath.Join(dir, "deps"), filepath.Join(build, "bin"), label)
		}
	}
	for _, e := range Extensions {
		if ext := InstalledExtension(root, e.Name, major); ext != "" {
			sources(ExtensionDir(root, e.Name, major, ext), e.Title)
		}
	}
	sources(CustomDir(root, major), "your extensions folder")

	had := readLinks(build)
	for rel, src := range had {
		if want[rel] == src && linked(src, filepath.Join(build, rel)) {
			continue
		}
		// Only what Link made is removed: a symlink anywhere, a copy only on Windows.
		dst := filepath.Join(build, rel)
		if info, err := os.Lstat(dst); err == nil && (info.Mode()&os.ModeSymlink != 0 || runtime.GOOS == "windows") {
			if err := os.Remove(dst); err != nil {
				return changed, warnings, err
			}
		}
		delete(had, rel)
		changed = true
	}

	now := map[string]string{}
	for _, rel := range slices.Sorted(maps.Keys(want)) {
		src, dst := want[rel], filepath.Join(build, rel)
		if had[rel] == src {
			now[rel] = src
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			warnings = append(warnings, fmt.Sprintf("%s's %s is not used: PostgreSQL %s has a file of that name", owner[rel], filepath.Base(rel), major))
			continue
		}
		if err := linkFile(src, dst); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s's %s: %v", owner[rel], filepath.Base(rel), err))
			continue
		}
		now[rel] = src
		changed = true
	}
	if changed || len(now) != len(had) {
		if err := writeLinks(build, now); err != nil {
			return changed, warnings, err
		}
	}
	return changed, warnings, nil
}

// serverDirs finds a build's pkglibdir and extension directory: lib/postgresql and
// share/postgresql/extension, or lib and share/extension when the install prefix already named
// PostgreSQL. The one holding plpgsql is it.
func serverDirs(build string) (lib, ext string) {
	for _, dir := range []string{filepath.Join(build, "lib", "postgresql"), filepath.Join(build, "lib")} {
		if matches, _ := filepath.Glob(filepath.Join(dir, "plpgsql.*")); len(matches) > 0 {
			lib = dir
			break
		}
	}
	for _, dir := range []string{filepath.Join(build, "share", "postgresql", "extension"), filepath.Join(build, "share", "extension")} {
		if _, err := os.Stat(filepath.Join(dir, "plpgsql.control")); err == nil {
			ext = dir
			break
		}
	}
	return lib, ext
}

// linked reports whether dst is (still) what Link made of src.
func linked(src, dst string) bool {
	if runtime.GOOS != "windows" {
		target, err := os.Readlink(dst)
		return err == nil && target == src
	}
	a, errA := os.Stat(src)
	b, errB := os.Stat(dst)
	return errA == nil && errB == nil && a.Size() == b.Size() && !a.ModTime().After(b.ModTime())
}

// linkFile symlinks dst to src; Windows copies, since creating symlinks needs a privilege there.
func linkFile(src, dst string) error {
	if runtime.GOOS != "windows" {
		return os.Symlink(src, dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

func readLinks(build string) map[string]string {
	links := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(build, linksFile)); err == nil {
		_ = json.Unmarshal(data, &links)
	}
	return links
}

func writeLinks(build string, links map[string]string) error {
	path := filepath.Join(build, linksFile)
	if len(links) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(links, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// CustomFiles fingerprints a major's custom folder (names, sizes, modification times), so the
// daemon can link what is added to it without a restart.
func CustomFiles(root, major string) string {
	var b strings.Builder
	dir := CustomDir(root, major)
	for _, sub := range []string{"lib", "extension", "deps"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if info, err := e.Info(); err == nil {
				fmt.Fprintf(&b, "%s/%s %d %d\n", sub, e.Name(), info.Size(), info.ModTime().UnixNano())
			}
		}
	}
	return b.String()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// CustomExtension is an extension in a major's custom folder: its control file, and whether Link
// made it available to the server.
type CustomExtension struct {
	Name    string `json:"name"`    // the control file's name: what CREATE EXTENSION takes
	Version string `json:"version"` // its default_version, "" when it has none
	Linked  bool   `json:"linked"`  // the server has it (false while the major isn't installed)
	Problem string `json:"problem"` // why it can't be used, "" when nothing is in the way
}

// CustomExtensions lists the extensions in a major's custom folder, in name order. id is the
// major's installed build ("" when none is), whose links tell what the server has.
func CustomExtensions(root, major, id string) []CustomExtension {
	dir := CustomDir(root, major)
	entries, err := os.ReadDir(filepath.Join(dir, "extension"))
	if err != nil {
		return []CustomExtension{}
	}
	var links map[string]string
	var libDir, extDir string
	if id != "" {
		links = readLinks(Dir(root, id))
		libDir, extDir = serverDirs(Dir(root, id))
	}
	linkedTo := func(serverDir, name string) bool {
		if serverDir == "" {
			return false
		}
		rel, err := filepath.Rel(Dir(root, id), filepath.Join(serverDir, name))
		return err == nil && links[rel] != ""
	}
	out := []CustomExtension{}
	for _, e := range entries {
		file := e.Name()
		if e.IsDir() || !strings.HasSuffix(file, ".control") {
			continue
		}
		control := readControl(filepath.Join(dir, "extension", file))
		x := CustomExtension{Name: strings.TrimSuffix(file, ".control"), Version: control["default_version"]}
		module := strings.TrimPrefix(control["module_pathname"], "$libdir/")
		hasModule := module == "" || moduleFile(filepath.Join(dir, "lib"), module) != ""
		switch {
		case id == "":
			x.Problem = fmt.Sprintf("PostgreSQL %s isn't installed", major)
		case !linkedTo(extDir, file):
			x.Problem = fmt.Sprintf("not used: PostgreSQL %s or another extension has a %s", major, file)
		case !hasModule && (libDir == "" || moduleFile(libDir, module) == ""):
			x.Problem = fmt.Sprintf("its library %s isn't in lib/", module)
		case module != "" && hasModule && !linkedTo(libDir, moduleFile(filepath.Join(dir, "lib"), module)):
			x.Problem = fmt.Sprintf("its library is not used: PostgreSQL %s or another extension has a %s", major, moduleFile(filepath.Join(dir, "lib"), module))
		default:
			x.Linked = true
		}
		out = append(out, x)
	}
	return out
}

// readControl reads the key = 'value' lines of an extension's control file.
func readControl(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "'\"")
	}
	return out
}

// moduleFile is the file name of a module in dir (name.so, name.dylib or name.dll), "" if it isn't there.
func moduleFile(dir, name string) string {
	for _, suffix := range []string{"", ".so", ".dylib", ".dll"} {
		if _, err := os.Stat(filepath.Join(dir, name+suffix)); err == nil {
			return name + suffix
		}
	}
	return ""
}
