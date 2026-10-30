//go:build windows

package lsp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel.NewProc("CreateJobObjectW")
var setJob = kernel.NewProc("SetInformationJobObject")
var assignJob = kernel.NewProc("AssignProcessToJobObject")
var terminateJob = kernel.NewProc("TerminateJobObject")
var queryJob = kernel.NewProc("QueryInformationJobObject")

// An application-defined code distinguishes our job termination from an
// ordinary server failure (in particular the common exit status 1).
const serverJobKillExitCode = 0xE0000001

type jobLimits struct {
	PerProcess, PerJob                             int64
	Flags                                          uint32
	MinWorking, MaxWorking                         uintptr
	Active                                         uint32
	Affinity                                       uintptr
	Priority, Scheduling                           uint32
	IO                                             [6]uint64
	ProcessMemory, JobMemory, PeakProcess, PeakJob uintptr
}

type processTree struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	process  syscall.Handle
	job      syscall.Handle
	assigned bool
	stopping bool
	killed   bool
	err      error
	done     chan struct{}
}

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x4 | 0x08000000, HideWindow: true} // suspended, no console
}

func ownProcess(cmd *exec.Cmd) (*processTree, error) {
	t := &processTree{cmd: cmd, done: make(chan struct{})}
	// The suspended, unreaped child cannot recycle its PID before this retained
	// handle is acquired, assigned to the job, and resumed.
	h, err := syscall.OpenProcess(0x0100|0x0001|0x0800|0x00100000, false, uint32(cmd.Process.Pid))
	if err != nil {
		return t.failedStart(fmt.Errorf("open suspended server: %w", err))
	}
	t.process = h
	job, _, err := createJob.Call(0, 0)
	if job == 0 {
		return t.failedStart(fmt.Errorf("create server job: %w", err))
	}
	t.job = syscall.Handle(job)
	limits := jobLimits{Flags: 0x2000} // KILL_ON_JOB_CLOSE
	if ok, _, e := setJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		return t.failedStart(fmt.Errorf("set server job containment: %w", e))
	}
	if ok, _, e := assignJob.Call(job, uintptr(h)); ok == 0 {
		return t.failedStart(fmt.Errorf("assign suspended server to job: %w", e))
	}
	t.assigned = true
	resume := syscall.NewLazyDLL("ntdll.dll").NewProc("NtResumeProcess")
	if status, _, e := resume.Call(uintptr(h)); status != 0 {
		return t.failedStart(fmt.Errorf("resume server: status %x: %v", status, e))
	}
	go t.reap()
	return t, nil
}

func (t *processTree) failedStart(err error) (*processTree, error) {
	t.stop()
	go t.reap()
	if cleanupErr := t.wait(); cleanupErr != nil {
		// Keep the exact owner reachable through the returned error even though
		// no Client was created. Unverified cleanup must not orphan its handles.
		err = &failedProcessCleanup{owner: t, err: errors.Join(err, ErrProcessCleanupUnverified, cleanupErr)}
	}
	return nil, err
}

type failedProcessCleanup struct {
	owner *processTree
	err   error
}

func (e *failedProcessCleanup) Error() string { return e.err.Error() }
func (e *failedProcessCleanup) Unwrap() error { return e.err }

func (t *processTree) reap() {
	waitErr := t.cmd.Wait()
	t.mu.Lock()
	var exit *exec.ExitError
	killCode := uint32(1) // os.Process.Kill during suspended startup cleanup
	if t.assigned {
		killCode = serverJobKillExitCode
	}
	if waitErr != nil && !(t.killed && errors.As(waitErr, &exit) && uint32(exit.ExitCode()) == killCode) {
		t.err = errors.Join(t.err, fmt.Errorf("reap server: %w", waitErr))
	}
	t.mu.Unlock()
	t.stop()
	// Leader exit is not job exit. Query the retained exact job, not PIDs, and
	// do not report success until every member has left it.
	var jobErr error
	if t.assigned {
		jobErr = waitJobEmpty(t.job, time.Now().Add(5*time.Second))
	}
	t.mu.Lock()
	t.err = errors.Join(t.err, jobErr)
	if t.job != 0 {
		if err := syscall.CloseHandle(t.job); err != nil {
			t.err = errors.Join(t.err, fmt.Errorf("close server job: %w", err))
		} else {
			t.job = 0
		}
	}
	if t.process != 0 {
		if err := syscall.CloseHandle(t.process); err != nil {
			t.err = errors.Join(t.err, fmt.Errorf("close retained server process: %w", err))
		} else {
			t.process = 0
		}
	}
	t.mu.Unlock()
	close(t.done)
}

func (t *processTree) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopping {
		return
	}
	t.stopping = true
	if t.assigned {
		if ok, _, err := terminateJob.Call(uintptr(t.job), serverJobKillExitCode); ok == 0 {
			t.err = errors.Join(t.err, fmt.Errorf("terminate server job; containment unproven: %w", err))
		} else {
			t.killed = true
		}
	} else {
		// Setup failed before assignment. The process is still suspended, so it
		// could not have spawned descendants outside our job.
		if err := t.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.err = errors.Join(t.err, fmt.Errorf("kill suspended server: %w", err))
		} else if err == nil {
			t.killed = true
		}
	}
}

type jobAccounting struct {
	UserTime, KernelTime, PeriodUserTime, PeriodKernelTime  int64
	PageFaults, TotalProcesses, ActiveProcesses, Terminated uint32
}

func waitJobEmpty(job syscall.Handle, deadline time.Time) error {
	for {
		var info jobAccounting
		if ok, _, err := queryJob.Call(uintptr(job), 1, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0); ok == 0 {
			return fmt.Errorf("query server job; containment unproven: %w", err)
		}
		if info.ActiveProcesses == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("server job still has %d active processes; containment unproven", info.ActiveProcesses)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (t *processTree) wait() error {
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	select {
	case <-t.done:
	case <-timer.C:
		t.mu.Lock()
		defer t.mu.Unlock()
		return errors.Join(t.err, errors.New("server shutdown timed out; job termination and reap completion unproven"))
	}
	return t.err
}
