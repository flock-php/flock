//go:build windows

package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// File is the system hosts file.
func File() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "etc", "hosts")
}

// HelperDir is where the helper's executable lives: under Program Files, so only administrators
// can replace the program that runs as LocalSystem.
func HelperDir() string {
	dir := os.Getenv("ProgramFiles")
	if dir == "" {
		dir = `C:\Program Files`
	}
	return filepath.Join(dir, "Flock")
}

// HelperName is the helper's executable name (cmd/flock-helper), shipped next to flock.exe.
const HelperName = "flock-helper.exe"

// HelperExe is the installed helper's executable.
func HelperExe() string { return filepath.Join(HelperDir(), HelperName) }

// HelperRevision is the version of what the helper does (cmd/flock-helper, RunHelper and what it
// calls). Raise it with every change to them: `flock install` replaces the installed helper, which
// needs administrator permission, only when its revision differs, not with every Flock release.
const HelperRevision = 1

// revisionValue is where InstallHelper records the helper's revision: in the service's registry
// key, which every user can read.
const revisionValue = "FlockRevision"

const serviceKey = `SYSTEM\CurrentControlSet\Services\` + ServiceName

// Status describes the installed helper as any user can see it.
type Status struct {
	Installed bool
	Running   bool
	Command   string // the service's command line, to check its TLD and source file
	Revision  int    // the installed helper's HelperRevision (0: a helper from before revisions)
}

// HelperStatus queries the service with the rights every user has (no administrator needed).
func HelperStatus() Status {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return Status{}
	}
	defer windows.CloseServiceHandle(scm)

	name, _ := windows.UTF16PtrFromString(ServiceName)
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return Status{}
	}
	s := &mgr.Service{Name: ServiceName, Handle: h}
	defer s.Close()

	st := Status{Installed: true}
	if q, err := s.Query(); err == nil {
		st.Running = q.State == svc.Running
	}
	if c, err := s.Config(); err == nil {
		st.Command = c.BinaryPathName
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, serviceKey, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue(revisionValue); err == nil {
			st.Revision = int(v)
		}
		k.Close()
	}
	return st
}

// Args are the helper's service arguments.
func Args(source, tld string) []string {
	return []string{"--source", source, "--tld", tld}
}

// Configured reports whether the installed helper runs from HelperExe and reads source for tld.
// The command line is compared the way the service manager builds it (mgr.CreateService).
func (s Status) Configured(source, tld string) bool {
	want := syscall.EscapeArg(HelperExe())
	for _, a := range Args(source, tld) {
		want += " " + syscall.EscapeArg(a)
	}
	return s.Installed && strings.EqualFold(s.Command, want)
}

// InstallHelper (administrator only) copies exe (a flock-helper.exe) to HelperExe and (re)creates the service.
func InstallHelper(exe, source, tld string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service manager: %w", err)
	}
	defer m.Disconnect()

	// The running helper locks its executable; stop it before replacing it.
	s, err := m.OpenService(ServiceName)
	if err == nil {
		stop(s)
		if err := s.Delete(); err != nil {
			s.Close()
			return fmt.Errorf("remove old hosts helper: %w", err)
		}
		s.Close()
		waitDeleted(m)
	}

	if err := os.MkdirAll(HelperDir(), 0o755); err != nil {
		return err
	}
	if err := copyExe(exe, HelperExe()); err != nil {
		return fmt.Errorf("copy helper: %w", err)
	}

	s, err = m.CreateService(ServiceName, HelperExe(), mgr.Config{
		DisplayName: "Flock hosts helper",
		Description: "Adds the domains of your Flock sites to the hosts file.",
		StartType:   mgr.StartAutomatic,
	}, Args(source, tld)...)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, serviceKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("record helper revision: %w", err)
	}
	err = k.SetDWordValue(revisionValue, HelperRevision)
	k.Close()
	if err != nil {
		return fmt.Errorf("record helper revision: %w", err)
	}

	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 24*60*60)

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

// IsBlocked reports whether err is Windows refusing to run the helper: Smart App Control or an
// App Control policy, which block unsigned programs from running as a service.
func IsBlocked(err error) bool {
	return errors.Is(err, windows.ERROR_SYSTEM_INTEGRITY_POLICY_VIOLATION)
}

// RemoveHelper (administrator only) deletes the service, its executable and the hosts block.
func RemoveHelper() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service manager: %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(ServiceName); err == nil {
		stop(s)
		_ = s.Delete()
		s.Close()
		waitDeleted(m)
	}
	_ = os.RemoveAll(HelperDir())
	return apply(nil)
}

func stop(s *mgr.Service) {
	if _, err := s.Control(svc.Stop); err != nil {
		return
	}
	for range 50 {
		if q, err := s.Query(); err != nil || q.State == svc.Stopped {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitDeleted waits until a deleted service is gone (it lingers while handles are open).
func waitDeleted(m *mgr.Mgr) {
	for range 50 {
		s, err := m.OpenService(ServiceName)
		if err != nil {
			return
		}
		s.Close()
		time.Sleep(100 * time.Millisecond)
	}
}

func copyExe(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		// Still mapped by a process that is shutting down: move it aside, which Windows allows.
		aside := dst + ".old"
		_ = os.Remove(aside)
		if os.Rename(dst, aside) != nil {
			return err
		}
		return os.Rename(tmp, dst)
	}
	return nil
}

// RunHelper is the service's main loop: it mirrors source into the hosts file.
// A service has no console, so failures also go to HelperLog.
func RunHelper(source, tld string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
		if err != nil {
			logHelper(err)
		}
	}()
	if !label.MatchString(tld) {
		return fmt.Errorf("invalid --tld %q", tld)
	}
	inService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("detect service: %w", err)
	}
	if !inService {
		return errors.New("flock-helper only runs as the Flock hosts service (installed by `flock install`)")
	}
	if err := svc.Run(ServiceName, &helper{source: source, tld: tld}); err != nil {
		return fmt.Errorf("run service: %w", err)
	}
	return nil
}

// HelperLog is where the helper records why it stopped.
func HelperLog() string { return filepath.Join(HelperDir(), "hosts-helper.log") }

func logHelper(err error) {
	f, ferr := os.OpenFile(HelperLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %v\n", time.Now().Format(time.RFC3339), err)
}

type helper struct {
	source, tld string
}

func (h *helper) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	defer func() {
		if r := recover(); r != nil {
			logHelper(fmt.Errorf("panic: %v", r))
			panic(r)
		}
	}()
	status <- svc.Status{State: svc.StartPending}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	var last []byte
	sync := func() {
		data, err := os.ReadFile(h.source)
		if err != nil {
			data = nil // no source (Flock uninstalled for this user): remove the entries
		}
		if last != nil && bytes.Equal(data, last) {
			return
		}
		if apply(ParseSource(data, h.tld)) == nil {
			last = data
		}
	}

	sync()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			sync()
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- r.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				return false, 0
			}
		}
	}
}

// apply writes domains into the hosts file when they differ from what is there.
// Antivirus tools sometimes hold the file briefly, so writes are retried.
func apply(domains []string) error {
	path := File()
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next := Render(current, domains)
	if bytes.Equal(current, next) {
		return nil
	}
	for range 5 {
		if err = os.WriteFile(path, next, 0o644); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}
