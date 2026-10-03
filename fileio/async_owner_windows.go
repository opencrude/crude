//go:build windows

package fileio

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ownProcessTree(cmd *exec.Cmd) (*ownedCommandTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create process job: %w", err)
	}
	closeJob := func() { _ = windows.CloseHandle(job) }
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		closeJob()
		return nil, fmt.Errorf("set process job limits: %w", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		closeJob()
		return nil, fmt.Errorf("open child process: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(process)
		closeJob()
		return nil, fmt.Errorf("assign process job: %w", err)
	}
	var mutex sync.Mutex
	closed, stopped := false, false
	kill := func() error {
		mutex.Lock()
		defer mutex.Unlock()
		if closed {
			return os.ErrProcessDone
		}
		if stopped {
			return nil
		}
		if err := windows.TerminateJobObject(job, 1); err != nil {
			return err
		}
		stopped = true
		return nil
	}
	return &ownedCommandTree{
		kill: kill,
		wait: func() error {
			status, err := windows.WaitForSingleObject(process, windows.INFINITE)
			if err != nil || status != windows.WAIT_OBJECT_0 {
				_ = kill()
				_ = cmd.Wait()
				return fmt.Errorf("wait for owned command: status %d: %v", status, err)
			}
			_ = kill()
			return cmd.Wait()
		},
		close: func() {
			mutex.Lock()
			defer mutex.Unlock()
			if !closed {
				closed = true
				_ = windows.CloseHandle(process)
				closeJob()
			}
		},
	}, nil
}

func resumeOwnedProcess(cmd *exec.Cmd) error {
	const processSuspendResume = 0x0800
	process, err := windows.OpenProcess(processSuspendResume, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open suspended process: %w", err)
	}
	defer windows.CloseHandle(process)
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	status, _, callErr := resume.Call(uintptr(process))
	if status != 0 {
		return fmt.Errorf("resume owned process: status 0x%x: %w", status, callErr)
	}
	return nil
}
