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

// WindowsProcessTree owns one kill-on-close Job Object, never a PID lookup.
type WindowsProcessTree struct {
	mu      sync.Mutex
	job     windows.Handle
	stopped bool
}

// OwnWindowsProcessTree assigns a retained, suspended process to its lifetime.
// The caller retains responsibility for resuming and closing the process handle.
func OwnWindowsProcessTree(process windows.Handle) (*WindowsProcessTree, error) {
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
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		closeJob()
		return nil, fmt.Errorf("assign process job: %w", err)
	}
	return &WindowsProcessTree{job: job}, nil
}

func (tree *WindowsProcessTree) Terminate() error {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.job == 0 {
		return os.ErrProcessDone
	}
	if tree.stopped {
		return nil
	}
	if err := windows.TerminateJobObject(tree.job, 1); err != nil {
		return err
	}
	tree.stopped = true
	return nil
}

func (tree *WindowsProcessTree) Close() error {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.job == 0 {
		return nil
	}
	if err := windows.CloseHandle(tree.job); err != nil {
		return err
	}
	tree.job = 0
	return nil
}

func ownProcessTree(cmd *exec.Cmd) (*ownedCommandTree, error) {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, fmt.Errorf("open child process: %w", err)
	}
	tree, err := OwnWindowsProcessTree(process)
	if err != nil {
		_ = windows.CloseHandle(process)
		return nil, err
	}
	return &ownedCommandTree{
		kill: tree.Terminate,
		wait: func() error {
			status, err := windows.WaitForSingleObject(process, windows.INFINITE)
			if err != nil || status != windows.WAIT_OBJECT_0 {
				_ = tree.Terminate()
				_ = cmd.Wait()
				return fmt.Errorf("wait for owned command: status %d: %v", status, err)
			}
			_ = tree.Terminate()
			return cmd.Wait()
		},
		close: sync.OnceFunc(func() {
			_ = windows.CloseHandle(process)
			_ = tree.Close()
		}),
	}, nil
}

func resumeOwnedProcess(cmd *exec.Cmd) error {
	const processSuspendResume = 0x0800
	process, err := windows.OpenProcess(processSuspendResume, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open suspended process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	status, _, callErr := resume.Call(uintptr(process))
	if status != 0 {
		return fmt.Errorf("resume owned process: status 0x%x: %w", status, callErr)
	}
	return nil
}
