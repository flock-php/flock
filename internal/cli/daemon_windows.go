//go:build windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"
)

// redirectOutput points stdout and stderr at f. flockd.exe is a GUI program without a console,
// so without this, output and Go runtime panics would be lost.
func redirectOutput(f *os.File) {
	_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(f.Fd()))
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
	os.Stdout, os.Stderr = f, f
}

// runWatchdog keeps `flock daemon` running, like launchd's KeepAlive on macOS: it restarts the
// daemon with backoff when it crashes. The daemon runs in a job object that is closed when the
// watchdog ends, so ending the scheduled task (`flock restart`) takes the daemon and everything it
// started down with it.
func runWatchdog(cmd *cobra.Command, logFile string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// The daemon runs as the console flock.exe next to flockd.exe, in a console without a window:
	// the programs it starts (php-cgi, postgres, php -v, ...) inherit that console. Started from
	// the windowless flockd.exe directly, every one of them would open a console window.
	daemonExe := self
	if console := filepath.Join(filepath.Dir(self), "flock.exe"); !strings.EqualFold(console, self) {
		if _, err := os.Stat(console); err == nil {
			daemonExe = console
		}
	}
	job := killOnCloseJob()

	backoff := time.Second
	for {
		started := time.Now()
		args := []string{"daemon"}
		if logFile != "" {
			args = append(args, "--log-file", logFile)
		}
		child := exec.Command(daemonExe, args...)
		child.Stdout, child.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
		child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}

		if err := child.Start(); err != nil {
			cmd.Printf("[watchdog] %s could not start the daemon: %v\n", time.Now().Format(time.RFC3339), err)
		} else {
			assignToJob(job, child.Process.Pid)
			_ = child.Wait()
			switch code := child.ProcessState.ExitCode(); code {
			case 0, exitAlreadyRunning:
				return nil
			default:
				cmd.Printf("[watchdog] %s daemon exited with code %d\n", time.Now().Format(time.RFC3339), code)
			}
		}

		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		time.Sleep(backoff)
		backoff = min(backoff*2, 30*time.Second)
	}
}

func killOnCloseJob() windows.Handle {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	return job
}

func assignToJob(job windows.Handle, pid int) {
	if job == 0 {
		return
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(p)
	_ = windows.AssignProcessToJobObject(job, p)
}
