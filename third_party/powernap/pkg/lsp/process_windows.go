//go:build windows

package lsp

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel.NewProc("CreateJobObjectW")
var setJob = kernel.NewProc("SetInformationJobObject")
var assignJob = kernel.NewProc("AssignProcessToJobObject")
var terminateJob = kernel.NewProc("TerminateJobObject")

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
	mu   sync.Mutex
	job  syscall.Handle
	done chan struct{}
}

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x4 | 0x08000000, HideWindow: true} // suspended, no console
}

func ownProcess(cmd *exec.Cmd) (*processTree, error) {
	// The suspended, unreaped child cannot recycle its PID before this retained
	// handle is acquired, assigned to the job, and resumed.
	h, err := syscall.OpenProcess(0x0100|0x0001|0x0800|0x00100000, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, err
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	job, _, err := createJob.Call(0, 0)
	if job == 0 {
		return nil, err
	}
	fail := func(err error) (*processTree, error) { _ = syscall.CloseHandle(syscall.Handle(job)); return nil, err }
	limits := jobLimits{Flags: 0x2000} // KILL_ON_JOB_CLOSE
	if ok, _, e := setJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		return fail(e)
	}
	if ok, _, e := assignJob.Call(job, uintptr(h)); ok == 0 {
		return fail(e)
	}
	resume := syscall.NewLazyDLL("ntdll.dll").NewProc("NtResumeProcess")
	if status, _, e := resume.Call(uintptr(h)); status != 0 {
		return fail(fmt.Errorf("resume server: status %x: %v", status, e))
	}
	t := &processTree{job: syscall.Handle(job), done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		t.stop()
		t.mu.Lock()
		_ = syscall.CloseHandle(t.job)
		t.job = 0
		t.mu.Unlock()
		close(t.done)
	}()
	return t, nil
}

func (t *processTree) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_, _, _ = terminateJob.Call(uintptr(t.job), 1)
	}
}
