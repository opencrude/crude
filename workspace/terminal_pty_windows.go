//go:build windows

package workspace

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"crude/fileio"

	"golang.org/x/sys/windows"
)

// conpty implements terminalIO using the Windows ConPTY API
// (CreatePseudoConsole + CreateProcess with STARTUPINFOEX).
type conpty struct {
	hpc              windows.Handle
	procInfo         windows.ProcessInformation
	procHandle       windows.Handle
	readPipe         *os.File
	writePipe        *os.File
	tree             *fileio.WindowsProcessTree
	wait             chan waitResult
	waitDone         chan struct{}
	closed           atomic.Bool
	closeOnce        sync.Once
	consoleCloseOnce sync.Once
	resizeMu         sync.Mutex
	consoleClosed    bool // protected by resizeMu
}

type waitResult struct {
	code int
	err  error
}

func startTerminalIO(command string, args, env []string, cwd string, cols, rows int) (terminalIO, error) {
	// Build the command line. CreateProcess wants a single mutable UTF-16
	// command line string, not an argv. Quote each argument using the
	// CommandLineToArgvW-compatible algorithm so paths with spaces survive.
	cmdLine := buildWindowsCommandLine(command, args)

	// Two pipe pairs: one for PTY input (we write → ConPTY reads),
	// one for PTY output (ConPTY writes → we read).
	var ptyInRead, ptyInWrite, ptyOutRead, ptyOutWrite windows.Handle
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle:      1, // handles inheritable so the ConPTY side can take them
		SecurityDescriptor: nil,
	}
	if err := windows.CreatePipe(&ptyInRead, &ptyInWrite, sa, 0); err != nil {
		return nil, fmt.Errorf("conpty: create input pipe: %w", err)
	}
	if err := windows.CreatePipe(&ptyOutRead, &ptyOutWrite, sa, 0); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(ptyInWrite)
		return nil, fmt.Errorf("conpty: create output pipe: %w", err)
	}

	// The ConPTY takes ownership of ptyInRead and ptyOutWrite. Make sure
	// our side's handles are NOT inherited by the child process; otherwise
	// the child keeps the write end of our input pipe open and Read never
	// sees EOF. SetHandleInformation clears the inherit flag by passing
	// HANDLE_FLAG_INHERIT as the mask and 0 as the flags value.
	if err := windows.SetHandleInformation(ptyInWrite, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		_ = windows.CloseHandle(ptyOutWrite)
		return nil, fmt.Errorf("conpty: protect input write handle: %w", err)
	}
	if err := windows.SetHandleInformation(ptyOutRead, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		_ = windows.CloseHandle(ptyOutWrite)
		return nil, fmt.Errorf("conpty: protect output read handle: %w", err)
	}

	size := windows.Coord{
		X: int16(cols),
		Y: int16(rows),
	}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(size, ptyInRead, ptyOutWrite, 0, &hpc); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		_ = windows.CloseHandle(ptyOutWrite)
		return nil, fmt.Errorf("conpty: CreatePseudoConsole: %w", err)
	}

	// CreatePseudoConsole consumed these handles; close our references so
	// the only outstanding handle on each is inside the ConPTY.
	_ = windows.CloseHandle(ptyInRead)
	_ = windows.CloseHandle(ptyOutWrite)

	// Build the PROC_THREAD_ATTRIBUTE_LIST with the PSEUDOCONSOLE attribute.
	// The attribute list is embedded in STARTUPINFOEX so CreateProcess can
	// attach the child to the pseudo console.
	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, fmt.Errorf("conpty: attribute list: %w", err)
	}
	defer attrList.Delete()

	if err := attrList.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(hpc), unsafe.Sizeof(hpc)); err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, fmt.Errorf("conpty: set pseudoconsole attribute: %w", err)
	}

	startupInfo := &windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			// The pseudoconsole supplies the child's console handles. Passing
			// STARTF_USESTDHANDLES here would explicitly replace them with zeros.
			Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
		},
		ProcThreadAttributeList: attrList.List(),
	}

	var procInfo windows.ProcessInformation
	cmdLinePtr, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, fmt.Errorf("conpty: encode command line: %w", err)
	}

	var cwdPtr *uint16
	if cwd != "" {
		cwdPtr, err = windows.UTF16PtrFromString(cwd)
		if err != nil {
			windows.ClosePseudoConsole(hpc)
			_ = windows.CloseHandle(ptyInWrite)
			_ = windows.CloseHandle(ptyOutRead)
			return nil, fmt.Errorf("conpty: encode cwd: %w", err)
		}
	}

	envBlock, err := buildWindowsEnvBlock(env)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, fmt.Errorf("conpty: build env block: %w", err)
	}

	creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(
		nil,        // appName: let CreateProcess parse the command line
		cmdLinePtr, // commandLine: must be mutable UTF-16
		nil, nil,   // process/thread security attributes
		false, // the pseudoconsole attribute does not require handle inheritance
		creationFlags,
		envBlock,
		cwdPtr,
		&startupInfo.StartupInfo,
		&procInfo,
	); err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, fmt.Errorf("conpty: CreateProcess: %w", err)
	}
	tree, err := fileio.OwnWindowsProcessTree(procInfo.Process)
	if err == nil {
		_, err = windows.ResumeThread(procInfo.Thread)
	}
	if err != nil {
		_ = windows.TerminateProcess(procInfo.Process, 1)
		_, _ = windows.WaitForSingleObject(procInfo.Process, windows.INFINITE)
		_ = windows.CloseHandle(procInfo.Thread)
		_ = windows.CloseHandle(procInfo.Process)
		if tree != nil {
			_ = tree.Close()
		}
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(ptyInWrite)
		_ = windows.CloseHandle(ptyOutRead)
		return nil, fmt.Errorf("conpty: own and resume process: %w", err)
	}

	// We only need the process handle for wait/terminate; the thread handle
	// is not useful after the process starts.
	_ = windows.CloseHandle(procInfo.Thread)

	c := &conpty{
		hpc:        hpc,
		procInfo:   procInfo,
		procHandle: procInfo.Process,
		readPipe:   os.NewFile(uintptr(ptyOutRead), "conpty-output"),
		writePipe:  os.NewFile(uintptr(ptyInWrite), "conpty-input"),
		tree:       tree,
		wait:       make(chan waitResult, 1),
		waitDone:   make(chan struct{}),
	}
	go c.waitLoop()
	return c, nil
}

