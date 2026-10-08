package viewmodel

import (
	"crypto/sha256"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/gitreview"
	"crude/tui/screens/localdiff"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

// Compare with disk must open the actual comparison in a movable main tab,
// rather than adding a new entry point to the old one-hunk floating inspector.
func TestLocalDiffDiskRouteOpensMovableComparisonWithoutChangingSource(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "local-change.txt")
	ed := app.EditorState.Editor
	original := ed.Value()
	ed.ReplaceDocument("working copy marker\nsecond change\n")
	current, revision := ed.Value(), ed.Revision()
	cmd := app.compareEditorWithDisk(ed)
	if cmd == nil {
		t.Fatal("comparison did not prepare a command")
	}
	_, _ = app.Update(cmd())
	_ = app.View()
	active := app.Win.ActiveTab()
	if active == nil || active.Resource.Kind != window.ResourceLocalDiff {
		t.Fatalf("Compare with disk did not open a movable main comparison tab: %+v", active)
	}
	if app.overlayHost.IsOpen(editorChangesOverlay) {
		t.Fatal("full comparison still opens the one-hunk floating inspector")
	}
	if ed.Value() != current || ed.Revision() != revision || !ed.Modified() {
		t.Fatal("read-only comparison changed source bytes, revision or dirty state")
	}
	disk, err := os.ReadFile(paths[0])
	if err != nil || string(disk) != original {
		t.Fatalf("read-only comparison changed disk: %q, %v", disk, err)
	}
	view := app.activeLocalDiff()
	comparison := view.screen.Comparison()
	if comparison.Before != original || comparison.After != current {
		t.Fatalf("comparison evidence does not match disk and unsaved bytes: %+v", comparison)
	}
	paneJourneyRequireText(t, app, app.Win.ActiveGroup(), "working copy marker")
	group := app.Win.ActiveGroup()
	right := app.Win.SplitGroup(group, ui.Horizontal, false)
	app.Win.MoveTabToGroup(view.tab, right, 0)
	app.focusMainGroup(right)
	_ = app.View()
	paneJourneyRequireText(t, app, right, "working copy marker")
	paneJourneyKey(app, 'w', tea.ModCtrl)
	if app.Win.IndexOf(view.tab) >= 0 || app.EditorState.Editor != ed || ed.Value() != current {
		t.Fatal("closing diff closed or recreated its dirty source")
	}
}

func TestLocalDiffSavedSnapshotRefreshAndAsyncOwnership(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "snapshot.txt", "neighbor.txt")
	app.activateEditorTab(paths[0])
	ed := app.EditorState.Editor
	saved := ed.Value()
	ed.ReplaceDocument("first unsaved revision\n")
	first := app.openLocalDiff(ed, localDiffSaved)
	view := app.activeLocalDiff()
	initial := first().(localDiffPreparedMsg)
	ed.ReplaceDocument("newer unsaved revision\n")
	app.activateEditorTab(paths[1])
	selected := app.Win.ActiveTab().ID
	_, _ = app.Update(initial)
	if app.Win.ActiveTab().ID != selected || view.screen.Comparison().After != "first unsaved revision\n" || view.screen.Comparison().Before != saved {
		t.Fatal("late completion stole focus or discarded the captured immutable evidence")
	}
	if !strings.Contains(view.notice, "Source changed") {
		t.Fatal("changed source snapshot is not identified as stale")
	}
	old := app.prepareLocalDiff(view)()
	current := ed.BufferSnapshot()
	ed.AcceptSave(current.Revision, sha256.Sum256(current.Data), current.Data)
	ed.ReplaceDocument("third revision\n")
	latest := app.prepareLocalDiff(view)()
	_, _ = app.Update(latest)
	_, _ = app.Update(old)
	if got := view.screen.Comparison(); got.Before != "newer unsaved revision\n" || got.After != "third revision\n" {
		t.Fatalf("stale request replaced current savepoint snapshot: %+v", got)
	}
	if app.Win.ActiveTab().ID != selected {
		t.Fatal("background refresh changed selected resource")
	}
	app.closeLocalDiff(view.tab)
	_, _ = app.Update(latest)
	if len(app.localDiffs) != 0 || app.Win.ActiveTab().ID != selected {
		t.Fatal("closed comparison resurrected from late completion")
	}
	cmd := app.openLocalDiff(ed, localDiffSaved)
	stale := cmd()
	before := app.activeLocalDiff().screen.Comparison()
	app.workspaceSwitchGen.Add(1)
	_, _ = app.Update(stale)
	if app.activeLocalDiff().screen.Comparison() != before {
		t.Fatal("previous-workspace preparation was admitted")
	}
}

