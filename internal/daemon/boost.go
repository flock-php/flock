package daemon

import (
	"flock/internal/api"
	"flock/internal/boost"
)

// withBoost adds whether each site's project uses Laravel Boost and has Flock's guideline. It is
// read when the status is, so it follows `boost:install` and `flock boost` run in a terminal.
func withBoost(sites []api.SiteStatus) []api.SiteStatus {
	out := make([]api.SiteStatus, len(sites))
	for i, s := range sites {
		s.Boost = boost.State(s.Path)
		out[i] = s
	}
	return out
}
