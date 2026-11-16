package diffview

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/textdiff"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func resolvingScreen(t *testing.T, c Comparison, initial Choice) (*Screen, *ui.Host) {
	t.Helper()
	s := New(theme.DefaultDark())
	s.SetResolvable(true, initial)
	prepared, err := PrepareContext(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	s.SetPrepared(prepared)
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(100, 12)
	host.Layout()
	return s, host
}

func paintText(host *ui.Host, w, h int) string {
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var out strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if c := buf.Get(x, y); c.Char != 0 && c.Width != 0 {
				out.WriteRune(c.Char)
			}
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// Each change keeps the left side, the right side or both; equal text stays.
func TestResolvedTextFollowsEachChoice(t *testing.T) {
	c := Comparison{Path: "a.txt", Before: "one\ntwo\nthree\nfour\n", After: "one\n2\nthree\n4\n"}
	s, _ := resolvingScreen(t, c, ChoiceRight)
	if got, ok := s.ResolvedText(); !ok || got != c.After {
		t.Fatalf("initial right choices must keep the right text: %q %v", got, ok)
	}
	s.choose(0, ChoiceLeft)
	s.choose(1, ChoiceBoth)
	if got, _ := s.ResolvedText(); got != "one\ntwo\nthree\nfour\n4\n" {
		t.Fatalf("mixed choices: %q", got)
	}
	s.choose(0, ChoiceLeft)
	s.choose(1, ChoiceLeft)
	if got, _ := s.ResolvedText(); got != c.Before {
		t.Fatalf("all left: %q", got)
	}
}

// Open changes block the result; the keys and the gutter boxes decide them.
func TestResolutionKeysAndGutterBoxesDecideChanges(t *testing.T) {
	c := Comparison{Path: "a.txt", BeforeLabel: "On disk", AfterLabel: "Your changes", Before: "a\nb\nc\n", After: "a\nB\nc\n"}
	s, host := resolvingScreen(t, c, ChoiceNone)
	if _, ok := s.ResolvedText(); ok || s.Decided() {
		t.Fatal("an open change must block the result")
	}
	painted := paintText(host, 100, 12)
	for _, want := range []string{"Accept left", "Accept right", "Accept both", "[ ]"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("missing %q in:\n%s", want, painted)
		}
	}
	s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: '1', Text: "1"}))
	if got, ok := s.ResolvedText(); !ok || got != c.Before {
		t.Fatalf("key 1 must accept the left side: %q", got)
	}
	// Clicking the right pane's box adds the right side: both are kept.
	row := s.prepared.hunks[0] - s.scroll.Offset() + 1
	s.after.Handle(ui.EventContext{LocalX: 1, LocalY: row}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if s.Choices()[0] != ChoiceBoth {
		t.Fatalf("the right box must add the right side, got %v", s.Choices()[0])
	}
	if painted := paintText(host, 100, 12); strings.Count(painted, "[x]") != 2 {
		t.Fatalf("both boxes must be checked:\n%s", painted)
	}
	if status := s.ResolutionStatus(); status != "Change 1 of 1 · all chosen" {
		t.Fatalf("status %q", status)
	}
}

// Git conflicts supply their regions; each region is one change.
func TestPrepareChangesUsesExactRegions(t *testing.T) {
	c := Comparison{Before: "x\nours\ny\n", After: "x\ntheirs\ny\n"}
	changes := []textdiff.Hunk{{OldStart: 1, NewStart: 1, OldLines: []string{"ours\n"}, NewLines: []string{"theirs\n"}}}
	p, err := PrepareChangesContext(context.Background(), c, changes)
	if err != nil || p.HunkCount() != 1 {
		t.Fatalf("exact regions: %v %d", err, p.HunkCount())
	}
	if _, err := PrepareChangesContext(context.Background(), c, []textdiff.Hunk{{OldStart: 2, NewStart: 1}}); err == nil {
		t.Fatal("misaligned regions must be refused")
	}
}
