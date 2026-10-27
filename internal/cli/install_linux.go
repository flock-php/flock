package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"flock/internal/certs"
	"flock/internal/dns"
	"flock/internal/node"
)

// serviceStarts says when the system unit starts the daemon.
const serviceStarts = "with the computer"

const (
	profileMarker = "# Flock (added by `flock install`)"
	managedMarker = "# Managed by Flock"
)

// Where the privileged setup puts its files. Tests move them.
var (
	unitDir       = "/etc/systemd/system"
	packagedUnits = "/usr/lib/systemd/system"
	// polkitRule is in the vendor directory, which users can read (unlike /etc/polkit-1/rules.d),
	// so ensurePrivileged can check it.
	polkitRule = "/usr/share/polkit-1/rules.d/50-flock.rules"
	// caAnchors are the system trust store's source directories and the command that rebuilds the
	// store from them: Debian and Ubuntu, then Fedora and RHEL.
	caAnchors = []struct{ dir, update string }{
		{"/usr/local/share/ca-certificates", "update-ca-certificates"},
		{"/etc/pki/ca-trust/source/anchors", "update-ca-trust"},
	}
)

// shellProfiles are the profiles addToShellPath writes to: ~/.bashrc always, ~/.zshrc if present.
var shellProfiles = []string{".bashrc", ".zshrc"}

// ---------------------------------------------------------------------------------------------
// Shell PATH

// addToShellPath prepends dirs, in this order, for every shell: a marked line in ~/.bashrc (and
// ~/.zshrc if present), a file in fish's conf.d if fish is set up, and one in environment.d for
// programs started from the desktop. The shell lines only apply while Flock's bin dir exists, so
// a Flock removed without `flock uninstall` doesn't leave PATH pointing nowhere.
func addToShellPath(dirs ...string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	quoted := make([]string, len(dirs))
	for i, dir := range dirs {
		quoted[i] = strconv.Quote(dir)
	}
	line := fmt.Sprintf(`if [ -d %s ]; then export PATH=%s:"$PATH"; fi`, quoted[0], strings.Join(quoted, ":"))
	for _, name := range shellProfiles {
		path := filepath.Join(home, name)
		if _, err := os.Stat(path); err != nil && name != ".bashrc" {
			continue
		}
		if err := setMarkedLine(path, line); err != nil {
			return err
		}
	}

	if fish := filepath.Join(home, ".config", "fish"); dirExists(fish) {
		script := fmt.Sprintf("%s\nif test -d %s\n    set -gx PATH %s $PATH\nend\n", profileMarker, quoted[0], strings.Join(quoted, " "))
		if err := writeOwnFile(filepath.Join(fish, "conf.d", "flock.fish"), script); err != nil {
			return err
		}
	}

	// environment.d expands ${PATH} but takes no quotes, and systemd's user manager reads it only
	// at login.
	env := fmt.Sprintf("%s\nPATH=%s:${PATH}\n", profileMarker, strings.Join(dirs, ":"))
	return writeOwnFile(filepath.Join(home, ".config", "environment.d", "flock.conf"), env)
}

