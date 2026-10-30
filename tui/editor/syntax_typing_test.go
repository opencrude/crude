package editor

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Block the real source worker admission rather than replacing a particular
// lexer implementation. Both native and lexical preparation must preserve the
// currently painted text while queued behind a saturated parser budget.
func pauseSyntaxWorkers(tb testing.TB) func() {
	tb.Helper()
	for range cap(syntaxWorkers) {
		select {
		case syntaxWorkers <- struct{}{}:
		case <-time.After(10 * time.Second):
			tb.Fatal("existing syntax workers did not settle")
		}
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			for range cap(syntaxWorkers) {
				<-syntaxWorkers
			}
		})
	}
	tb.Cleanup(release)
	return release
}

func BenchmarkSyntaxTypingPendingPaint(b *testing.B) {
	for _, lines := range []int{200, 20_000} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			ed := NewPreview(theme.DefaultDark(), "typing.go", "package main\nvar value = 1\n"+strings.Repeat("// unchanged documentation\n", lines), 1)
			b.Cleanup(func() { closeSyntax(ed) })
			settleSyntax(b, ed)
			// The fixture is an editable buffer, not a read-only preview action.
			ed.readOnly = false
			pauseSyntaxWorkers(b)
			ed.SetCursorPosition(1, len("var value"))
			surf := surface.NewSurface("typing", 120, 40, cell.TransparentBlank())
			ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
			b.ReportAllocs()
			b.ResetTimer()
			insert := true
			for b.Loop() {
				key := tea.KeyPressMsg{Code: tea.KeyBackspace}
				if insert {
					key = tea.KeyPressMsg{Code: 'x', Text: "x"}
				}
				ed.HandleEvent(key)
				ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
				insert = !insert
			}
		})
	}
}

func TestSyntaxTypingDoesNotBlankUnchangedVisibleRows(t *testing.T) {
	ed := loadEditorText(t, "typing.go", "package main\n// unchanged documentation\nfunc answer() int { return 42 }\nvar value = 1\n"+strings.Repeat("// more unchanged documentation\n", 40))
	ed.SetCursorPosition(3, len("var value"))
	before := paintEditor(ed, 100, 24)
	// Register release after the editor cleanup so a failed assertion cannot
	// strand its owned syntax worker at teardown.
	t.Cleanup(func() { closeSyntax(ed) })
	pauseSyntaxWorkers(t)
	ed.HandleEvent(tea.KeyPressMsg{Code: 'x', Text: "x"})
	command := ed.SyntaxCommand()
	if command == nil {
		t.Fatal("edit did not schedule syntax")
	}
	select {
	case <-ed.SyntaxDone():
		t.Fatal("syntax completed through saturated worker admission")
	default:
	}
	// Render while the exact replacement parse is blocked, rather than hiding
	// the transition by waiting for it before painting.
	after := paintEditor(ed, 100, 24)
	changed := 0
	for y := 0; y < 24; y++ {
		if y == 3 {
			continue
		}
		for x := ed.gutterWidth(); x < 98; x++ {
			a, b := before.Get(x, y), after.Get(x, y)
			if a.Char == b.Char && !a.FG.Equal(b.FG) {
				changed++
			}
		}
	}
	t.Logf("unchanged source cells recoloured during one character: %d", changed)
	if changed != 0 {
		t.Fatalf("typing blanked %d unchanged syntax cells before publication", changed)
	}
	// The edited line maps its surviving keyword and literal too, instead of
	// replacing the entire line with plain ink until the parse finishes.
	for _, x := range []int{ed.gutterWidth(), ed.gutterWidth() + 1, ed.gutterWidth() + 2} {
		if a, b := before.Get(x, 3), after.Get(x, 3); !a.FG.Equal(b.FG) {
			t.Fatal("typing blanked the edited line's unchanged keyword")
		}
	}
}

