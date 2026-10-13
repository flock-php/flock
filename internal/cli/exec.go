package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"

	"github.com/spf13/cobra"

	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/dumps"
	"flock/internal/envutil"
	"flock/internal/node"
	"flock/internal/php"
	"flock/internal/postgres"
	"flock/internal/valkey"
)

// tools are the commands Flock puts on PATH as shims.
var tools = append([]string{"php", "composer", "node", "npm", "npx", "redis-cli", "valkey-cli"}, postgres.ClientTools...)

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

			return execReplace(path, argv, envutil.Merge(os.Environ(), env))
		},
	}
}

// viteTLSEnv hands a secured site's certificate to laravel-vite-plugin, which reads
// VITE_DEV_SERVER_KEY/CERT from the environment, so `npm run dev` serves over HTTPS
// without any vite.config.js changes. The host comes from APP_URL in the project's .env.
func viteTLSEnv(s *state, site config.Site, inSite bool) []string {
	if !inSite || !site.Secured || os.Getenv("VITE_DEV_SERVER_KEY") != "" {
		return nil
	}

	certPath, keyPath := certs.SitePaths(filepath.Join(s.root, "certs"), s.cfg.Domain(site))
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Stat(p); err != nil {
			return nil
		}
	}

	return []string{"VITE_DEV_SERVER_KEY=" + keyPath, "VITE_DEV_SERVER_CERT=" + certPath}
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
		if s.cfg.Dumps {
			env = append(env, dumps.Env(s.cfg.Ports.Dumps)...)
		}
		if tool == "composer" {
			return path, append([]string{path, php.ComposerPhar(s.root)}, args...), env, nil
		}
		return path, append([]string{path}, args...), env, nil

	case "node", "npm", "npx":
		site, inSite := s.cfg.SiteForDir(cwd)

		spec := node.DetectSpec(cwd)
		if spec == "" {
			spec = s.cfg.DefaultNode
			if inSite {
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
		env = append(env, viteTLSEnv(s, site, inSite)...)
		path = filepath.Join(binDir, name)
		return path, append([]string{path}, args...), env, nil

	case "redis-cli", "valkey-cli":
		versions, _ := valkey.Installed(s.root)
		if len(versions) == 0 {
			return "", nil, nil, fmt.Errorf("redis is not installed — run `flock redis install`")
		}
		path = valkey.CLI(s.root, versions[0])
		if s.cfg.Ports.Redis != 6379 && !slices.Contains(args, "-p") {
			args = append([]string{"-p", strconv.Itoa(s.cfg.Ports.Redis)}, args...)
		}
		return path, append([]string{path}, args...), nil, nil
	}

	if slices.Contains(postgres.ClientTools, tool) {
		versions, _ := postgres.InstalledClients(s.root)
		if len(versions) == 0 {
			return "", nil, nil, fmt.Errorf("PostgreSQL client tools are not installed — run `flock postgres install-client`")
		}
		path = filepath.Join(postgres.ClientDir(s.root, versions[0]), "bin", tool)

		// Default to Flock's server; explicit flags or PG* variables still win.
		for key, value := range map[string]string{"PGHOST": "127.0.0.1", "PGPORT": strconv.Itoa(s.cfg.Ports.Postgres), "PGUSER": "postgres"} {
			if os.Getenv(key) == "" {
				env = append(env, key+"="+value)
			}
		}
		return path, append([]string{path}, args...), env, nil
	}

	return "", nil, nil, fmt.Errorf("unknown tool %q (supported: %v)", tool, tools)
}
