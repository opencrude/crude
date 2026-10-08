package gitreview

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func reviewFixture() workspace.GitComparison {
	return workspace.GitComparison{RepositoryRoot: "/repo", Path: "src/main.go", AbsolutePath: "/repo/src/main.go", Area: workspace.GitAreaWorking, BaseLabel: "Index", TargetLabel: "Working tree (disk)", Before: "package main\nvar value = 1\n", After: "package main\nvar value = 2\n", Fingerprint: "exact-reviewed-bytes", BeforePresent: true, AfterPresent: true}
}

func reviewHost(screen *Screen, width, height int) *ui.Host {
	host := ui.NewHost(screen.th, screen)
	screen.SetFocusHandler(host.Focus)
	host.Resize(width, height)
	host.Layout()
	host.Paint(surface.NewCellBuffer(width, height))
	return host
}

func reviewText(buf *surface.CellBuffer) string {
	var text strings.Builder
	for row := 0; row < buf.Height(); row++ {
		for column := 0; column < buf.Width(); column++ {
			current := buf.Get(column, row)
			if current.Content != "" {
				text.WriteString(current.Content)
			} else if current.Char == 0 {
				text.WriteByte(' ')
			} else if current.Width > 0 {
				text.WriteRune(current.Char)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func TestGitReviewNativeTabsAndCompleteUnifiedComparison(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.SetComparison(PrepareComparison(screen.th, reviewFixture()))
	host := reviewHost(screen, 120, 28)
	buf := surface.NewCellBuffer(120, 28)
	host.Paint(buf)
	text := reviewText(buf)
	for _, expected := range []string{"src/main.go", "Changes", "History", "Branches", "Index", "Working tree (disk)", "-var value = 1", "+var value = 2", "Stage file", "Review commit"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in native review:\n%s", expected, text)
		}
	}
	if host.Component(ListID) != nil {
		t.Fatal("default main review duplicates sidebar file inventory")
	}
	if !host.Focus(TabsID) {
		t.Fatal("focus mode strip")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if screen.Mode() != ModeHistory || result.Action == nil || result.Action.ID != ActionMode || result.Action.Payload.(ModeRequest).Mode != ModeHistory {
		t.Fatalf("shared tab mode intent=%+v mode=%s", result, screen.Mode())
	}
}

func TestGitReviewNarrowFallbackRestoresPreferenceAndBounds(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.SetComparison(PrepareComparison(screen.th, reviewFixture()))
	screen.SetPresentation(SideBySide)
	host := reviewHost(screen, 150, 30)
	if screen.Presentation() != SideBySide {
		t.Fatal("wide preferred presentation was not used")
	}
	before := screen.viewer
	host.Resize(50, 25)
	host.Layout()
	if screen.Presentation() != Unified || screen.PreferredPresentation() != SideBySide {
		t.Fatal("narrow fallback overwrote preferred layout")
	}
	for _, width := range []int{1, 8, 23, 50, 120} {
		host.Resize(width, 25)
		host.Layout()
		assertReviewBounds(t, screen, ui.Rect{W: width, H: 25})
	}
	host.Resize(150, 30)
	host.Layout()
	if screen.Presentation() != SideBySide || screen.viewer != before {
		t.Fatal("wide restore replaced the reviewed source or preference")
	}
}

func assertReviewBounds(t *testing.T, component ui.Component, parent ui.Rect) {
	t.Helper()
	bounds := component.Bounds()
	if !bounds.Empty() && (bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > parent.W || bounds.Y+bounds.H > parent.H) {
		t.Fatalf("%s bounds=%+v outside parent=%+v", component.ID(), bounds, parent)
	}
	if group, ok := component.(ui.Parent); ok {
		for _, child := range group.Children() {
			assertReviewBounds(t, child, ui.Rect{W: bounds.W, H: bounds.H})
		}
	}
}

func TestGitReviewReadonlyScrollSearchCopyAndTypedMutation(t *testing.T) {
	screen := New(theme.DefaultDark())
	comparison := reviewFixture()
	var before, after strings.Builder
	for line := 0; line < 160; line++ {
		fmt.Fprintf(&before, "old line %d %s\n", line, strings.Repeat("horizontal ", 20))
		fmt.Fprintf(&after, "new line %d %s\n", line, strings.Repeat("horizontal ", 20))
	}
	comparison.Before, comparison.After = before.String(), after.String()
	screen.SetComparison(PrepareComparison(screen.th, comparison))
	host := reviewHost(screen, 110, 26)
	if !host.Focus(screen.DocumentID()) {
		t.Fatal("focus unified reader")
	}
	value := screen.viewer.Comparison().After
	host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if screen.viewer.Comparison().After != value {
		t.Fatal("read-only comparison accepted edits")
	}
	document, _ := host.ScreenRect(screen.DocumentID())
	host.Handle(tea.MouseWheelMsg{X: document.X + 4, Y: document.Y + 2, Button: tea.MouseWheelDown})
	if screen.viewer.ScrollOffset() == 0 {
		t.Fatal("native diff wheel did not scroll")
	}
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	result := host.Handle(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if result.Cmd == nil || result.Action != nil {
		t.Fatal("copy must return an off-thread clipboard command, not navigation")
	}
	host.Handle(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	host.MarkLayout()
	host.Layout()
	if host.Focused() != SearchID {
		t.Fatalf("search focus=%s", host.Focused())
	}
	for _, character := range "new line 70" {
		host.Handle(tea.KeyPressMsg{Code: character, Text: string(character)})
	}
	if !screen.viewer.NextMatch(false) {
		t.Fatal("native search did not find comparison content")
	}
	result = screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionStage})
	request, ok := result.Action.Payload.(ComparisonRequest)
	if !ok || request.Comparison.Fingerprint != comparison.Fingerprint || request.Comparison.After != comparison.After {
		t.Fatalf("mutation does not carry the exact reviewed bytes: %+v", result)
	}
	screen.SetError(workspace.ErrGitReviewStale)
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionStage}); result.Action != nil {
		t.Fatal("stale error left stage enabled")
	}
}

func TestGitReviewCommitDraftExactSetAndStaleFailures(t *testing.T) {
	screen := New(theme.DefaultDark())
	first, second := reviewFixture(), reviewFixture()
	first.Area, second.Area, second.Path = workspace.GitAreaStaged, workspace.GitAreaStaged, "second.go"
	review := workspace.GitCommitReview{RepositoryRoot: "/repo", Head: "head", Fingerprint: "staged-set", Comparisons: []workspace.GitComparison{first, second}}
	screen.InstallComparisons([]PreparedComparison{PrepareComparison(screen.th, first), PrepareComparison(screen.th, second)})
	screen.SetCommitReview(review)
	screen.SetCommitMessage("Subject\n\nDetailed message")
	if screen.canCommit() {
		t.Fatal("commit enabled before second staged file was reviewed")
	}
	screen.list.list.SelectIndex(1)
	row, _ := screen.list.list.Selected()
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "gitreview.activate", Payload: row})
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCommit})
	if result.Action == nil || result.Action.ID != ActionCommit {
		t.Fatalf("reviewed commit not available: %+v", result)
	}
	request := result.Action.Payload.(CommitRequest)
	if request.Message != "Subject\n\nDetailed message" || request.Review.Fingerprint != review.Fingerprint || len(request.Review.Comparisons) != 2 {
		t.Fatalf("commit lost exact draft or set: %+v", request)
	}
	screen.SetError(errors.New("commit identity unavailable"))
	if screen.CommitMessage() != request.Message {
		t.Fatal("failed commit destroyed draft")
	}
	screen.SetError(workspace.ErrGitReviewStale)
	if screen.canCommit() || screen.CommitMessage() != request.Message {
		t.Fatal("stale set did not preserve draft while refusing submission")
	}
	screen.SetComparison(PrepareComparison(screen.th, reviewFixture()))
	screen.SetMode(ModeBranches)
	screen.SetMode(ModeChanges)
	if screen.CommitMessage() != request.Message {
		t.Fatal("file/mode browsing destroyed commit draft")
	}
}

