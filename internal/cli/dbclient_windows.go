package cli

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// dbClients are the GUI clients `flock db` can open when the system doesn't route connection URLs
// to one: each takes a connection URL as its argument.
var dbClients = []struct{ dir, exe string }{
	{"TablePlus", "TablePlus.exe"},
	{"TablePro", "TablePro.exe"},
	{"DataGrip", "datagrip64.exe"},
	{"DBeaver", "dbeaver.exe"},
}

// openScheme hands the URL to the default client: whichever program registered the scheme.
func openScheme() string {
	for _, scheme := range []string{"postgres", "mysql"} {
		if k, err := registry.OpenKey(registry.CLASSES_ROOT, scheme+`\shell\open\command`, registry.QUERY_VALUE); err == nil {
			k.Close()
			return `cmd /c start ""`
		}
	}
	return ""
}

// clientFallback opens the URL in the first installed client. Windows has no /Applications to
// list, so an installed client is found by its usual folder.
func clientFallback() string {
	for _, c := range dbClients {
		for _, root := range []string{`C:\Program Files`, `C:\Program Files (x86)`, filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs")} {
			if fileExists(filepath.Join(root, c.dir, c.exe)) {
				return `"` + filepath.Join(root, c.dir, c.exe) + `"`
			}
		}
	}
	return ""
}
