package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

const (
	launchLabel   = "dev.flock.daemon"
	profileMarker = "# Flock (added by `flock install`)"
)

func exeName(name string) string { return name }

func writeShims(root, bin string) error {
	dir := filepath.Join(root, "bin")
	for _, tool := range tools {
		script := fmt.Sprintf("#!/bin/sh\nexec %q exec %s \"$@\"\n", bin, tool)
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// addToShellPath prepends dirs, in this order, in ~/.zshrc (and ~/.bash_profile if present): a
// marked export line, appended once and rewritten when the directories change (an older Flock
// only added the shims).
func addToShellPath(dirs ...string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	quoted := make([]string, len(dirs))
	for i, dir := range dirs {
		quoted[i] = strconv.Quote(dir)
	}
	export := fmt.Sprintf("export PATH=%s:\"$PATH\"", strings.Join(quoted, ":"))
	for _, name := range []string{".zshrc", ".bash_profile"} {
		path := filepath.Join(home, name)
		data, err := os.ReadFile(path)
		if err != nil && name != ".zshrc" {
			continue
		}
		lines := strings.Split(string(data), "\n")
		if i := slices.Index(lines, profileMarker); i >= 0 {
			if i+1 < len(lines) && lines[i+1] == export {
				continue
			}
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "export PATH=") {
				lines[i+1] = export
			} else {
				lines = slices.Insert(lines, i+1, export)
			}
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
				return err
			}
			continue
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString("\n" + profileMarker + "\n" + export + "\n")
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist"), nil
}

// installService registers a LaunchAgent that keeps `flock daemon` running for this user.
func installService(root, bin string) error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	logFile := filepath.Join(root, "log", "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
		return err
	}

	// launchd starts agents with a minimal PATH; give PHP (and anything it spawns) the shims too.
	envPath := filepath.Join(root, "bin") + ":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>daemon</string></array>
	<key>EnvironmentVariables</key>
	<dict><key>PATH</key><string>%s</string></dict>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>ProcessType</key><string>Interactive</string>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, launchLabel, xmlEscape(bin), xmlEscape(envPath), xmlEscape(logFile), xmlEscape(logFile))

	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}

	domain := "gui/" + strconv.Itoa(os.Getuid())
	stopService(domain)

	// bootout returns before launchd has fully released the job, so bootstrap can briefly fail with EIO.
	var lastErr error
	for range 20 {
		out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("launchctl bootstrap: %v: %s", err, out)
		time.Sleep(250 * time.Millisecond)
	}
	return lastErr
}

// stopService unloads the agent and waits until launchd forgets it.
func stopService(domain string) {
	_ = exec.Command("launchctl", "bootout", domain+"/"+launchLabel).Run()
	for range 40 {
		if exec.Command("launchctl", "print", domain+"/"+launchLabel).Run() != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func uninstallService() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	stopService("gui/" + strconv.Itoa(os.Getuid()))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func resolverFile(tld string) string { return "/etc/resolver/" + tld }

func resolverContent(dnsPort int) string {
	return fmt.Sprintf("# Managed by Flock\nnameserver 127.0.0.1\nport %d\n", dnsPort)
}

// ensurePrivileged checks the root-owned bits and, if anything is missing, re-runs flock under sudo to fix them.
func ensurePrivileged(cmd *cobra.Command, s *state, bin string, ca *certs.CA) error {
	current, _ := os.ReadFile(resolverFile(s.cfg.TLD))
	resolverOK := string(current) == resolverContent(s.cfg.Ports.DNS)
	trusted := exec.Command("security", "verify-cert", "-c", ca.CertPath(), "-p", "ssl").Run() == nil

	if resolverOK && trusted {
		cmd.Printf("✓ *.%s resolves via Flock\n✓ Flock CA is trusted\n", s.cfg.TLD)
		return nil
	}

	cmd.Println("→ configuring DNS and trusting the local CA (needs your password)…")
	sudo := exec.Command("sudo", bin, "privileged-setup",
		"--tld", s.cfg.TLD, "--dns-port", strconv.Itoa(s.cfg.Ports.DNS), "--ca", ca.CertPath())
	sudo.Stdin, sudo.Stdout, sudo.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := sudo.Run(); err != nil {
		return fmt.Errorf("privileged setup: %w", err)
	}
	cmd.Printf("✓ *.%s resolves via Flock\n✓ Flock CA is trusted\n", s.cfg.TLD)
	return nil
}

func newPrivilegedSetupCommand() *cobra.Command {
	var tld, caPath string
	var dnsPort int
	var remove bool

	cmd := &cobra.Command{
		Use:    "privileged-setup",
		Short:  "Root-only steps of install/uninstall (run via sudo by flock itself)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Geteuid() != 0 {
				return fmt.Errorf("must run as root")
			}
			if tld == "" || strings.ContainsAny(tld, "/.") {
				return fmt.Errorf("invalid --tld %q", tld)
			}

			if remove {
				data, _ := os.ReadFile(resolverFile(tld))
				if strings.HasPrefix(string(data), "# Managed by Flock") {
					_ = os.Remove(resolverFile(tld))
				}
				if caPath != "" {
					_ = exec.Command("security", "remove-trusted-cert", "-d", caPath).Run()
				}
				return nil
			}

			if err := os.MkdirAll("/etc/resolver", 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(resolverFile(tld), []byte(resolverContent(dnsPort)), 0o644); err != nil {
				return err
			}

			out, err := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot",
				"-k", "/Library/Keychains/System.keychain", caPath).CombinedOutput()
			if err != nil {
				return fmt.Errorf("trust CA: %v: %s", err, out)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&tld, "tld", "", "")
	cmd.Flags().IntVar(&dnsPort, "dns-port", 0, "")
	cmd.Flags().StringVar(&caPath, "ca", "", "")
	cmd.Flags().BoolVar(&remove, "remove", false, "")

	return cmd
}

func newUninstallCommand() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Flock: daemon, DNS/CA trust and downloaded files (keeps your sites' settings and databases unless --purge)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			purge = decidePurge(s.root, purge)
			if err := uninstallService(); err != nil {
				return err
			}
			cmd.Println("✓ background daemon removed")

			self, _ := os.Executable()
			sudo := exec.Command("sudo", self, "privileged-setup", "--remove",
				"--tld", s.cfg.TLD, "--ca", filepath.Join(s.root, "certs", "ca.crt"))
			sudo.Stdin, sudo.Stdout, sudo.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := sudo.Run(); err != nil {
				return err
			}
			cmd.Printf("✓ removed %s and CA trust\n", resolverFile(s.cfg.TLD))
			if err := finishUninstall(cmd, s.root, purge); err != nil {
				return err
			}
			cmd.Printf("remove the PATH line marked %q from your shell profile\n", profileMarker)
			return nil
		},
	}
	uninstallFlags(cmd, &purge)
	return cmd
}

// appDirs: the macOS app keeps its preferences in UserDefaults, which uninstalling the app handles.
func appDirs() []string { return nil }

// removeAfterExit is never needed: macOS deletes running programs.
func removeAfterExit(string) bool { return false }

// restartService makes launchd kill and relaunch the daemon.
func restartService() error {
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + launchLabel
	if err := exec.Command("launchctl", "print", target).Run(); err != nil {
		return fmt.Errorf("the background daemon is not installed — run `flock install`")
	}
	if out, err := exec.Command("launchctl", "kickstart", "-k", target).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl kickstart: %v: %s", err, out)
	}
	return nil
}
