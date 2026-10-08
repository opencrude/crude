package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	"crude/tui/editor"
	"crude/tui/ui"
)

func paneOpenFixture(t *testing.T) (*App, string) {
	t.Helper()
	m, ws := newRealShellApp(t)
	t.Cleanup(func() { ws.Close() })
	path := filepath.Join(t.TempDir(), "prepared.txt")
	if err := os.WriteFile(path, []byte("disk original"), 0o600); err != nil {
		t.Fatal(err)
	}
	return m, path
}

func TestPaneFilesOpenPreparesOffUIAndInstallsInTarget(t *testing.T) {
	m, path := paneOpenFixture(t)
	original := m.Win.ActiveGroup()
	target := m.Win.SplitGroup(original, ui.Horizontal, false)
	paths := []string{path}
	cmd := m.openPaneFiles(paths, target)
	paths[0] = "changed caller slice"
	if m.BufferMgr.Get(path) != nil || len(m.EditorState.Tabs) != 0 {
		t.Fatal("request performed an eager file open")
	}
	// This change after request capture proves the command owns the read.
	if err := os.WriteFile(path, []byte("command read"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := cmd().(paneFilesOpenedMsg)
	if m.BufferMgr.Get(path) != nil || len(m.EditorState.Tabs) != 0 {
		t.Fatal("preparation published live state before Update")
	}
	m.applyPaneFilesOpened(msg)
	tab := m.Win.TabByTitle(m.editorTabTitle(path))
	if tab == nil || tab.Group != target || m.Win.ActiveGroup() != target || m.EditorState.Editor.Value() != "command read" {
		t.Fatal("completion did not select the prepared file in its target pane")
	}
}

func TestPaneFilesOpenRejectsStaleOwners(t *testing.T) {
	for _, boundary := range []string{"cancel", "workspace", "manager", "group", "newer request"} {
		t.Run(boundary, func(t *testing.T) {
			m, path := paneOpenFixture(t)
			target := m.Win.SplitGroup(m.Win.ActiveGroup(), ui.Horizontal, false)
			msg := m.openPaneFiles([]string{path}, target)().(paneFilesOpenedMsg)
			switch boundary {
			case "cancel":
				m.openPaneFiles(nil, target)
			case "workspace":
				m.workspaceSwitchGen.Add(1)
			case "manager":
				m.BufferMgr = editor.NewBufferManager()
			case "group":
				m.Win.RemoveEmptyGroup(target)
			case "newer request":
				m.openPaneFiles([]string{path}, target)
			}
			m.applyPaneFilesOpened(msg)
			if m.BufferMgr.Get(path) != nil || m.Win.TabByTitle(m.editorTabTitle(path)) != nil || m.EditorState.Tabs[path] != nil {
				t.Fatal("stale completion registered a buffer or tab")
			}
		})
	}
}

func TestPaneFilesOpenPreservesNewerEditorAndMovesIdentity(t *testing.T) {
	m, path := paneOpenFixture(t)
	original := m.Win.ActiveGroup()
	target := m.Win.SplitGroup(original, ui.Horizontal, false)
	msg := m.openPaneFiles([]string{path}, target)().(paneFilesOpenedMsg)
	m.Win.FocusGroup(original)
	if _, err := m.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	ed := m.EditorState.Tabs[path].Editor
	m.BufferMgr.Get(path).ReplaceContents("unsaved newer text")
	ed.SetCursorPosition(0, 4)
	id := m.Win.TabByTitle(m.editorTabTitle(path)).ID
	m.Win.FocusGroup(target)
	m.handleTabSwitch()
	m.applyPaneFilesOpened(msg)
	if m.Win.TabCount() != 1 || m.Win.GroupOf(id) != target || m.EditorState.Tabs[path].Editor != ed || ed.Value() != "unsaved newer text" || ed.CursorCol() != 4 || !ed.Modified() {
		t.Fatal("completion duplicated or reloaded the newer live editor")
	}
}

func TestPaneFilesOpenPreservesLaterEmptyGroupAndFocus(t *testing.T) {
	m, path := paneOpenFixture(t)
	target := m.Win.ActiveGroup()
	msg := m.openPaneFiles([]string{path}, target)().(paneFilesOpenedMsg)
	empty := m.Win.SplitGroup(target, ui.Horizontal, false)
	m.handleTabSwitch()
	m.setFocusTabBar()
	m.applyPaneFilesOpened(msg)
	if m.Win.ActiveGroup() != empty || m.Win.ActiveTab() != nil || m.EditorState.ActiveView != "" || m.FocusStack.Top() != FocusTabBar {
		t.Fatal("late open stole the exact empty group or explicit focus")
	}
	if tab := m.Win.TabByTitle(m.editorTabTitle(path)); tab == nil || tab.Group != target {
		t.Fatal("background completion failed to open in target")
	}
}

func TestPaneFilesOpenPreservesLaterSelectedEditor(t *testing.T) {
	m, path := paneOpenFixture(t)
	paths := openEditorTabs(t, m, "first.go", "later.go")
	target := m.Win.ActiveGroup()
	m.Win.SwitchTo(m.Win.IndexOf(m.Win.TabByTitle(m.editorTabTitle(paths[0])).ID))
	m.handleTabSwitch()
	msg := m.openPaneFiles([]string{path}, target)().(paneFilesOpenedMsg)
	later := m.Win.TabByTitle(m.editorTabTitle(paths[1])).ID
	m.Win.SwitchTo(m.Win.IndexOf(later))
	m.handleTabSwitch()
	m.setFocusTabBar()
	m.applyPaneFilesOpened(msg)
	if m.Win.ActiveTab().ID != later || m.EditorState.ActiveView != paths[1] || m.FocusStack.Top() != FocusTabBar {
		t.Fatal("completion replaced a later explicit tab selection")
	}
}

func TestPaneFilesOpenExistingSkipsDiskAndHonorsClose(t *testing.T) {
	m, path := paneOpenFixture(t)
	if _, err := m.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Opening this path from disk would now fail because it is a directory.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	msg := m.openPaneFiles([]string{path}, m.Win.ActiveGroup())().(paneFilesOpenedMsg)
	if !msg.files[0].existing || msg.files[0].err != nil || msg.files[0].buffer != nil {
		t.Fatal("existing open document performed a redundant disk read")
	}
	m.closeEditorTabForce(path)
	m.applyPaneFilesOpened(msg)
	if m.EditorState.Tabs[path] != nil || m.BufferMgr.Get(path) != nil {
		t.Fatal("completion resurrected an explicitly closed editor")
	}
}
