//go:build windows

package workspace

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Windows reuses process IDs and keeps a dead parent's ID in its children.
// An old program (here a Notepad from an earlier session) whose parent ID now
// names the terminal's shell is not work that the shell started.
func TestTerminalProgramIgnoresAnOlderProcessThatInheritedTheShellID(t *testing.T) {
	shellStart := windows.NsecToFiletime(time.Date(2026, 11, 17, 22, 0, 0, 0, time.UTC).UnixNano())
	starts := map[uint32]windows.Filetime{
		10: windows.NsecToFiletime(time.Date(2026, 11, 15, 7, 53, 0, 0, time.UTC).UnixNano()),
		11: windows.NsecToFiletime(time.Date(2026, 11, 17, 22, 0, 5, 0, time.UTC).UnixNano()),
	}
	start := func(pid uint32) (windows.Filetime, error) {
		if t, ok := starts[pid]; ok {
			return t, nil
		}
		return windows.Filetime{}, errors.New("access denied")
	}
	old := processEntry{pid: 10, name: "Notepad.exe"}
	if name, busy := shellProgram([]processEntry{old}, shellStart, start); busy {
		t.Fatalf("older process with the shell's reused ID counts as running work: %q", name)
	}
	newer := processEntry{pid: 11, name: "powershell.exe"}
	if name, busy := shellProgram([]processEntry{old, {pid: 12, name: "conhost.exe"}, newer}, shellStart, start); !busy || name != "powershell" {
		t.Fatalf("program started by the shell = %q, %v; want powershell, true", name, busy)
	}
	// A child whose start time cannot be read still blocks a stop.
	if name, busy := shellProgram([]processEntry{{pid: 13, name: "TOOL.EXE"}}, shellStart, start); !busy || name != "TOOL" {
		t.Fatalf("unreadable child = %q, %v; want TOOL, true", name, busy)
	}
}

// The real snapshot path still finds a program that a live shell started.
func TestTerminalProgramFindsTheShellsRunningChild(t *testing.T) {
	shell := exec.Command("cmd.exe", "/d", "/q", "/c", "ping -n 30 127.0.0.1 >nul")
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Process.Kill(); _ = shell.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		name, busy, err := terminalProgram(pidIO{pid: shell.Process.Pid})
		if err != nil {
			t.Fatal(err)
		}
		if busy {
			if !strings.EqualFold(name, "ping") {
				t.Fatalf("running program = %q, want ping", name)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the shell's running ping was not found")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type pidIO struct{ pid int }

func (p pidIO) Read([]byte) (int, error)  { return 0, nil }
func (p pidIO) Write([]byte) (int, error) { return 0, nil }
func (p pidIO) SetSize(int, int) error    { return nil }
func (p pidIO) Close() error              { return nil }
func (p pidIO) Wait() (int, error)        { return 0, nil }
func (p pidIO) Pid() int                  { return p.pid }
