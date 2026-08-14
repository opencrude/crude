package workspace

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestTerminalIDsRemainMonotonicAcrossManagerLifetimes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	create := func(t *testing.T, mgr *TerminalManager, title string) TerminalInfo {
		t.Helper()
		info, err := mgr.Create(TerminalCreateOpts{
			Command: "/bin/cat",
			CWD:     t.TempDir(),
			Cols:    20,
			Rows:    8,
			Title:   title,
		})
		if err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
		return info
	}

	oldMgr := NewTerminalManager(t.TempDir())
	oldInfo := create(t, oldMgr, "old")
	oldMgr.CloseAll()

	newMgr := NewTerminalManager(t.TempDir())
	newInfo := create(t, newMgr, "new")
	t.Cleanup(newMgr.CloseAll)

	if oldInfo.ID == newInfo.ID {
		t.Fatalf("replacement manager reused terminal ID %q", oldInfo.ID)
	}
	oldSeq, oldErr := strconv.ParseUint(strings.TrimPrefix(oldInfo.ID, "t"), 10, 64)
	newSeq, newErr := strconv.ParseUint(strings.TrimPrefix(newInfo.ID, "t"), 10, 64)
	if oldErr != nil || newErr != nil {
		t.Fatalf("unexpected terminal IDs old=%q new=%q", oldInfo.ID, newInfo.ID)
	}
	if newSeq <= oldSeq {
		t.Fatalf("terminal IDs not process-monotonic: old=%q new=%q", oldInfo.ID, newInfo.ID)
	}
}
