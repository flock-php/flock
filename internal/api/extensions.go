package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"flock/internal/php"
	"flock/internal/phpext"
)

var extensionName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// composerRequire runs `composer require ext-<name>:*` in a project with its PHP; tests replace it.
var composerRequire = func(ctx context.Context, root, minor, dir, name string) error {
	if _, err := os.Stat(php.ComposerPhar(root)); err != nil {
		return errors.New("Composer isn't installed — run `flock install`")
	}
	cmd := exec.CommandContext(ctx, php.Binary(root, minor), php.ComposerPhar(root), "require", "--no-interaction", "ext-"+name+":*")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), php.CLIEnv(root, minor)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return fmt.Errorf("composer require ext-%s failed: %s", name, strings.TrimSpace(lines[len(lines)-1]))
	}
	return nil
}

func registerExtensions(mux *http.ServeMux, b Backend) {
	report := func(site SiteStatus) phpext.Report {
		exts, _ := php.Extensions(b.Root(), site.PHP)
		return phpext.Check(site.Path, site.PHP, exts)
	}

	// The PHP extensions a site requires or seems to use, and whether its PHP has them.
	mux.HandleFunc("GET /v1/sites/{name}/extensions", func(w http.ResponseWriter, r *http.Request) {
		if site, ok := findSite(w, b, r.PathValue("name")); ok {
			writeJSON(w, http.StatusOK, report(site))
		}
	})

	// Adds `"ext-<name>": "*"` to the project's composer.json with Composer, so the requirement
	// travels with the project.
	mux.HandleFunc("POST /v1/sites/{name}/extensions/{ext}/require", func(w http.ResponseWriter, r *http.Request) {
		site, ok := findSite(w, b, r.PathValue("name"))
		if !ok {
			return
		}
		name := phpext.Normalize(r.PathValue("ext"))
		if !extensionName.MatchString(name) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid extension name %q", r.PathValue("ext")))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		if err := composerRequire(ctx, b.Root(), site.PHP, site.Path, name); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = b.Reload() // the site's problem may be gone
		writeJSON(w, http.StatusOK, report(site))
	})
}

// SiteExtensions fetches the PHP extensions a site needs.
func (c *Client) SiteExtensions(ctx context.Context, name string) (phpext.Report, error) {
	var out phpext.Report
	err := c.get(ctx, "/v1/sites/"+url.PathEscape(name)+"/extensions", &out)
	return out, err
}

// RequireExtension adds ext-<ext> to a site's composer.json.
func (c *Client) RequireExtension(ctx context.Context, name, ext string) (phpext.Report, error) {
	var out phpext.Report
	err := c.timeout(5*time.Minute).do(ctx, http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/extensions/"+url.PathEscape(ext)+"/require", &out)
	return out, err
}
