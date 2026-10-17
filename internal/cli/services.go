package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"flock/internal/dumps"
	"flock/internal/envutil"
	"flock/internal/php"
	"flock/internal/project"
)

func newXdebugCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "xdebug [on|off|breakpoints on|off]",
		Short: "Load Xdebug only for requests that ask for it (browser extension, XDEBUG_TRIGGER, PhpStorm breakpoints)",
		Long: `Flock runs a second PHP pool that loads Xdebug and sends it only the requests that want to
be debugged, so every other request stays fast:

  - requests from the Xdebug browser extensions (the XDEBUG_SESSION cookie)
  - URLs with ?XDEBUG_TRIGGER=1 (or XDEBUG_SESSION_START)
  - every request of a site with breakpoints in PhpStorm (.idea/workspace.xml in the project)

Listen for debug connections on port 9003 in your IDE. ` + "`flock debug`" + ` runs PHP on the command
line with Xdebug.

  flock xdebug                    show the settings
  flock xdebug on|off             Xdebug detection
  flock xdebug breakpoints on|off follow PhpStorm breakpoints`,
		Args:      cobra.MaximumNArgs(2),
		ValidArgs: []string{"on", "off", "breakpoints"},
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			if len(args) == 0 {
				cmd.Printf("xdebug detection is %s, PhpStorm breakpoints are %s\n",
					onOff(s.cfg.Xdebug), onOff(s.cfg.XdebugBreakpoints && s.cfg.Xdebug))
				return nil
			}

			setting, value := &s.cfg.Xdebug, args[0]
			if args[0] == "breakpoints" {
				if len(args) < 2 {
					return fmt.Errorf("expected `flock xdebug breakpoints on|off`")
				}
				setting, value = &s.cfg.XdebugBreakpoints, args[1]
			} else if len(args) > 1 {
				return fmt.Errorf("unexpected %q", args[1])
			}
			if value != "on" && value != "off" {
				return fmt.Errorf("expected on or off, got %q", value)
			}

			*setting = value == "on"
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}

			switch {
			case !s.cfg.Xdebug:
				cmd.Println("xdebug off — no request loads it; `flock debug` still works on the command line")
			case args[0] == "breakpoints" && s.cfg.XdebugBreakpoints:
				cmd.Println("PhpStorm breakpoints on — a site with breakpoints sends every request to Xdebug")
			case args[0] == "breakpoints":
				cmd.Println("PhpStorm breakpoints off — use the Xdebug browser extension or ?XDEBUG_TRIGGER=1")
			default:
				cmd.Println("xdebug on — listen on port 9003 in your IDE, then set a breakpoint in PhpStorm,")
				cmd.Println("use the Xdebug browser extension, or add ?XDEBUG_TRIGGER=1 to the URL")
			}
			return nil
		},
	}
	return cmd
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func newDebugCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "debug <php args...>",
		Short: "Run PHP with Xdebug, e.g. `flock debug artisan migrate` (listen on port 9003 in your IDE)",
		Example: `  flock debug artisan queue:work --once
  flock debug vendor/bin/phpunit
  flock debug composer install`,
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			tool := "php"
			if args[0] == "composer" {
				tool, args = "composer", args[1:]
			}
			path, argv, env, err := resolveTool(s, cwd, tool, args)
			if err != nil {
				return err
			}
			minor := project.PHPFor(s.cfg, siteFor(s, cwd), cwd).Version
			if !php.XdebugAvailable(s.root, minor) {
				return fmt.Errorf("PHP %s was installed without Xdebug — reinstall it from a newer build", minor)
			}
			return execReplace(path, argv, envutil.Merge(os.Environ(), append(env, php.DebugEnv(s.root, minor)...)))
		},
	}
}

func newDumpsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dumps [on|off|clear]",
		Short: "Send dump()/dd() output to Flock instead of the page or terminal, or list captured dumps",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			switch {
			case len(args) == 0:
				entries, err := dumps.List(s.root)
				if err != nil {
					return err
				}
				state := "off"
				if s.cfg.Dumps {
					state = "on"
				}
				cmd.Printf("dump capture is %s — %d dump(s)\n", state, len(entries))
				for i := len(entries) - 1; i >= 0; i-- {
					e := entries[i]
					where := ""
					if e.Source != nil {
						where = fmt.Sprintf("%s:%d", e.Source.File, e.Source.Line)
					}
					cmd.Printf("\n── %s %s\n%s\n", e.Time.Local().Format("15:04:05"), where, e.Text)
				}
				return nil
			case args[0] == "clear":
				return dumps.Clear(s.root)
			case args[0] == "on" || args[0] == "off":
				s.cfg.Dumps = args[0] == "on"
				if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
				cmd.Printf("dump capture %s\n", args[0])
				return nil
			}
			return fmt.Errorf("expected on, off or clear, got %q", args[0])
		},
	}
}
