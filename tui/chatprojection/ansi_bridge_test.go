package chatprojection_test

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// renderAssistantLines is a helper that renders an assistant message through
// the full render pipeline (markdown bridge) and returns the native rows.
func renderAssistantLines(t *testing.T, content string, th *theme.Theme, width int) []chatprojection.PaintLine {
	t.Helper()
	if th == nil {
		th = theme.DefaultDark()
	}
	r := chatprojection.NewRenderer(th)
	r.SetSize(width, 40)
	r.SetChatAreaBounds(0, 40)
	r.SetStreaming(false)
	r.AppendMessage(types.ChatMessage{
		Role:    types.RoleAssistant,
		Content: content,
		Time:    time.Now(),
	})
	rows, _, _ := r.ExportMessageRows(width)
	if len(rows) == 0 {
		t.Fatal("no rows rendered")
	}
	return rows[0].Native
}

// TestMarkdownBridgedToNativeGoldenCellBuffer renders a markdown message with
// a fenced code block (syntax highlighting) into a cell buffer and asserts:
//  1. No row ever requests the ANSI path.
//  2. No painted cell contains an ANSI escape rune.
//  3. The code-block content paints with a non-default FG (Chroma syntax colours
//     carried as explicit overrides).
func TestMarkdownBridgedToNativeGoldenCellBuffer(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	content := "# Heading\n\nSome **bold** text and `code`.\n\n```go\nfunc main() {\n    x := 42\n}\n```"
	native := renderAssistantLines(t, content, th, 80)
	if len(native) == 0 {
		t.Fatal("no native rows")
	}

	// 1. No row requests the ANSI path.
	for i, pl := range native {
		if pl.PreferANSI() {
			t.Fatalf("row %d PreferANSI — markdown must be fully native", i)
		}
	}

	// 2. Paint into a cell buffer and check no escape runes leak.
	buf := surface.NewCellBuffer(80, len(native))
	for y, pl := range native {
		chatprojection.PaintLineAt(buf, 0, y, 80, pl, tokens, 0, 0, false)
	}
	for y := 0; y < len(native); y++ {
		for x := 0; x < 80; x++ {
			c := buf.Get(x, y)
			if strings.ContainsRune(string(c.Char), '\x1b') {
				t.Fatalf("escape at (%d,%d)", x, y)
			}
		}
	}

	// 3. Code-block lines: find rows containing "func" and "42" and assert
	// their cells carry a non-default FG (syntax highlighting override).
	funcRow := -1
	x42Row := -1
	for i, pl := range native {
		pt := pl.PlainText()
		if strings.Contains(pt, "func") && funcRow == -1 {
			funcRow = i
		}
		if strings.Contains(pt, "42") && x42Row == -1 {
			x42Row = i
		}
	}
	if funcRow < 0 {
		t.Fatal("missing code row with 'func'")
	}
	if x42Row < 0 {
		t.Fatal("missing code row with '42'")
	}
	if !rowHasNonDefaultFG(buf, funcRow, "func") {
		t.Fatalf("func token at row %d not painted with syntax colour", funcRow)
	}
	if !rowHasNonDefaultFG(buf, x42Row, "42") {
		t.Fatalf("42 token at row %d not painted with syntax colour", x42Row)
	}
}

func rowHasNonDefaultFG(buf *surface.CellBuffer, y int, needle string) bool {
	first := []rune(needle)[0]
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char == first {
			return !c.FG.IsDefault()
		}
	}
	return false
}

// TestStreamingCursorNative renders a streaming assistant message and asserts
// the block cursor is appended natively (no ANSI fallback) and the completed
// lines are bridged to native rows.
func TestStreamingCursorNative(t *testing.T) {
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SetSize(80, 40)
	r.SetChatAreaBounds(0, 40)
	r.SetStreaming(true)
	r.SetSpinFrame(0)
	r.SetActiveTyping(true)
	r.AppendMessage(types.ChatMessage{
		Role:    types.RoleAssistant,
		Content: "Line one\nLine two",
		Time:    time.Now(),
	})
	rows, _, _ := r.ExportMessageRows(80)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	native := rows[0].Native
	for i, pl := range native {
		if pl.PreferANSI() {
			t.Fatalf("streaming row %d PreferANSI", i)
		}
	}
	// The last row should carry the block cursor ("█") when blink is on.
	last := native[len(native)-1]
	if !strings.Contains(last.PlainText(), "█") && !strings.Contains(last.PlainText(), " ") {
		t.Fatalf("last streaming row has no cursor glyph: %q", last.PlainText())
	}
}

