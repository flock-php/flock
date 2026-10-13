package cli

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"flock/internal/certs"
	"flock/internal/hosts"
	"flock/internal/paths"
)

// On Windows, `flock install` sets up:
//
//   - <root>\bin\php.exe, composer.exe, ... — hard links to flock.exe, which acts as
//     `flock exec <tool>` when started under a tool's name (see ShimTool)
//   - <root>\bin on the user's PATH (HKCU\Environment)
//   - flockd.exe, a copy of flock.exe marked as a GUI program so it runs without a console window,
//     started at logon by the "Flock Daemon" scheduled task as `flockd daemon --watchdog`
//   - with administrator rights, once: the hosts helper service (see package hosts) and the
//     Flock CA in the machine's trusted root store
const (
	taskName   = "Flock Daemon"
	runKeyName = "Flock Daemon"
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
)

func exeName(name string) string { return name + ".exe" }

// writeShims links every tool name to the flock binary.
func writeShims(root, bin string) error {
	dir := filepath.Join(root, "bin")
	data, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	for _, tool := range tools {
		dst := filepath.Join(dir, exeName(tool))
		if err := placeLink(bin, dst, data); err != nil {
			return fmt.Errorf("%s: %w", dst, err)
		}
	}
	return nil
}

// placeLink makes dst a hard link to src (a copy where links are not possible), moving a running
// dst aside first: Windows cannot delete a running executable but can rename it.
func placeLink(src, dst string, data []byte) error {
	if old, _ := filepath.Glob(dst + ".old*"); len(old) > 0 {
		for _, o := range old {
			_ = os.Remove(o)
		}
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(dst, fmt.Sprintf("%s.old%d", dst, time.Now().UnixNano())); err != nil {
			return err
		}
	}
	if os.Link(src, dst) == nil {
		return nil
	}
	return os.WriteFile(dst, data, 0o755)
}

// addToShellPath prepends dir to the user's PATH and tells running programs about it.
func addToShellPath(dir string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	current, kind, err := k.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	if kind == 0 {
		kind = registry.EXPAND_SZ
	}
	for _, entry := range filepath.SplitList(current) {
		if strings.EqualFold(filepath.Clean(entry), filepath.Clean(dir)) {
			warnSystemPHP(dir)
			return nil
		}
	}

	value := dir
	if current != "" {
		value += ";" + current
	}
	if kind == registry.SZ {
		err = k.SetStringValue("Path", value)
	} else {
		err = k.SetExpandStringValue("Path", value)
	}
	if err != nil {
		return err
	}
	broadcastEnvironmentChange()
	fmt.Printf("✓ %s added to your PATH (open a new terminal to use it)\n", dir)
	warnSystemPHP(dir)
	return nil
}

// warnSystemPHP points out PHP installations on the system PATH, which Windows puts before the
// user's PATH and therefore before Flock's shims.
func warnSystemPHP(dir string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	systemPath, _, _ := k.GetStringValue("Path")
	for _, entry := range filepath.SplitList(systemPath) {
		entry, _ = registry.ExpandString(entry)
		for _, tool := range []string{"php.exe", "composer.bat", "node.exe"} {
			if _, err := os.Stat(filepath.Join(entry, tool)); err == nil {
				fmt.Printf("! %s is on the system PATH, which Windows searches before %s — remove it there so `%s` runs Flock's version\n",
					filepath.Join(entry, tool), dir, strings.TrimSuffix(strings.TrimSuffix(tool, ".exe"), ".bat"))
			}
		}
	}
}

func removeFromShellPath(dir string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	current, kind, err := k.GetStringValue("Path")
	if err != nil {
		return
	}
	var kept []string
	for _, entry := range filepath.SplitList(current) {
		if entry != "" && !strings.EqualFold(filepath.Clean(entry), filepath.Clean(dir)) {
			kept = append(kept, entry)
		}
	}
	value := strings.Join(kept, ";")
	if kind == registry.SZ {
		_ = k.SetStringValue("Path", value)
	} else {
		_ = k.SetExpandStringValue("Path", value)
	}
	broadcastEnvironmentChange()
}

