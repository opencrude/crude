package sourcecontrol

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestScreenPaintsGroupedRepositoryState(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetSnapshot(workspace.GitSnapshot{
		Branch: "feature/polish",
		Changes: []workspace.GitChange{
			{Path: "staged.go", Area: workspace.GitAreaStaged, Code: "M", Description: "Modified"},
			{Path: "working.go", Area: workspace.GitAreaWorking, Code: "M", Description: "Modified"},
			{Path: "new.go", Area: workspace.GitAreaUntracked, Code: "U", Description: "Untracked"},
		},
		RecentCommits: []workspace.GitCommitSummary{{
			Hash: "0123456789abcdef", ShortHash: "01234567", Subject: "polish source control",
		}},
		Branches: []workspace.GitBranchSummary{
			{Name: "feature/polish", Current: true},
			{Name: "main"},
			{Name: "origin/feature/polish", Remote: true},
		},
	})
	host := ui.NewHost(th, screen)
	host.Resize(42, 18)
	host.Layout()
	buf := surface.NewCellBuffer(42, 18)
	host.Paint(buf)
	text := sourceControlText(buf)
	if strings.Contains(text, "SOURCE CONTROL") {
		t.Fatalf("panel name must not paint:\n%s", text)
	}
	for _, want := range []string{
		"feature/polish", "Staged", "Changes", "staged.go", "working.go", "new.go", "Review", "History", "Branches",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"RECENT COMMITS", "LOCAL BRANCHES", "REMOTE BRANCHES", "UNTRACKED", "polish source control", "origin/feature/polish", "Commit message"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("sidebar retained %q:\n%s", unwanted, text)
		}
	}
}

func TestFilterAndChangeActivationEmitTypedActions(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	change := workspace.GitChange{
		Path: "main.go", AbsolutePath: "/repo/main.go",
		Area: workspace.GitAreaWorking, Code: "M",
	}
	screen.SetSnapshot(workspace.GitSnapshot{Changes: []workspace.GitChange{change}})
	host := ui.NewHost(th, screen)
	screen.SetFocusHandler(host.Focus)
	host.Resize(40, 12)
	host.Layout()

	if !host.Focus(FilterID) {
		t.Fatal("focus filter")
	}
	for _, r := range "main" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if screen.Filter() != "main" {
		t.Fatalf("filter=%q", screen.Filter())
	}

	if !host.Focus(ListID) {
		t.Fatal("focus list")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.ID != ActionOpenChange {
		t.Fatalf("change activation=%#v", result)
	}
	got, ok := result.Action.Payload.(workspace.GitChange)
	if !ok || got.Path != change.Path {
		t.Fatalf("change payload=%#v", result.Action.Payload)
	}
}

func TestSidebarSelectionNeverStagesAndRequiresExplicitActivation(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetSnapshot(workspace.GitSnapshot{Changes: []workspace.GitChange{{Path: "main.go", Area: workspace.GitAreaWorking, Code: "M"}}})
	host := ui.NewHost(th, screen)
	host.Resize(42, 18)
	host.Layout()
	row, _ := host.ScreenRect(ListID)
	click := tea.MouseClickMsg{X: row.X + 2, Y: row.Y + screen.list.SelectedIndex(), Button: tea.MouseLeft}
	if result := host.Handle(click); result.Action != nil {
		t.Fatalf("single selection emitted action=%+v", result.Action)
	}
	host.Handle(tea.MouseReleaseMsg{X: click.X, Y: click.Y, Button: tea.MouseLeft})
	result := host.Handle(click)
	if result.Action == nil || result.Action.ID != ActionOpenChange {
		t.Fatalf("explicit double-click=%+v", result)
	}
}

func TestSidebarFilenameUsesNormalForegroundAndFilterPreservesSelection(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetSnapshot(workspace.GitSnapshot{Changes: []workspace.GitChange{{Path: "first/main.go", Area: workspace.GitAreaWorking, Code: "M"}, {Path: "second/main.go", Area: workspace.GitAreaWorking, Code: "M"}}})
	screen.list.SelectIndex(3)
	screen.selected = screen.list.Items()[3].ID
	screen.SetFilter("first")
	screen.SetFilter("")
	selected, ok := screen.list.Selected()
	if !ok || selected.Payload.(Item).Change.Path != "second/main.go" {
		t.Fatalf("filter lost selected path: %+v", selected)
	}
	host := ui.NewHost(th, screen)
	host.Resize(42, 18)
	host.Layout()
	buf := surface.NewCellBuffer(42, 18)
	host.Paint(buf)
	row, _ := host.ScreenRect(ListID)
	if got := buf.Get(row.X, row.Y+2).FG; got != screen.tokens.Color(ui.ColorFg) {
		t.Fatalf("ordinary modified filename uses attention ink: %v", got)
	}
}

func sourceControlText(buf *surface.CellBuffer) string {
	var out strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			out.WriteRune(r)
		}
		out.WriteByte('\n')
	}
	return out.String()
}
