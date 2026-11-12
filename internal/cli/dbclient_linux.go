package cli

import (
	"os/exec"
	"strings"
)

// dbApps are the GUI clients `flock db` can open when the system doesn't route connection URLs to
// one. Each takes the URL as its argument.
var dbApps = []string{"tableplus", "datagrip", "dbeaver"}

// schemeHandler reports whether the desktop has a client registered for scheme, which xdg-open
// then hands the URL to. A headless machine (no xdg-open) has none.
func schemeHandler(scheme string) bool {
	if _, ok := lookPath("xdg-open"); !ok {
		return false
	}
	out, err := exec.Command("xdg-mime", "query", "default", "x-scheme-handler/"+scheme).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// openScheme hands the URL to the default client: the desktop's registered handler for it, via
// xdg-open.
func openScheme() string {
	if schemeHandler("postgres") || schemeHandler("mysql") {
		return "xdg-open"
	}
	return ""
}

// clientFallback opens the URL in the first installed client.
func clientFallback() string {
	for _, app := range dbApps {
		if path, ok := lookPath(app); ok {
			return path
		}
	}
	return ""
}
