package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/binaries"
	"flock/internal/config"
	"flock/internal/frankenphp"
	"flock/internal/project"
)

func newFrankenPHPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "frankenphp",
		Short: "Serve sites with FrankenPHP, optionally in Laravel Octane's worker mode",
		Long: `A site on FrankenPHP gets a FrankenPHP process of its own; Flock's web server keeps its domains
and certificates and passes the requests on. Requests that ask for Xdebug still go to the
regular PHP pool. FrankenPHP needs PHP 8.2 or newer, and is downloaded for a PHP version the
first time a site needs it. php.ini is the regular PHP's (see ` + "`flock php list`" + `).

  flock frankenphp                     show the site in this directory and the installed builds
  flock frankenphp on [site]           serve the site with FrankenPHP (--octane: worker mode)
  flock frankenphp off [site]          back to PHP-FPM (php-cgi on Windows)
  flock frankenphp restart [site]      restart the site's FrankenPHP, e.g. to load changed code in Octane
  flock frankenphp list                FrankenPHP builds, installed and available
  flock frankenphp install <php>       install or update the build for a PHP version (e.g. 8.4)
  flock frankenphp uninstall <php>     remove it`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if site, err := siteArg(s, nil); err == nil {
				st, err := s.client().Status(cmd.Context())
				if err != nil {
					return err
				}
				for _, x := range st.Sites {
					if x.Name == site.Name {
						printSiteServer(cmd, x)
					}
				}
			}
			return listFrankenPHP(cmd, s)
		},
	}

	var octane, classic bool
	on := &cobra.Command{
		Use:   "on [site]",
		Short: "Serve a site with FrankenPHP (--octane for Laravel Octane's worker mode, --classic to leave it)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if octane && classic {
				return errors.New("give either --octane or --classic")
			}
			if binaries.Platform() == "" {
				return errors.New("FrankenPHP isn't available for this platform")
			}
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			if minor := project.PHPFor(s.cfg, &site, site.Path).Version; !frankenphp.Supports(minor) {
				return fmt.Errorf("FrankenPHP needs PHP %s or newer; %s uses %s (`flock isolate php 8.4`)", frankenphp.MinimumMinor, site.Name, minor)
			}
			worker := site.Worker
			if !site.FrankenPHP() && !octane {
				worker = "" // switching over starts in classic mode unless asked otherwise
			}
			switch {
			case octane:
				worker = config.WorkerOctane
				if !frankenphp.HasOctane(site.Path) {
					return errors.New("Laravel Octane isn't installed in " + site.Path + " — run `composer require laravel/octane` first")
				}
			case classic:
				worker = ""
			}
			if err := s.cfg.SetServer(&site, config.ServerFrankenPHP, worker); err != nil {
				return err
			}
			s.cfg.Upsert(site)
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return waitForFrankenPHP(ctx, cmd, s, site.Name)
		},
	}
	on.Flags().BoolVar(&octane, "octane", false, "run Laravel Octane's workers (the app stays booted between requests)")
	on.Flags().BoolVar(&classic, "classic", false, "boot the app for every request, as PHP-FPM does")

	off := &cobra.Command{
		Use:   "off [site]",
		Short: "Serve a site with PHP-FPM again (php-cgi on Windows)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			if err := s.cfg.SetServer(&site, config.ServerFastCGI, site.Worker); err != nil {
				return err
			}
			s.cfg.Upsert(site)
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("%s is served with %s\n", site.Name, fastCGIName())
			return nil
		},
	}

	restart := &cobra.Command{
		Use:   "restart [site]",
		Short: "Restart a site's FrankenPHP (Octane's workers load changed code)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			st, err := s.client().RestartSite(cmd.Context(), site.Name)
			if err != nil {
				return err
			}
			for _, x := range st.Sites {
				if x.Name == site.Name {
					printSiteServer(cmd, x)
				}
			}
			return nil
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List FrankenPHP builds, installed and available",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			return listFrankenPHP(cmd, s)
		},
	}

	var from string
	install := &cobra.Command{
		Use:   "install <php-version> | --from <dir|archive>",
		Short: "Install or update FrankenPHP for a PHP version, or a local package with --from",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			switch {
			case from != "" && len(args) > 0:
				return errors.New("give either a PHP version or --from, not both")
			case from == "" && len(args) == 0:
				return errors.New("which PHP version? e.g. `flock frankenphp install 8.4`")
			case from != "":
				// On Windows a running build is locked: switch its sites off first (the daemon replaces
				// builds it downloads itself after stopping them).
				minor, info, err := frankenphp.InstallFrom(s.root, from, 0, nil)
				if err != nil {
					return err
				}
				if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
				cmd.Printf("installed FrankenPHP %s for PHP %s (%s)\n", info.Version, minor, info.PHP)
				return nil
			}

			minor := args[0]
			if !frankenphp.Supports(minor) {
				return fmt.Errorf("give a PHP version from %s on, like 8.4", frankenphp.MinimumMinor)
			}
			if _, err := s.client().InstallFrankenPHP(cmd.Context(), minor); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return followFrankenPHPInstall(ctx, cmd, s, minor)
		},
	}
	install.Flags().StringVar(&from, "from", "", "a FrankenPHP package: directory, .tar.gz or .zip")

	uninstall := &cobra.Command{
		Use:   "uninstall <php-version>",
		Short: "Remove FrankenPHP for a PHP version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if _, err := s.client().UninstallFrankenPHP(cmd.Context(), args[0]); err != nil {
				return err
			}
			cmd.Printf("removed FrankenPHP for PHP %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(on, off, restart, list, install, uninstall)
	return cmd
}

