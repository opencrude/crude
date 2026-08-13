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
		},
	})
	host := ui.NewHost(th, screen)
	host.Resize(42, 18)
	host.Layout()
	buf := surface.NewCellBuffer(42, 18)
	host.Paint(buf)
	text := sourceControlText(buf)
	for _, want := range []string{
		"SOURCE CONTROL", "feature/polish", "STAGED CHANGES", "CHANGES",
		"UNTRACKED", "RECENT COMMITS", "LOCAL BRANCHES", "polish source control",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
}

func TestCommitFieldAndChangeActivationEmitTypedActions(t *testing.T) {
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

	if !host.Focus(CommitID) {
		t.Fatal("focus commit")
	}
	for _, r := range "fix bug" {
		host.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if result.Action == nil || result.Action.ID != ActionCommit || result.Action.Payload != "fix bug" {
		t.Fatalf("commit result=%#v", result)
	}

	if !host.Focus(ListID) {
		t.Fatal("focus list")
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.ID != ActionOpenChange {
		t.Fatalf("change activation=%#v", result)
	}
	got, ok := result.Action.Payload.(workspace.GitChange)
	if !ok || got.Path != change.Path {
		t.Fatalf("change payload=%#v", result.Action.Payload)
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
