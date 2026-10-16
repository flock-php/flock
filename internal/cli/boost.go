package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"flock/internal/boost"
)

func newBoostCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "boost",
		Short: "Add Flock's guideline to this project's Laravel Boost guidelines (CLAUDE.md, AGENTS.md, ...)",
		Long: "Writes .ai/guidelines/flock.md, which tells AI agents that Flock serves the app and picks its PHP\n" +
			"version, and runs `php artisan boost:update` so Boost adds it to the agents' guideline files.\n" +
			"Commit the file like the rest of .ai/guidelines.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			if site := siteFor(s, dir); site != nil {
				dir = site.Path
			}

			switch boost.State(dir) {
			case boost.None:
				cmd.Println("This project doesn't use Laravel Boost, so there is nothing to add: agents get Flock's guidance from")
				cmd.Println("its MCP server (`flock mcp install` registers it with your AI clients).")
				return nil
			case boost.Custom:
				return errors.New(boost.ErrCustom.Error() + ": " + boost.File(dir))
			}

			self, err := os.Executable()
			if err != nil {
				return err
			}
			cmd.Printf("wrote %s\n", boost.File(dir))
			cmd.PrintErrln("running php artisan boost:update…")
			if _, err := boost.Apply(cmd.Context(), self, dir); err != nil {
				return err
			}
			cmd.Println("Boost's guideline files now include Flock's guideline.")
			return nil
		},
	}
}
