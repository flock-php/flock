package cli

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"flock/internal/api"
	"flock/internal/mcpclients"
	"flock/internal/mcpserver"
	"flock/internal/paths"
)

func newMCPCommand() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run Flock's MCP server, which AI agents (Claude Code, Cursor, ...) start to work with Flock",
		Long: "Runs Flock's MCP server over stdin/stdout. AI clients start it themselves once it is registered with them.\n\n" +
			"It finds the project from the folder the client starts it in; --site (or FLOCK_SITE) names a site or\n" +
			"project folder instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Started by hand rather than by a client: there is nobody to talk JSON-RPC with.
			if term.IsTerminal(int(os.Stdin.Fd())) {
				cmd.Println("This is Flock's MCP server: AI clients like Claude Code and Cursor start it themselves.")
				cmd.Println("Register it with them with `flock mcp install`, or in the Flock app (Settings → AI).")
				return nil
			}

			root, err := paths.Root()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			return mcpserver.Run(cmd.Context(), mcpserver.Options{
				Version: Version,
				Root:    root,
				Daemon:  api.NewClient(paths.Socket(root)),
				Dir:     cwd,
				Site:    cmp.Or(site, os.Getenv("FLOCK_SITE")),
			})
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "the site (name or project folder) to work with, instead of the current folder's")

	cmd.AddCommand(newMCPInstallCommand(), newMCPUninstallCommand(), newMCPStatusCommand())
	return cmd
}

func mcpEnv() (mcpclients.Env, error) {
	root, err := paths.Root()
	if err != nil {
		return mcpclients.Env{}, err
	}
	return mcpclients.DefaultEnv(root)
}

var clientsFlagHelp = "the client: " + strings.Join(mcpclients.IDs(), ", ") + " (default: every one that is installed)"

func newMCPInstallCommand() *cobra.Command {
	var only []string
	cmd := &cobra.Command{
		Use:   "install [--client <client>]",
		Short: "Register Flock's MCP server with your AI clients (once for all projects)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := mcpEnv()
			if err != nil {
				return err
			}
			targets, err := mcpTargets(env, only, func(c mcpclients.Client) bool { return c.State == mcpclients.Available })
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				cmd.Println("No AI client to register Flock with (supported: Claude Code, Cursor, VS Code, Windsurf and Codex).")
				cmd.Println("For another client, add this to its MCP configuration:")
				cmd.Println(mcpclients.Snippet(env))
				return nil
			}
			if len(only) == 0 && interactive() {
				names := make([]string, len(targets))
				for i, c := range targets {
					names[i] = c.Name
				}
				if !confirm("Register Flock with "+strings.Join(names, ", ")+"?", true) {
					return nil
				}
			}

			var failed error
			for _, c := range targets {
				if err := mcpclients.Register(env, c.ID); err != nil {
					cmd.PrintErrf("%s: %v\n", c.Name, err)
					failed = errors.New("some clients could not be set up")
					continue
				}
				cmd.Printf("%s: registered (restart it, or reload its MCP servers, to pick Flock up)\n", c.Name)
			}
			return failed
		},
	}
	cmd.Flags().StringSliceVar(&only, "client", nil, clientsFlagHelp)
	return cmd
}

func newMCPUninstallCommand() *cobra.Command {
	var only []string
	cmd := &cobra.Command{
		Use:   "uninstall [--client <client>]",
		Short: "Remove Flock's MCP server from your AI clients",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := mcpEnv()
			if err != nil {
				return err
			}
			targets, err := mcpTargets(env, only, func(c mcpclients.Client) bool { return c.State == mcpclients.Registered })
			if err != nil {
				return err
			}
			for _, c := range targets {
				if err := mcpclients.Unregister(env, c.ID); err != nil {
					return fmt.Errorf("%s: %w", c.Name, err)
				}
				cmd.Printf("%s: removed\n", c.Name)
			}
			if len(targets) == 0 {
				cmd.Println("Flock isn't registered with any AI client.")
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&only, "client", nil, clientsFlagHelp)
	return cmd
}

func newMCPStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which AI clients Flock's MCP server is registered with",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := mcpEnv()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "CLIENT\tID\tSTATE")
			for _, c := range mcpclients.List(env) {
				state := map[string]string{
					mcpclients.Missing: "not installed", mcpclients.Available: "installed, Flock not registered", mcpclients.Registered: "registered",
				}[c.State]
				if c.Problem != "" {
					state += " — " + c.Problem
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, c.ID, state)
			}
			_ = w.Flush()
			cmd.Printf("\nThe server: %s %s\n", env.Command, strings.Join(env.Args, " "))
			return nil
		},
	}
}

// mcpTargets are the clients named with --client, or else every client that matches.
func mcpTargets(env mcpclients.Env, only []string, match func(mcpclients.Client) bool) ([]mcpclients.Client, error) {
	if len(only) > 0 {
		var out []mcpclients.Client
		for _, id := range only {
			c, err := mcpclients.Get(env, id)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, nil
	}
	var out []mcpclients.Client
	for _, c := range mcpclients.List(env) {
		if match(c) {
			out = append(out, c)
		}
	}
	return out, nil
}
