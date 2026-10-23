package project

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"flock/internal/config"
)

// Root is the .flockrc key and the "tool" of `flock pin root` for the document root.
const Root = "root"

// RootDirs are the conventional document roots, in the order detection tries them: public/
// (Laravel, Symfony, Slim, Laminas, CodeIgniter), www/ (Nette), web/ (Yii2, Drupal, Craft,
// Bedrock) and webroot/ (CakePHP). Each counts only with an index.php in it.
var RootDirs = []string{"public", "www", "web", "webroot"}

// Problems of a document root (DocRoot.State).
const (
	RootMissing  = "missing"   // the root set for the site or in .flockrc isn't a directory
	RootNotFound = "not_found" // a Composer project with no index.php in any of RootDirs or itself
	RootEmpty    = "empty"     // the root has neither an index.php nor an index.html
)

// rootError is an invalid document root: it is ErrInvalid, without calling the root a version.
type rootError string

func (e rootError) Error() string        { return string(e) }
func (e rootError) Is(target error) bool { return target == ErrInvalid }

func invalidRoot(format string, args ...any) error { return rootError(fmt.Sprintf(format, args...)) }

// DocRoot is the folder a site is served from, and why.
type DocRoot struct {
	// Dir is the absolute folder; Rel is it relative to the project, slash-separated ("." for the
	// project folder itself).
	Dir string
	Rel string
	// Source is Isolated (set for the site on this machine), Project (root= in the project's
	// .flockrc) or Default (detected).
	Source string
	// Pinned is what the project's .flockrc says ("" when nothing), also when an isolation wins
	// over it; File is that .flockrc.
	Pinned string
	File   string
	// State is "" when the site can be served from Dir, else RootMissing, RootNotFound or RootEmpty.
	State string
}

// RootFor decides the document root of a site: the one set for it on this machine, else root=
// in the .flockrc of its own folder, else the first of RootDirs with an index.php, else the
// project folder. Unlike versions, root= is not looked up in parent folders: a .flockrc in a
// parked folder would otherwise move the root of every project in it.
func RootFor(site *config.Site) DocRoot {
	r := DocRoot{Source: Default}
	rc := filepath.Join(site.Path, RC)
	if pinned, _ := readKey(rc, Root); pinned != "" {
		r.Pinned, r.File = pinned, rc
	}
	switch {
	case site.Root != "":
		r.Source, r.Rel = Isolated, site.Root
	case r.Pinned != "":
		r.Source, r.Rel = Project, r.Pinned
	default:
		r.Rel = DetectRoot(site.Path)
		r.Dir = filepath.Join(site.Path, filepath.FromSlash(r.Rel))
		switch {
		case r.Rel != ".":
		case exists(filepath.Join(site.Path, "composer.json")) && !exists(filepath.Join(site.Path, "index.php")):
			r.State = RootNotFound
		case !hasIndex(r.Dir):
			r.State = RootEmpty
		}
		return r
	}
	rel, err := CleanRoot(r.Rel)
	if err != nil {
		r.State = RootMissing
		return r
	}
	r.Rel, r.Dir = rel, filepath.Join(site.Path, filepath.FromSlash(rel))
	if info, err := os.Stat(r.Dir); err != nil || !info.IsDir() {
		r.State = RootMissing
	} else if !hasIndex(r.Dir) {
		r.State = RootEmpty
	}
	return r
}

// IndexFiles are what a document root needs to serve its front page: the PHP front controller or
// a static page.
var IndexFiles = []string{"index.php", "index.html"}

