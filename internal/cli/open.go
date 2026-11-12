package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"flock/internal/config"
	"flock/internal/project"
	"flock/internal/worktree"
)

// siteOrDirArg resolves the site `flock open` and `flock db` target: a site by name, or a directory
// that is a site or inside one, as Herd's `herd open` takes; without an argument, the site the
// working directory is in.
func siteOrDirArg(s *state, args []string) (config.Site, error) {
	if len(args) == 0 {
		return siteArg(s, nil)
	}
	if site, ok := s.cfg.FindSite(args[0]); ok {
		return site, nil
	}
	if info, err := os.Stat(args[0]); err == nil && info.IsDir() {
		if site, ok := s.cfg.SiteForDir(args[0]); ok {
			return site, nil
		}
		return config.Site{}, fmt.Errorf("%s is not a Flock site — run `flock link` there first", args[0])
	}
	return config.Site{}, fmt.Errorf("no site named %q", args[0])
}

func newOpenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "open [site|dir]",
		Short: "Open a site in the browser (defaults to the one containing the working directory)",
		Long: `Opens a site in the browser: the site containing the working directory, a site by name, or
the one a directory is (or contains), as Herd's open does.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteOrDirArg(s, args)
			if err != nil {
				return err
			}

			url := siteURL(s.cfg, site)
			if st, err := s.client().Status(cmd.Context()); err == nil {
				for _, ss := range st.Sites {
					if ss.Name == site.Name && ss.URL != "" {
						url = ss.URL // as the daemon serves it (extra domains, a secured one)
					}
				}
			}
			if err := openFile(url); err != nil {
				// Without a browser to hand it to (a server, SSH), saying where it is still helps.
				return fmt.Errorf("open %s: %w", url, err)
			}
			cmd.Printf("%s → %s\n", site.Name, url)
			return nil
		},
	}
}

// siteURL is the site's URL while the daemon can't say (it isn't running): its main domain.
func siteURL(cfg *config.Config, site config.Site) string {
	scheme := "http"
	if site.Secured {
		scheme = "https"
	}
	port := cfg.Ports.HTTP
	if scheme == "https" {
		port = cfg.Ports.HTTPS
	}
	url := scheme + "://" + cfg.Domain(site)
	if (scheme == "https" && port != 443) || (scheme == "http" && port != 80) {
		url += ":" + strconv.Itoa(port)
	}
	return url
}

func newDBCommand() *cobra.Command {
	var client string
	var printOnly bool

	cmd := &cobra.Command{
		Use:   "db [site|dir]",
		Short: "Open a site's database in a GUI client (defaults to the one containing the working directory)",
		Long: `Opens the database of a site: the one containing the working directory, one by name, or the
one a directory is (or contains).

With no client given, Flock hands the connection URL to the program registered for postgres:// and
mysql:// links, or an installed TablePlus, TablePro, DataGrip or DBeaver; --url prints it instead.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteOrDirArg(s, args)
			if err != nil {
				return err
			}

			keys, err := databaseKeys(&site)
			if err != nil {
				return err
			}
			url, err := connectionURL(site.Name, keys)
			if err != nil {
				return err
			}

			// The URL carries the password: it is only shown without it, never printed with it.
			client = dbClient(client)
			if printOnly {
				cmd.Print(connectionLine(site.Name, keys, url))
				return nil
			}
			if client == "" {
				cmd.Printf("%s → %s\n", site.Name, maskedURL(url))
				return nil
			}

			if err := runClient(client, url); err != nil {
				return err
			}
			cmd.Printf("%s → %s\n", site.Name, maskedURL(url))
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", "", "open the database with this instead: `<client> <url>`, or a shell command with %s")
	cmd.Flags().BoolVar(&printOnly, "url", false, "print the connection URL instead of opening a client (the password is left out)")
	return cmd
}

// dbClient is the client `flock db` opens the database with: --client, FLOCK_DB_CLIENT, or the
// system's own client for a connection URL. "" prints the URL instead.
func dbClient(flag string) string {
	if flag != "" {
		return flag
	}
	if env := os.Getenv("FLOCK_DB_CLIENT"); env != "" {
		return env
	}
	return defaultDBClient()
}

