package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"flock/internal/config"
)

// siteArg resolves the site a command targets: the named one, or the one containing the working directory.
func siteArg(s *state, args []string) (config.Site, error) {
	if len(args) > 0 {
		site, ok := s.cfg.FindSite(args[0])
		if !ok {
			return config.Site{}, fmt.Errorf("no site named %q", args[0])
		}
		return site, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return config.Site{}, err
	}
	site, ok := s.cfg.SiteForDir(cwd)
	if !ok {
		return config.Site{}, fmt.Errorf("%s is not a Flock site — run `flock link` first", cwd)
	}
	return site, nil
}

func newLinkCommand() *cobra.Command {
	var phpVersion string
	var secure bool

	cmd := &cobra.Command{
		Use:   "link [name]",
		Short: "Serve the current directory as <name>.test (defaults to the directory name)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			name := config.SiteName(filepath.Base(cwd))
			if len(args) > 0 {
				name = config.SiteName(args[0])
			}

			site := config.Site{Name: name, Path: cwd, PHP: phpVersion, Secured: secure}
			if existing, ok := s.cfg.FindSite(name); ok && !existing.Parked {
				site.Node = existing.Node
				if phpVersion == "" {
					site.PHP = existing.PHP
				}
				if !cmd.Flags().Changed("secure") {
					site.Secured = existing.Secured
				}
			}
			s.cfg.Upsert(site)

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}

			scheme := "http"
			if site.Secured {
				scheme = "https"
			}
			cmd.Printf("linked %s → %s://%s\n", cwd, scheme, s.cfg.Domain(site))
			return nil
		},
	}

	cmd.Flags().StringVar(&phpVersion, "php", "", "PHP version for this site (default: the global default)")
	cmd.Flags().BoolVar(&secure, "secure", false, "serve over HTTPS")

	return cmd
}

func newUnlinkCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink [name]",
		Short: "Stop serving a linked site",
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
			if !s.cfg.Remove(site.Name) {
				return fmt.Errorf("%s is served from a parked directory; use `flock forget` on the parent instead", site.Name)
			}

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("unlinked %s\n", site.Name)
			return nil
		},
	}
}

func newParkCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "park [dir]",
		Short: "Serve every subdirectory of dir (default: current directory) as <subdir>.test",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			dir, err := dirArg(args)
			if err != nil {
				return err
			}
			if !slices.Contains(s.cfg.Parked, dir) {
				s.cfg.Parked = append(s.cfg.Parked, dir)
			}

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("parked %s\n", dir)
			return nil
		},
	}
}

func newForgetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "forget [dir]",
		Short: "Stop serving a parked directory",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			dir, err := dirArg(args)
			if err != nil {
				return err
			}
			i := slices.Index(s.cfg.Parked, dir)
			if i < 0 {
				return fmt.Errorf("%s is not parked", dir)
			}
			s.cfg.Parked = slices.Delete(s.cfg.Parked, i, i+1)

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("forgot %s\n", dir)
			return nil
		},
	}
}

func dirArg(args []string) (string, error) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	return filepath.Abs(dir)
}

// newSecureCommand builds `secure` (on=true) and `unsecure` (on=false).
func newSecureCommand(on bool) *cobra.Command {
	use, short := "secure [name]", "Serve a site over HTTPS with a locally trusted certificate"
	if !on {
		use, short = "unsecure [name]", "Serve a site over plain HTTP"
	}

	return &cobra.Command{
		Use:   use,
		Short: short,
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
			// Parked sites become linked so the setting can be stored.
			site.Secured = on
			site.Parked = false
			s.cfg.Upsert(site)

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}

			scheme := "http"
			if on {
				scheme = "https"
			}
			cmd.Printf("%s → %s://%s\n", site.Name, scheme, s.cfg.Domain(site))
			return nil
		},
	}
}

func newIsolateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "isolate <php-version> [name]",
		Short: "Pin a site to a PHP version (e.g. `flock isolate 8.4`)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			site, err := siteArg(s, args[1:])
			if err != nil {
				return err
			}
			site.PHP = args[0]
			site.Parked = false
			s.cfg.Upsert(site)

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("%s now uses PHP %s\n", site.Name, args[0])
			return nil
		},
	}
}
