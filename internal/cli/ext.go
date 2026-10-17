package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"flock/internal/envutil"
	"flock/internal/php"
	"flock/internal/phpext"
	"flock/internal/project"
)

func newExtCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ext",
		Short: "Show the PHP extensions this project requires or seems to use",
		Long: "Lists the extensions composer.json and the locked packages require (`ext-*`), the ones a Laravel\n" +
			"app's .env and config show it uses without requiring them, and whether Flock's PHP has each.\n" +
			"Requiring an extension in composer.json makes `composer install` refuse a server without it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, dir, minor, err := extProject()
			if err != nil {
				return err
			}
			exts, err := php.Extensions(s.root, minor)
			if err != nil {
				return err
			}
			report := phpext.Check(dir, minor, exts)

			header := "PHP " + minor
			if report.Laravel {
				header += " · Laravel"
			}
			cmd.Println(header)
			if len(report.Extensions) == 0 {
				cmd.Println("  The project requires no PHP extensions.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, e := range report.Extensions {
				mark, detail := "✓", "required by "+strings.Join(e.RequiredBy, ", ")
				if !e.Declared() {
					mark, detail = "!", "not required: "+e.Hint+" — `flock ext add "+e.Name+"`"
				}
				if !e.Available {
					mark, detail = "✗", detail+" — Flock's PHP "+minor+" doesn't include it"
				}
				fmt.Fprintf(w, "  %s %s\t%s\n", mark, e.Name, detail)
			}
			return w.Flush()
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "add <extension>",
		Short: "Require a PHP extension in composer.json (composer require ext-<extension>:*)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, dir, _, err := extProject()
			if err != nil {
				return err
			}
			name := phpext.Normalize(args[0])
			path, argv, env, err := resolveTool(s, dir, "composer", []string{"require", "--no-interaction", "ext-" + name + ":*"})
			if err != nil {
				return err
			}
			c := exec.Command(path, argv[1:]...)
			c.Dir, c.Stdout, c.Stderr = dir, cmd.OutOrStdout(), cmd.ErrOrStderr()
			c.Env = envutil.Merge(os.Environ(), env)
			return c.Run()
		},
	})
	return cmd
}

// extProject is the project in the working directory (the site's root when it is in a site) and
// the PHP version it uses.
func extProject() (*state, string, string, error) {
	s, err := load()
	if err != nil {
		return nil, "", "", err
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, "", "", err
	}
	site := siteFor(s, dir)
	if site != nil {
		dir = site.Path
	}
	return s, dir, project.PHPFor(s.cfg, site, dir).Version, nil
}
