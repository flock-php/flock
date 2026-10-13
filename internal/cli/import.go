package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"flock/internal/config"
)

type lerdSites struct {
	Sites []struct {
		Name        string   `yaml:"name"`
		Domains     []string `yaml:"domains"`
		Path        string   `yaml:"path"`
		PHPVersion  string   `yaml:"php_version"`
		NodeVersion string   `yaml:"node_version"`
		Secured     bool     `yaml:"secured"`
	} `yaml:"sites"`
}

type lerdConfig struct {
	PHP struct {
		DefaultVersion string `yaml:"default_version"`
	} `yaml:"php"`
	Node struct {
		DefaultVersion string `yaml:"default_version"`
	} `yaml:"node"`
	Parked []string `yaml:"parked_directories"`
}

func newImportCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "import", Short: "Import sites and settings from another tool"}

	cmd.AddCommand(&cobra.Command{
		Use:   "lerd",
		Short: "Import parked directories, linked sites and default versions from Lerd",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			s, err := load()
			if err != nil {
				return err
			}

			var lc lerdConfig
			if data, err := os.ReadFile(filepath.Join(home, ".config", "lerd", "config.yaml")); err == nil {
				if err := yaml.Unmarshal(data, &lc); err != nil {
					return fmt.Errorf("lerd config: %w", err)
				}
			}
			if lc.PHP.DefaultVersion != "" {
				s.cfg.DefaultPHP = lc.PHP.DefaultVersion
			}
			if lc.Node.DefaultVersion != "" {
				s.cfg.DefaultNode = lc.Node.DefaultVersion
			}
			for _, dir := range lc.Parked {
				if _, err := os.Stat(dir); err == nil && !slices.Contains(s.cfg.Parked, dir) {
					s.cfg.Parked = append(s.cfg.Parked, dir)
					cmd.Printf("parked %s\n", dir)
				}
			}

			data, err := os.ReadFile(filepath.Join(home, ".local", "share", "lerd", "sites.yaml"))
			if err != nil {
				return fmt.Errorf("lerd sites: %w", err)
			}
			var ls lerdSites
			if err := yaml.Unmarshal(data, &ls); err != nil {
				return fmt.Errorf("lerd sites: %w", err)
			}

			for _, site := range ls.Sites {
				node := site.NodeVersion
				if node == s.cfg.DefaultNode {
					node = ""
				}
				php := site.PHPVersion
				if php == s.cfg.DefaultPHP {
					php = ""
				}
				s.cfg.Upsert(config.Site{Name: config.SiteName(site.Name), Path: site.Path, PHP: php, Node: node, Secured: site.Secured})
				cmd.Printf("linked %s → %s\n", site.Name, site.Path)
			}

			return s.commit(cmd.Context(), cmd.ErrOrStderr())
		},
	})

	return cmd
}
