package gitmerge

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

func newTestResolver(t *testing.T) (*Resolver, *[]Decision) {
	t.Helper()
	var decisions []Decision
	r := New(ui.NewTokenSet(theme.DefaultDark()), func(d Decision) tea.Cmd {
		decisions = append(decisions, d)
		return nil
	})
	r.SetConflicts(workspace.GitOperationMerge, []string{"a.go", "b.go"})
	r.SetFile(conflictFile(t))
	r.Layout(ui.LayoutContext{}, ui.Rect{W: 120, H: 30})
	return r, &decisions
}

func key(r *Resolver, text string) {
	msg := tea.KeyPressMsg{Code: rune(text[0]), Text: text}
	if text == "ctrl+s" {
		msg = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	r.Handle(ui.EventContext{}, ui.KeyEvent(msg))
}

// Long unchanged stretches collapse; each conflict shows both sides with the
// file's own line numbers.
func TestResolverRowsCollapseContextAndNumberLines(t *testing.T) {
	r, _ := newTestResolver(t)
	var collapsed, headers int
	for _, row := range r.rows {
		switch row.kind {
		case rowCollapsed:
			collapsed++
			if !strings.Contains(row.text, "7 unchanged lines") {
				t.Fatalf("collapsed row = %q", row.text)
			}
		case rowHeader:
			headers++
		}
		if row.kind == rowLine && row.side == sideIncoming && row.text == "theirs\n" && row.line != 14 {
			t.Fatalf("incoming line number = %d, want 14", row.line)
		}
	}
	if collapsed != 1 || headers != 2 {
		t.Fatalf("collapsed=%d headers=%d", collapsed, headers)
	}
}

// Keys decide the current conflict and move between conflicts; Save is
// offered only once every conflict is decided.
func TestResolverKeyboardDecidesAndSaves(t *testing.T) {
	r, decisions := newTestResolver(t)
	key(r, "ctrl+s")
	if len(*decisions) != 0 {
		t.Fatal("saved with undecided conflicts")
	}
	key(r, "1")
	key(r, "n")
	key(r, "3")
	if got := r.Choices(); got[0] != workspace.GitChooseCurrent || got[1] != workspace.GitChooseBoth {
		t.Fatalf("choices = %v", got)
	}
	key(r, "ctrl+s")
	if len(*decisions) != 1 || (*decisions)[0].Kind != DecideSave || (*decisions)[0].Fingerprint != "f1" || len((*decisions)[0].Choices) != 2 {
		t.Fatalf("decisions = %+v", *decisions)
	}
	key(r, "e")
	if last := (*decisions)[len(*decisions)-1]; last.Kind != DecideOpen || last.Line != 17 {
		t.Fatalf("open decision = %+v, want line 17, the second conflict marker", last)
	}
}

// Clicking a side's lines chooses that side.
func TestResolverClickChoosesSide(t *testing.T) {
	r, _ := newTestResolver(t)
	docBounds := r.doc.Bounds()
	for row := 0; row < docBounds.H; row++ {
		item, ok := r.doc.ItemAtViewportRow(row)
		if !ok {
			continue
		}
		if line := r.rows[item.Payload.(int)]; line.kind == rowLine && line.side == sideIncoming && line.conflict == 0 {
			r.Handle(ui.EventContext{LocalX: docBounds.X + 8, LocalY: docBounds.Y + row}, ui.Event{Kind: ui.EventMouseClick, MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft}})
			break
		}
	}
	if got := r.Choices(); got[0] != workspace.GitChooseIncoming {
		t.Fatalf("click chose %v", got)
	}
}

// A file no longer conflicted leaves the dialog; with nothing left, the
// completing action is offered.
func TestResolverFollowsResolvedFiles(t *testing.T) {
	r, decisions := newTestResolver(t)
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
