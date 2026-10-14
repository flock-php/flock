package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"flock/internal/certs"
	"flock/internal/dns"
	"flock/internal/node"
)

const (
	launchLabel   = "dev.flock.daemon"
	profileMarker = "# Flock (added by `flock install`)"
	// appBundleID and appExecutable identify the menu bar app (macos/build-app.sh).
	appBundleID   = "dev.flock.bar"
	appExecutable = "FlockBar"
)

// shellProfiles are the profiles addToShellPath writes to (~/.zshrc always, ~/.bash_profile if present).
var shellProfiles = []string{".zshrc", ".bash_profile"}

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
	for _, name := range shellProfiles {
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

// removeFromShellProfiles takes out what addToShellPath added: the marker, its export line and
// the blank line before them. It reports whether a profile had them.
func removeFromShellProfiles() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	removed := false
	for _, name := range shellProfiles {
		path := filepath.Join(home, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		i := slices.Index(lines, profileMarker)
		if i < 0 {
			continue
		}
		end := i + 1
		if end < len(lines) && strings.HasPrefix(lines[end], "export PATH=") {
			end++
		}
		if i > 0 && lines[i-1] == "" {
			i--
		}
		lines = slices.Delete(lines, i, end)
		info, err := os.Stat(path)
		if err != nil {
			return removed, err
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), info.Mode().Perm()); err != nil {
			return removed, err
		}
		removed = true
	}
	return removed, nil
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

	envPath := launchdPath(root)
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

// launchdPath is the daemon's PATH. launchd starts agents with a minimal one; give PHP (and anything
// it spawns) the shims and the default Node.js version's global packages too, in the order
// `flock install` puts them on the shell's PATH.
func launchdPath(root string) string {
	return strings.Join([]string{filepath.Join(root, "bin"), node.GlobalBin(node.CurrentGlobalDir(root)),
		"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
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

// ensurePrivileged checks the resolver file and the CA's trust and fixes what is missing: the
// resolver file as root (re-running flock under sudo), the trust as the user (see trustCA).
func ensurePrivileged(cmd *cobra.Command, s *state, bin string, ca *certs.CA) error {
	current, _ := os.ReadFile(dns.ResolverFile(s.cfg.TLD))
	if string(current) != dns.ResolverContent(s.cfg.Ports.DNS) {
		cmd.Printf("→ configuring *.%s DNS (needs your password)…\n", s.cfg.TLD)
		if err := runPrivileged(cmd, "Flock wants to set up *."+s.cfg.TLD+" DNS.", bin, "privileged-setup",
			"--tld", s.cfg.TLD, "--dns-port", strconv.Itoa(s.cfg.Ports.DNS)); err != nil {
			return fmt.Errorf("privileged setup: %w", err)
		}
	}
	cmd.Printf("✓ *.%s resolves via Flock\n", s.cfg.TLD)

	if exec.Command("security", "verify-cert", "-c", ca.CertPath(), "-p", "ssl").Run() != nil {
		cmd.Println("→ trusting the local CA (macOS asks for your password)…")
		if err := trustCA(ca.CertPath()); err != nil {
			return fmt.Errorf("trust CA: %w", err)
		}
	}
	cmd.Println("✓ Flock CA is trusted")
	return nil
}

// errDeclined is what the app looks for to tell a cancelled password dialog from a failure.
var errDeclined = errors.New("administrator permission was declined")

// loginKeychain is the user's login keychain, where trustCA puts the CA.
func loginKeychain() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Keychains", "login.keychain-db")
}

// trustCA trusts the CA as a root for the user. It runs as the user, not root: macOS only changes
// trust settings after its own authorization dialog, which a root process started through
// osascript (the app's `flock install`) can't show, while sudo at a terminal can. Browsers, curl
// and PHP's bundle (certs.SystemRoots) all honour the user's trust settings.
func trustCA(caPath string) error {
	out, err := exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-k", loginKeychain(), caPath).CombinedOutput()
	return securityError(err, out)
}

// untrustCA removes the CA's trust from the user's settings and, for installs made before trustCA,
// the admin settings, then the CA itself from the login keychain. macOS asks for the password.
func untrustCA(caPath string) error {
	fingerprint, err := certs.FileFingerprint(caPath)
	if err != nil {
		return nil // no CA, nothing trusted
	}
	if certs.TrustedByAdmin(fingerprint) {
		out, err := exec.Command("security", "remove-trusted-cert", "-d", caPath).CombinedOutput()
		if err := securityError(err, out); err != nil {
			return err
		}
	}
	if certs.TrustedByUser(fingerprint) {
		out, err := exec.Command("security", "remove-trusted-cert", caPath).CombinedOutput()
		if err := securityError(err, out); err != nil {
			return err
		}
	}
	_ = exec.Command("security", "delete-certificate", "-Z", fingerprint, loginKeychain()).Run()
	return nil
}

// securityError turns a failed `security` run into an error, a cancelled dialog into errDeclined.
func securityError(err error, out []byte) error {
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	switch {
	case strings.Contains(text, "canceled"), strings.Contains(text, "cancelled"):
		return errDeclined
	case strings.Contains(text, "no user interaction was possible"):
		return fmt.Errorf("%s — run it from a logged-in desktop session, not over SSH", text)
	}
	return fmt.Errorf("%v: %s", err, text)
}

// runPrivileged runs a command as root. At a terminal that is sudo, which asks for the password
// there; without one (the Flock app runs `flock install`) macOS' own password dialog asks instead.
func runPrivileged(cmd *cobra.Command, prompt, exe string, args ...string) error {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		sudo := exec.Command("sudo", append([]string{exe}, args...)...)
		sudo.Stdin, sudo.Stdout, sudo.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
		return sudo.Run()
	}

	out, err := exec.Command("osascript", administratorScript(prompt, exe, args...)...).CombinedOutput()
	if text := strings.TrimSpace(string(out)); err != nil {
		// -128: the user cancelled the password dialog.
		if strings.Contains(text, "(-128)") {
			return errDeclined
		}
		return fmt.Errorf("%v: %s", err, text)
	} else if text != "" {
		cmd.Println(text)
	}
	return nil
}

// administratorScript is osascript's argument list to run exe with administrator privileges. The
// command line goes in as an argument rather than into the script's source, so it only needs
// quoting for the shell that `do shell script` runs it with.
func administratorScript(prompt, exe string, args ...string) []string {
	words := []string{shellQuote(exe)}
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	return []string{
		"-e", "on run argv",
		"-e", "do shell script (item 1 of argv) with prompt (item 2 of argv) with administrator privileges",
		"-e", "end run",
		strings.Join(words, " ") + " 2>&1", prompt,
	}
}

// shellQuote quotes s for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func newPrivilegedSetupCommand() *cobra.Command {
	var tld string
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
				data, _ := os.ReadFile(dns.ResolverFile(tld))
				if strings.HasPrefix(string(data), "# Managed by Flock") {
					_ = os.Remove(dns.ResolverFile(tld))
				}
				return nil
			}

			if err := os.MkdirAll("/etc/resolver", 0o755); err != nil {
				return err
			}
			return os.WriteFile(dns.ResolverFile(tld), []byte(dns.ResolverContent(dnsPort)), 0o644)
		},
	}

	cmd.Flags().StringVar(&tld, "tld", "", "")
	cmd.Flags().IntVar(&dnsPort, "dns-port", 0, "")
	cmd.Flags().BoolVar(&remove, "remove", false, "")

	return cmd
}