// setMarkedLine puts line under profileMarker in the file at path: appended once, rewritten when
// it changes.
func setMarkedLine(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(string(data), "\n")
	i := slices.Index(lines, profileMarker)
	if i < 0 {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString("\n" + profileMarker + "\n" + line + "\n")
		return errors.Join(err, f.Close())
	}
	switch {
	case i+1 < len(lines) && lines[i+1] == line:
		return nil
	case i+1 < len(lines) && ownLine(lines[i+1]):
		lines[i+1] = line
	default:
		lines = slices.Insert(lines, i+1, line)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// ownLine reports whether a line after profileMarker is one addToShellPath wrote.
func ownLine(line string) bool {
	return strings.HasPrefix(line, "if [ -d ") && strings.Contains(line, "export PATH=")
}

// writeOwnFile writes a file only Flock writes, creating its directory.
func writeOwnFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// removeFromShellProfiles takes out what addToShellPath added: the marker, its line and the blank
// line before them, and Flock's own fish and environment.d files. It reports whether there was any.
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
		if end < len(lines) && ownLine(lines[end]) {
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
	for _, path := range []string{
		filepath.Join(home, ".config", "fish", "conf.d", "flock.fish"),
		filepath.Join(home, ".config", "environment.d", "flock.conf"),
	} {
		if err := os.Remove(path); err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
	}
	return removed, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ---------------------------------------------------------------------------------------------
// Background service: a systemd system unit per user

// unitTemplate is flock@.service, the daemon of the user it is instantiated for. A system unit
// rather than a user one, so the daemon can be given CAP_NET_BIND_SERVICE for ports 80 and 443.
// Where to find the user's flock and data dir is in each instance's drop-in (instanceDropIn).
const unitTemplate = `# Managed by Flock
[Unit]
Description=Flock local development environment for %i
After=network.target

[Service]
User=%i
AmbientCapabilities=CAP_NET_BIND_SERVICE
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
`

// serviceUser is the user whose daemon this flock manages, and so the unit's instance name.
func serviceUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// unitName is the daemon's unit for name, flock@name.service.
func unitName(name string) string { return "flock@" + name + ".service" }

// packaged reports whether Flock was installed from its .deb or .rpm, which ship flock@.service
// and the polkit rule: the privileged setup then leaves both to the package manager.
func packaged() bool {
	return fileExists(filepath.Join(packagedUnits, "flock@.service"))
}

// packagedBinary is flock in the .deb and .rpm. Tests move it.
var packagedBinary = "/usr/libexec/flockphp/flock"

// linkPackaged makes dst a symlink to the package's flock when self is it. Updates come from the
// package manager (the system's updates, or `flock update`), which replaces only that file: the
// link brings them to the CLI at once, and to the daemon when the package restarts it.
func linkPackaged(self, dst string) (bool, error) {
	if self != packagedBinary {
		return false, nil
	}
	if target, err := os.Readlink(dst); err == nil && target == packagedBinary {
		return true, nil
	}
	// Next to it, then renamed over it: replaces an earlier copy, even a running one.
	tmp := dst + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(packagedBinary, tmp); err != nil {
		return true, err
	}
	return true, os.Rename(tmp, dst)
}

// refreshAfterUpgrade rewrites what `flock install` keeps in the data dir for the binary that
// runs it: an upgrade through the package manager doesn't run `flock install`, so the daemon
// does this when the package restarts it. Only for the package's flock, through the link.
func refreshAfterUpgrade(cmd *cobra.Command, root string) {
	bin := filepath.Join(root, "bin", "flock")
	if target, err := os.Readlink(bin); err != nil || target != packagedBinary {
		return
	}
	if err := writeShims(root, bin); err != nil {
		cmd.PrintErrln("Could not update the shims:", err)
	}
}

// templatePath is where flock@.service is: the package's, or the one the privileged setup writes.
func templatePath() string {
	if packaged() {
		return filepath.Join(packagedUnits, "flock@.service")
	}
	return filepath.Join(unitDir, "flock@.service")
}

// dropInPath is the instance's drop-in, which also marks Flock as set up for the user.
func dropInPath(name string) string {
	return filepath.Join(unitDir, unitName(name)+".d", "flock.conf")
}

// instanceDropIn runs bin's daemon with root as its data dir and the PATH `flock install` gives
// the shell, logging where `flock logs daemon` reads.
func instanceDropIn(root, bin string) string {
	path := strings.Join([]string{filepath.Join(root, "bin"), node.GlobalBin(node.CurrentGlobalDir(root)),
		"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"}, ":")
	return managedMarker + "\n[Service]\n" +
		"Environment=" + systemdQuote("FLOCK_HOME="+root, false) + " " + systemdQuote("PATH="+path, false) + "\n" +
		"ExecStart=\n" +
		"ExecStart=" + systemdQuote(bin, true) + " daemon --log-file " + systemdQuote(filepath.Join(root, "log", "daemon.log"), true) + "\n"
}

// systemdQuote quotes a word for a unit file: double quotes with C escapes, % doubled so systemd
// doesn't take it for a specifier, and in ExecStart= (exec) $ doubled so it isn't a variable.
func systemdQuote(s string, exec bool) string {
	r := []string{`\`, `\\`, `"`, `\"`, "%", "%%"}
	if exec {
		r = append(r, "$", "$$")
	}
	return `"` + strings.NewReplacer(r...).Replace(s) + `"`
}

// polkitRuleContent lets every user start, stop and restart their own Flock daemon without a
// password (flock restart, the app's Restart, flock install after the first one).
const polkitRuleContent = `// Managed by Flock
polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units" &&
        action.lookup("unit") == "flock@" + subject.user + ".service" &&
        ["start", "stop", "restart"].indexOf(action.lookup("verb")) >= 0) {
        return polkit.Result.YES;
    }
});
`

// installService (re)starts the daemon. Registering it needs root and is part of the privileged
// setup, which `flock install` runs first (privilegedFirst).
func installService(root, _ string) error {
	if err := os.MkdirAll(filepath.Join(root, "log"), 0o700); err != nil {
		return err
	}
	name, err := serviceUser()
	if err != nil {
		return err
	}
	if !serviceInstalled() {
		return fmt.Errorf("%s isn't set up — run `flock install` without --skip-privileged", unitName(name))
	}
	return systemctl("restart", unitName(name))
}

// serviceInstalled reports whether the user's daemon unit is set up.
func serviceInstalled() bool {
	name, err := serviceUser()
	return err == nil && fileExists(dropInPath(name))
}

// uninstallService stops the daemon; the privileged setup (--remove) unregisters it.
func uninstallService() error {
	name, err := serviceUser()
	if err != nil {
		return err
	}
	if !serviceInstalled() {
		return nil
	}
	return systemctl("stop", unitName(name))
}

// restartService makes systemd stop and start the daemon.
func restartService() error {
	name, err := serviceUser()
	if err != nil {
		return err
	}
	if !serviceInstalled() {
		return fmt.Errorf("the background daemon is not installed — run `flock install`")
	}
	return systemctl("restart", unitName(name))
}

// systemctl runs systemctl; as the user, the polkit rule allows managing their own daemon.
func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--no-ask-password"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------------------------------------------------------------------------------------------
// Privileged setup

// privilegedFirst: `flock install` runs ensurePrivileged before it replaces anything, so declining
// leaves the installed version as it was; the service it registers is started afterwards.
const privilegedFirst = true

// ensurePrivileged checks what needs root and does what is missing in one sudo or pkexec run:
// the daemon's unit, the polkit rule, the resolved drop-in and the CA in the system trust store.
func ensurePrivileged(cmd *cobra.Command, s *state, _ string, ca *certs.CA) error {
	name, err := serviceUser()
	if err != nil {
		return err
	}
	bin := filepath.Join(s.root, "bin", exeName("flock"))
	anchor := caAnchorPath(name)

	ok := fileExists(templatePath()) &&
		fileHas(dropInPath(name), instanceDropIn(s.root, bin)) &&
		(fileHas(polkitRule, polkitRuleContent) || unreadable(polkitRule)) &&
		dns.ResolvedProblem(s.cfg.TLD, s.cfg.Ports.DNS) == "" &&
		(anchor == "" || sameFile(anchor, ca.CertPath())) &&
		certs.FirefoxPolicyInstalled(name, ca.CertPath())
	if !ok {
		if !dns.ResolvedRunning() {
			return errors.New("systemd-resolved isn't running: Flock sends ." + s.cfg.TLD + " lookups to its DNS server through it")
		}
		cmd.Printf("→ setting up the background service, *.%s DNS and the local CA (needs your password)…\n", s.cfg.TLD)
		self, err := os.Executable()
		if err != nil {
			return err
		}
		if err := runPrivileged(cmd, self, "privileged-setup", "--user", name, "--root", s.root,
			"--tld", s.cfg.TLD, "--dns-port", strconv.Itoa(s.cfg.Ports.DNS), "--ca", ca.CertPath()); err != nil {
			return fmt.Errorf("privileged setup: %w", err)
		}
	}
	cmd.Printf("✓ *.%s resolves via Flock\n", s.cfg.TLD)
	if anchor == "" {
		cmd.Println("! no system trust store found (update-ca-certificates or update-ca-trust): trust Flock's CA yourself")
	} else {
		cmd.Println("✓ Flock CA is trusted")
	}
	// The browsers' own databases are the user's: no password needed. The daemon checks them again
	// when it starts, for browsers installed later.
	if err := certs.TrustInBrowsers(ca.CertPath()); err != nil {
		cmd.Printf("! Chrome and other Chromium browsers don't trust the Flock CA: %v\n", err)
	} else {
		cmd.Println("✓ Firefox, Chrome and other Chromium browsers trust it")
	}
	return nil
}

// caAnchorPath is where the privileged setup puts user's CA in the system trust store, "" when
// the distribution has neither Debian's nor Fedora's tooling. Every user has their own Flock CA.
func caAnchorPath(user string) string {
	for _, a := range caAnchors {
		if dirExists(a.dir) {
			return filepath.Join(a.dir, "flock-"+user+".crt")
		}
	}
	return ""
}

// fileHas reports whether the file at path holds exactly content.
func fileHas(path, content string) bool {
	data, err := os.ReadFile(path)
	return err == nil && string(data) == content
}

// unreadable reports whether path can't be checked because the user may not read it (a
// distribution that locks the polkit rules away): it was written in the same run as the drop-in.
func unreadable(path string) bool {
	_, err := os.ReadFile(path)
	return errors.Is(err, fs.ErrPermission)
}

func sameFile(a, b string) bool {
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

// runPrivileged runs a command as root. At a terminal that is sudo, which asks for the password
// there; without one (the Flock app runs `flock install`) pkexec asks through the desktop's
// polkit agent.
func runPrivileged(cmd *cobra.Command, exe string, args ...string) error {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		sudo := exec.Command("sudo", append([]string{exe}, args...)...)
		sudo.Stdin, sudo.Stdout, sudo.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
		return sudo.Run()
	}

	out, err := exec.Command("pkexec", append([]string{exe}, args...)...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	var exit *exec.ExitError
	switch {
	// 126: the password dialog was dismissed.
	case errors.As(err, &exit) && exit.ExitCode() == 126:
		return errDeclined
	case err != nil:
		return fmt.Errorf("%v: %s", err, text)
	case text != "":
		cmd.Println(text)
	}
	return nil
}

// validUser is what useradd accepts by default, and so what can name a unit instance unescaped.
var validUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

func newPrivilegedSetupCommand() *cobra.Command {
	var name, root, tld, caPath, repo string
	var dnsPort int
	var remove, ifMissing bool

	cmd := &cobra.Command{
		Use:    "privileged-setup",
		Short:  "Root-only steps of install/uninstall (run via sudo or pkexec by flock itself)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Geteuid() != 0 {
				return fmt.Errorf("must run as root")
			}
			// --repo <suite>: Flock's package repository (`flock channel`, and the package's
			// install with --if-missing). Not per user.
			if repo != "" {
				return writeRepo(repo, ifMissing)
			}
			if !validUser.MatchString(name) {
				return fmt.Errorf("invalid --user %q", name)
			}
			if remove {
				return removePrivileged(name)
			}
			if tld == "" || strings.ContainsAny(tld, "/. ") {
				return fmt.Errorf("invalid --tld %q", tld)
			}
			if !filepath.IsAbs(root) || !filepath.IsAbs(caPath) {
				return fmt.Errorf("--root and --ca must be absolute paths")
			}
			return setupPrivileged(name, root, tld, dnsPort, caPath)
		},
	}

	cmd.Flags().StringVar(&name, "user", "", "")
	cmd.Flags().StringVar(&root, "root", "", "")
	cmd.Flags().StringVar(&tld, "tld", "", "")
	cmd.Flags().IntVar(&dnsPort, "dns-port", 0, "")
	cmd.Flags().StringVar(&caPath, "ca", "", "")
	cmd.Flags().BoolVar(&remove, "remove", false, "")
	cmd.Flags().StringVar(&repo, "repo", "", "")
	cmd.Flags().BoolVar(&ifMissing, "if-missing", false, "")

	return cmd
}

// setupPrivileged registers name's daemon, lets them manage it, sends tld to Flock's DNS server
// and trusts the CA system-wide. It is safe to run again: every file is rewritten.
func setupPrivileged(name, root, tld string, dnsPort int, caPath string) error {
	if !packaged() {
		if err := writeOwnFile(templatePath(), unitTemplate); err != nil {
			return err
		}
		if err := writeOwnFile(polkitRule, polkitRuleContent); err != nil {
			return err
		}
	}
	bin := filepath.Join(root, "bin", exeName("flock"))
	if err := writeOwnFile(dropInPath(name), instanceDropIn(root, bin)); err != nil {
		return err
	}
	if err := runRoot("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := runRoot("systemctl", "enable", unitName(name)); err != nil {
		return err
	}

	if !fileHas(dns.ResolvedFile(), dns.ResolvedContent(tld, dnsPort)) {
		if old := resolvedPort(); old != 0 && old != dnsPort {
			unlabelDNSPort(old)
		}
		if err := labelDNSPort(dnsPort); err != nil {
			return err
		}
		if err := writeOwnFile(dns.ResolvedFile(), dns.ResolvedContent(tld, dnsPort)); err != nil {
			return err
		}
		if err := runRoot("systemctl", "restart", "systemd-resolved"); err != nil {
			return err
		}
	}

	if err := certs.InstallFirefoxPolicy(name, caPath); err != nil {
		return err
	}
	return trustSystemCA(name, caPath)
}

// trustSystemCA copies name's CA into the system trust store's sources and rebuilds the store.
func trustSystemCA(name, caPath string) error {
	for _, a := range caAnchors {
		if !dirExists(a.dir) {
			continue
		}
		anchor := filepath.Join(a.dir, "flock-"+name+".crt")
		if sameFile(anchor, caPath) {
			return nil
		}
		data, err := os.ReadFile(caPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(anchor, data, 0o644); err != nil {
			return err
		}
		return runRoot(a.update)
	}
	return nil
}

// labelDNSPort labels Flock's DNS port dns_port_t under SELinux: the policy only lets
// systemd-resolved query DNS ports, and Flock's (53535) is an ephemeral one. The .rpm requires
// semanage (policycoreutils-python-utils) where SELinux is.
func labelDNSPort(port int) error {
	if !dns.SELinux() {
		return nil
	}
	if !hasCommand("semanage") {
		return errors.New("SELinux is on, and labelling Flock's DNS port for systemd-resolved needs semanage: install policycoreutils-python-utils")
	}
	p := strconv.Itoa(port)
	for _, proto := range []string{"tcp", "udp"} {
		// -a fails when the port is labelled already (by an earlier setup): -m relabels it.
		if runRoot("semanage", "port", "-a", "-t", "dns_port_t", "-p", proto, p) != nil {
			if err := runRoot("semanage", "port", "-m", "-t", "dns_port_t", "-p", proto, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// unlabelDNSPort takes labelDNSPort's label off port again, as far as it is there.
func unlabelDNSPort(port int) {
	if !dns.SELinux() || !hasCommand("semanage") {
		return
	}
	for _, proto := range []string{"tcp", "udp"} {
		_ = runRoot("semanage", "port", "-d", "-p", proto, strconv.Itoa(port))
	}
}

// resolvedPort is the DNS port the resolved drop-in sends lookups to, 0 without one.
func resolvedPort() int {
	data, err := os.ReadFile(dns.ResolvedFile())
	if err != nil {
		return 0
	}
	for line := range strings.Lines(string(data)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "DNS=127.0.0.1:"); ok {
			port, _ := strconv.Atoi(rest)
			return port
		}
	}
	return 0
}

// removePrivileged undoes setupPrivileged for name. What other users' daemons share (the
// template, the polkit rule and DNS) goes with the last one.
func removePrivileged(name string) error {
	_ = runRoot("systemctl", "disable", "--now", unitName(name))
	errs := []error{os.RemoveAll(filepath.Dir(dropInPath(name))), certs.RemoveFirefoxPolicy(name)}
	for _, a := range caAnchors {
		anchor := filepath.Join(a.dir, "flock-"+name+".crt")
		if fileExists(anchor) {
			errs = append(errs, os.Remove(anchor), runRoot(a.update))
		}
	}

	others, _ := filepath.Glob(filepath.Join(unitDir, "flock@?*.service.d"))
	if len(others) == 0 {
		if !packaged() {
			for _, path := range []string{filepath.Join(unitDir, "flock@.service"), polkitRule} {
				errs = append(errs, removeManaged(path))
			}
		}
		if fileExists(dns.ResolvedFile()) {
			if port := resolvedPort(); port != 0 {
				unlabelDNSPort(port)
			}
			errs = append(errs, removeManaged(dns.ResolvedFile()))
			errs = append(errs, runRoot("systemctl", "restart", "systemd-resolved"))
		}
	}
	errs = append(errs, runRoot("systemctl", "daemon-reload"))
	return errors.Join(errs...)
}

// removeManaged deletes a file the privileged setup wrote, and nothing someone else put there.
func removeManaged(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(data, []byte(managedMarker)) && !bytes.HasPrefix(data, []byte("// Managed by Flock")) {
		return nil
	}
	return os.Remove(path)
}

func runRoot(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------------------------------------------------------------------------------------------
// Uninstall

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
			name, err := serviceUser()
			if err != nil {
				return err
			}

			if err := uninstallService(); err != nil {
				return err
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			if err := runPrivileged(cmd, self, "privileged-setup", "--remove", "--user", name); err != nil {
				return err
			}
			cmd.Println("✓ background daemon removed")
			if err := certs.UntrustInBrowsers(filepath.Join(s.root, "certs", "ca.crt")); err != nil {
				cmd.Printf("! could not remove the Flock CA from your browsers: %v\n", err)
			}
			cmd.Printf("✓ removed Flock's *.%s DNS setup and the Flock CA's trust\n", s.cfg.TLD)

			switch removed, err := removeFromShellProfiles(); {
			case err != nil:
				cmd.Printf("! could not update your shell profile (%v) — remove the PATH line marked %q\n", err, profileMarker)
			case removed:
				cmd.Println("✓ removed Flock from your PATH (open a new terminal)")
			}

			return finishUninstall(cmd, s.root, purge)
		},
	}
	uninstallFlags(cmd, &purge)
	return cmd
}

// appDirs are the app's own files outside the data dir, removed on uninstall: its settings, the
// start at login, and WebKit's data and cache (named after the program).
func appDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	xdg := func(name, fallback string) string {
		if dir := os.Getenv(name); filepath.IsAbs(dir) {
			return dir
		}
		return filepath.Join(home, fallback)
	}
	config := xdg("XDG_CONFIG_HOME", ".config")
	return []string{
		filepath.Join(config, "flock"),
		filepath.Join(config, "autostart", "dev.flock.Flock.desktop"),
		filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "flock-app"),
		filepath.Join(xdg("XDG_CACHE_HOME", ".cache"), "flock-app"),
	}
}