func TestGitReviewBranchSelectionIsInspectionOnly(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.SetSnapshot(workspace.GitSnapshot{RepositoryRoot: "/repo", Head: "head", Branches: []workspace.GitBranchSummary{{Name: "main", Current: true}, {Name: "feature"}}})
	screen.SetMode(ModeBranches)
	host := reviewHost(screen, 120, 26)
	host.Focus(ListID)
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyDown}); result.Action != nil {
		t.Fatal("branch selection emitted mutation")
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Action != nil {
		t.Fatal("branch inspection checked out a branch")
	}
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCheckout})
	if result.Action == nil || result.Action.Payload.(CheckoutRequest).Branch.Name != "feature" {
		t.Fatalf("explicit checkout=%+v", result)
	}
}

func TestGitReviewBinaryLoadingAndEmptyAreExplicit(t *testing.T) {
	for _, state := range []string{"binary", "empty", "loading", "error"} {
		t.Run(state, func(t *testing.T) {
			screen := New(theme.DefaultDark())
			comparison := reviewFixture()
			want := ""
			switch state {
			case "binary":
				comparison.Binary = true
				want = "Binary file"
			case "empty":
				comparison.Before, comparison.After = "", ""
				comparison.Untracked = true
				want = "Untracked empty file"
			case "loading":
				want = "Loading comparison"
			case "error":
				want = "Comparison unavailable"
			}
			screen.SetComparison(PrepareComparison(screen.th, comparison))
			switch state {
			case "loading":
				screen.SetLoading(true)
			case "error":
				screen.SetError(errors.New("missing index object"))
			}
			host := reviewHost(screen, 120, 25)
			buf := surface.NewCellBuffer(120, 25)
			host.Paint(buf)
			if !strings.Contains(reviewText(buf), want) {
				t.Fatalf("%s silently rendered as a successful empty diff:\n%s", state, reviewText(buf))
			}
		})
	}
}