func (c *conpty) waitLoop() {
	defer close(c.waitDone)
	_, err := windows.WaitForSingleObject(c.procHandle, windows.INFINITE)
	var code uint32
	if err == nil {
		err = windows.GetExitCodeProcess(c.procHandle, &code)
	}
	_ = c.tree.Terminate()
	_ = c.tree.Close()
	// ConPTY keeps its output pipe open after the last client exits. Close
	// the pseudoconsole while the manager's reader is still draining so its
	// final bytes flush and that reader observes EOF before exit publication.
	c.closeConsole()
	c.wait <- waitResult{code: int(code), err: err}
}

func (c *conpty) closeConsole() {
	c.consoleCloseOnce.Do(func() {
		c.resizeMu.Lock()
		c.consoleClosed = true
		c.resizeMu.Unlock()
		// ClosePseudoConsole can wait for the output consumer. Do not hold a
		// lock a service Resize might await while owning the parser lock.
		windows.ClosePseudoConsole(c.hpc)
	})
}

func (c *conpty) Read(b []byte) (int, error) {
	if c == nil || c.closed.Load() {
		return 0, fmt.Errorf("conpty: read handle closed")
	}
	return c.readPipe.Read(b)
}

func (c *conpty) Write(b []byte) (int, error) {
	if c == nil || c.closed.Load() {
		return 0, fmt.Errorf("conpty: write handle closed")
	}
	return c.writePipe.Write(b)
}

func (c *conpty) SetSize(cols, rows int) error {
	if c == nil {
		return fmt.Errorf("conpty: not initialized")
	}
	c.resizeMu.Lock()
	defer c.resizeMu.Unlock()
	if c.closed.Load() || c.consoleClosed {
		return fmt.Errorf("conpty: closed")
	}
	size := windows.Coord{
		X: int16(cols),
		Y: int16(rows),
	}
	return windows.ResizePseudoConsole(c.hpc, size)
}

func (c *conpty) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		_ = c.tree.Terminate()
		_ = c.tree.Close()
		// os.File coordinates closing with in-flight reads/writes, so a
		// recycled raw handle can never become another I/O target.
		_ = c.writePipe.Close()
		_ = c.readPipe.Close()
		c.closeConsole()
		<-c.waitDone
		_ = windows.CloseHandle(c.procHandle)
	})
	return nil
}

func (c *conpty) Wait() (int, error) {
	if c == nil {
		return -1, fmt.Errorf("conpty: nil")
	}
	res := <-c.wait
	return res.code, res.err
}

func (c *conpty) Pid() int {
	if c == nil || c.closed.Load() {
		return 0
	}
	return int(c.procInfo.ProcessId)
}

// buildWindowsCommandLine constructs a CreateProcess-compatible command line
// from an executable path and argument list. Each argument is quoted using
// the CommandLineToArgvW-compatible algorithm so paths with spaces survive.
func buildWindowsCommandLine(exe string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, quoteWindowsArg(exe))
	for _, a := range args {
		parts = append(parts, quoteWindowsArg(a))
	}
	return strings.Join(parts, " ")
}

// quoteWindowsArg quotes a single argument for the Windows command line.
// Implements the algorithm described by CommandLineToArgvW: backslashes
// are special only when they precede a double quote.
func quoteWindowsArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\n\v\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			b.WriteRune(r)
		case '"':
			for i := 0; i < slashes; i++ {
				b.WriteByte('\\')
			}
			b.WriteString(`\"`)
			slashes = 0
		default:
			slashes = 0
			b.WriteRune(r)
		}
	}
	for i := 0; i < slashes; i++ {
		b.WriteByte('\\')
	}
	b.WriteByte('"')
	return b.String()
}

// buildWindowsEnvBlock converts a KEY=VALUE environment list into the
// null-terminated, double-null-terminated UTF-16 environment block that
// CreateProcess expects when CREATE_UNICODE_ENVIRONMENT is set. Each entry
// is separated by a null character and the block ends with a second null.
func buildWindowsEnvBlock(env []string) (*uint16, error) {
	if len(env) == 0 {
		return nil, nil
	}
	// Build the UTF-16 slice manually: UTF16PtrFromString rejects embedded
	// nulls, so we concatenate each entry's UTF-16 encoding plus a null,
	// then a final terminating null.
	var block []uint16
	for _, e := range env {
		chars, err := windows.UTF16FromString(e)
		if err != nil {
			return nil, err
		}
		// UTF16FromString appends a null terminator; keep it as the entry
		// separator and drop any extra.
		block = append(block, chars...)
	}
	// UTF16FromString contributes the separator null for the final entry.
	// CreateProcess requires one additional null to terminate the block.
	block = append(block, 0)
	return &block[0], nil
}
