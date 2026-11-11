//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configure starts children without a console window of their own, in case the daemon itself has
// no console (otherwise they share its hidden one).
func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}

// killTree kills cmd and the processes it started (those still its descendants), with taskkill.
func killTree(cmd *exec.Cmd) {
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	configure(kill)
	_ = kill.Run()
	_ = cmd.Process.Kill()
}

// shellCommand runs line through cmd.exe. /s makes cmd take what's between the outer quotes as
// typed, so the line's own quotes reach the program; /d skips AutoRun commands.
func shellCommand(line string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = `C:\Windows\System32\cmd.exe`
	}
	cmd := exec.Command(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + shell + `" /d /s /c "` + line + `"`}
	return cmd
}

// terminate kills the process: Windows has no portable graceful signal for console-less children
// (services that can be asked to exit another way set Spec.Stop).
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

// suspend starts the process suspended, so newTree can put it in its job before it starts any
// child. newTree resumes it.
func suspend(cmd *exec.Cmd) {
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// tree is a job object of the child's own, nested in the daemon's (see started): every process
// it starts joins it, and stays in it after the child exits.
type tree struct{ job windows.Handle }

// newTree puts a child started by suspend in a job of its own, then resumes it. It resumes the
// child even when the job can't be made, so the child runs either way.
func newTree(cmd *exec.Cmd) (*tree, error) {
	defer resume(uint32(cmd.Process.Pid))

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}

	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(p)
	if err := windows.AssignProcessToJobObject(job, p); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &tree{job: job}, nil
}

// resume resumes the threads of a process started suspended (its only one, the main thread).
func resume(pid uint32) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snap, &entry); err == nil; err = windows.Thread32Next(snap, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		if th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID); err == nil {
			_, _ = windows.ResumeThread(th)
			_ = windows.CloseHandle(th)
		}
	}
}

// signal ends the tree: Windows has no portable graceful signal for console-less processes.
func (t *tree) signal(os.Signal) { t.kill() }

func (t *tree) kill() { _ = windows.TerminateJobObject(t.job, 1) }

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

// empty reports whether no process is left in the job.
func (t *tree) empty() bool {
	var info jobAccounting
	if err := windows.QueryInformationJobObject(t.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return true
	}
	return info.ActiveProcesses == 0
}

// close closes the job, which kills whatever is still in it.
func (t *tree) close() { _ = windows.CloseHandle(t.job) }
