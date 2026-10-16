package gitpanel

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitdiff"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func snapshot(changes ...workspace.GitChange) workspace.GitSnapshot {
	return workspace.GitSnapshot{RepositoryRoot: "/repo", Branch: "main", Changes: changes}
}

func working(path string) workspace.GitChange {
	return workspace.GitChange{Path: path, AbsolutePath: "/repo/" + path, Area: workspace.GitAreaWorking, Code: "M"}
}

func staged(path string) workspace.GitChange {
	return workspace.GitChange{Path: path, AbsolutePath: "/repo/" + path, Area: workspace.GitAreaStaged, Code: "M"}
}

func mount(t *testing.T, panel *Panel, width, height int) (*ui.Host, string) {
	t.Helper()
	host := ui.NewHost(panel.th, panel)
	panel.SetFocusHandler(host.Focus, host.Focused)
	host.Resize(width, height)
	host.Layout()
	return host, repaint(host, width, height)
}

func repaint(host *ui.Host, width, height int) string {
	host.Layout()
	buf := surface.NewCellBuffer(width, height)
	host.Paint(buf)
	var text strings.Builder
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			current := buf.Get(column, row)
			switch {
			case current.Content != "":
				text.WriteString(current.Content)
			case current.Char == 0:
				text.WriteByte(' ')
			case current.Width > 0:
				text.WriteRune(current.Char)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func key(code rune, text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: text} }

func TestPanelGroupsChangesAndPreviewsSelection(t *testing.T) {
	panel := New(theme.DefaultDark())
	panel.SetSnapshot(snapshot(staged("README.md"), working("src/main.go"), working("src/util.go")))
	panel.SetPrepared(gitdiff.PrepareChanges([]workspace.GitChangeComparison{{
		Change:     staged("README.md"),
		Comparison: workspace.GitComparison{Path: "README.md", Area: workspace.GitAreaStaged, Before: "old\n", After: "new title\n", BeforePresent: true, AfterPresent: true},
	}}))
	_, text := mount(t, panel, 140, 20)
	for _, want := range []string{"Changes 3", "History", "Branches", "main", "Staged  1", "Changes  2", "Unstage all", "Stage all", "main.go", "src", "✓ Commit 1", "new title"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "README.md") > strings.Index(text, "main.go") {
		t.Fatal("staged section must lead")
	}
}

func TestPanelSpaceStagesSelectionAndCursorStaysInPlace(t *testing.T) {
	panel := New(theme.DefaultDark())
	panel.SetSnapshot(snapshot(working("a.go"), working("b.go"), working("c.go")))
	host, _ := mount(t, panel, 140, 20)
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	result := host.Handle(key(tea.KeySpace, " "))
	changes, ok := result.Action.Payload.([]workspace.GitChange)
	if result.Action.ID != ActionStage || !ok || len(changes) != 1 || changes[0].Path != "b.go" {
		t.Fatalf("space=%+v", result.Action)
	}
	// After staging b.go the cursor lands on the next change, not the top.
	panel.SetSnapshot(snapshot(staged("b.go"), working("a.go"), working("c.go")))
	if change, _ := panel.SelectedChange(); change.Path != "a.go" && change.Path != "c.go" {
		t.Fatalf("cursor jumped to %q", change.Path)
	}
	if change, _ := panel.SelectedChange(); change.Path == "b.go" {
		t.Fatal("cursor followed the staged file instead of staying in place")
	}
}

func TestPanelCommitRequestIsTheVisibleSet(t *testing.T) {
	panel := New(theme.DefaultDark())
	panel.SetSnapshot(snapshot(staged("a.go"), working("b.go")))
	mount(t, panel, 140, 20)
	result := panel.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCommit})
	if result.Action != nil {
		t.Fatal("empty message committed")
	}
	panel.SetMessage("Ship it")
	result = panel.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCommit})
	request, ok := result.Action.Payload.(CommitRequest)
	if !ok || request.StageAll || len(request.Paths) != 1 || request.Paths[0] != "a.go" {
		t.Fatalf("staged commit request=%+v", result.Action)
	}
	if result := panel.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCommit}); result.Action != nil {
		t.Fatal("a second commit started while the first is running")
	}
	panel.CompleteCommit(request)
	if panel.Message() != "" {
		t.Fatal("committed draft was not cleared")
	}

	panel.SetSnapshot(snapshot(working("b.go"), working("c.go")))
	panel.SetMessage("Everything")
	result = panel.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCommit})
	request = result.Action.Payload.(CommitRequest)
	if !request.StageAll || strings.Join(request.Paths, ",") != "b.go,c.go" {
		t.Fatalf("commit-all request=%+v", request)
	}
}

func TestPanelRefusesCommitWithConflicts(t *testing.T) {
	panel := New(theme.DefaultDark())
	conflict := workspace.GitChange{Path: "x.go", Area: workspace.GitAreaConflict, Code: "U"}
	panel.SetSnapshot(snapshot(staged("a.go"), conflict))
	panel.SetMessage("merge")
	if result := panel.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCommit}); result.Action != nil {
		t.Fatal("commit allowed with unresolved conflicts")
	}
}

func TestPanelCleanTreeAndNarrowLayout(t *testing.T) {
	panel := New(theme.DefaultDark())
	panel.SetSnapshot(snapshot())
	_, text := mount(t, panel, 60, 16)
	if !strings.Contains(text, "Working tree clean") {
		t.Fatalf("clean state:\n%s", text)
	}
	panel.SetSnapshot(snapshot(working("a.go")))
	host, _ := mount(t, panel, 60, 16)
	if host.Component(PreviewID) != nil && !host.Component(PreviewID).Bounds().Empty() {
		t.Fatal("narrow panel must give the list the full width")
	}
	host.Focus(ListID)
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Action == nil || result.Action.ID != ActionReview {
		t.Fatalf("Enter must open the full review on narrow panels: %+v", result.Action)
	}
}