func TestLocalDiffActionsTargetExactScreenAndKeepSnapshotsAfterSourceClose(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 44)
	paths := openEditorTabs(t, app, "one.txt", "two.txt")
	var views []*localDiffView
	for _, path := range paths {
		app.activateEditorTab(path)
		ed := app.EditorState.Editor
		ed.ReplaceDocument("snapshot of " + path + "\n")
		cmd := app.openLocalDiff(ed, localDiffSaved)
		_, _ = app.Update(cmd())
		views = append(views, app.activeLocalDiff())
	}
	first, second := views[0], views[1]
	app.handleHostAction(&ui.Action{ID: localdiff.ActionOpenSource, Payload: first.screen})
	if app.EditorState.Editor != first.source {
		t.Fatal("open source targeted selected diff instead of the action owner")
	}
	app.closeEditorTabForce(paths[0])
	app.syncLocalDiffFreshness()
	retained := first.screen.Comparison()
	if !strings.Contains(first.notice, "Source closed") || retained.After == "" {
		t.Fatal("closing source lost snapshot or failed to mark source lifetime")
	}
	app.Win.SwitchTo(app.Win.IndexOf(second.tab))
	app.handleTabSwitch()
	if cmd := app.handleHostAction(&ui.Action{ID: localdiff.ActionRefresh, Payload: first.screen}); cmd != nil {
		t.Fatal("closed editor started a new read or reused another buffer")
	}
	app.handleHostAction(&ui.Action{ID: localdiff.ActionClose, Payload: first.screen})
	if app.Win.ActiveTab().ID != second.tab || app.localDiffs[first.tab] != nil || app.localDiffs[second.tab] != second {
		t.Fatal("close targeted the wrong comparison")
	}
}

func TestGitReviewAndLocalDiffPaintInIndependentGroups(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 48)
	paths := openEditorTabs(t, app, "live.txt")
	ed := app.EditorState.Editor
	ed.ReplaceDocument("local distinct content\n")
	cmd := app.compareEditorWithDisk(ed)
	_, _ = app.Update(cmd())
	local := app.activeLocalDiff()
	left := app.Win.ActiveGroup()
	right := app.Win.SplitGroup(left, ui.Horizontal, false)
	app.focusMainGroup(right)
	app.openGitReview(gitreview.ModeChanges, nil) // no background Git work needed for this projection fixture
	app.gitReview.SetComparison(gitreview.PrepareComparison(app.Theme, workspace.GitComparison{Path: "git.txt", BaseLabel: "HEAD", TargetLabel: "Index", Before: "old git content\n", After: "new git content\n", Fingerprint: "fixture"}))
	app.gitReview.SetPresentation(gitreview.SideBySide)
	_ = app.View()
	paneJourneyRequireText(t, app, left, "local distinct content")
	paneJourneyRequireText(t, app, right, "new git content")
	app.ApplyThemePreset("light")
	_ = app.View()
	paneJourneyRequireText(t, app, left, "local distinct content")
	app.focusMainGroup(left)
	_ = app.View()
	if app.rootHost.Focused() != local.screen.DocumentID() {
		t.Fatalf("diff keyboard focus=%s want %s", app.rootHost.Focused(), local.screen.DocumentID())
	}
	if app.EditorState.Tabs[paths[0]].Editor != ed || !ed.Modified() {
		t.Fatal("comparison composition replaced the original editor")
	}
}

func TestComparisonTabPlusPointerHoverLeavesStripUnpainted(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	openEditorTabs(t, app, "hover.txt")
	command := app.openLocalDiff(app.EditorState.Editor, localDiffSaved)
	_, _ = app.Update(command())
	_ = app.View()
	group := app.mainPanes.groups[app.Win.ActiveGroup()]
	strip, ok := app.rootHost.ScreenRect(group.tabs.ID())
	if !ok {
		t.Fatal("missing strip")
	}
	button := group.tabs.NewButtonBounds()
	if button.H != 1 {
		t.Fatalf("plus target must align with one-row tab labels, got %+v", button)
	}
	// The pointer gets no highlight on the plus at all: a cell is a whole row,
	// so a hover fill either falls short of the bar's rule or hangs past it.
	for row := 0; row < ui.StripHeight; row++ {
		_, _ = app.Update(tea.MouseMotionMsg{X: strip.X + button.X, Y: strip.Y + row})
		app.forceFrame()
		_ = app.View()
		for x := 0; x < button.W; x++ {
			if c := app.mainSurf.Buffer().Get(strip.X+button.X+x, strip.Y); c.BG != app.rootHost.Tokens().Color(ui.ColorBg) {
				t.Fatalf("comparison strip hover painted the label band at %d: %+v", x, c)
			}
			c := app.mainSurf.Buffer().Get(strip.X+button.X+x, strip.Y+1)
			if c.BG != app.rootHost.Tokens().Color(ui.ColorBg) || c.Char != '─' {
				t.Fatalf("comparison strip hover touched the separator at %d: %+v", x, c)
			}
		}
	}
}
