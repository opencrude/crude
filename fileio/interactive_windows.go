//go:build windows

package fileio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type commandConsole struct {
	input, output *os.File
	hpc           windows.Handle
	once          sync.Once
}

func (c *commandConsole) Write(data []byte) (int, error) { return c.input.Write(data) }
func (c *commandConsole) Close() error {
	c.once.Do(func() {
		_ = c.input.Close()
		// The output pump stays alive while ClosePseudoConsole drains.
		windows.ClosePseudoConsole(c.hpc)
		_ = c.output.Close()
	})
	return nil
}

func startInteractiveProcess(cmd *exec.Cmd, tty bool, output io.Writer) (io.WriteCloser, <-chan struct{}, error) {
	if !tty {
		input, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			_ = input.Close()
			return nil, nil, err
		}
		return input, nil, nil
	}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, nil, err
	}
	defer windows.CloseHandle(inRead)
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(inWrite)
		return nil, nil, err
	}
	defer windows.CloseHandle(outWrite)
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: 120, Y: 40}, inRead, outWrite, 0, &hpc); err != nil {
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, nil, err
	}
	console := &commandConsole{input: os.NewFile(uintptr(inWrite), "exec-stdin"), output: os.NewFile(uintptr(outRead), "exec-stdout"), hpc: hpc}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(output, console.output) }()
	fail := func(err error) (io.WriteCloser, <-chan struct{}, error) { _ = console.Close(); return nil, nil, err }
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(err)
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(hpc), unsafe.Sizeof(hpc)); err != nil {
		return fail(err)
	}
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))}, ProcThreadAttributeList: attrs.List()}
	args := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		args[i] = syscall.EscapeArg(arg)
	}
	line, err := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return fail(err)
	}
	app, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return fail(err)
	}
	var cwd *uint16
	if cmd.Dir != "" {
		cwd, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return fail(err)
		}
	}
	var process windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(app, line, nil, nil, false, flags, nil, cwd, &startup.StartupInfo, &process); err != nil {
		return fail(fmt.Errorf("start ConPTY: %w", err))
	}
	defer windows.CloseHandle(process.Thread)
	defer windows.CloseHandle(process.Process)
	cmd.Process, err = os.FindProcess(int(process.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		return fail(err)
	}
	// The shared command manager assigns the suspended process to its kill-on-
	// close job before resuming it. cmd.Wait reaps this os.Process as usual.
	return console, done, nil
}