func TestPanelStreamsGeneratedMessageAndRestoresDraftOnFailure(t *testing.T) {
	panel := New(theme.DefaultDark())
	panel.SetSnapshot(snapshot(working("a.go")))
	host, _ := mount(t, panel, 140, 20)
	panel.SetMessage("my own draft")
	result := panel.HandleAction(ui.EventContext{}, ui.Action{ID: ActionGenerate})
	request, ok := result.Action.Payload.(GenerateRequest)
	if !ok || !request.StageAll {
		t.Fatalf("generate with nothing staged must describe everything: %+v", result.Action)
	}
	panel.BeginWriting()
	panel.AppendWriting("Fix ")
	panel.AppendWriting("parser")
	text := repaint(host, 140, 20)
	if panel.Message() != "Fix parser" || !strings.Contains(text, "Aeron is writing") || !strings.Contains(text, "[ Stop ]") {
		t.Fatalf("streaming state message=%q:\n%s", panel.Message(), text)
	}
	if result := panel.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCommit}); result.Action != nil {
		t.Fatal("commit allowed while the message is still being written")
	}
	panel.FinishWriting("", context.Canceled)
	if panel.Message() != "my own draft" || panel.Writing() {
		t.Fatalf("stop did not restore the draft: %q", panel.Message())
	}
	panel.BeginWriting()
	panel.FinishWriting("Fix parser edge case", nil)
	if panel.Message() != "Fix parser edge case" || !strings.Contains(repaint(host, 140, 20), "[ Generate ]") {
		t.Fatal("finished draft was not installed")
	}
}

func TestPanelDividerDragsBetweenListAndPreview(t *testing.T) {
	panel := New(theme.DefaultDark())
	panel.SetSnapshot(snapshot(working("a.go")))
	host, _ := mount(t, panel, 140, 20)
	before := panel.divider
	bounds := panel.splitter.Bounds()
	host.Handle(tea.MouseClickMsg{X: bounds.X, Y: bounds.Y + 4, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: bounds.X + 12, Y: bounds.Y + 4, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: bounds.X + 12, Y: bounds.Y + 4, Button: tea.MouseLeft})
	repaint(host, 140, 20)
	if panel.divider != before+12 {
		t.Fatalf("divider %d → %d, want +12", before, panel.divider)
	}
}

// The status row offers the controls the repository state needs, and the
// one that finishes a merge survives a narrow panel.
func TestStatusRowControlsFollowRepositoryState(t *testing.T) {
	panel := New(theme.DefaultDark())
	snapshot := workspace.GitSnapshot{RepositoryRoot: "/r", Branch: "main", Upstream: "origin/main", Ahead: 2, Behind: 1, Remotes: []string{"origin"}}
	panel.SetSnapshot(snapshot)
	labels := func() []string {
		panel.Layout(ui.LayoutContext{}, ui.Rect{W: 60, H: 20})
		var out []string
		for _, button := range panel.statusButtons() {
			if !button.Bounds().Empty() {
				out = append(out, button.Semantics().Label)
			}
		}
		return out
	}
	if got := strings.Join(labels(), ","); got != "Pull ↓1,Push ↑2" && got != "Fetch,Pull ↓1,Push ↑2" {
		t.Fatalf("sync controls = %q", got)
	}
	snapshot.Operation = workspace.GitOperationMerge
	snapshot.Changes = []workspace.GitChange{{Path: "a.go", Area: workspace.GitAreaConflict}}
	panel.SetSnapshot(snapshot)
	if got := strings.Join(labels(), ","); got != "Resolve 1,Abort" {
		t.Fatalf("conflict controls = %q", got)
	}
	snapshot.Changes = []workspace.GitChange{{Path: "a.go", Area: workspace.GitAreaStaged}}
	panel.SetSnapshot(snapshot)
	if got := strings.Join(labels(), ","); got != "Complete,Abort" {
		t.Fatalf("resolved controls = %q", got)
	}
	panel.SetRemoteBusy("Push")
	if got := strings.Join(labels(), ","); got != "Stop" {
		t.Fatalf("busy controls = %q", got)
	}
}

// Either side of the split view can be hidden and restored; the other side
// takes the whole width meanwhile.
func TestSplitSidesCollapseAndRestore(t *testing.T) {
	panel := New(theme.DefaultDark())
	panel.SetSnapshot(workspace.GitSnapshot{RepositoryRoot: "/r", Branch: "main", Changes: []workspace.GitChange{{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}}})
	layout := func() { panel.Layout(ui.LayoutContext{}, ui.Rect{W: 140, H: 30}) }
	key := func(k string) {
		panel.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: rune(k[0]), Text: k}))
		layout()
	}
	layout()
	if panel.list.Bounds().Empty() || panel.preview.Bounds().Empty() {
		t.Fatal("split view not showing both sides")
	}
	key("[")
	if !panel.list.Bounds().Empty() || panel.preview.Bounds().W < 130 {
		t.Fatalf("list not hidden: list=%v preview=%v", panel.list.Bounds(), panel.preview.Bounds())
	}
	key("[")
	key("]")
	if !panel.preview.Bounds().Empty() || panel.list.Bounds().W < 130 {
		t.Fatalf("preview not hidden: list=%v preview=%v", panel.list.Bounds(), panel.preview.Bounds())
	}
	key("]")
	if panel.list.Bounds().Empty() || panel.preview.Bounds().Empty() {
		t.Fatal("split view not restored")
	}
}
