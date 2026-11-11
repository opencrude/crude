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
func terminalProgram(t terminalIO) (string, bool, error) {
	shell := t.Pid()
	if shell <= 0 {
		return "", false, errors.New("terminal state is unavailable")
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", false, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if int(entry.ParentProcessID) != shell {
			continue
		}
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(name, "conhost.exe") || strings.EqualFold(name, "OpenConsole.exe") {
			continue
		}
		return strings.TrimSuffix(name, ".exe"), true, nil
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return "", false, err
	}
	return "", false, nil
}
