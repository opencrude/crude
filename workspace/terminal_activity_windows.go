//go:build windows

package workspace

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// terminalProgram reports the first child process of the terminal's shell.
// A console has no foreground process group, so any program the shell started
// counts as running work.
//
// Windows keeps a dead parent's process ID in its children and reuses process
// IDs, so a parent ID alone can name an unrelated older process: a child must
// also start no earlier than the shell. A process whose start time cannot be
// read still counts, so a stop never ignores work it cannot inspect.
func terminalProgram(t terminalIO) (string, bool, error) {
	shell := t.Pid()
	if shell <= 0 {
		return "", false, errors.New("terminal state is unavailable")
	}
	shellStart, err := processStart(uint32(shell))
	if err != nil {
		return "", false, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	var children []processEntry
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if int(entry.ParentProcessID) == shell {
			children = append(children, processEntry{pid: entry.ProcessID, name: windows.UTF16ToString(entry.ExeFile[:])})
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return "", false, err
	}
	name, busy := shellProgram(children, shellStart, processStart)
	return name, busy, nil
}

// processEntry is one child process from a process snapshot.
type processEntry struct {
	pid  uint32
	name string
}

// shellProgram picks the first child that the shell itself started.
func shellProgram(children []processEntry, shellStart windows.Filetime, start func(uint32) (windows.Filetime, error)) (string, bool) {
	for _, child := range children {
		if strings.EqualFold(child.name, "conhost.exe") || strings.EqualFold(child.name, "OpenConsole.exe") {
			continue
		}
		if created, err := start(child.pid); err == nil && created.Nanoseconds() < shellStart.Nanoseconds() {
			continue // an older process whose dead parent had this ID
		}
		name := child.name
		if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
			name = name[:len(name)-4]
		}
		return name, true
	}
	return "", false
}

// processStart returns a process's creation time.
func processStart(pid uint32) (windows.Filetime, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return windows.Filetime{}, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return windows.Filetime{}, err
	}
	return created, nil
}
