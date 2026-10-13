package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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

// addToShellPath prepends the shim dir in ~/.zshrc (and ~/.bash_profile if present), once.
func addToShellPath(dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	block := fmt.Sprintf("\n%s\nexport PATH=%q:\"$PATH\"\n", profileMarker, dir)
	for _, name := range []string{".zshrc", ".bash_profile"} {
		path := filepath.Join(home, name)
		data, err := os.ReadFile(path)
		if err != nil && name != ".zshrc" {
			continue
		}
		if bytes.Contains(data, []byte(profileMarker)) {
			continue
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(block)
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
	_ = exec.Command("launchctl", "bootout", domain+"/"+launchLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, out)
	}
	return nil
}

func uninstallService() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	_ = exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+launchLabel).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xmlEscapeText(&b, s)
	return b.String()
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
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the background daemon and remove DNS/CA trust (keeps your data and config)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
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
			cmd.Printf("data is kept in %s — remove the PATH line marked %q from your shell profile\n", s.root, profileMarker)
			return nil
		},
	}
}
