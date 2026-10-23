package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"flock/internal/api"
	"flock/internal/share"
)

func newShareCommand() *cobra.Command {
	var allow []string
	var stop, debug bool

	cmd := &cobra.Command{
		Use:   "share [name]",
		Short: "Share a site at a temporary public URL (a Cloudflare quick tunnel) until you press Ctrl+C",
		Long: "Share a site at a temporary https://<words>.trycloudflare.com address, to show work in progress.\n" +
			"It's for previews, never for production: the share ends when you press Ctrl+C, after 8 hours, or\n" +
			"when Flock stops. Visitors get the built assets (Flock runs `npm run build` when they're out of\n" +
			"date) and never debug pages, dumps, Telescope or the other dev tools.\n\n" +
			"With --allow, Cloudflare asks visitors for a one-time code sent to an allowed address first.\n" +
			"With --debug, visitors get the app's error pages with stack traces, for showing a bug to someone you trust.",
		Example: "  flock share\n  flock share --allow colleague@acme.com --allow '*@client.com'\n  flock share --debug --allow colleague@acme.com",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			ctx, client, out := cmd.Context(), s.client(), cmd.OutOrStdout()

			if stop {
				if err := client.StopShare(ctx, site.Name); err != nil {
					return err
				}
				fmt.Fprintf(out, "stopped sharing %s\n", site.Name)
				return nil
			}
			if _, err := share.NormalizeAllow(allow); err != nil {
				return err
			}

			// Stop on Ctrl+C or when the terminal closes, also while still starting.
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer signal.Stop(signals)

			if _, err := client.StartShare(ctx, site.Name, allow, debug); err != nil {
				return err
			}
			return followShare(ctx, client, site.Name, out, signals)
		},
	}

	cmd.Flags().StringArrayVar(&allow, "allow", nil, "only let in this email address, or everyone at a domain with *@domain (repeatable)")
	cmd.Flags().BoolVar(&stop, "stop", false, "stop sharing the site (e.g. a share started in the app)")
	cmd.Flags().BoolVar(&debug, "debug", false, "turn the app's debug mode on for visitors: they see stack traces")
	return cmd
}

// followShare shows a share's progress and address, and stops it when the user presses Ctrl+C.
func followShare(ctx context.Context, client *api.Client, site string, out io.Writer, signals <-chan os.Signal) error {
	stopShare := func(prefix string) error {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.StopShare(stopCtx, site); err != nil {
			return err
		}
		fmt.Fprintf(out, "%sstopped sharing %s\n", prefix, site)
		return nil
	}

	var keys <-chan byte
	var restore func()
	defer func() {
		if restore != nil {
			restore()
		}
	}()
	// nl is the line ending: the terminal doesn't return the carriage by itself in raw mode.
	nl := "\n"

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	shown, building, rebuilding := false, false, false
	for {
		st, err := client.Share(ctx, site)
		if err != nil {
			if shown {
				fmt.Fprint(out, "the share ended"+nl)
				return nil
			}
			return err
		}

		switch {
		case st.State == share.Failed:
			return errors.New(st.Error)
		case st.State == share.Building && !building:
			building = true
			fmt.Fprintln(out, "building the assets (npm run build)…")
		case st.State == share.Running && !shown:
			shown = true
			showShare(out, st)
			if interactive() {
				keys, restore = readKeys()
				if restore != nil {
					nl = "\r\n"
				}
			}
		case shown && st.Rebuilding != rebuilding:
			rebuilding = st.Rebuilding
			switch {
			case rebuilding:
				fmt.Fprint(out, "rebuilding the assets…"+nl)
			case st.RebuildError != "":
				fmt.Fprint(out, st.RebuildError+nl)
			default:
				fmt.Fprint(out, "assets rebuilt — reload the page to see the changes"+nl)
			}
		}

		select {
		case <-signals:
			return stopShare(nl)
		case k, ok := <-keys:
			switch {
			case !ok:
				keys = nil
			case k == 3 || k == 4: // Ctrl+C, Ctrl+D in raw mode
				if restore != nil {
					restore()
					restore = nil
				}
				return stopShare("")
			case (k == 'r' || k == 'R') && st.CanRebuild:
				if _, err := client.RebuildShare(ctx, site); err != nil {
					fmt.Fprint(out, err.Error()+nl)
				}
			}
		case <-tick.C:
		}
	}
}

func showShare(out io.Writer, st api.ShareStatus) {
	copied := ""
	if copyToClipboard(st.URL) == nil {
		copied = "  (copied to clipboard)"
	}
	fmt.Fprintf(out, "\nsharing %s\n\n  %s%s\n", st.Site, st.URL, copied)
	if len(st.Allow) > 0 {
		fmt.Fprintf(out, "  only for %s (Cloudflare emails them a code)\n", strings.Join(st.Allow, ", "))
	}
	if st.Debug {
		fmt.Fprintln(out, "  debug mode is on: visitors see stack traces")
	}
	if st.Warning != "" {
		fmt.Fprintf(out, "\nwarning: %s\n", st.Warning)
	}
	if qr, err := share.QRText(st.URL); err == nil && term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintf(out, "\n%s", qr)
	}
	hints := []string{}
	if st.ExpiresAt != nil {
		hints = append(hints, "ends at "+st.ExpiresAt.Local().Format(time.Kitchen))
	}
	if st.CanRebuild {
		hints = append(hints, "r rebuild assets")
	}
	hints = append(hints, "Ctrl+C stop")
	fmt.Fprintf(out, "\n  %s\n\n", strings.Join(hints, " · "))
}

// readKeys puts the terminal in raw mode and sends what is typed; restore puts it back. Both are
// nil when stdin isn't a terminal that can be put in raw mode.
func readKeys() (<-chan byte, func()) {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, nil
	}
	keys := make(chan byte)
	go func() {
		buf := make([]byte, 1)
		for {
			if n, err := os.Stdin.Read(buf); err != nil {
				close(keys)
				return
			} else if n == 1 {
				keys <- buf[0]
			}
		}
	}()
	return keys, func() { _ = term.Restore(fd, old) }
}

// copyToClipboard puts text on the clipboard with the system's own tool.
func copyToClipboard(text string) error {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"clip.exe"}}
	default:
		candidates = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	}
	err := errors.New("no clipboard tool found")
	for _, c := range candidates {
		path, lookErr := exec.LookPath(c[0])
		if lookErr != nil {
			continue
		}
		cmd := exec.Command(path, c[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err = cmd.Run(); err == nil {
			return nil
		}
	}
	return err
}
