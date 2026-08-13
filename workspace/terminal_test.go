package workspace

import (
	"bytes"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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

// TestPTYChildResolvesPinnedTerminfo proves the exact PTY child environment
// (TERM=xterm-ghostty plus TERMINFO from TerminfoDir) resolves the pinned
// Ghostty terminfo entry: tput must report 256 colors and infocmp must find
// the entry. Previously Crude advertised TERM=xterm-ghostty with no resolvable
// database, so tput failed and child rc scripts broke.
func TestPTYChildResolvesPinnedTerminfo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	for _, tool := range []string{"sh", "infocmp", "tput"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available; ncurses tools required", tool)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CRUDE_TERMINFO_DIR", "")

	io, err := startTerminalIO("sh", []string{"-c",
		`printf 'colors='; tput colors; echo; infocmp -x xterm-ghostty >/dev/null && echo TERMINFO_OK; exit 0`,
	}, t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("start PTY: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var out bytes.Buffer
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		n, err := io.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		got := out.String()
		if strings.Contains(got, "colors=256") && strings.Contains(got, "TERMINFO_OK") {
			return
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("pinned terminfo not resolved in PTY child; output so far:\n%s", out.String())
}

// TestTerminalCloseKillsProcessGroup proves closing a terminal reaps the
// shell's descendants, not just the shell. Previously Close killed only the
// direct child, so anything the shell had spawned (builds, watchers, sleeps)
// survived as orphans.
func TestTerminalCloseKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	io, err := startTerminalIO("/bin/sh", []string{"-c",
		`sleep 300 & echo "CHILD:$!"; wait`,
	}, t.TempDir(), 40, 10)
	if err != nil {
		t.Fatalf("start PTY: %v", err)
	}
	// Reap the leader like TerminalManager.waitLoop does.
	go func() { _, _ = io.Wait() }()

	childPid := 0
	var out bytes.Buffer
	buf := make([]byte, 1024)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && childPid == 0 {
		n, readErr := io.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			for _, line := range strings.Split(out.String(), "\n") {
				line = strings.TrimSpace(line)
				if rest, ok := strings.CutPrefix(line, "CHILD:"); ok {
					if pid, convErr := strconv.Atoi(strings.TrimSpace(rest)); convErr == nil && pid > 0 {
						childPid = pid
					}
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	if childPid == 0 {
		t.Fatalf("never observed the child pid; output:\n%s", out.String())
	}
	if syscall.Kill(childPid, 0) != nil {
		t.Fatalf("fixture child %d is not running before Close", childPid)
	}

	if err := io.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(childPid, syscall.SIGKILL)
	t.Fatalf("descendant %d survived terminal close (orphaned process group)", childPid)
}

// TestTerminalManagerCloseAllReapsChildren covers the app-quit path: every
// PTY child must be gone after CloseAll.
func TestTerminalManagerCloseAllReapsChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	mgr := NewTerminalManager(t.TempDir())
	pids := make([]int, 0, 2)
	for i := 0; i < 2; i++ {
		info, err := mgr.Create(TerminalCreateOpts{
			Command: "/bin/sleep", Args: []string{"300"},
			CWD: t.TempDir(), Cols: 40, Rows: 10,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if info.PID <= 0 {
			t.Fatalf("no pid for %s", info.ID)
		}
		pids = append(pids, info.PID)
	}
	mgr.CloseAll()
	if got := len(mgr.List()); got != 0 {
		t.Fatalf("%d sessions left after CloseAll", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range pids {
		for {
			if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
				break
			}
			if !time.Now().Before(deadline) {
				t.Fatalf("pid %d still alive after CloseAll", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
