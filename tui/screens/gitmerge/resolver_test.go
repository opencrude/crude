package gitmerge

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/diffview"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func conflictFile(t *testing.T) workspace.GitConflictFile {
	t.Helper()
	var text strings.Builder
	for i := 1; i <= 10; i++ {
		text.WriteString("context line\n")
	}
	text.WriteString("<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> feature\nmiddle\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> feature\nend\n")
	segments, err := workspace.ParseGitConflicts(text.String())
	if err != nil {
		t.Fatal(err)
	}
	return workspace.GitConflictFile{Path: "a.go", Fingerprint: "f1", Segments: segments}
}

func newTestResolver(t *testing.T) (*Resolver, *ui.Host, *[]Decision) {
	t.Helper()
	var decisions []Decision
	r := New(ui.NewTokenSet(theme.DefaultDark()), func(d Decision) tea.Cmd {
		decisions = append(decisions, d)
		return nil
	})
	r.SetConflicts(workspace.GitOperationMerge, []string{"a.go", "b.go"})
	file := conflictFile(t)
	prepared, err := Prepare(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	r.SetFile(file, prepared)
	host := ui.NewHost(theme.DefaultDark(), r)
	host.Resize(140, 30)
	host.Layout()
	host.Focus(r.DocumentID())
	return r, host, &decisions
}

func key(host *ui.Host, text string) {
	msg := tea.KeyPressMsg{Code: rune(text[0]), Text: text}
	if text == "ctrl+s" {
		msg = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	host.Handle(msg)
}

// Each conflict is one change of the shared comparison: current on the
// left, incoming on the right.
func TestResolverShowsEachConflictAsOneChange(t *testing.T) {
	r, _, _ := newTestResolver(t)
	if got := len(r.viewer.Choices()); got != 2 {
		t.Fatalf("changes = %d, want one per conflict", got)
	}
	if c := r.viewer.Comparison(); c.BeforeLabel != "Current · HEAD" || c.AfterLabel != "Incoming · feature" {
		t.Fatalf("labels %q / %q", c.BeforeLabel, c.AfterLabel)
	}
}

// The shared keys decide the current conflict and move between conflicts;
// Save is offered only once every conflict is decided.
func TestResolverKeyboardDecidesAndSaves(t *testing.T) {
	r, host, decisions := newTestResolver(t)
	key(host, "ctrl+s")
	if len(*decisions) != 0 {
		t.Fatal("saved with undecided conflicts")
	}
	key(host, "1")
	key(host, "n")
	key(host, "3")
	if got := r.Choices(); got[0] != workspace.GitChooseCurrent || got[1] != workspace.GitChooseBoth {
		t.Fatalf("choices = %v", got)
	}
	key(host, "ctrl+s")
	if len(*decisions) != 1 || (*decisions)[0].Kind != DecideSave || (*decisions)[0].Fingerprint != "f1" || len((*decisions)[0].Choices) != 2 {
		t.Fatalf("decisions = %+v", *decisions)
	}
	key(host, "e")
	if last := (*decisions)[len(*decisions)-1]; last.Kind != DecideOpen || last.Line != 17 {
		t.Fatalf("open decision = %+v, want line 17, the second conflict marker", last)
	}
}

// Reloading the same bytes keeps the choices made so far.
func TestResolverKeepsChoicesForTheSameFile(t *testing.T) {
	r, host, _ := newTestResolver(t)
	key(host, "2")
	file := conflictFile(t)
	prepared, _ := Prepare(context.Background(), file)
	r.SetFile(file, prepared)
	if got := r.Choices(); got[0] != workspace.GitChooseIncoming {
		t.Fatalf("choices lost: %v", got)
	}
	file.Fingerprint = "f2"
	r.SetFile(file, prepared)
	if got := r.viewer.Choices(); got[0] != diffview.ChoiceNone {
		t.Fatalf("changed bytes must restart the choices: %v", got)
	}
}

// A file no longer conflicted leaves the dialog; with nothing left, the
// completing action is offered.
func TestResolverFollowsResolvedFiles(t *testing.T) {
	r, _, decisions := newTestResolver(t)
	r.SetConflicts(workspace.GitOperationMerge, []string{"b.go"})
	if _, loaded := r.File(); loaded {
		t.Fatal("a resolved file stayed loaded")
	}
	r.act(actionComplete)
	if len(*decisions) != 0 {
		t.Fatal("completion offered with a conflict left")
	}
	r.SetConflicts(workspace.GitOperationMerge, nil)
	r.act(actionComplete)
	if len(*decisions) != 1 || (*decisions)[0].Kind != DecideComplete {
		t.Fatalf("decisions = %+v", *decisions)
	}
}
