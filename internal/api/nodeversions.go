package api

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"flock/internal/config"
	"flock/internal/node"
)

// NodeVersions is the Node pane's data: every major line with its install state.
type NodeVersions struct {
	CheckedAt  *time.Time    `json:"checked_at"`
	CheckError string        `json:"check_error"`
	Versions   []NodeRelease `json:"versions"`
}

// NodeRelease is a node.Line plus what is installed of it.
type NodeRelease struct {
	node.Line
	Installed string `json:"installed"` // newest installed version of the major, e.g. "22.10.0"
	Default   bool   `json:"default"`
	Update    bool   `json:"update"` // a newer release than the installed one exists
	// Npm is the npm the installed version uses (bundled, or updated into its global prefix);
	// NpmLatest the newest npm that supports it, when that is newer.
	Npm       string `json:"npm"`
	NpmLatest string `json:"npm_latest"`
}

// nodeCatalog caches nodejs.org's release lines for the daemon's lifetime.
var nodeCatalog = &node.Catalog{Installer: &node.Installer{}, MaxAge: 6 * time.Hour}

func registerNodeVersions(mux *http.ServeMux, b Backend) {
	respond := func(w http.ResponseWriter, r *http.Request, refresh bool) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		writeJSON(w, http.StatusOK, nodeVersions(ctx, b, refresh))
	}
	major := func(r *http.Request) (string, error) {
		v := r.PathValue("major")
		if n, err := strconv.Atoi(v); err != nil || n <= 0 {
			return "", invalid("invalid Node.js version %q", v)
		}
		return v, nil
	}

	mux.HandleFunc("GET /v1/node/versions", func(w http.ResponseWriter, r *http.Request) { respond(w, r, false) })
	mux.HandleFunc("POST /v1/node/check", func(w http.ResponseWriter, r *http.Request) { respond(w, r, true) })

	// Installs the newest release of a major (also how an installed major is updated), then drops
	// the older releases of that major.
	mux.HandleFunc("POST /v1/node/versions/{major}/install", func(w http.ResponseWriter, r *http.Request) {
		m, err := major(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		version, err := (&node.Installer{Root: b.Root()}).Install(ctx, m)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		node.RemoveOlder(b.Root(), version)
		respond(w, r, false)
	})

	// Updates the npm of a major to the newest version that supports its installed Node.js.
	mux.HandleFunc("POST /v1/node/versions/{major}/npm", func(w http.ResponseWriter, r *http.Request) {
		m, err := major(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		release, ok := findNodeRelease(nodeVersions(ctx, b, false), m)
		switch {
		case !ok || release.Installed == "":
			writeError(w, http.StatusBadRequest, invalid("Node.js %s is not installed", m))
			return
		case release.NpmLatest == "":
			writeError(w, http.StatusBadRequest, invalid("npm %s is the newest version that supports Node.js %s", release.Npm, release.Installed))
			return
		}
		if err := node.UpdateNpm(ctx, b.Root(), release.Installed, release.NpmLatest); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		respond(w, r, false)
	})

	mux.HandleFunc("DELETE /v1/node/versions/{major}", func(w http.ResponseWriter, r *http.Request) {
		m, err := major(r)
		if err == nil {
			err = b.Mutate(func(cfg *config.Config) error { return checkNodeUninstall(cfg, m) })
		}
		if err == nil {
			err = node.Uninstall(b.Root(), m)
		}
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		respond(w, r, false)
	})
}

// NodeVersions fetches every Node.js major line with its install state.
func (c *Client) NodeVersions(ctx context.Context) (NodeVersions, error) {
	var out NodeVersions
	err := c.slow().get(ctx, "/v1/node/versions", &out)
	return out, err
}

// InstallNode installs the newest release of a major and waits for it.
func (c *Client) InstallNode(ctx context.Context, major string) (NodeVersions, error) {
	var out NodeVersions
	err := c.timeout(10*time.Minute).do(ctx, http.MethodPost, "/v1/node/versions/"+major+"/install", &out)
	return out, err
}

func findNodeRelease(v NodeVersions, major string) (NodeRelease, bool) {
	for _, r := range v.Versions {
		if r.Major == major {
			return r, true
		}
	}
	return NodeRelease{}, false
}

// checkNodeUninstall refuses to remove the default version or one a linked site is pinned to.
func checkNodeUninstall(cfg *config.Config, major string) error {
	if cfg.DefaultNode == major {
		return fmt.Errorf("%w: Node.js %s is the default version — make another version the default first", errConflict, major)
	}
	for _, s := range cfg.Sites {
		if s.Node == major {
			return fmt.Errorf("%w: the site %s uses Node.js %s", errConflict, s.Name, major)
		}
	}
	return nil
}

// nodeVersions merges the published lines with what is installed. Installed majors that are no
// longer offered (older than node.MinimumMajor) are listed too, so they can be uninstalled.
func nodeVersions(ctx context.Context, b Backend, refresh bool) NodeVersions {
	cfg, _ := config.Load(b.Root())
	if cfg == nil {
		cfg = config.Default()
	}

	lines, checkedAt, err := nodeCatalog.Lines(ctx, refresh)
	out := NodeVersions{Versions: []NodeRelease{}}
	if !checkedAt.IsZero() {
		out.CheckedAt = &checkedAt
	}
	if err != nil {
		out.CheckError = err.Error()
	}

	installed := map[string]string{}        // major → newest installed version
	versions, _ := node.Installed(b.Root()) // newest first
	for _, v := range versions {
		if m := node.MajorOf(v); m != "" && installed[m] == "" {
			installed[m] = v
		}
	}

	// npm's registry data is only needed (and fetched) when something is installed.
	latestNpm := func(string) string { return "" }
	if len(versions) > 0 {
		var npmErr error
		latestNpm, npmErr = nodeCatalog.Npm(ctx, refresh)
		if npmErr != nil && out.CheckError == "" {
			out.CheckError = "npm registry: " + npmErr.Error()
		}
	}

	listed := map[string]bool{}
	add := func(line node.Line) {
		release := NodeRelease{Line: line, Installed: installed[line.Major], Default: line.Major == cfg.DefaultNode}
		release.Update = release.Installed != "" && node.Newer(line.Latest, release.Installed)
		if release.Installed != "" {
			release.Npm, _ = node.Npm(b.Root(), release.Installed)
			if latest := latestNpm(release.Installed); release.Npm != "" && node.Newer(latest, release.Npm) {
				release.NpmLatest = latest
			}
		}
		out.Versions = append(out.Versions, release)
		listed[line.Major] = true
	}
	for _, line := range lines {
		add(line)
	}
	for _, v := range versions {
		if m := node.MajorOf(v); !listed[m] {
			add(node.Line{Major: m, Latest: installed[m]})
		}
	}
	slices.SortStableFunc(out.Versions, func(a, b NodeRelease) int {
		x, _ := strconv.Atoi(a.Major)
		y, _ := strconv.Atoi(b.Major)
		return cmp.Compare(y, x) // newest first
	})
	return out
}
