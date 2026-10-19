package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"flock/internal/api"
	"flock/internal/certs"
	"flock/internal/dns"
	"flock/internal/node"
	"flock/internal/paths"
)

const serviceUnit = "flock.service"

// shellProfiles are the profiles addToShellPath writes to: ~/.bashrc always, ~/.zshrc if present.
var shellProfiles = []string{".bashrc", ".zshrc"}

// unitPath is the systemd user unit that runs the daemon.
func unitPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "systemd", "user", serviceUnit), nil
}

// systemdUser reports whether a systemd user manager runs for this user; containers and WSL
// without systemd have none.
func systemdUser() bool {
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

// installService runs the daemon as a systemd user service, which starts it at login. Without a
// systemd user manager it starts the daemon directly, and nothing starts it after a reboot.
func installService(root, bin string) error {
	logFile := filepath.Join(root, "log", "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
		return err
	}
	// Installs from before the CLI became flock-php left a bin/flock that shadows util-linux's.
	_ = os.Remove(filepath.Join(root, "bin", "flock"))
	if !systemdUser() {
		serviceNote = "daemon runs in the background; without a systemd user session nothing starts it at login, so run `flock-php restart` after a reboot"
		stopDaemon(root)
		return startDetached(root, bin)
	}

	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(unitFile(root, bin, logFile)), 0o644); err != nil {
		return err
	}
	// A daemon started without systemd (by an earlier install) would keep the service from starting.
	if systemctl("--user", "is-active", "--quiet", serviceUnit) != nil {
		stopDaemon(root)
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", serviceUnit}, {"restart", serviceUnit}} {
		if err := systemctl(append([]string{"--user"}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

// unitFile is the daemon's systemd unit. A second daemon exits with exitAlreadyRunning, which
// isn't worth restarting; KillMode=mixed lets the daemon stop its services (PostgreSQL, PHP-FPM,
// ...) itself before systemd kills what is left.
func unitFile(root, bin, logFile string) string {
	return fmt.Sprintf(`# Managed by Flock (flock install)
[Unit]
Description=Flock daemon

[Service]
ExecStart=%s daemon --log-file %s
Environment=%s %s
Restart=always
RestartPreventExitStatus=%d
KillMode=mixed

[Install]
WantedBy=default.target
`, unitArg(bin), unitArg(logFile), unitEnv("PATH="+servicePath(root)), unitEnv("FLOCK_HOME="+root), exitAlreadyRunning)
}

// unitArg quotes s as one word of ExecStart, where systemd expands specifiers (%) and variables ($).
func unitArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s) + `"`
}

// unitEnv quotes an assignment of Environment=, where systemd expands specifiers (%).
func unitEnv(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s) + `"`
}

// servicePath is the daemon's PATH: Flock's shims and the default Node.js version's global packages
// first, in the order `flock install` puts them on the shell's PATH, then the system's directories.
func servicePath(root string) string {
	return strings.Join([]string{filepath.Join(root, "bin"), node.GlobalBin(node.CurrentGlobalDir(root)),
		"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"}, ":")
}

// startDetached starts the daemon in a session of its own, so it outlives the terminal that ran flock.
func startDetached(root, bin string) error {
	cmd := exec.Command(bin, "daemon", "--log-file", filepath.Join(root, "log", "daemon.log"))
	cmd.Env = append(os.Environ(), "PATH="+servicePath(root), "FLOCK_HOME="+root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// stopDaemon ends the running daemon and waits (up to 30 s, while it stops its services) until it
// is gone.
func stopDaemon(root string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := api.NewClient(paths.Socket(root)).Status(ctx)
	if err != nil || st.PID <= 0 {
		return
	}
	_ = syscall.Kill(st.PID, syscall.SIGTERM)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && !processGone(st.PID); {
		time.Sleep(100 * time.Millisecond)
	}
}

// processGone reports whether pid has ended. A zombie counts as gone: a detached daemon's parent
// can be a container's init that never reaps it.
func processGone(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	_, after, _ := strings.Cut(string(data), ") ")
	return strings.HasPrefix(after, "Z")
}

// serviceInstalled reports whether the daemon's systemd unit is installed.
func serviceInstalled() bool {
	path, err := unitPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func uninstallService() error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if systemdUser() {
			_ = systemctl("--user", "disable", "--now", serviceUnit)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if systemdUser() {
			_ = systemctl("--user", "daemon-reload")
		}
	}
	// Also a daemon started without systemd.
	if root, err := paths.Root(); err == nil {
		stopDaemon(root)
	}
	return nil
}

// restartService has systemd restart the daemon, or restarts a daemon started without systemd.
func restartService() error {
	root, err := paths.Root()
	if err != nil {
		return err
	}
	if serviceInstalled() && systemdUser() {
		return systemctl("--user", "restart", serviceUnit)
	}
	bin := filepath.Join(root, "bin", paths.CLI())
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("the background daemon is not installed — run `flock-php install`")
	}
	stopDaemon(root)
	return startDetached(root, bin)
}

// privilegedFirst: `flock install` runs ensurePrivileged last, as the installed flock binary.
const privilegedFirst = false

// installHelperBinary: Linux has no hosts helper.
func installHelperBinary(string) error { return nil }

// ensurePrivileged checks what needs root (the unit that routes the TLD to Flock through
// systemd-resolved, the CA in the system's trust store, listening on ports below 1024) and does
// what is missing in one sudo run of privileged-setup. Browsers keep their own trust stores,
// which the user can change.
func ensurePrivileged(cmd *cobra.Command, s *state, bin string, ca *certs.CA) error {
	tld, dnsPort := s.cfg.TLD, s.cfg.Ports.DNS
	var args []string
	resolved := dns.ResolvedRunning()
	if resolved && dns.ResolvedProblem(tld, dnsPort) != "" {
		args = append(args, "--tld", tld, "--dns-port", strconv.Itoa(dnsPort))
	}
	if !systemTrusts(ca.CertPath()) {
		args = append(args, "--ca", ca.CertPath())
	}
	lowPorts := os.Geteuid() != 0 && needsLowPorts(s.cfg.Ports.HTTP, s.cfg.Ports.HTTPS) && !canBindLowPorts(bin)
	if lowPorts {
		args = append(args, "--bind-low-ports", bin)
	}

	if len(args) > 0 {
		cmd.Printf("→ privileged-setup %s (as root: sudo may ask for your password)…\n", strings.Join(args, " "))
		if err := runPrivileged(cmd, bin, append([]string{"privileged-setup"}, args...)...); err != nil {
			return fmt.Errorf("privileged setup: %w", err)
		}
	}
	switch {
	case !resolved:
		cmd.Printf("! systemd-resolved isn't running: for *.%s, have your DNS resolver forward .%s to 127.0.0.1 port %d\n", tld, tld, dnsPort)
	case resolvesToFlock(tld):
		cmd.Printf("✓ *.%s resolves via Flock\n", tld)
		if problem := dns.LinkProblem(tld); problem != "" {
			cmd.Printf("! but %s\n", problem)
		}
	default:
		cmd.Printf("! *.%s doesn't resolve via Flock yet: see `systemctl status %s` and `flock-php status`\n", tld, dns.Unit)
	}
	cmd.Println("✓ Flock CA is trusted")
	trustInBrowsers(cmd, ca.CertPath())

	if lowPorts {
		// The capability applies to processes started after it was set.
		if err := restartService(); err != nil {
			return fmt.Errorf("restart the daemon: %w", err)
		}
		cmd.Printf("✓ the daemon may listen on ports %d and %d\n", s.cfg.Ports.HTTP, s.cfg.Ports.HTTPS)
	}
	return nil
}

// resolvesToFlock looks up fresh names under tld through the system resolver, as browsers and PHP
// do, until one answers 127.0.0.1 (for up to ~10 s: the daemon may still be starting its DNS server).
func resolvesToFlock(tld string) bool {
	for range 10 {
		nonce := make([]byte, 6)
		_, _ = rand.Read(nonce)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		addrs, err := (&net.Resolver{PreferGo: false}).LookupHost(ctx, "flock-install-"+hex.EncodeToString(nonce)+"."+tld)
		cancel()
		if err == nil && slices.Contains(addrs, "127.0.0.1") {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// runPrivileged runs a command as root: directly when flock already runs as root, else through
// sudo, which asks for the password at the terminal.
func runPrivileged(cmd *cobra.Command, exe string, args ...string) error {
	run := exec.Command("sudo", append([]string{exe}, args...)...)
	if os.Geteuid() == 0 {
		run = exec.Command(exe, args...)
	}
	run.Stdin, run.Stdout, run.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
	return run.Run()
}

// systemTrusts reports whether the system's trust store has the CA, the way Go, curl and PHP see it.
func systemTrusts(caPath string) bool {
	data, err := os.ReadFile(caPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	_, err = cert.Verify(x509.VerifyOptions{})
	return err == nil
}

// needsLowPorts reports whether listening on one of ports takes CAP_NET_BIND_SERVICE here.
func needsLowPorts(ports ...int) bool {
	start := 1024
	if data, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			start = n
		}
	}
	return slices.Min(ports) < start
}

// canBindLowPorts reports whether bin's file capabilities make CAP_NET_BIND_SERVICE permitted and
// effective.
func canBindLowPorts(bin string) bool {
	buf := make([]byte, 24)
	n, err := unix.Getxattr(bin, "security.capability", buf)
	return err == nil && bindCapability(buf[:n])
}

// bindCapability reads a security.capability attribute (struct vfs_cap_data): the flags, then the
// permitted set's low 32 bits.
func bindCapability(data []byte) bool {
	const flagsEffective = 0x1 // VFS_CAP_FLAGS_EFFECTIVE
	if len(data) < 8 || binary.LittleEndian.Uint32(data[0:4])&flagsEffective == 0 {
		return false
	}
	return binary.LittleEndian.Uint32(data[4:8])&(1<<unix.CAP_NET_BIND_SERVICE) != 0
}

// browserDatabases are the NSS databases of Chrome, Chromium and Firefox's profiles (also as snaps),
// which don't read the system's trust store.
func browserDatabases() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var dbs []string
	for _, dir := range []string{".pki/nssdb", "snap/chromium/current/.pki/nssdb", ".mozilla/firefox/*", "snap/firefox/common/.mozilla/firefox/*"} {
		found, _ := filepath.Glob(filepath.Join(home, dir, "cert9.db"))
		for _, f := range found {
			dbs = append(dbs, filepath.Dir(f))
		}
	}
	return dbs
}

// nssNickname names the CA in NSS databases; the fingerprint tells apart the CAs of reinstalls.
func nssNickname(caPath string) string {
	fingerprint, _ := certs.FileFingerprint(caPath)
	return "Flock CA " + fingerprint[:min(8, len(fingerprint))]
}

// trustInBrowsers adds the CA to the browsers' NSS databases with certutil.
func trustInBrowsers(cmd *cobra.Command, caPath string) {
	dbs := browserDatabases()
	if len(dbs) == 0 {
		return
	}
	if _, err := exec.LookPath("certutil"); err != nil {
		cmd.Println("! Chrome and Firefox don't trust Flock's CA yet: install certutil (libnss3-tools or nss-tools) and run `flock-php install` again")
		return
	}
	nickname := nssNickname(caPath)
	for _, db := range dbs {
		if exec.Command("certutil", "-d", "sql:"+db, "-L", "-n", nickname).Run() == nil {
			continue
		}
		if out, err := exec.Command("certutil", "-d", "sql:"+db, "-A", "-t", "C,,", "-n", nickname, "-i", caPath).CombinedOutput(); err != nil {
			cmd.Printf("! could not add Flock's CA to %s: %v: %s\n", db, err, bytes.TrimSpace(out))
		}
	}
	cmd.Println("✓ Flock CA is trusted by Chrome and Firefox")
}

// untrustInBrowsers removes what trustInBrowsers added.
func untrustInBrowsers(caPath string) {
	if _, err := exec.LookPath("certutil"); err != nil {
		return
	}
	nickname := nssNickname(caPath)
	for _, db := range browserDatabases() {
		_ = exec.Command("certutil", "-d", "sql:"+db, "-D", "-n", nickname).Run()
	}
}

// tldPattern is a TLD privileged-setup writes into the unit that routes it to Flock.
var tldPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,63}$`)

// caAnchor is where the system's trust store takes another CA, and the command that rebuilds the
// store: Debian and Ubuntu, then Fedora and RHEL, then Arch.
func caAnchor() (path string, update []string, err error) {
	if _, err := exec.LookPath("update-ca-certificates"); err == nil {
		return "/usr/local/share/ca-certificates/flock.crt", []string{"update-ca-certificates"}, nil
	}
	if _, err := exec.LookPath("update-ca-trust"); err == nil {
		for _, dir := range []string{"/etc/pki/ca-trust/source/anchors", "/etc/ca-certificates/trust-source/anchors"} {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return filepath.Join(dir, "flock.crt"), []string{"update-ca-trust", "extract"}, nil
			}
		}
	}
	return "", nil, errors.New("found neither update-ca-certificates nor update-ca-trust: add Flock's CA to the system's trust store yourself")
}

func runTool(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, bytes.TrimSpace(out))
	}
	return nil
}

// removeLegacyDropIn removes the resolved.conf drop-in of installs before dns.Link, which put Flock
// among resolved's global DNS servers.
func removeLegacyDropIn(tld string) {
	file := dns.LegacyDropIn(tld)
	if data, _ := os.ReadFile(file); tld != "" && strings.HasPrefix(string(data), "# Managed by Flock") {
		_ = os.Remove(file)
		_ = runTool("systemctl", "try-restart", "systemd-resolved")
	}
}

// removeLegacyNMFile removes the NetworkManager drop-in under its earlier name, which sorted before
// drop-ins that could replace its list.
func removeLegacyNMFile() {
	if data, _ := os.ReadFile(dns.LegacyNMFile()); string(data) == dns.NMContent {
		_ = os.Remove(dns.LegacyNMFile())
	}
}

func newPrivilegedSetupCommand() *cobra.Command {
	var tld, caPath, lowPortsBin string
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
			if tld != "" && !tldPattern.MatchString(tld) {
				return fmt.Errorf("invalid --tld %q", tld)
			}

			if remove {
				removeLegacyDropIn(tld)
				if data, _ := os.ReadFile(dns.ResolvedFile()); strings.HasPrefix(string(data), "# Managed by Flock") {
					_ = runTool("systemctl", "disable", "--now", dns.Unit) // its ExecStop deletes the link
					_ = os.Remove(dns.ResolvedFile())
					_ = runTool("systemctl", "daemon-reload")
				}
				removeLegacyNMFile()
				if data, _ := os.ReadFile(dns.NMFile()); string(data) == dns.NMContent {
					_ = os.Remove(dns.NMFile())
					// Install created them when NetworkManager wasn't there; with it, they aren't empty.
					_ = os.Remove(dns.NMConfDir)
					_ = os.Remove(filepath.Dir(dns.NMConfDir))
					_ = runTool("systemctl", "try-reload-or-restart", "NetworkManager.service")
				}
				if anchor, update, err := caAnchor(); err == nil {
					if _, err := os.Stat(anchor); err == nil {
						_ = os.Remove(anchor)
						return runTool(update[0], update[1:]...)
					}
				}
				return nil
			}

			if dnsPort > 0 {
				if tld == "" {
					return fmt.Errorf("--dns-port needs --tld")
				}
				removeLegacyDropIn(tld)
				// NetworkManager must leave the link alone before the unit creates it again.
				removeLegacyNMFile()
				if err := os.MkdirAll(dns.NMConfDir, 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(dns.NMFile(), []byte(dns.NMContent), 0o644); err != nil {
					return err
				}
				_ = runTool("systemctl", "try-reload-or-restart", "NetworkManager.service") // fails without NetworkManager
				if err := os.WriteFile(dns.ResolvedFile(), []byte(dns.ResolvedContent(tld, dnsPort)), 0o644); err != nil {
					return err
				}
				for _, args := range [][]string{{"daemon-reload"}, {"enable", dns.Unit}, {"restart", dns.Unit}} {
					if err := runTool("systemctl", args...); err != nil {
						return err
					}
				}
			}
			if caPath != "" {
				anchor, update, err := caAnchor()
				if err != nil {
					return err
				}
				data, err := os.ReadFile(caPath)
				if err != nil {
					return err
				}
				if err := os.WriteFile(anchor, data, 0o644); err != nil {
					return err
				}
				if err := runTool(update[0], update[1:]...); err != nil {
					return err
				}
			}
			if lowPortsBin != "" {
				if !filepath.IsAbs(lowPortsBin) {
					return fmt.Errorf("invalid --bind-low-ports %q", lowPortsBin)
				}
				return runTool("setcap", "cap_net_bind_service=+ep", lowPortsBin)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&tld, "tld", "", "")
	cmd.Flags().IntVar(&dnsPort, "dns-port", 0, "")
	cmd.Flags().StringVar(&caPath, "ca", "", "")
	cmd.Flags().StringVar(&lowPortsBin, "bind-low-ports", "", "")
	cmd.Flags().BoolVar(&remove, "remove", false, "")

	return cmd
}

func newUninstallCommand() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Flock: daemon, PATH entry, DNS/CA trust and downloaded files (keeps your sites' settings and databases unless --purge)",
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

			caPath := filepath.Join(s.root, "certs", "ca.crt")
			untrustInBrowsers(caPath)
			self, _ := os.Executable()
			if err := runPrivileged(cmd, self, "privileged-setup", "--remove", "--tld", s.cfg.TLD); err != nil {
				return err
			}
			cmd.Printf("✓ removed %s and the Flock CA's trust\n", dns.ResolvedFile())

			return finishUninstall(cmd, s.root, purge)
		},
	}
	uninstallFlags(cmd, &purge)
	return cmd
}

// appDirs: Flock has no app on Linux.
func appDirs() []string { return nil }

// removeAfterExit is never needed: Linux deletes running programs.
func removeAfterExit(string) bool { return false }
