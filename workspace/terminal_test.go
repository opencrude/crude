package workspace

import (
	"bytes"
	"runtime"
	"testing"
	"time"
)

func TestTerminalManagerCreateWriteClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    10,
		Title:   "cat",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info.ID == "" || !info.Alive {
		t.Fatalf("info=%+v", info)
	}
	list := mgr.List()
	if len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("List=%v", list)
	}
	if err := mgr.Write(info.ID, []byte("hi\n")); err != nil {
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

func TestTerminalManagerResize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
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
	time.Sleep(20 * time.Millisecond)
}

func TestTerminalWriteDoesNotBlockUI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{
		Command: "/bin/sleep",
		Args:    []string{"30"},
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    10,
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
