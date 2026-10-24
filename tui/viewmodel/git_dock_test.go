package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitdiff"
	"crude/tui/screens/gitpanel"
	"crude/tui/screens/gitreview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

func gitDockSnapshot(root string, changes ...workspace.GitChange) workspace.GitSnapshot {
	for index := range changes {
		changes[index].AbsolutePath = filepath.Join(root, filepath.FromSlash(changes[index].Path))
	}
	return workspace.GitSnapshot{RepositoryRoot: root, Branch: "main", Changes: changes}
}

// Git lives in the bottom dock: the rail has no Git destination, the footer
// owns the toggle, and opening it lands keyboard focus on the change list.
func TestGitIsADockDestinationNotARailView(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 140, 44
	for _, view := range app.toolLayout.Rail {
		if view == "git" {
			t.Fatal("Git still has a rail destination")
		}
	}
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), workspace.GitChange{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}))
	app.toggleGitPanel()
	if !app.gitPanelOpen() || app.termDock.ActivePanel() != termdock.PanelGit {
		t.Fatal("Git panel did not open in the dock")
	}
	app.paintShellFrame()
	if focused := app.rootHost.Focused(); focused != gitpanel.ListID {
		t.Fatalf("focus=%s want the change list", focused)
	}
	plain := bufferPlain(app.mainSurf.Buffer())
	for _, want := range []string{"Changes 1", "a.go", "Commit"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("dock missing %q:\n%s", want, plain)
		}
	}
	segments := app.footerStatusSegments()
	found := false
	for _, segment := range segments {
		if segment.Action == "git" {
			found = segment.Active
		}
	}
	if !found {
		t.Fatal("footer Git pill is not marked active while the panel is open")
	}
	app.toggleGitPanel()
	if app.gitPanelOpen() {
		t.Fatal("a second toggle with focus inside must hide the panel")
	}
}

// The Explorer context menu offers the changes under a folder or file, and
// View Changes opens the review scoped to exactly that path.
func TestExplorerContextMenuViewsChangesForAPath(t *testing.T) {
	app := newFullTestApp(t)
	root := t.TempDir()
	wsOf(app).RootPath = root
	app.gitPanel.SetSnapshot(gitDockSnapshot(root,
		workspace.GitChange{Path: "src/a.go", Area: workspace.GitAreaWorking, Code: "M"},
		workspace.GitChange{Path: "src/b.go", Area: workspace.GitAreaStaged, Code: "A"},
		workspace.GitChange{Path: "notes/c.md", Area: workspace.GitAreaWorking, Code: "M"},
	))
	section, ok := app.explorerGitSection(filepath.Join(root, "src"), true)
	if !ok || section.SectionLabel != "Git" {
		t.Fatal("folder has no Git section")
	}
	var view ui.ContextMenuItem
	labels := []string{}
	for _, item := range section.Items {
		labels = append(labels, item.Label+"|"+item.Detail)
		if item.Label == "View Changes" {
			view = item
		}
	}
	if view.Disabled || view.Detail != "2 files" {
		t.Fatalf("folder menu=%v", labels)
	}
	view.Action()
	if view := app.activeGitReview(); view == nil || view.screen.Scope() != "src" {
		t.Fatal("View Changes did not open the review scoped to src")
	}
	clean, ok := app.explorerGitSection(filepath.Join(root, "unchanged.go"), false)
	if !ok || !clean.Items[0].Disabled || clean.Items[0].Detail != "no changes" {
		t.Fatalf("clean file menu=%+v", clean.Items[0])
	}
	if _, ok := app.explorerGitSection(filepath.Join(filepath.Dir(root), "outside"), false); ok {
		t.Fatal("a path outside the repository offered Git actions")
	}
}

// Staging reads disk: a file with unsaved editor bytes is refused rather than
// staging something the user is not looking at.
func TestGitStageRefusesUnsavedEditorBytes(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paths := openEditorTabs(t, app, "dirty.go")
	app.EditorState.Editor.ReplaceDocument("package dirty // unsaved\n")
	change := workspace.GitChange{Path: "dirty.go", AbsolutePath: paths[0], Area: workspace.GitAreaWorking, Code: "M"}
	if cmd := app.stageGitChanges([]workspace.GitChange{change}); cmd != nil {
		t.Fatal("staging a dirty buffer started a mutation")
	}
	intent := gitdiff.FileIntent{Op: gitdiff.OpStage, Change: change, Comparison: workspace.GitComparison{Path: "dirty.go", Fingerprint: "fp"}}
	if cmd := app.handleGitFileIntent(intent); cmd != nil {
		t.Fatal("staging a dirty buffer from the review started a mutation")
	}
}

// The prepared change set flows to the panel and to an open review, and a
// reveal requested before preparation lands once the file exists.
func TestPreparedChangesReachPanelAndReview(t *testing.T) {
	app := newFullTestApp(t)
	change := workspace.GitChange{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), change))
	app.openGitFileReview(change)
	view := app.activeGitReview()
	if view == nil || view.reveal == "" {
		t.Fatal("reveal was consumed before the file was prepared")
	}
	app.gitPrepareGen++
	files := gitdiff.PrepareChanges([]workspace.GitChangeComparison{{Change: change, Comparison: workspace.GitComparison{Path: "a.go", Before: "a\n", After: "b\n", BeforePresent: true, AfterPresent: true}}})
	app.applyGitChangesPrepared(gitChangesPreparedMsg{workspace: app.workspaceSwitchGen.Load(), generation: app.gitPrepareGen, files: files})
	if len(app.gitPanel.PreparedList()) != 1 || view.screen.Source() != gitreview.SourceChanges {
		t.Fatal("prepared set did not reach the panel")
	}
	if total, _, _ := view.screen.Document().Stats(); total != 1 || view.reveal != "" {
		t.Fatalf("review files=%d pending reveal=%q", total, view.reveal)
	}
	stale := gitChangesPreparedMsg{workspace: app.workspaceSwitchGen.Load(), generation: app.gitPrepareGen - 1}
	app.applyGitChangesPrepared(stale)
	if len(app.gitPanel.PreparedList()) != 1 {
		t.Fatal("a stale preparation replaced the current one")
	}
}

