package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func gridTokens() *TokenSet { return NewTokenSet(theme.DefaultDark()) }

func gridText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(c.Char)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func gridPress(g *KeyValueGrid, code rune, text string) EventResult {
	return g.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: code, Text: text}))
}

func TestKeyValueGrid_RoundTripsLineProtocol(t *testing.T) {
	rows := ParseKeyValueLines("X-Tenant: acme\nX-Trace: enabled")
	if len(rows) != 2 || rows[0].Key != "X-Tenant" || rows[0].Value != "acme" || rows[1].Value != "enabled" {
		t.Fatalf("parsed rows=%+v", rows)
	}
	if got := FormatKeyValueLines(rows); got != "X-Tenant: acme\nX-Trace: enabled" {
		t.Fatalf("formatted=%q", got)
	}
	// A line without a colon keeps its text so the user can repair it.
	malformed := ParseKeyValueLines("X-Broken")
	if len(malformed) != 1 || malformed[0].Key != "X-Broken" || malformed[0].Value != "" {
		t.Fatalf("malformed rows=%+v", malformed)
	}
	if got := FormatKeyValueLines(nil); got != "" {
		t.Fatalf("empty rows formatted=%q", got)
	}
	if got := FormatKeyValueLines([]KeyValuePair{{Key: "  ", Value: "dropped"}}); got != "" {
		t.Fatalf("blank row must not serialize: %q", got)
	}
}

func TestKeyValueGrid_AddRowAndTypeAdvancesCells(t *testing.T) {
	g := NewKeyValueGrid("grid", gridTokens())
	g.SetValue("X-Tenant: acme")
	g.Layout(LayoutContext{}, Rect{W: 40, H: 6})

	gridPress(g, tea.KeyDown, "") // row 1
	gridPress(g, tea.KeyDown, "") // add row
	if g.FocusedRow() != 1 {
		t.Fatalf("focused row=%d want add row", g.FocusedRow())
	}
	gridPress(g, tea.KeyEnter, "")
	if g.Count() != 2 || !g.Editing() || g.FocusedRow() != 1 {
		t.Fatalf("add row count=%d editing=%v row=%d", g.Count(), g.Editing(), g.FocusedRow())
	}
	for _, r := range "X-New" {
		gridPress(g, r, string(r))
	}
	if got := g.Value(); got != "X-Tenant: acme\nX-New" {
		t.Fatalf("after name=%q", got)
	}
	gridPress(g, tea.KeyEnter, "") // advance to the value cell
	if !g.Editing() || g.FocusedRow() != 1 {
		t.Fatalf("value cell not open: editing=%v row=%d", g.Editing(), g.FocusedRow())
	}
	for _, r := range "yes" {
		gridPress(g, r, string(r))
	}
	if got := g.Value(); got != "X-Tenant: acme\nX-New: yes" {
		t.Fatalf("after value=%q", got)
	}
	gridPress(g, tea.KeyEnter, "") // advance past the last row to the add row
	if g.Editing() || g.FocusedRow() != 2 {
		t.Fatalf("Enter on last cell must park on the add row: editing=%v row=%d", g.Editing(), g.FocusedRow())
	}
}

