//go:build windows

package workspace

import (
	"bytes"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestBuildWindowsEnvBlockIsDoubleNullTerminated(t *testing.T) {
	block, err := buildWindowsEnvBlock([]string{"A=1", "B=2"})
	if err != nil {
		t.Fatal(err)
	}
	got := unsafe.Slice(block, 9)
	want := []uint16{'A', '=', '1', 0, 'B', '=', '2', 0, 0}
	if !bytes.Equal(uint16Bytes(got), uint16Bytes(want)) {
		t.Fatalf("environment block=%v want %v", got, want)
	}
}

func uint16Bytes(values []uint16) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(&values[0])), len(values)*2)
}

// TestConPTYCreateWriteClose verifies a ConPTY session can be created, written
// to, and closed cleanly. This is the Windows counterpart of
// TestTerminalManagerCreateWriteClose.
func TestConPTYCreateWriteClose(t *testing.T) {
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{
		CWD:   t.TempDir(),
		Cols:  40,
		Rows:  10,
		Title: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info.ID == "" || !info.Alive {
		t.Fatalf("info=%+v", info)
	}
	if info.PID <= 0 {
		t.Fatalf("no pid: %+v", info)
	}
	list := mgr.List()
	if len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("List=%v", list)
	}
	if err := mgr.Write(info.ID, []byte("echo hello\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	mgr.SetPlacement(info.ID, TerminalFloating)
	got, ok := mgr.Get(info.ID)
	if !ok || got.Placement != TerminalFloating {
		t.Fatalf("Get=%+v %v", got, ok)
	}
	mgr.Close(info.ID)
	if len(mgr.List()) != 0 {
		t.Fatal("Close should remove the session")
	}
}

// TestConPTYResize verifies the ConPTY can be resized.
func TestConPTYResize(t *testing.T) {
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	if err := mgr.Resize(info.ID, 60, 20); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	got, _ := mgr.Get(info.ID)
	if got.Cols != 60 || got.Rows != 20 {
		t.Fatalf("size=%dx%d", got.Cols, got.Rows)
	}
}

// TestConPTYCloseKillsChild verifies that closing a terminal terminates the
// child process. This is the Windows counterpart of
// TestTerminalCloseKillsProcessGroup. ConPTY does not have process groups, so
// we check that the direct child process is no longer running after Close.
func TestConPTYCloseKillsChild(t *testing.T) {
	io, err := startTerminalIO("", nil, t.TempDir(), 40, 10)
	if err != nil {
		t.Fatalf("start PTY: %v", err)
	}
	pid := io.Pid()
	if pid <= 0 {
		t.Fatalf("no pid")
	}
	if !processAlive(t, uint32(pid)) {
		t.Fatalf("child %d is not running before Close", pid)
	}
	go func() { _, _ = io.Wait() }()
	if err := io.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(t, uint32(pid)) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child %d still alive after Close", pid)
}

// TestConPTYManagerCloseAllReapsChildren is the Windows counterpart of
// TestTerminalManagerCloseAllReapsChildren.
func TestConPTYManagerCloseAllReapsChildren(t *testing.T) {
	mgr := NewTerminalManager(t.TempDir())
	pids := make([]uint32, 0, 2)
	for i := 0; i < 2; i++ {
		info, err := mgr.Create(TerminalCreateOpts{
			CWD: t.TempDir(), Cols: 40, Rows: 10,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if info.PID <= 0 {
			t.Fatalf("no pid for %s", info.ID)
		}
		pids = append(pids, uint32(info.PID))
	}
	mgr.CloseAll()
	if got := len(mgr.List()); got != 0 {
		t.Fatalf("%d sessions left after CloseAll", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range pids {
		for processAlive(t, pid) {
			if !time.Now().Before(deadline) {
				t.Fatalf("pid %d still alive after CloseAll", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestConPTYWriteDoesNotBlockUI verifies the async ptyWriter doesn't block on
// a large write, same as the Unix TestTerminalWriteDoesNotBlockUI.
func TestConPTYWriteDoesNotBlockUI(t *testing.T) {
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{
		CWD: t.TempDir(), Cols: 40, Rows: 10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	payload := bytes.Repeat([]byte("x"), 256*1024)
	start := time.Now()
	if err := mgr.Write(info.ID, payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Write blocked for %s; PTY stdin must be asynchronous", elapsed)
	}
}

// processAlive checks whether a process with the given PID is still running.
// It uses OpenProcess + GetExitCodeProcess (STILL_ACTIVE = 259).
func processAlive(t *testing.T, pid uint32) bool {
	t.Helper()
	const stillActive = 259
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