func hasIndex(dir string) bool {
	for _, name := range IndexFiles {
		if exists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// DetectRoot is the first of RootDirs in dir with an index.php in it, else "." (dir itself).
func DetectRoot(dir string) string {
	for _, name := range RootDirs {
		if exists(filepath.Join(dir, name, "index.php")) {
			return name
		}
	}
	return "."
}

// RootFiles are the paths whose changes can change the document root of a site, or whether it
// can be served from it (for watching): the index.php of each of RootDirs, composer.json, and the
// index files of the project folder and of the root in use.
func RootFiles(site *config.Site) []string {
	files := []string{filepath.Join(site.Path, "composer.json")}
	for _, name := range RootDirs {
		files = append(files, filepath.Join(site.Path, name, "index.php"))
	}
	for _, dir := range []string{site.Path, RootFor(site).Dir} {
		if dir == "" {
			continue // a root that doesn't exist
		}
		for _, name := range IndexFiles {
			files = append(files, filepath.Join(dir, name))
		}
	}
	return files
}

// CleanRoot normalizes a document root given relative to the project ("./src/www/" is "src/www")
// and refuses one that is absolute or leads out of the project.
func CleanRoot(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", invalidRoot("give the document root as a folder in the project, e.g. src/www")
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", invalidRoot("the document root %s must be relative to the project, e.g. src/www", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", invalidRoot("the document root %s is outside the project", rel)
	}
	return clean, nil
}

// ValidRoot checks a document root for the project in dir: relative, inside it and a folder.
// It returns the root normalized (see CleanRoot).
func ValidRoot(dir, rel string) (string, error) {
	clean, err := CleanRoot(rel)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean))); err != nil || !info.IsDir() {
		return "", invalidRoot("%s is not a folder in %s", clean, dir)
	}
	return clean, nil
}

// RootFromPath turns a folder given on the command line into a root for the project in dir: an
// absolute path must lie in the project; a relative one is relative to the project.
func RootFromPath(dir, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return ValidRoot(dir, path)
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", invalidRoot("%s is outside the project %s", path, dir)
	}
	return ValidRoot(dir, rel)
}

// PinRoot writes the document root into the .flockrc of the project in dir and returns the file.
func PinRoot(dir, rel string) (string, error) {
	clean, err := ValidRoot(dir, rel)
	if err != nil {
		return "", err
	}
	rc := filepath.Join(dir, RC)
	return rc, setKey(rc, Root, clean)
}

// PinSiteRoot pins a site's document root into its project (by default the one it uses now) and
// lifts the root set for it on this machine, so the project decides from now on. It returns the
// file it wrote and whether it changed site (which the caller then stores).
func PinSiteRoot(site *config.Site, rel string) (file string, lifted bool, err error) {
	if rel == "" {
		rel = RootFor(site).Rel
	}
	if file, err = PinRoot(site.Path, rel); err != nil {
		return "", false, err
	}
	if site.Root != "" {
		site.Root, lifted = "", true
	}
	return file, lifted, nil
}

// skipDirs are never searched for a document root: dependencies, build output, caches, logs and tests.
var skipDirs = []string{
	"vendor", "node_modules", "bower_components", "storage", "temp", "tmp", "cache", "log", "logs",
	"var", "tests", "test", "spec", "docs", "build", "dist",
}

// suggestDepth is how deep SuggestRoots looks, and maxSuggestions how many it returns.
const (
	suggestDepth   = 3
	maxSuggestions = 5
)

// SuggestRoots finds folders in the project in dir that hold an index.php and could be its
// document root ("src/www"), shallowest first. It looks up to three levels deep and skips hidden
// folders and those in skipDirs.
func SuggestRoots(dir string) []string {
	var found []string
	level := []string{"."}
	for depth := 1; depth <= suggestDepth && len(level) > 0; depth++ {
		var next []string
		for _, rel := range level {
			entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				continue
			}
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() || strings.HasPrefix(name, ".") || slices.Contains(skipDirs, strings.ToLower(name)) {
					continue
				}
				child := name
				if rel != "." {
					child = rel + "/" + name
				}
				if exists(filepath.Join(dir, filepath.FromSlash(child), "index.php")) {
					found = append(found, child)
				}
				next = append(next, child)
			}
		}
		level = next
	}
	// os.ReadDir sorts each folder, and levels come in order: shallowest first, then by name.
	if len(found) > maxSuggestions {
		found = found[:maxSuggestions]
	}
	return found
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
