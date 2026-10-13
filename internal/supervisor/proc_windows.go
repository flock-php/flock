//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configure starts children without a console window of their own, in case the daemon itself has
// no console (otherwise they share its hidden one).
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

// terminate kills the process; Windows has no portable graceful signal for console-less children.
func terminate(cmd *exec.Cmd, _ os.Signal) {
	_ = cmd.Process.Kill()
}

var (
	jobOnce sync.Once
	job     windows.Handle
)

// started adds a child to a job object that is closed (killing every member) when the daemon
// exits, however it exits. Without it, php-cgi and postgres outlive a killed daemon and keep
// their ports, so the next daemon cannot start them. Children they spawn join the job too.
func started(cmd *exec.Cmd) {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			_ = windows.CloseHandle(h)
			return
		}
		job = h // never closed: the OS closes it when the daemon exits
	})
	if job == 0 {
		return
	}

	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(p)
	_ = windows.AssignProcessToJobObject(job, p)
}
