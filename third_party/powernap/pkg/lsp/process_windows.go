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
var createCompletionPort = kernel.NewProc("CreateIoCompletionPort")
var getCompletion = kernel.NewProc("GetQueuedCompletionStatus")

type jobCompletionAssociation struct {
	key  uintptr
	port syscall.Handle
}

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
	port     syscall.Handle
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
	port, _, e := createCompletionPort.Call(^uintptr(0), 0, 0, 1)
	if port == 0 {
		return t.failedStart(fmt.Errorf("create server job completion port: %w", e))
	}
	t.port = syscall.Handle(port)
	association := jobCompletionAssociation{key: job, port: t.port}
	if ok, _, e := setJob.Call(job, 7, uintptr(unsafe.Pointer(&association)), unsafe.Sizeof(association)); ok == 0 {
		return t.failedStart(fmt.Errorf("associate server job completion port: %w", e))
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
	// Leader exit is not job exit. Await the retained job's completion message
	// before reporting success.
	var jobErr error
	if t.assigned {
		jobErr = waitJobEmpty(t.port, time.Now().Add(5*time.Second))
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
	if t.port != 0 {
		if err := syscall.CloseHandle(t.port); err != nil {
			t.err = errors.Join(t.err, fmt.Errorf("close server job completion port: %w", err))
		} else {
			t.port = 0
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

func waitJobEmpty(port syscall.Handle, deadline time.Time) error {
	if port == 0 {
		return fmt.Errorf("wait for server job completion; containment unproven: %w", syscall.Errno(6))
	}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("server job completion timed out; containment unproven")
		}
		ms := uint32((remaining + time.Millisecond - 1) / time.Millisecond)
		var message uint32
		var key, overlapped uintptr
		if ok, _, err := getCompletion.Call(uintptr(port), uintptr(unsafe.Pointer(&message)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&overlapped)), uintptr(ms)); ok == 0 {
			return fmt.Errorf("wait for server job completion; containment unproven: %w", err)
		}
		if message == 4 { // JOB_OBJECT_MSG_ACTIVE_PROCESS_ZERO
			return nil
		}
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