func TestKeyValueGrid_RemovesRowByKeyAndByGutterClick(t *testing.T) {
	g := NewKeyValueGrid("grid", gridTokens())
	g.SetValue("X-Tenant: acme\nX-Trace: enabled")
	g.Layout(LayoutContext{}, Rect{W: 40, H: 6})

	gridPress(g, tea.KeyBackspace, "")
	if got := g.Value(); got != "X-Trace: enabled" {
		t.Fatalf("Backspace removed the wrong row: %q", got)
	}
	gridPress(g, tea.KeyBackspace, "")
	if got := g.Value(); got != "" {
		t.Fatalf("Backspace must clear the table: %q", got)
	}
	// The add row itself is never removable.
	g.SetValue("X-Trace: enabled")
	g.Layout(LayoutContext{}, Rect{W: 40, H: 6})
	gridPress(g, tea.KeyDown, "")
	if res := gridPress(g, tea.KeyBackspace, ""); res.Handled {
		t.Fatal("Backspace on the add row must not remove a row")
	}
	if got := g.Value(); got != "X-Trace: enabled" {
		t.Fatalf("add-row Backspace changed the set: %q", got)
	}

	g.SetValue("X-Tenant: acme\nX-Trace: enabled")
	g.Layout(LayoutContext{}, Rect{W: 40, H: 6})
	res := g.Handle(EventContext{Focused: true, LocalX: g.Bounds().W - 2, LocalY: 1}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if !res.Handled {
		t.Fatal("remove gutter click was not handled")
	}
	if got := g.Value(); got != "X-Trace: enabled" {
		t.Fatalf("gutter click removed the wrong row: %q", got)
	}
}

func TestKeyValueGrid_ClickAddsRowAndClickRemovesNothingWhenReadOnly(t *testing.T) {
	g := NewKeyValueGrid("grid", gridTokens())
	g.Layout(LayoutContext{}, Rect{W: 40, H: 3})
	res := g.Handle(EventContext{Focused: true, LocalX: 4, LocalY: 2}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if !res.Handled || g.Count() != 1 || !g.Editing() {
		t.Fatalf("click on the add row must append a pair: handled=%v count=%d editing=%v", res.Handled, g.Count(), g.Editing())
	}

	inherited := NewKeyValueGrid("grid", gridTokens())
	inherited.SetValue("X-Tenant: acme")
	inherited.SetReadOnly(true)
	inherited.Layout(LayoutContext{}, Rect{W: 40, H: 4})
	res = inherited.Handle(EventContext{Focused: true, LocalX: 4, LocalY: 1}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if !res.Handled || inherited.Count() != 1 {
		t.Fatalf("inherited table must not edit: count=%d", inherited.Count())
	}
	if res := gridPress(inherited, 'x', "x"); res.Handled {
		t.Fatal("inherited table must swallow edits")
	}
}

func TestKeyValueGrid_PaintsTableLanguage(t *testing.T) {
	g := NewKeyValueGrid("grid", gridTokens())
	g.SetTitle("Extra headers", "Sent with every request.")
	g.SetValue("X-Tenant: acme")
	g.Layout(LayoutContext{}, Rect{W: 40, H: 4})
	buf := surface.NewCellBuffer(40, 4)
	g.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	out := gridText(buf)
	if !strings.Contains(out, "Name") || !strings.Contains(out, "Value") {
		t.Fatalf("missing column headers:\n%s", out)
	}
	if !strings.Contains(out, "X-Tenant") || !strings.Contains(out, "acme") {
		t.Fatalf("missing row cells:\n%s", out)
	}
	if !strings.Contains(out, "Add header") {
		t.Fatalf("missing add row:\n%s", out)
	}
	// The focused row takes the shared list focus tint, siblings keep the
	// panel fill, and the header keeps its own raised band.
	_, focusBG := gridTokens().ListRowColors(StateFocused)
	rowBG := buf.Get(0, 1).BG
	if rowBG != focusBG {
		t.Fatalf("focused row bg=%v want list focus %v", rowBG, focusBG)
	}
	if got := buf.Get(0, 2).BG; got == rowBG {
		t.Fatal("add row must not share the focused row tint")
	}

	// An inherited table drops the add row and its gutter glyph entirely.
	g.SetReadOnly(true)
	g.Layout(LayoutContext{}, Rect{W: 40, H: 3})
	buf = surface.NewCellBuffer(40, 3)
	g.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	out = gridText(buf)
	if strings.Contains(out, "Add header") || strings.Contains(out, "✕") {
		t.Fatalf("inherited table kept editing affordances:\n%s", out)
	}
	if !strings.Contains(out, "X-Tenant") {
		t.Fatalf("inherited table lost its rows:\n%s", out)
	}
}

func TestKeyValueGrid_HoverIsDistinctFromFocus(t *testing.T) {
	g := NewKeyValueGrid("grid", gridTokens())
	g.SetValue("X-Tenant: acme\nX-Trace: enabled")
	g.Layout(LayoutContext{}, Rect{W: 40, H: 4})
	g.Handle(EventContext{Focused: true, LocalX: 4, LocalY: 2}, MouseMotionEvent(tea.MouseMotionMsg{}))
	if g.FocusedRow() == 2 {
		t.Fatal("hover must not move keyboard focus")
	}
	buf := surface.NewCellBuffer(40, 4)
	g.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	hover := buf.Get(0, 2).BG
	focus := buf.Get(0, 1).BG
	if hover == focus {
		t.Fatal("hover row must differ from the focused row")
	}
}

func TestKeyValueGrid_MalformedRowSurvivesEditing(t *testing.T) {
	g := NewKeyValueGrid("grid", gridTokens())
	g.SetValue("X-Broken")
	g.Layout(LayoutContext{}, Rect{W: 40, H: 4})
	if g.Count() != 1 || g.Rows()[0].Value != "" {
		t.Fatalf("malformed line must stay editable: rows=%+v", g.Rows())
	}
	gridPress(g, tea.KeyRight, "")
	gridPress(g, tea.KeyEnter, "")
	for _, r := range "enabled" {
		gridPress(g, r, string(r))
	}
	if got := g.Value(); got != "X-Broken: enabled" {
		t.Fatalf("repaired row=%q", got)
	}
}

// TestKeyValueGrid_UnfocusedPaintKeepsNoCaret guards the modal-prompt case:
// the form can retain internal focus while the window hands focus to a prompt,
// and the table must not keep blinking a caret in a blurred field.
func TestKeyValueGrid_UnfocusedPaintKeepsNoCaret(t *testing.T) {
	g := NewKeyValueGrid("grid", gridTokens())
	g.SetValue("X-Tenant: acme")
	g.Layout(LayoutContext{}, Rect{W: 40, H: 4})
	gridPress(g, tea.KeyEnter, "")
	if !g.Editing() {
		t.Fatal("cell editor did not open")
	}
	focused := surface.NewCellBuffer(40, 4)
	g.Paint(focused, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	blurred := surface.NewCellBuffer(40, 4)
	g.Paint(blurred, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: false})
	if focused.Get(1+0, 1).Char == blurred.Get(1, 1).Char {
		t.Fatal("focused cell should paint the editor, blurred cell should not")
	}
	if !strings.Contains(gridText(blurred), "X-Tenant") {
		t.Fatalf("blurred table still shows the row value:\n%s", gridText(blurred))
	}
}
