package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitpanel"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

// The header keeps the branch and the working-tree counts as separate
// clickable segments: the branch opens the branch picker, the counts open Git
// in a window. The footer keeps editor hints only.
func TestHeaderGitSegmentsSplitBranchFromChanges(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{
		Branch: "main", Ahead: 1, Behind: 2,
		Changes: []workspace.GitChange{{Path: "one"}, {Path: "two"}},
	})
	app.paintShellFrame()

	segments := app.statusHeaderLeftSegments()
	branch := segmentWithAction(segments, "git-branch")
	if branch == nil {
		t.Fatalf("header identity must include the branch segment: %+v", segments)
	}
	changes := segmentWithAction(segments, "git-changes")
	if changes == nil {
		t.Fatalf("header identity must include a separate changes segment: %+v", segments)
	}
	if !strings.Contains(branch.Text, "main") {
		t.Fatalf("branch segment text=%q missing the branch", branch.Text)
	}
	for _, want := range []string{"✎ 2", "↑1 ↓2"} {
		if !strings.Contains(changes.Text, want) {
			t.Fatalf("changes segment text=%q missing %q", changes.Text, want)
		}
	}
	if strings.Contains(branch.Text, "✎") || strings.Contains(branch.Text, "↑") {
		t.Fatalf("branch segment must not carry working-tree counts: %q", branch.Text)
	}
	if strings.Count(branch.Text, "⑂")+strings.Count(branch.Text, "") != 1 {
		t.Fatalf("changed-count must not reuse the branch glyph: %q", branch.Text)
	}
	header, footer := rowText(app, 0), rowText(app, app.height-1)
	if !strings.Contains(header, "↑1 ↓2") {
		t.Fatalf("header missing tracking: %q", header)
	}
	if strings.Contains(footer, "↑1 ↓2") {
		t.Fatalf("footer retained tracking: %q", footer)
	}
}

// segmentWithAction returns the first header segment bound to action.
func segmentWithAction(segments []shellheader.Segment, action string) *shellheader.Segment {
	for index := range segments {
		if segments[index].Action == action {
			return &segments[index]
		}
	}
	return nil
}

// Left-click on the header Git segment opens the branch dropdown anchored
// just under the header; right-click keeps the shell region menu contract.
func TestHeaderGitClickOpensBranchDropdown(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{
		Branch: "main",
		Branches: []workspace.GitBranchSummary{
			{Name: "main", Current: true},
			{Name: "feature/x"},
			{Name: "origin/main", Remote: true},
		},
	})
	app.paintShellFrame()

	_, cmd := app.Update(shellheader.ActionMsg{Action: "git-branch"})
	if cmd != nil {
		_ = runBranchSwitcherCmd(t, cmd)
	}
	fw := app.windowManager.Window(string(ui.OverlayContextMenu))
	if fw == nil {
		t.Fatal("branch dropdown should open a context menu popup")
	}
	st := app.LastContextMenu
	if st == nil {
		t.Fatal("branch dropdown state missing after open")
	}
	var labels []string
	for _, sec := range st.Sections {
		for _, it := range sec.Items {
			labels = append(labels, it.Label)
		}
	}
	for _, want := range []string{"main", "feature/x", "origin/main", "Refresh", "Open Git panel", "Review all changes"} {
		if !containsLabel(labels, want) {
			t.Fatalf("dropdown labels=%v missing %q", labels, want)
		}
	}
	// Local/Remote grouping: origin/main must sit under the Remote section.
	remoteOK := false
	for _, sec := range st.Sections {
		if sec.SectionLabel != "Remote" {
			continue
		}
		for _, it := range sec.Items {
			if it.Label == "origin/main" {
				remoteOK = true
			}
		}
	}
	if !remoteOK {
		t.Fatalf("origin/main must list under the Remote section: %+v", st.Sections)
	}
	// The current branch is visible but disabled so Enter never re-checkouts.
	for _, sec := range st.Sections {
		for _, it := range sec.Items {
			if it.Label == "main" && !it.Disabled {
				t.Fatal("current branch must be disabled in the dropdown")
			}
		}
	}
	// Popup anchors under the two-row header chrome.
	x, y := fw.Position()
	headerRect, ok := app.rootHost.ScreenRect(shellheader.ScreenID)
	if !ok || headerRect.Empty() {
		t.Fatal("header must be mounted with geometry")
	}
	if y != headerRect.Y+headerRect.H {
		t.Fatalf("dropdown y=%d want just below the header (%d)", y, headerRect.Y+headerRect.H)
	}
	if x != headerRect.X {
		t.Fatalf("dropdown x=%d want header left %d", x, headerRect.X)
	}
	app.overlayHost.Close(ui.OverlayContextMenu)
}

// No branches (unborn repo / no remotes): fall back to the Git dock panel
// instead of a dead one-item menu.
func TestHeaderGitDropdownFallsBackToPanelWithoutBranches(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{Branch: "main"})
	app.paintShellFrame()

	_, cmd := app.Update(shellheader.ActionMsg{Action: "git-branch"})
	if cmd == nil {
		t.Fatal("fallback must still return the panel toggle command")
	}
	if fw := app.windowManager.Window(string(ui.OverlayContextMenu)); fw != nil {
		t.Fatal("no branches: dropdown must not open, panel toggles instead")
	}
	if !app.gitPanelOpen() {
		t.Fatal("no branches: the Git dock panel must open instead")
	}
}