// Opening the review on a short terminal hides the dock instead of leaving a
// few unreadable rows; a tall terminal keeps both visible.
func TestGitReviewMakesRoomOnShortTerminals(t *testing.T) {
	for _, test := range []struct {
		height   int
		keepDock bool
	}{{30, false}, {60, true}} {
		app := newFullTestApp(t)
		app.width, app.height = 120, test.height
		app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), workspace.GitChange{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}))
		app.toggleGitPanel()
		app.openGitReview("", "")
		if app.gitPanelOpen() != test.keepDock {
			t.Fatalf("height=%d dock open=%v want %v", test.height, app.gitPanelOpen(), test.keepDock)
		}
	}
}

// The review owns Ctrl+F, copy and hunk keys through the real key router.
func TestGitReviewKeysReachTheReviewThroughTheShell(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 140, 44
	change := workspace.GitChange{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), change))
	app.openGitReview("", "")
	review := app.activeGitReview().screen
	review.ShowChanges(gitdiff.PrepareChanges([]workspace.GitChangeComparison{{Change: change, Comparison: workspace.GitComparison{Path: "a.go", Before: "a\n", After: "b\n", BeforePresent: true, AfterPresent: true, Fingerprint: "fp"}}}), "main")
	app.paintShellFrame()
	if app.rootHost.Focused() != review.DocumentID() {
		t.Fatalf("review focus=%s", app.rootHost.Focused())
	}
	app.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	app.paintShellFrame()
	if !review.FindOpen() || app.rootHost.Focused() != review.FindID() {
		t.Fatalf("Ctrl+F did not open the review find bar (focus=%s)", app.rootHost.Focused())
	}
}

// Every file, folder, commit and the all-changes review is its own tab, so
// several reviews stay open together and reopening activates the existing tab.
func TestGitReviewsAreIndependentTabs(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 50
	a := workspace.GitChange{Path: "src/a.go", Area: workspace.GitAreaWorking, Code: "M"}
	b := workspace.GitChange{Path: "src/b.go", Area: workspace.GitAreaWorking, Code: "M"}
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), a, b))
	app.openGitFileReview(a)
	first := app.activeGitReview()
	app.openGitFileReview(b)
	second := app.activeGitReview()
	app.openGitReview("", "")
	all := app.activeGitReview()
	app.openGitCommitReview(workspace.GitCommitSummary{Hash: strings.Repeat("a", 40), ShortHash: "aaaaaaa"})
	commit := app.activeGitReview()
	if len(app.gitReviews) != 4 || first == second || second == all || all == commit {
		t.Fatalf("reviews=%d: each review must be its own tab", len(app.gitReviews))
	}
	if first.screen.DocumentID() == second.screen.DocumentID() {
		t.Fatal("two review tabs share a document identity")
	}
	app.openGitFileReview(a)
	if app.activeGitReview() != first || len(app.gitReviews) != 4 {
		t.Fatal("reopening a file review created a duplicate tab")
	}
	app.gitPrepareGen++
	files := gitdiff.PrepareChanges([]workspace.GitChangeComparison{
		{Change: a, Comparison: workspace.GitComparison{Path: "src/a.go", Before: "x\n", After: "y\n", BeforePresent: true, AfterPresent: true}},
		{Change: b, Comparison: workspace.GitComparison{Path: "src/b.go", Before: "x\n", After: "z\n", BeforePresent: true, AfterPresent: true}},
	})
	app.applyGitChangesPrepared(gitChangesPreparedMsg{workspace: app.workspaceSwitchGen.Load(), generation: app.gitPrepareGen, files: files})
	for name, view := range map[string]*gitReviewView{"a": first, "b": second, "all": all} {
		want := 1
		if name == "all" {
			want = 2
		}
		if total, _, _ := view.screen.Document().Stats(); total != want {
			t.Fatalf("%s tab shows %d files, want %d", name, total, want)
		}
	}
	if commit.screen.Source() != gitreview.SourceCommit {
		t.Fatal("prepared working changes replaced a commit tab")
	}
	app.closeGitReview(second.tab)
	if _, open := app.gitReviews[second.tab]; open {
		t.Fatal("closed review kept its screen")
	}
}

// Streamed draft text and the final message apply only to the current run;
// a stopped or superseded run cannot overwrite a newer draft.
func TestCommitMessageWriterIgnoresStaleRuns(t *testing.T) {
	app := newFullTestApp(t)
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), workspace.GitChange{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}))
	app.gitCommitWriteSequence = 2
	app.gitPanel.BeginWriting()
	app.applyCommitMessageDelta(gitCommitDeltaMsg{sequence: 1, text: "stale"})
	app.applyCommitMessageDelta(gitCommitDeltaMsg{sequence: 2, text: "Fix "})
	app.applyCommitMessageWritten(gitCommitWrittenMsg{sequence: 1, message: "stale"})
	if !app.gitPanel.Writing() || app.gitPanel.Message() != "Fix " {
		t.Fatalf("stale run touched the draft: %q", app.gitPanel.Message())
	}
	app.applyCommitMessageWritten(gitCommitWrittenMsg{sequence: 2, message: "Fix parser"})
	if app.gitPanel.Writing() || app.gitPanel.Message() != "Fix parser" {
		t.Fatalf("final message=%q", app.gitPanel.Message())
	}
}