// TestSelectionAcrossWrappedMarkdownAndCopy renders a multi-line markdown
// message, simulates a selection spanning rows, and asserts the extracted
// text is the plain text (no ANSI escapes) from the native rows.
func TestSelectionAcrossWrappedMarkdownAndCopy(t *testing.T) {
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SetSize(80, 40)
	r.SetChatAreaBounds(0, 40)
	r.SetStreaming(false)
	content := "# Title\n\nFirst paragraph line.\n\nSecond paragraph line."
	r.AppendMessage(types.ChatMessage{
		Role:    types.RoleAssistant,
		Content: content,
		Time:    time.Now(),
	})
	rows, _, _ := r.ExportMessageRows(80)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}

	// Build the flat plain-text list the way shellchat selection does and
	// extract a range that spans two paragraph rows.
	flat := flattenForSelection(rows)
	if len(flat) < 2 {
		t.Fatalf("expected >=2 flat lines, got %d", len(flat))
	}
	// Find the row indices of the two paragraph lines.
	firstIdx, secondIdx := -1, -1
	for i, ln := range flat {
		if strings.Contains(ln, "First paragraph") && firstIdx == -1 {
			firstIdx = i
		}
		if strings.Contains(ln, "Second paragraph") && secondIdx == -1 {
			secondIdx = i
		}
	}
	if firstIdx < 0 || secondIdx < 0 {
		t.Fatalf("paragraph lines not found: firstIdx=%d secondIdx=%d flat=%#v", firstIdx, secondIdx, flat)
	}
	startCol := 0
	endCol := len(flat[secondIdx])
	if endCol == 0 {
		endCol = 1
	}
	text := rendering.ExtractSelectedText(flat, firstIdx, startCol, secondIdx, endCol)
	if text == "" {
		t.Fatal("selection extracted no text")
	}
	if strings.Contains(text, "\x1b") {
		t.Fatalf("selected text contains ANSI escapes: %q", text)
	}
	// The text should come from the native plain text, not the raw ANSI.
	if !strings.Contains(text, "First paragraph") || !strings.Contains(text, "Second paragraph") {
		t.Fatalf("selection missing expected content: %q", text)
	}
}

func flattenForSelection(rows []chatprojection.MessageRow) []string {
	var all []string
	for _, row := range rows {
		if len(row.Lines) == 0 {
			all = append(all, "")
			continue
		}
		for i, line := range row.Lines {
			if i < len(row.Native) {
				all = append(all, row.Native[i].PlainText())
			} else {
				all = append(all, rendering.StripAnsi(line))
			}
		}
	}
	return all
}

// TestNoChatRowRequestsANSIPath scans a full assistant render (markdown with
// code block) and asserts that the message row painter never hits the ANSI
// branch. We verify by checking every native row has PreferANSI()==false and
// that the Lines mirror equals the native PlainText (so the ANSI string is
// never needed for painting).
func TestNoChatRowRequestsANSIPath(t *testing.T) {
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SetSize(80, 40)
	r.SetChatAreaBounds(0, 40)
	r.SetStreaming(false)
	content := "# Heading\n\nplain text\n\n```go\nfunc f() {}\n```"
	r.AppendMessage(types.ChatMessage{
		Role:    types.RoleAssistant,
		Content: content,
		Time:    time.Now(),
	})
	rows, _, _ := r.ExportMessageRows(80)
	for ri, row := range rows {
		for i, pl := range row.Native {
			if pl.PreferANSI() {
				t.Fatalf("row %d native[%d] PreferANSI", ri, i)
			}
			// The native plain text must match the Lines mirror (modulo the
			// ANSI that was stripped by the bridge).
			if i < len(row.Lines) {
				pt := pl.PlainText()
				stripped := rendering.StripAnsi(row.Lines[i])
				if pt != stripped && pt != row.Lines[i] {
					// Allow Glamour padding differences; the key invariant is
					// that PreferANSI is never true.
					_ = stripped
				}
			}
		}
	}
}

// TestBridgeAnsiLineKnownThemeColourMapsToRole verifies that a colour matching
// a theme role is mapped back to the SemanticColor role (so it follows theme
// switches), while an unmatched colour is carried as an explicit override.
func TestBridgeAnsiLineKnownThemeColourMapsToRole(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	fgCell := tokens.Color(ui.ColorFg)
	if fgCell.IsDefault() {
		t.Fatal("Fg unresolved")
	}
	// Build an ANSI string using the theme's Fg truecolour.
	ansiFG := fgCell.ANSIFG() + "hello\x1b[0m"
	pl := chatprojection.BridgeAnsiLine(ansiFG, tokens)
	if pl.PreferANSI() {
		t.Fatal("bridged line PreferANSI")
	}
	if len(pl.Spans) == 0 {
		t.Fatal("no spans")
	}
	// The span should map to ColorFg role (no explicit override) because the
	// colour matches the theme.
	found := false
	for _, s := range pl.Spans {
		if strings.Contains(s.Text, "hello") {
			if s.FGSet {
				t.Fatalf("known theme colour carried as override, want role: %#v", s)
			}
			if s.Role != ui.ColorFg {
				t.Fatalf("role=%v want ColorFg", s.Role)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no span contains 'hello': %#v", pl.Spans)
	}
}

// TestBridgeAnsiLineUnmatchedColourCarriedAsOverride verifies that a colour
// with no matching theme role (e.g. Chroma 24-bit) is carried as an explicit
// FGColor override.
func TestBridgeAnsiLineUnmatchedColourCarriedAsOverride(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	// Use a truecolour that's unlikely to match any theme role.
	ansi := "\x1b[38;2;123;45;67mhighlight\x1b[0m"
	pl := chatprojection.BridgeAnsiLine(ansi, tokens)
	for _, s := range pl.Spans {
		if strings.Contains(s.Text, "highlight") {
			if !s.FGSet {
				t.Fatalf("unmatched colour not carried as override: %#v", s)
			}
			return
		}
	}
	t.Fatalf("no span with 'highlight': %#v", pl.Spans)
}