// broadcastEnvironmentChange makes Explorer (and so new terminals) pick up the new PATH.
func broadcastEnvironmentChange() {
	env, _ := windows.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	var result uintptr
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(
		hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}

// daemonCommand is what the scheduled task (or the Run key) starts.
func daemonCommand(root string) (exe string, args []string) {
	return filepath.Join(root, "bin", "flockd.exe"),
		[]string{"daemon", "--watchdog", "--log-file", filepath.Join(root, "log", "daemon.log")}
}

// installService writes flockd.exe and registers a scheduled task that starts it at logon.
func installService(root, bin string) error {
	if err := os.MkdirAll(filepath.Join(root, "log"), 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	gui, err := guiSubsystem(data)
	if err != nil {
		return fmt.Errorf("flockd.exe: %w", err)
	}

	// Stop the running daemon first: it holds flockd.exe and the ports.
	_ = exec.Command("schtasks", "/End", "/TN", taskName).Run()
	exe, args := daemonCommand(root)
	if err := replaceExecutable(exe, gui); err != nil {
		return err
	}

	if err := registerTask(exe, args); err != nil {
		// Some policies forbid scheduled tasks for standard users; fall back to the Run key,
		// which starts the daemon at logon but only restarts it through the watchdog.
		fmt.Printf("! could not register a scheduled task (%v); starting the daemon at logon via the Run key instead\n", err)
		return installRunKey(exe, args)
	}
	removeRunKey()

	if out, err := exec.Command("schtasks", "/Run", "/TN", taskName).CombinedOutput(); err != nil {
		return fmt.Errorf("start the daemon: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func registerTask(exe string, args []string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}

	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}

	// Priority 4 is "normal": the default (7) runs the daemon, and so PHP, below normal priority.
	task := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Runs Flock (sites, PHP, mail, databases) in the background while you are signed in.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><UserId>%[1]s</UserId></LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author"><UserId>%[1]s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>4</Priority>
    <RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec><Command>%[2]s</Command><Arguments>%[3]s</Arguments></Exec>
  </Actions>
</Task>
`, xmlEscape(u.Uid), xmlEscape(exe), xmlEscape(strings.Join(quoted, " ")))

	// schtasks reads task XML as UTF-16 (with a byte order mark).
	encoded := utf16.Encode([]rune(task))
	buf := make([]byte, 2, 2+2*len(encoded))
	buf[0], buf[1] = 0xFF, 0xFE
	for _, r := range encoded {
		buf = append(buf, byte(r), byte(r>>8))
	}
	file, err := os.CreateTemp("", "flock-task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(buf); err != nil {
		file.Close()
		return err
	}
	file.Close()

	out, err := exec.Command("schtasks", "/Create", "/TN", taskName, "/XML", file.Name(), "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func taskInstalled() bool {
	return exec.Command("schtasks", "/Query", "/TN", taskName).Run() == nil
}

func installRunKey(exe string, args []string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	line := syscall.EscapeArg(exe)
	for _, a := range args {
		line += " " + syscall.EscapeArg(a)
	}
	if err := k.SetStringValue(runKeyName, line); err != nil {
		return err
	}
	return startDetached(exe, args)
}

func removeRunKey() {
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(runKeyName)
		k.Close()
	}
}

func runKeyInstalled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runKeyName)
	return err == nil
}

// startDetached starts the daemon outside this process' job and console, so it survives the
// terminal (or app) that ran flock.
func startDetached(exe string, args []string) error {
	const flags = windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := cmd.Start(); err != nil {
		// The job we run in may not allow breaking away.
		cmd = exec.Command(exe, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
	}
	return cmd.Process.Release()
}

func uninstallService() error {
	if taskInstalled() {
		_ = exec.Command("schtasks", "/End", "/TN", taskName).Run()
		if out, err := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F").CombinedOutput(); err != nil {
			return fmt.Errorf("remove the scheduled task: %v: %s", err, bytes.TrimSpace(out))
		}
	}
	if runKeyInstalled() {
		removeRunKey()
		stopDaemonProcess()
	}
	return nil
}

// stopDaemonProcess ends a daemon that was not started by the scheduled task (Run key fallback)
// by ending its watchdog, whose job object takes the daemon and its services down.
func stopDaemonProcess() {
	_ = exec.Command("taskkill", "/F", "/IM", "flockd.exe").Run()
}

// restartService ends the scheduled task (killing the daemon and everything it runs) and starts it again.
func restartService() error {
	root, err := paths.Root()
	if err != nil {
		return err
	}
	if taskInstalled() {
		_ = exec.Command("schtasks", "/End", "/TN", taskName).Run()
		if out, err := exec.Command("schtasks", "/Run", "/TN", taskName).CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks /Run: %v: %s", err, bytes.TrimSpace(out))
		}
		return nil
	}
	if runKeyInstalled() {
		stopDaemonProcess()
		exe, args := daemonCommand(root)
		return startDetached(exe, args)
	}
	return fmt.Errorf("the background daemon is not installed — run `flock install`")
}

// ensurePrivileged checks the administrator-owned parts and, if anything is missing or outdated,
// re-runs flock elevated (Windows shows a UAC prompt) to fix them.
func ensurePrivileged(cmd *cobra.Command, s *state, bin string, ca *certs.CA) error {
	source := hosts.SourceFile(s.root)
	helper := hosts.HelperStatus()
	helperOK := helper.Configured(source, s.cfg.TLD) && helper.Running && sameFile(bin, hosts.HelperExe())
	trusted := exec.Command("certutil", "-store", "Root", ca.Fingerprint()).Run() == nil

	if helperOK && trusted {
		cmd.Printf("✓ *.%s sites are added to the hosts file\n✓ Flock CA is trusted\n", s.cfg.TLD)
		return nil
	}

	cmd.Println("→ installing the hosts helper and trusting the local CA (Windows asks for permission)…")
	err := runElevated(bin, "privileged-setup", "--tld", s.cfg.TLD, "--ca", ca.CertPath(), "--source", source)
	if err != nil {
		return fmt.Errorf("privileged setup: %w", err)
	}
	cmd.Printf("✓ *.%s sites are added to the hosts file\n✓ Flock CA is trusted\n", s.cfg.TLD)
	return nil
}

// sameFile reports whether two files have the same contents (the helper is a copy of flock.exe).
func sameFile(a, b string) bool {
	da, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	db, err := os.ReadFile(b)
	if err != nil {
		return false
	}
	return sha256.Sum256(da) == sha256.Sum256(db)
}

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     windows.Handle
}

// runElevated runs exe with administrator rights (UAC prompt), waits for it and returns its error.
// The elevated process has no access to our console, so it reports errors through a file.
func runElevated(exe string, args ...string) error {
	result, err := os.CreateTemp("", "flock-elevated-*.txt")
	if err != nil {
		return err
	}
	result.Close()
	defer os.Remove(result.Name())

	args = append(args, "--result-file", result.Name())
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}

	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	const seeMaskNoCloseProcess, seeMaskNoAsync, swHide = 0x40, 0x100, 0
	info := shellExecuteInfo{fMask: seeMaskNoCloseProcess | seeMaskNoAsync, lpVerb: verb, lpFile: file, lpParameters: params, nShow: swHide}
	info.cbSize = uint32(unsafe.Sizeof(info))

	ok, _, callErr := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return errors.New("administrator permission was declined")
		}
		return callErr
	}
	defer windows.CloseHandle(info.hProcess)

	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return err
	}
	if code != 0 {
		if msg, _ := os.ReadFile(result.Name()); len(bytes.TrimSpace(msg)) > 0 {
			return errors.New(string(bytes.TrimSpace(msg)))
		}
		return fmt.Errorf("the elevated setup failed with exit code %d", code)
	}
	return nil
}

func newPrivilegedSetupCommand() *cobra.Command {
	var tld, caPath, source, resultFile string
	var remove bool

	cmd := &cobra.Command{
		Use:    "privileged-setup",
		Short:  "Administrator-only steps of install/uninstall (run elevated by flock itself)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := privilegedSetup(tld, caPath, source, remove)
			if resultFile != "" && err != nil {
				_ = os.WriteFile(resultFile, []byte(err.Error()), 0o644)
			}
			return err
		},
	}

	cmd.Flags().StringVar(&tld, "tld", "", "")
	cmd.Flags().StringVar(&caPath, "ca", "", "")
	cmd.Flags().StringVar(&source, "source", "", "")
	cmd.Flags().StringVar(&resultFile, "result-file", "", "")
	cmd.Flags().BoolVar(&remove, "remove", false, "")
	return cmd
}

func privilegedSetup(tld, caPath, source string, remove bool) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("must run as administrator")
	}

	if remove {
		err := hosts.RemoveHelper()
		if caPath != "" {
			if fp, ferr := certs.FileFingerprint(caPath); ferr == nil {
				_ = exec.Command("certutil", "-delstore", "Root", fp).Run()
			}
		}
		return err
	}

	if tld == "" || strings.ContainsAny(tld, `/\. `) {
		return fmt.Errorf("invalid --tld %q", tld)
	}
	if !filepath.IsAbs(source) || filepath.Base(source) != "hosts.txt" {
		return fmt.Errorf("invalid --source %q", source)
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := hosts.InstallHelper(self, source, tld); err != nil {
		return fmt.Errorf("hosts helper: %w", err)
	}

	out, err := exec.Command("certutil", "-addstore", "-f", "Root", caPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("trust CA: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func newUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the background daemon and remove the hosts helper and CA trust (keeps your data and config)",
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

			removeFromShellPath(filepath.Join(s.root, "bin"))
			cmd.Println("✓ removed Flock from your PATH")

			self, _ := os.Executable()
			if err := runElevated(self, "privileged-setup", "--remove", "--ca", filepath.Join(s.root, "certs", "ca.crt")); err != nil {
				return err
			}
			cmd.Println("✓ removed the hosts helper, Flock's hosts entries and CA trust")
			cmd.Printf("data is kept in %s\n", s.root)
			return nil
		},
	}
}