func TestSyntaxProvisionalMappingRespectsSourceGeometry(t *testing.T) {
	// Explicit source byte positions and expected cell maps, independent of
	// the mapping implementation: tab to col 4, 界 to 6, e+mark to 7, Z to 8.
	before := syntaxLine{text: "\t界e\u0301Z", colours: []string{"tab", "tab", "tab", "tab", "wide", "wide", "accent", "tail"}}
	for _, tc := range []struct {
		name           string
		start, end     int
		text, wantText string
		want           []string
	}{
		{"insert-before-tab", 0, 0, "x", "x\t界e\u0301Z", []string{"tab", "tab", "tab", "tab", "wide", "wide", "accent", "tail"}},
		{"delete-wide", 1, 4, "", "\te\u0301Z", []string{"tab", "tab", "tab", "tab", "accent", "tail"}},
		{"replace-with-zwj", 4, 7, "👩‍💻", "\t界👩‍💻Z", []string{"tab", "tab", "tab", "tab", "wide", "wide", "accent", "accent", "tail"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mapSyntaxLine(before, bufferLineChange{byteStart: tc.start, byteEnd: tc.end, text: tc.text})
			if got.text != tc.wantText || strings.Join(got.colours, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("mapped %q %v, want %q %v", got.text, got.colours, tc.wantText, tc.want)
			}
		})
	}
}

func TestSyntaxPendingLineInsertionDeletionAndReplacement(t *testing.T) {
	ed := loadEditorText(t, "lines.go", "package main\r\n// keep 界 e\u0301\r\nvar tail = 42\r\n")
	paintEditor(ed, 80, 10)
	t.Cleanup(func() { closeSyntax(ed) })
	pauseSyntaxWorkers(t)
	ed.SetCursorPosition(0, 0)
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	ed.SyntaxCommand()
	select {
	case <-ed.SyntaxDone():
		t.Fatal("syntax completed through saturated worker admission")
	default:
	}
	buf := paintEditor(ed, 80, 10)
	if got := buf.Get(ed.gutterWidth(), 2); got.Char != '/' || !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("newline lost shifted comment: %+v", got)
	}
	ed.SetCursorPosition(1, 0)
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyBackspace})
	buf = paintEditor(ed, 80, 10)
	if got := buf.Get(ed.gutterWidth(), 1); got.Char != '/' || !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("join lost shifted comment: %+v", got)
	}
	ed.ApplyDisk([]byte("# replacement has no matching old source\n"))
	if ed.lineColorMap(1) != nil {
		t.Fatal("replacement kept unrelated line decorations")
	}
	if len(ed.chromaLineScratch) != 0 {
		t.Fatal("scratch retained discarded line decorations")
	}
}

func TestSyntaxPendingThemeAndWidthDoNotReuseOldCellMaps(t *testing.T) {
	ed := loadEditorText(t, "theme.go", "package main\n// comment\nvar value = 1\n")
	paintEditor(ed, 80, 10)
	ed.SetCursorPosition(2, len("var value"))
	ed.InsertRune('x')
	if len(ed.lineColorMap(1)) == 0 {
		t.Fatal("fixture lost mapped comment")
	}
	ed.InvalidateWidthProfile()
	if len(ed.chromaLineCache) != 0 {
		t.Fatal("old terminal cell geometry survived recalibration")
	}
	settleSyntax(t, ed)
	paintEditor(ed, 80, 10)
	ed.InsertRune('y')
	ed.SetTheme(theme.DefaultLight())
	if len(ed.chromaLineCache) != 0 {
		t.Fatal("old theme decorations survived replacement")
	}
	settleSyntax(t, ed)
	if got := paintEditor(ed, 80, 10).Get(ed.gutterWidth(), 1); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("new theme comment colour not published: %+v", got)
	}
}

func TestSyntaxMappingRejectsMissingMutationHistory(t *testing.T) {
	ed := loadEditorText(t, "gap.go", "package main\n// comment\nvar value = 1\n")
	paintEditor(ed, 80, 10)
	for range 33 {
		if _, err := ed.buf.Insert(Position{Line: 2}, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	ed.invalidateEditorCaches()
	if len(ed.chromaLineCache) != 0 {
		t.Fatal("incomplete mutation history mapped unproven source offsets")
	}
}

func BenchmarkSyntaxMapInlineDecorations(b *testing.B) {
	line := syntaxLine{text: "\tvar value = \"界 é\" // unchanged", colours: make([]string, 44)}
	for i := range line.colours {
		line.colours[i] = "token"
	}
	edit := bufferLineChange{byteStart: 10, byteEnd: 10, text: "x"}
	b.ReportAllocs()
	for b.Loop() {
		_ = mapSyntaxLine(line, edit)
	}
}
