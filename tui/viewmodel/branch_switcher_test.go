package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellheader"
	"crude/tui/ui"
	"crude/workspace"
)

// The header Git segment folds the branch plus the repo counts (changed
// files, ahead/behind) into one clickable segment; the footer keeps editor
// hints only.
func TestHeaderGitSegmentOwnsRepoCounts(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.sourceControl.SetSnapshot(workspace.GitSnapshot{
		Branch: "main", Ahead: 1, Behind: 2,
		Changes: []workspace.GitChange{{Path: "one"}, {Path: "two"}},
	})
	app.paintShellFrame()

	segments := app.statusHeaderLeftSegments()
	if len(segments) == 0 {
		t.Fatal("header identity must include the git segment")
	}
	git := segments[len(segments)-1]
	if git.Action != "git-branch" {
		t.Fatalf("git segment action=%q want git-branch (branch dropdown)", git.Action)
	}
	for _, want := range []string{"main", "✎ 2", "↑1 ↓2"} {
		if !strings.Contains(git.Text, want) {
			t.Fatalf("git segment text=%q missing %q", git.Text, want)
		}
	}
	if strings.Count(git.Text, "⑂")+strings.Count(git.Text, "") != 1 {
		t.Fatalf("changed-count must not reuse the branch glyph: %q", git.Text)
	}
	header, footer := rowText(app, 0), rowText(app, app.height-1)
	if !strings.Contains(header, "↑1 ↓2") {
		t.Fatalf("header missing tracking: %q", header)
	}
	if strings.Contains(footer, "↑1 ↓2") {
		t.Fatalf("footer retained tracking: %q", footer)
	}
}

// Left-click on the header Git segment opens the branch dropdown anchored
// just under the header; right-click keeps the shell region menu contract.
func TestHeaderGitClickOpensBranchDropdown(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.sourceControl.SetSnapshot(workspace.GitSnapshot{
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
	for _, want := range []string{"main", "feature/x", "origin/main", "Refresh", "Open source control"} {
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

// No branches (unborn repo / no remotes): fall back to the source-control
// panel instead of a dead one-item menu.
func TestHeaderGitDropdownFallsBackToPanelWithoutBranches(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.sourceControl.SetSnapshot(workspace.GitSnapshot{Branch: "main"})
	app.paintShellFrame()

	_, cmd := app.Update(shellheader.ActionMsg{Action: "git-branch"})
	if cmd == nil {
		t.Fatal("fallback must still return the panel toggle command")
	}
	if fw := app.windowManager.Window(string(ui.OverlayContextMenu)); fw != nil {
		t.Fatal("no branches: dropdown must not open, panel toggles instead")
	}
	if app.activityView != activityGit {
		t.Fatalf("activityView=%v want source control panel", app.activityView)
	}
}

// Right-click anywhere keeps the region/menu contract: the branch dropdown
// only answers LEFT-click ActionMsgs from the header segment.
func TestHeaderGitRightClickKeepsRegionMenu(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 28
	app.sourceControl.SetSnapshot(workspace.GitSnapshot{
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
