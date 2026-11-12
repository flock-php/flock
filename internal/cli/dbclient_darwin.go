package cli

import (
	"os"
	"os/exec"
	"path/filepath"
)

// dbApps are the GUI clients `flock db` can open when the system doesn't route connection URLs to
// one. Each takes the URL as its argument.
var dbApps = []struct{ name, bundle string }{
	{"TablePlus", "TablePlus.app"},
	{"TablePro", "TablePro.app"},
	{"DataGrip", "DataGrip.app"},
	{"DBeaver", "DBeaver.app"},
	{"Sequel Ace", "Sequel Ace.app"},
}

// openScheme hands the URL to the default client: macOS opens URLs with the app registered for
// them. "" when no client registered a database scheme, as macOS asks which app to use then.
func openScheme() string {
	if schemeHandler("postgres") || schemeHandler("mysql") {
		return "open"
	}
	return ""
}

// clientFallback opens the URL in the first installed client.
func clientFallback() string {
	home, _ := os.UserHomeDir()
	for _, app := range dbApps {
		for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
			if fileExists(filepath.Join(dir, app.bundle)) {
				return "open -a " + app.name
			}
		}
	}
	return ""
}

// schemeHandler reports whether macOS has a client registered for scheme: `open` answers with the
// app it would use, or with an error when there is none.
func schemeHandler(scheme string) bool {
	out, err := exec.Command("open", "--json", "-h", scheme+":").Output()
	return err == nil && len(out) > 2
}
