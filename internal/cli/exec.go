package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/catalog"
	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/dumps"
	"flock/internal/envutil"
	"flock/internal/node"
	"flock/internal/php"
	"flock/internal/postgres"
	"flock/internal/project"
	"flock/internal/valkey"
)

// tools are the commands Flock puts on PATH as shims.
var tools = append([]string{"php", "composer", "node", "npm", "npx", "redis-cli", "valkey-cli"}, postgres.ClientTools...)

// ShimTool returns the tool a shim executable stands for ("php" for ...\bin\php.exe), or "".
func ShimTool(argv0 string) string {
	name := strings.ToLower(filepath.Base(argv0))
	name = strings.TrimSuffix(name, ".exe")
	if slices.Contains(tools, name) {
		return name
	}
	return ""
}

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
			// A missing PHP or Node version (on Windows also Redis or the PostgreSQL client tools): offer
			// to install it, then run the command after all.
			if err != nil && interactive() {
				installed, installErr := offerInstall(cmd.Context(), s, err)
				if installErr != nil {
					return installErr
				}
				if installed {
					path, argv, env, err = resolveTool(s, cwd, args[0], args[1:])
				}
			}
			if err != nil {
				return err
			}

			return execReplace(path, argv, envutil.Merge(os.Environ(), env))
		},
	}
}

// siteFor is the site dir belongs to, or nil.
func siteFor(s *state, dir string) *config.Site {
	if site, ok := s.cfg.SiteForDir(dir); ok {
		return &site
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
		// Isolated for the site, else pinned in the project (.flockrc), else the default.
		minor := project.PHPFor(s.cfg, siteFor(s, cwd), cwd).Version

		installed, _ := php.Installed(s.root)
		if !slices.Contains(installed, minor) {
			return "", nil, nil, &phpMissingError{minor: minor}
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

		// Isolated for the site, else pinned in the project (.flockrc, .nvmrc, .node-version),
		// else the default.
		spec := project.NodeFor(s.cfg, siteFor(s, cwd), cwd).Version

		binDir, version, err := node.Resolve(s.root, spec)
		if err != nil {
			return "", nil, nil, err
		}

		name := tool
		if runtime.GOOS == "windows" && tool != "node" {
			name += ".cmd"
		} else if runtime.GOOS == "windows" {
			name += ".exe"
		}

		// npm and npx are scripts that look `node` up on PATH. Global packages, and an npm updated
		// with `npm install -g npm`, go to the major's global prefix (see node.GlobalDir).
		env = append(node.NpmEnv(s.root, version, binDir), "NODE_EXTRA_CA_CERTS="+filepath.Join(s.root, "certs", "ca.crt"))
		env = append(env, viteTLSEnv(s, site, inSite)...)
		path = filepath.Join(binDir, name)
		if tool != "node" {
			if updated := filepath.Join(node.GlobalBin(node.GlobalDir(s.root, node.MajorOf(version))), name); fileExists(updated) {
				path = updated
			}
		}
		return path, append([]string{path}, args...), env, nil

	case "redis-cli", "valkey-cli":
		// redis-cli talks to Redis when it is installed, else to Valkey (whose CLI is the same);
		// valkey-cli always to Valkey.
		port := s.cfg.Ports.Valkey
		if x, ok := s.cfg.Services.Extra("redis"); ok && tool == "redis-cli" && fileExists(catalog.RedisCLI(s.root, x.Version)) {
			path, port = catalog.RedisCLI(s.root, x.Version), x.Port
		} else if versions, _ := valkey.Installed(s.root); len(versions) > 0 {
			path = valkey.CLI(s.root, versions[0])
		} else {
			return "", nil, nil, errRedisMissing
		}
		if port != 6379 && !slices.Contains(args, "-p") {
			args = append([]string{"-p", strconv.Itoa(port)}, args...)
		}
		return path, append([]string{path}, args...), nil, nil
	}

	if slices.Contains(postgres.ClientTools, tool) {
		versions, _ := postgres.InstalledClients(s.root)
		if len(versions) == 0 {
			return "", nil, nil, errPostgresClientMissing
		}
		path = postgres.ClientTool(s.root, versions[0], tool)

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
