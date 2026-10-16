package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"flock/internal/boost"
	"flock/internal/mcpclients"
)

// boostApply writes Flock's guideline and runs boost:update; tests replace it.
var boostApply = boost.Apply

func registerBoost(mux *http.ServeMux, b Backend) {
	// Adds Flock's guideline to a site's Laravel Boost guidelines (or updates it), then runs
	// `php artisan boost:update` with the site's PHP so the agents' files include it.
	mux.HandleFunc("POST /v1/sites/{name}/boost", func(w http.ResponseWriter, r *http.Request) {
		site, ok := findSite(w, b, r.PathValue("name"))
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if _, err := boostApply(ctx, mcpclients.Executable(b.Root()), site.Path); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, boost.ErrNotBoost) || errors.Is(err, boost.ErrCustom) {
				code = http.StatusBadRequest
			}
			writeError(w, code, err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})
}

// AddBoostGuideline adds Flock's guideline to a site's Laravel Boost guidelines.
func (c *Client) AddBoostGuideline(ctx context.Context, name string) (Status, error) {
	var out Status
	err := c.slow().do(ctx, http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/boost", &out)
	return out, err
}