// databaseKeys reads the settings a connection URL is built from, as the app itself gets them: its
// .env. The .env is in the document root when Flock serves a subdirectory (root= in .flockrc).
func databaseKeys(site *config.Site) (map[string]string, error) {
	path := filepath.Join(site.Path, ".env")
	env, err := worktree.ReadDotenv(path)
	if errors.Is(err, os.ErrNotExist) {
		path = filepath.Join(project.RootFor(site).Dir, ".env")
		env, err = worktree.ReadDotenv(path)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	keys := map[string]string{}
	for _, key := range dbKeys {
		keys[key] = env.Get(key)
	}
	if keys["DB_CONNECTION"] == "" {
		return nil, fmt.Errorf("%s has no DB_CONNECTION", path)
	}
	return keys, nil
}

// dbKeys are the .env settings a connection URL is built from.
var dbKeys = []string{"DB_CONNECTION", "DB_DATABASE", "DB_USERNAME", "DB_PASSWORD", "DB_HOST", "DB_PORT"}

// driver is what DB_CONNECTION names: the URL scheme its client expects, its default port, and
// whether it is a SQLite file rather than a server.
type driver struct {
	scheme string
	port   int
	sqlite bool
}

// drivers maps every DB_CONNECTION Flock knows; the .env keeps whatever spelling the app uses.
var drivers = map[string]driver{
	"pgsql":      {scheme: "postgres", port: 5432},
	"postgres":   {scheme: "postgres", port: 5432},
	"postgresql": {scheme: "postgres", port: 5432},
	"mysql":      {scheme: "mysql", port: 3306},
	"mariadb":    {scheme: "mysql", port: 3306},
	"sqlite":     {sqlite: true},
	"sqlite3":    {sqlite: true},
	"mongodb":    {scheme: "mongodb", port: 27017},
}

// databaseURL is the connection URL of the database the site uses, from what its .env says.
func databaseURL(site *config.Site) (string, error) {
	keys, err := databaseKeys(site)
	if err != nil {
		return "", err
	}
	return connectionURL(site.Name, keys)
}

// connectionURL builds the URL for the settings databaseKeys read.
func connectionURL(site string, keys map[string]string) (string, error) {
	database := keys["DB_DATABASE"]
	if database == "" {
		return "", fmt.Errorf("no DB_DATABASE for %s", site)
	}

	d, ok := drivers[keys["DB_CONNECTION"]]
	switch {
	case !ok:
		return "", fmt.Errorf("unknown DB_CONNECTION %q", keys["DB_CONNECTION"])
	case d.sqlite:
		return "sqlite:" + database, nil
	}
	return connURL(d.scheme, keys, d.port, database), nil
}

// maskedURL is url without its password, for anything printed or logged. The rest stays exactly as
// URL clients get it (a user name with an @ stays encoded).
func maskedURL(raw string) string {
	at := strings.LastIndexByte(raw, '@')
	if at < 0 {
		return raw
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw
	}

	userInfo := rest[:at-len(scheme)-3]
	user, _, hasPassword := strings.Cut(userInfo, ":")
	if !hasPassword {
		return raw
	}
	return scheme + "://" + user + ":****@" + raw[at+1:]
}

// connectionLine is the connection as `flock db --url` prints it: what it is, where, and the URL
// to paste in a client.
func connectionLine(site string, keys map[string]string, url string) string {
	if drivers[keys["DB_CONNECTION"]].sqlite {
		return fmt.Sprintf("%s → %s\ndatabase  %s\nurl       %s\n", site, maskedURL(url),
			strings.TrimPrefix(url, "sqlite:"), maskedURL(url))
	}
	port := keys["DB_PORT"]
	if port == "" {
		port = strconv.Itoa(drivers[keys["DB_CONNECTION"]].port)
	}
	return fmt.Sprintf("%s → %s\ndriver    %s\nhost      %s:%s\ndatabase  %s\nuser      %s\nurl       %s\n",
		site, maskedURL(url), keys["DB_CONNECTION"], hostOr(keys["DB_HOST"]), port,
		keys["DB_DATABASE"], keys["DB_USERNAME"], maskedURL(url))
}

func hostOr(host string) string {
	if host == "" {
		return "127.0.0.1"
	}
	return host
}

// runClient runs the client with the URL as its last argument, e.g. "open -a TablePlus" on
// macOS. A client with %s is a shell command (quote the %s: sh -c 'client "%s"'); the URL reaches
// the shell as an argument of its own, never inside the command, so nothing expands it.
func runClient(client, url string) error {
	var args []string
	if strings.Contains(client, "%s") {
		// The %s is only a placeholder: the shell gets the whole line, and the URL as $0, so a
		// password with $ or ' is neither expanded nor able to break the command.
		args = []string{"sh", "-c", client}
	} else {
		var err error
		if args, err = splitArgs(client); err != nil {
			return err
		}
	}

	out, err := exec.Command(args[0], append(args[1:], url)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", client, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// splitArgs splits a client command into its words, keeping quoted text ("Table Plus") together
// and dropping the quotes, as a shell would.
func splitArgs(command string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	flush := func() {
		if word.Len() > 0 {
			args = append(args, word.String())
			word.Reset()
		}
	}

	for _, r := range command {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			word.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("%s: unbalanced %c", command, quote)
	}
	flush()
	if len(args) == 0 {
		return nil, errors.New("empty --client")
	}
	return args, nil
}

// connURL builds a connection URL, leaving out the parts .env leaves empty (a database without a
// password, a server on the default port).
func connURL(scheme string, keys map[string]string, defaultPort int, database string) string {
	auth := ""
	if user := keys["DB_USERNAME"]; user != "" {
		auth = user
		if keys["DB_PASSWORD"] != "" {
			auth += ":" + keys["DB_PASSWORD"]
		}
		auth += "@"
	}

	host := keys["DB_HOST"]
	if host == "" {
		host = "127.0.0.1"
	}
	port := keys["DB_PORT"]
	if port == "" {
		port = strconv.Itoa(defaultPort)
	}
	return scheme + "://" + auth + net.JoinHostPort(host, port) + "/" + database
}