// Right-click anywhere keeps the region/menu contract: the branch dropdown
// only answers LEFT-click ActionMsgs from the header segment.
func TestHeaderGitRightClickKeepsRegionMenu(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{
		Branch:   "main",
		Branches: []workspace.GitBranchSummary{{Name: "main", Current: true}},
	})
	app.paintShellFrame()

	headerRect, ok := app.rootHost.ScreenRect(shellheader.ScreenID)
	if !ok || headerRect.Empty() {
		t.Fatal("header must be mounted with geometry")
	}
	_, _ = app.dispatchMouseRightClick(tea.MouseClickMsg{X: headerRect.X + 1, Y: headerRect.Y, Button: tea.MouseRight})
	if fw := app.windowManager.Window(string(ui.OverlayContextMenu)); fw != nil {
		t.Fatal("right-click on the header must not open the branch dropdown (no region owns header right-click)")
	}
}

// The workspace root is a header identity segment of its own: clicking it
// lists the recent workspaces and their navigation context-menu commands,
// with the active root disabled like the current branch.
func TestHeaderWorkspacePathClickOpensWorkspaceSwitcher(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	root := t.TempDir()
	wsOf(app).RootPath = root
	app.workspaceInfos = []workspace.WorkspaceInfo{
		{ProjectDir: root, ProjectName: "current"},
		{ProjectDir: filepath.Join(t.TempDir(), "other"), ProjectName: "other"},
	}
	app.paintShellFrame()

	path := segmentWithAction(app.statusHeaderLeftSegments(), "workspace-path")
	if path == nil {
		t.Fatal("the workspace root must be a clickable header segment")
	}
	_, _ = app.Update(shellheader.ActionMsg{Action: "workspace-path"})
	if fw := app.windowManager.Window(string(ui.OverlayContextMenu)); fw == nil {
		t.Fatal("workspace path click must open a workspace switcher popup")
	}
	st := app.LastContextMenu
	if st == nil {
		t.Fatal("workspace switcher state missing after open")
	}
	got := map[string]ui.ContextMenuItem{}
	for _, sec := range st.Sections {
		for _, item := range sec.Items {
			got[item.Label] = item
		}
	}
	for _, want := range []string{"current", "other", "New Workspace…", "Refresh recent workspaces"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("workspace switcher=%v missing %q", got, want)
		}
	}
	if !got["current"].Disabled || got["other"].Disabled {
		t.Fatalf("workspace switcher must disable the current root only: %+v", got)
	}
	app.overlayHost.Close(ui.OverlayContextMenu)
}

// The change count opens the Git dock panel as its own movable window; the
// branch segment keeps the branch picker.
func TestHeaderChangesClickOpensGitPanelWindow(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{
		Branch:  "main",
		Changes: []workspace.GitChange{{Path: "one"}},
	})
	app.paintShellFrame()

	_, _ = app.Update(shellheader.ActionMsg{Action: "git-changes"})
	if got := app.gitPanel.Mode(); got != gitpanel.ModeChanges {
		t.Fatalf("git panel mode=%q want changes", got)
	}
	tab := app.panelTab(termdock.PanelGit)
	if tab == nil || !app.Win.IsFloating(tab.Group) {
		t.Fatalf("change count must open the Git panel in its own window: %+v", tab)
	}
	if !app.panelShown(termdock.PanelGit) {
		t.Fatal("the Git window must be the visible panel")
	}
	if fw := app.windowManager.Window(string(ui.OverlayContextMenu)); fw != nil {
		t.Fatal("change count must not open a dropdown")
	}
}

// Clicking the painted change glyph itself, not a synthetic ActionMsg, must
// open the Git window: the header's hit map has to cover the split segment.
func TestHeaderChangeGlyphClickOpensGitWindow(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 120, 28
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{
		Branch:  "main",
		Changes: []workspace.GitChange{{Path: "one"}},
	})
	app.paintShellFrame()
	headerRect, ok := app.rootHost.ScreenRect(shellheader.ScreenID)
	if !ok || headerRect.Empty() {
		t.Fatal("header must be mounted with geometry")
	}
	buf := app.mainSurf.Buffer()
	x := -1
	for col := 0; col < buf.Width(); col++ {
		if buf.Get(col, headerRect.Y).Char == '✎' {
			x = col
			break
		}
	}
	if x < 0 {
		t.Fatalf("painted header has no change glyph: %q", rowText(app, headerRect.Y))
	}
	_, cmd := app.Update(tea.MouseClickMsg{X: x, Y: headerRect.Y, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	tab := app.panelTab(termdock.PanelGit)
	if tab == nil || !app.Win.IsFloating(tab.Group) {
		t.Fatalf("clicking the painted change glyph must open the Git window: %+v", tab)
	}
}

func containsLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

// runBranchSwitcherCmd drains the openBranchSwitcher command. The command is
// the context-menu open (synchronous, nil-returning); draining keeps the test
// honest about the Update path returning a command at all.
func runBranchSwitcherCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	// openContextMenuState returns nil after opening synchronously; tolerate
	// either shape so the test pins behavior, not plumbing.
	if msg := cmd(); msg != nil {
		return []tea.Msg{msg}
	}
	return nil
}
