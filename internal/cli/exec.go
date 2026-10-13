package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/spf13/cobra"

	"flock/internal/node"
	"flock/internal/php"
)

// tools are the commands Flock puts on PATH as shims.
var tools = []string{"php", "composer", "node", "npm", "npx"}

func newExecCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "exec <tool> [args...]",
		Short:              "Run php/composer/node/npm/npx in the version the current project uses (what the shims call)",
		Hidden:             true,
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

			path, argv, env, err := resolveTool(s, cwd, args[0], args[1:])
			if err != nil {
				return err
			}

			return execReplace(path, argv, append(os.Environ(), env...))
		},
	}
}

// resolveTool decides which binary a shim runs, with which arguments and extra environment.
func resolveTool(s *state, cwd, tool string, args []string) (path string, argv, env []string, err error) {
	switch tool {
	case "php", "composer":
		minor := s.cfg.DefaultPHP
		if site, ok := s.cfg.SiteForDir(cwd); ok {
			minor = s.cfg.PHPFor(site)
		}

		installed, _ := php.Installed(s.root)
		if !slices.Contains(installed, minor) {
			return "", nil, nil, fmt.Errorf("PHP %s is not installed (installed: %v)", minor, installed)
		}

		// argv[0] must be the absolute binary path: PHP derives PHP_BINARY from it, and a bare
		// "php" would be looked up on PATH, so child processes (artisan test, queue workers) could
		// end up running another PHP installation.
		path = php.Binary(s.root, minor)
		env = php.Env(s.root, minor)
		if tool == "composer" {
			return path, append([]string{path, php.ComposerPhar(s.root)}, args...), env, nil
		}
		return path, append([]string{path}, args...), env, nil

	case "node", "npm", "npx":
		spec := node.DetectSpec(cwd)
		if spec == "" {
			spec = s.cfg.DefaultNode
			if site, ok := s.cfg.SiteForDir(cwd); ok {
				spec = s.cfg.NodeFor(site)
			}
		}

		binDir, _, err := node.Resolve(s.root, spec)
		if err != nil {
			return "", nil, nil, err
		}

		name := tool
		if runtime.GOOS == "windows" && tool != "node" {
			name += ".cmd"
		} else if runtime.GOOS == "windows" {
			name += ".exe"
		}

		// npm and npx are scripts that look `node` up on PATH.
		env = []string{
			"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
			"NODE_EXTRA_CA_CERTS=" + filepath.Join(s.root, "certs", "ca.crt"),
		}
		path = filepath.Join(binDir, name)
		return path, append([]string{path}, args...), env, nil
	}

	return "", nil, nil, fmt.Errorf("unknown tool %q (supported: %v)", tool, tools)
}
