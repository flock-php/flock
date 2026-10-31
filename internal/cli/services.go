package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/capture"
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
		Use:   "dumps [on|off|clear|page on|page off]",
		Short: "Send dump()/dd() output (and queries, logs, ... — see flock events) to Flock instead of the page or terminal, or list captured dumps",
		Long: `Capture sends dump() and dd() output to Flock instead of the page or terminal. On PHP versions
with Flock's agent it also records queries, logs, views, jobs and requests (see flock events);
other versions capture dumps only.

  flock dumps                list captured dumps
  flock dumps on|off         turn capture on or off
  flock dumps clear          delete captured dumps and events
  flock dumps page on|off    also show dump() output in the page or terminal (dd() always shows there)`,
		Args: cobra.MaximumNArgs(2),
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
				if s.cfg.Dumps {
					printCaptureNotes(cmd, s)
				}
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
				if err := capture.Clear(s.root); err != nil {
					return err
				}
				return dumps.Clear(s.root)
			case args[0] == "page" && len(args) == 2 && (args[1] == "on" || args[1] == "off"):
				s.cfg.DumpsInPage = args[1] == "on"
				if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
				if s.cfg.DumpsInPage {
					cmd.Println("dump() output also shows in the page or terminal")
				} else {
					cmd.Println("dump() output goes to Flock only (dd() still shows in the page)")
				}
				return nil
			case len(args) == 2:
				return fmt.Errorf("expected page on or page off, got %q", strings.Join(args, " "))
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

// printCaptureNotes says which kinds are switched off and which PHP versions only capture dumps.
func printCaptureNotes(cmd *cobra.Command, s *state) {
	if len(s.cfg.CaptureOff) > 0 {
		cmd.Printf("not captured: %s (flock events on <kind>)\n", strings.Join(s.cfg.CaptureOff, ", "))
	}
	installed, _ := php.Installed(s.root)
	var only []string
	for _, minor := range installed {
		if !capture.Supported(s.root, minor) {
			only = append(only, minor)
		}
	}
	if len(only) > 0 {
		cmd.Printf("PHP %s: dumps only — these builds don't have the %s extension yet (update them with flock php install)\n",
			strings.Join(only, ", "), capture.Extension)
	}
}

func newEventsCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "events [kind...|clear|on <kind...>|off <kind...>]",
		Short: "List the queries, logs, views, jobs and requests Flock captured, or choose which it captures",
		Long: `While capture is on (flock dumps on), Flock's agent records what PHP apps do. Kinds: query, log,
view, job and request. Queue workers only report what happens inside their jobs.

  flock events               the latest events, oldest first
  flock events query log     only these kinds
  flock events clear         delete captured events
  flock events off view      stop capturing views (on: start again)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			if len(args) > 0 && (args[0] == "on" || args[0] == "off") {
				kinds := args[1:]
				if len(kinds) == 0 {
					return fmt.Errorf("name the kinds to switch %s: %s", args[0], strings.Join(capture.Kinds, ", "))
				}
				if err := capture.Valid(kinds); err != nil {
					return err
				}
				off := slices.DeleteFunc(slices.Clone(s.cfg.CaptureOff), func(k string) bool { return slices.Contains(kinds, k) })
				if args[0] == "off" {
					off = append(off, kinds...)
				}
				s.cfg.CaptureOff = nil
				for _, k := range capture.Kinds {
					if slices.Contains(off, k) {
						s.cfg.CaptureOff = append(s.cfg.CaptureOff, k)
					}
				}
				if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
				cmd.Printf("capturing %s %s\n", strings.Join(kinds, ", "), args[0])
				return nil
			}
			if len(args) == 1 && args[0] == "clear" {
				return capture.Clear(s.root)
			}
			if err := capture.Valid(args); err != nil {
				return err
			}

			events, err := capture.List(s.root, limit, args...)
			if err != nil {
				return err
			}
			if !s.cfg.Dumps {
				cmd.Println("capture is off — turn it on with flock dumps on")
			} else {
				printCaptureNotes(cmd, s)
			}
			group := ""
			for i := len(events) - 1; i >= 0; i-- {
				e := events[i]
				if e.Group != group {
					group = e.Group
					cmd.Printf("\n── %s\n", eventOrigin(e))
				}
				cmd.Printf("%s %-7s %s\n", e.Time.Local().Format("15:04:05"), e.Kind, eventSummary(e))
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "how many events to show")
	return cmd
}

func eventOrigin(e capture.Event) string {
	switch {
	case e.Request != nil:
		return e.Request.Method + " " + e.Request.URI
	case e.CommandLine != nil:
		return *e.CommandLine
	}
	return "?"
}

func eventSummary(e capture.Event) string {
	ms := ""
	if e.DurationMS > 0 {
		ms = fmt.Sprintf(" (%.2f ms)", e.DurationMS)
	}
	switch e.Kind {
	case "query":
		sql := e.SQL
		if len(e.Bindings) > 0 {
			b, _ := json.Marshal(e.Bindings)
			sql += " " + string(b)
		}
		return sql + ms
	case "log":
		return e.Level + ": " + e.Message
	case "view":
		return e.View
	case "job":
		s := e.State + " " + e.Job + ms
		if e.Exception != "" {
			s += " — " + e.Exception
		}
		return s
	case "request":
		return fmt.Sprintf("%d%s", e.StatusCode, ms)
	}
	return ""
}