func fastCGIName() string {
	if binaries.Platform() == "windows-x64" {
		return "php-cgi"
	}
	return "PHP-FPM"
}

func printSiteServer(cmd *cobra.Command, site api.SiteStatus) {
	if site.Server != config.ServerFrankenPHP || site.FrankenPHP == nil {
		cmd.Printf("%s is served with %s\n", site.Name, fastCGIName())
		return
	}
	mode := "classic mode"
	if site.Worker == config.WorkerOctane {
		mode = "Laravel Octane worker mode"
	}
	fp := site.FrankenPHP
	switch fp.State {
	case api.FrankenPHPRunning:
		cmd.Printf("%s runs on FrankenPHP %s (PHP %s, %s) — %s, log: `flock logs %s`\n", site.Name, fp.Version, fp.PHPVersion, mode, site.URL, fp.Log)
	case api.FrankenPHPInstalling:
		cmd.Printf("%s: FrankenPHP for PHP %s is downloading; until then %s serves it\n", site.Name, site.PHP, fastCGIName())
	default:
		cmd.Printf("%s is set to FrankenPHP (%s), but %s serves it: %s\n", site.Name, mode, fastCGIName(), fp.Problem)
	}
}

// waitForFrankenPHP follows a site that was switched to FrankenPHP until it runs or fails.
func waitForFrankenPHP(ctx context.Context, cmd *cobra.Command, s *state, name string) error {
	shown := ""
	for {
		st, err := s.client().Status(ctx)
		if errors.Is(err, api.ErrDaemonNotRunning) {
			return nil // commit said so
		}
		if err != nil {
			return err
		}
		var site *api.SiteStatus
		for i := range st.Sites {
			if st.Sites[i].Name == name {
				site = &st.Sites[i]
			}
		}
		if site == nil || site.FrankenPHP == nil {
			return nil
		}
		fp := site.FrankenPHP
		if fp.State != api.FrankenPHPInstalling {
			printSiteServer(cmd, *site)
			if fp.State != api.FrankenPHPRunning {
				return errors.New(fp.Problem)
			}
			return nil
		}
		line := "downloading FrankenPHP for PHP " + site.PHP
		if fp.Progress >= 0 {
			line += fmt.Sprintf(" (%d%%)", int(fp.Progress*100))
		}
		if line != shown {
			cmd.Println(line)
			shown = line
		}
		select {
		case <-ctx.Done():
			cmd.Println("still downloading in the background — `flock frankenphp` shows how far it is")
			return nil
		case <-time.After(time.Second):
		}
	}
}

func followFrankenPHPInstall(ctx context.Context, cmd *cobra.Command, s *state, minor string) error {
	shown := ""
	for {
		st, err := s.client().FrankenPHP(ctx)
		if err != nil {
			return err
		}
		for _, b := range st.Builds {
			if b.PHP != minor {
				continue
			}
			if b.Job == nil {
				cmd.Printf("FrankenPHP %s for PHP %s (%s) is installed\n", b.Installed, minor, b.PHPVersion)
				return nil
			}
			if b.Job.State == "failed" {
				return fmt.Errorf("installing FrankenPHP for PHP %s failed: %s", minor, b.Job.Error)
			}
			line := b.Job.Phase
			if b.Job.Progress >= 0 {
				line += fmt.Sprintf(" (%d%%)", int(b.Job.Progress*100))
			}
			if line != shown {
				cmd.Println(line)
				shown = line
			}
		}
		select {
		case <-ctx.Done():
			cmd.Println("still installing in the background — `flock frankenphp list` shows how far it is")
			return nil
		case <-time.After(time.Second):
		}
	}
}

func listFrankenPHP(cmd *cobra.Command, s *state) error {
	st, err := s.client().FrankenPHP(cmd.Context())
	if err != nil {
		return err
	}
	if !st.Available {
		cmd.Println("FrankenPHP isn't available for this platform")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PHP\tFRANKENPHP\tAVAILABLE\tSTATE")
	for _, b := range st.Builds {
		installed, state := b.Installed, ""
		if installed == "" {
			installed = "—"
		} else {
			installed += " (PHP " + b.PHPVersion + ")"
		}
		switch {
		case b.Job != nil && b.Job.State == "failed":
			state = "install failed: " + b.Job.Error
		case b.Job != nil:
			state = b.Job.Phase
		case b.Update:
			state = "update available — `flock frankenphp install " + b.PHP + "`"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.PHP, installed, b.Latest, state)
	}
	if st.CheckError != "" {
		fmt.Fprintf(w, "\nthe package manifest couldn't be read: %s\n", st.CheckError)
	}
	return w.Flush()
}
