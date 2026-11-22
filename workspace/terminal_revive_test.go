//go:build !windows

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/internal/terminalvt"
	"crude/types/setting"
)

func reviveTestService(t *testing.T, path string) *TerminalService {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is required")
	}
	s, _ := testTerminalService(t)
	s.manager.SetSettings(setting.Terminal{Shell: setting.TerminalShellCustom, Command: sh, Args: []string{"-i"}})
	s.SetRevivalPath(path)
	return s
}

func waitTerminalText(t *testing.T, s *TerminalService, id, want string, count int) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		page, err := s.Rows(id, terminalvt.RowRequest{Start: 0, Count: terminalvt.MaxSnapshotRows})
		text := terminalText(page)
		if err == nil && strings.Count(text, want) >= count {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal never showed %q %d times:\n%s", want, count, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A core that stops keeps each interactive terminal's buffer; the next core
// shows it again, with its name, above a new shell in the same folder.
func TestStoppedCoreTerminalsComeBackWithTheirBuffers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terminal-buffers.json")
	first := reviveTestService(t, path)
	folder := t.TempDir()
	info, err := first.Create(TerminalCreateOpts{CWD: folder, Cols: 200, Rows: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.manager.Write(info.ID, []byte("echo KEPT-$((40+2))\n")); err != nil {
		t.Fatal(err)
	}
	waitTerminalText(t, first, info.ID, "KEPT-42", 1)
	if err := first.manager.rename(info.ID, "build"); err != nil {
		t.Fatal(err)
	}
	command, args := "/bin/sh", []string{"-c", "echo OWN-COMMAND; cat"}
	if _, err := first.Create(TerminalCreateOpts{Command: command, Args: args, Cols: 60, Rows: 8}); err != nil {
		t.Fatal(err)
	}
	first.CloseAll()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("stopping the core kept no terminal buffers", err)
	}

	second := reviveTestService(t, path)
	ids, err := second.Revive()
	if err != nil || len(ids) != 1 {
		t.Fatalf("revived %v, err %v; want the one interactive terminal", ids, err)
	}
	text := waitTerminalText(t, second, ids[0], "KEPT-42", 1)
	if !strings.Contains(text, "Crude kept this output") || strings.Contains(text, "OWN-COMMAND") {
		t.Fatalf("revived buffer:\n%s", text)
	}
	revived, _ := second.manager.Get(ids[0])
	if revived.Name != "build" || !revived.Alive {
		t.Fatalf("revived terminal %+v lost its name or has no shell", revived)
	}
	// The new shell runs in the saved folder and accepts input.
	if err := second.manager.Write(ids[0], []byte("pwd\n")); err != nil {
		t.Fatal(err)
	}
	waitTerminalText(t, second, ids[0], folder, 1)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("revived buffers stayed saved and would come back twice")
	}
	if again, _ := second.Revive(); len(again) != 0 {
		t.Fatal("a second revive created the terminals again")
	}
}

// A core that never revived the saved terminals (no window attached) keeps
// them for the next core instead of overwriting them.
func TestUnrevivedTerminalBuffersSurviveAnotherStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terminal-buffers.json")
	first := reviveTestService(t, path)
	info, err := first.Create(TerminalCreateOpts{Cols: 60, Rows: 8})
	if err != nil {
		t.Fatal(err)
	}
	_ = first.manager.Write(info.ID, []byte("echo FIRST-$((1+1))\n"))
	waitTerminalText(t, first, info.ID, "FIRST-2", 1)
	first.CloseAll()

	idle := reviveTestService(t, path)
	idle.CloseAll()

	last := reviveTestService(t, path)
	ids, err := last.Revive()
	if err != nil || len(ids) != 1 {
		t.Fatalf("revived %v, err %v", ids, err)
	}
	waitTerminalText(t, last, ids[0], "FIRST-2", 1)
}
