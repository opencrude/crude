package gitreview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/diffview"
	"crude/tui/screens/gitdiff"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func change(path, before, after string, area workspace.GitChangeArea) workspace.GitChangeComparison {
	c := workspace.GitChange{Path: path, AbsolutePath: "/repo/" + path, Area: area, Code: "M"}
	return workspace.GitChangeComparison{Change: c, Comparison: workspace.GitComparison{
		RepositoryRoot: "/repo", Path: path, AbsolutePath: "/repo/" + path, Area: area,
		Before: before, After: after, BeforePresent: true, AfterPresent: true, Fingerprint: "fp-" + path,
	}}
}

func numbered(n int, replace map[int]string) string {
	var text strings.Builder
	for line := 1; line <= n; line++ {
		if value, ok := replace[line]; ok {
			text.WriteString(value + "\n")
			continue
		}
		text.WriteString("line " + string(rune('a'+line%26)) + "\n")
	}
	return text.String()
}

func reviewFiles() []gitdiff.File {
	return gitdiff.PrepareChanges([]workspace.GitChangeComparison{
		change("src/main.go", numbered(40, nil), numbered(40, map[int]string{20: "changed twenty"}), workspace.GitAreaWorking),
		change("src/util.go", numbered(10, nil), numbered(10, map[int]string{2: "changed two"}), workspace.GitAreaStaged),
		change("notes/readme.md", "a\n", "b\n", workspace.GitAreaWorking),
	})
}

func render(t *testing.T, screen *Screen, width, height int) (*ui.Host, string) {
	t.Helper()
	host := ui.NewHost(screen.th, screen)
	screen.SetFocusHandler(host.Focus)
	host.Resize(width, height)
	host.Layout()
	buf := surface.NewCellBuffer(width, height)
	host.Paint(buf)
	return host, bufferText(buf)
}

func bufferText(buf *surface.CellBuffer) string {
	var text strings.Builder
	for row := 0; row < buf.Height(); row++ {
		for column := 0; column < buf.Width(); column++ {
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

func TestReviewStacksEveryFileWithNavigatorAndControls(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.ShowChanges(reviewFiles(), "main")
	_, text := render(t, screen, 160, 60)
	for _, want := range []string{"Working tree changes", "3 files", "[ Split ]", "[ Collapse all ]", "[ Refresh ]", "util.go", "main.go", "readme.md", "changed twenty", "changed two", "unchanged lines", "Unstage", "Stage", "Discard"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in review:\n%s", want, text)
		}
	}
	if strings.Index(text, "changed two") > strings.Index(text, "changed twenty") {
		t.Fatalf("staged file is not listed before working changes:\n%s", text)
	}
}

func TestReviewScopeIsAPermanentTabFilter(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.ShowChanges(reviewFiles(), "main")
	screen.SetScope("src/")
	host, text := render(t, screen, 160, 60)
	if strings.Contains(text, "readme.md") || !strings.Contains(text, "main.go") || !strings.Contains(text, "[ All changes ]") {
		t.Fatalf("scope did not filter to src:\n%s", text)
	}
	if screen.Title() != "Changes · src" {
		t.Fatalf("title=%q", screen.Title())
	}
	host.Focus(screen.ID())
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if screen.Scope() != "src" || result.Action == nil || result.Action.ID != ActionReturn {
		t.Fatalf("Esc must return from the tab, keeping its scope: %+v", result.Action)
	}
}

func TestReviewInstancesHaveDistinctIdentities(t *testing.T) {
	first, second := NewWithID(theme.DefaultDark(), "gitreview/1"), NewWithID(theme.DefaultDark(), "gitreview/2")
	if first.DocumentID() == second.DocumentID() || first.FindID() == second.FindID() || first.DividerID() == second.DividerID() {
		t.Fatal("two review tabs share component identities")
	}
}

func TestReviewNavigatorDividerDrags(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.ShowChanges(reviewFiles(), "main")
	host, _ := render(t, screen, 160, 40)
	before := screen.navWidth
	divider := screen.divider.Bounds()
	host.Handle(tea.MouseClickMsg{X: divider.X, Y: divider.Y + 3, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: divider.X + 10, Y: divider.Y + 3, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: divider.X + 10, Y: divider.Y + 3, Button: tea.MouseLeft})
	host.Layout()
	if screen.navWidth != before+10 {
		t.Fatalf("navigator width %d → %d, want +10", before, screen.navWidth)
	}
}

func TestReviewFileIntentCarriesExactComparison(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.ShowChanges(reviewFiles(), "main")
	host, _ := render(t, screen, 160, 60)
	host.Focus(screen.DocumentID())
	result := host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"})
	if result.Action == nil || result.Action.ID != gitdiff.ActionFile {
		t.Fatalf("stage key produced %+v", result.Action)
	}
	intent := result.Action.Payload.(gitdiff.FileIntent)
	if intent.Op != gitdiff.OpUnstage || intent.Comparison.Fingerprint != "fp-src/util.go" {
		t.Fatalf("first (staged) file intent=%+v", intent)
	}
}

func TestReviewCommitIsReadOnly(t *testing.T) {
	screen := New(theme.DefaultDark())
	files := gitdiff.PrepareComparisons([]workspace.GitComparison{{Path: "a.go", Before: "x\n", After: "y\n", BeforePresent: true, AfterPresent: true, Historical: true}})
	screen.ShowCommit(workspace.GitCommitEvidence{Commit: workspace.GitCommitSummary{ShortHash: "abc1234", Subject: "Fix it"}}, files)
	host, text := render(t, screen, 140, 30)
	if !strings.Contains(text, "abc1234") || !strings.Contains(text, "Fix it") || strings.Contains(text, "Discard") || strings.Contains(text, " Stage ") {
		t.Fatalf("commit review:\n%s", text)
	}
	host.Focus(screen.DocumentID())
	if result := host.Handle(tea.KeyPressMsg{Code: 's', Text: "s"}); result.Action != nil {
		t.Fatalf("read-only commit emitted %+v", result.Action)
	}
}

func TestReviewFindBarSearchesAndClosesWithEsc(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.ShowChanges(reviewFiles(), "main")
	host, _ := render(t, screen, 160, 40)
	host.Focus(screen.DocumentID())
	host.Handle(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !screen.FindOpen() || host.Focused() != screen.FindID() {
		t.Fatalf("Ctrl+F open=%v focus=%s", screen.FindOpen(), host.Focused())
	}
	for _, r := range "changed" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if command := screen.document.SearchCommand(); command != nil {
		command().(diffview.SearchReadyMsg).Apply()
	}
	t.Cleanup(screen.document.WaitSearch)
	host.Layout()
	buf := surface.NewCellBuffer(160, 40)
	host.Paint(buf)
	if text := bufferText(buf); !strings.Contains(text, "Find · 1 / 2 matches") {
		t.Fatalf("find status missing:\n%s", text)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if screen.FindOpen() || screen.Document().Query() != "" || host.Focused() != screen.DocumentID() {
		t.Fatal("Esc must close find and return to the document")
	}
}

func TestReviewKeyboardRefreshRetainsExactScreenOwner(t *testing.T) {
	screen := NewWithID(nil, "gitreview/exact")
	host, _ := render(t, screen, 140, 30)
	host.Focus(screen.DocumentID())
	result := host.Handle(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if result.Action == nil || result.Action.ID != ActionRefresh || result.Action.Payload != screen {
		t.Fatalf("refresh lost owner: %+v", result.Action)
	}
}
