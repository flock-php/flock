package cli

import (
	"github.com/spf13/cobra"

	"flock/internal/config"
)

func newTypeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "type <php|node> [site]",
		Short: "Serve a site as a PHP site or as a Node.js app whose dev server Flock runs",
		Long: "Flock tells a project's type from its files: a package.json with a dev script and nothing of PHP's " +
			"makes a Node.js app. This sets it instead.\n\n" +
			"A Node.js app's dev server (`npm run dev`, with the project's Node.js version) starts when the site " +
			"is first visited, on a port Flock picks.",
		Example:   "  flock type node\n  flock type php blog",
		Args:      cobra.RangeArgs(1, 2),
		ValidArgs: []string{config.TypePHP, config.TypeNode},
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args[1:])
			if err != nil {
				return err
			}
			if err := s.cfg.SetType(&site, args[0]); err != nil {
				return err
			}
			if err := s.cfg.Upsert(site); err != nil {
				return err
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("%s is served as a %s site\n", site.Name, config.TypeName(site.Kind()))
			return nil
		},
	}
}