func newUninstallCommand() *cobra.Command {
	var purge, keepApp bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Flock: daemon, PATH entry, DNS/CA trust, the menu bar app and downloaded files (keeps your sites' settings and databases unless --purge)",
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

			switch removed, err := removeFromShellProfiles(); {
			case err != nil:
				cmd.Printf("! could not update your shell profile (%v) — remove the PATH line marked %q\n", err, profileMarker)
			case removed:
				cmd.Println("✓ removed Flock from your PATH (open a new terminal)")
			}

			self, _ := os.Executable()
			if err := runPrivileged(cmd, "Flock wants to remove its *."+s.cfg.TLD+" DNS setup.", self, "privileged-setup", "--remove",
				"--tld", s.cfg.TLD); err != nil {
				return err
			}
			cmd.Printf("✓ removed %s\n", dns.ResolverFile(s.cfg.TLD))
			if err := untrustCA(filepath.Join(s.root, "certs", "ca.crt")); err != nil {
				return fmt.Errorf("remove CA trust: %w", err)
			}
			cmd.Println("✓ removed the Flock CA's trust")

			// The app runs this command with --keep-app, shows the result and then removes itself.
			if !keepApp {
				stopApp()
			}
			if err := finishUninstall(cmd, s.root, purge); err != nil {
				return err
			}
			if !keepApp {
				// Through cfprefsd, which would otherwise keep (and write back) its cached copy.
				_ = exec.Command("defaults", "delete", appBundleID).Run()
				for _, app := range installedApps() {
					if err := os.RemoveAll(app); err != nil {
						cmd.Printf("! could not remove %s (%v) — move it to the Trash\n", app, err)
					} else {
						cmd.Printf("✓ removed %s\n", app)
					}
				}
			}
			return nil
		},
	}
	uninstallFlags(cmd, &purge)
	cmd.Flags().BoolVar(&keepApp, "keep-app", false, "leave the Flock app installed (used by the app, which removes itself)")
	_ = cmd.Flags().MarkHidden("keep-app")
	return cmd
}

// stopApp quits the menu bar app (it can't work without Flock anyway).
func stopApp() {
	_ = exec.Command("pkill", "-x", appExecutable).Run()
}

// appLocations are where the app is installed from the DMG.
var appLocations = func() []string {
	locations := []string{"/Applications/Flock.app"}
	if home, err := os.UserHomeDir(); err == nil {
		locations = append(locations, filepath.Join(home, "Applications", "Flock.app"))
	}
	return locations
}

// installedApps are the copies of the menu bar app in the Applications folders; a bundle with
// another identifier that happens to be called Flock.app is left alone.
func installedApps() []string {
	var apps []string
	for _, app := range appLocations() {
		info, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
		if err == nil && strings.Contains(string(info), "<string>"+appBundleID+"</string>") {
			apps = append(apps, app)
		}
	}
	return apps
}

// appDirs are the app's preferences, caches and saved window state, removed on uninstall.
func appDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	library := filepath.Join(home, "Library")
	return []string{
		filepath.Join(library, "Preferences", appBundleID+".plist"),
		filepath.Join(library, "Caches", appBundleID),
		filepath.Join(library, "HTTPStorages", appBundleID),
		filepath.Join(library, "WebKit", appBundleID),
		filepath.Join(library, "Saved Application State", appBundleID+".savedState"),
	}
}

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
