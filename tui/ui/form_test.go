package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestFormRoutesPasteAndMouseToRetainedFields(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	textValue := StringValue("before")
	cycleValue := StringValue("one")
	model := NewFormModel()
	model.AddField(&FieldState{ID: "text", Label: "Text", Type: FieldText, Value: textValue, Initial: textValue})
	model.AddField(&FieldState{ID: "cycle", Label: "Cycle", Type: FieldCycle, Value: cycleValue, Initial: cycleValue, Options: []FormOption{{ID: "one", Label: "one"}, {ID: "two", Label: "two"}}})
	form := NewForm("form", model, "Form", "Shared field interactions", tokens)
	host := NewHost(theme.DefaultDark(), form)
	host.RegisterTree(form)
	host.Resize(60, 10)
	host.Layout()
	host.Focus(form.ID())

	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	host.Handle(tea.PasteMsg{Content: "after"})
	if got := model.Field("text").Value.String(); got != "after" {
		t.Fatalf("pasted text=%q, want after", got)
	}

	// The second field starts after the title, detail, divider, and first row.
	host.Handle(tea.MouseClickMsg{X: 20, Y: 4, Button: tea.MouseLeft})
	if got := model.Field("cycle").Value.String(); got != "two" {
		t.Fatalf("mouse cycle value=%q, want two", got)
	}
}

func TestActionBarPaintsSemanticVariants(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	bar := NewActionBar("actions", tokens,
		ActionBarButton{Label: "Save", Action: "save", Variant: VariantPrimary, Enabled: true},
		ActionBarButton{Label: "Delete", Action: "delete", Variant: VariantDanger, Enabled: true},
	)
	bar.SetAlignEnd(false)
	bar.SetSurfaceBackground(ColorPopupBg)
	bar.Layout(LayoutContext{Theme: theme.DefaultDark()}, Rect{W: 40, H: 1})
	buf := surface.NewCellBuffer(40, 1)
	bar.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark()})
	surfaceBG := tokens.Color(ColorPopupBg)
	if got := buf.Get(1, 0).BG; !got.Equal(surfaceBG) {
		t.Fatalf("idle primary background=%v, want surface %v", got, surfaceBG)
	}
	if got, want := buf.Get(1, 0).FG, tokens.Color(ColorAccent); !got.Equal(want) {
		t.Fatalf("idle primary fg=%v, want accent %v", got, want)
	}
	deleteX := bar.buttons[1].button.Bounds().X + 1
	if got, want := buf.Get(deleteX, 0).FG, tokens.Color(ColorError); !got.Equal(want) {
		t.Fatalf("idle danger fg=%v, want error %v", got, want)
	}
	bar.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	wantFill := tokens.Color(ColorAccent)
	if got := buf.Get(1, 0).BG; !got.Equal(wantFill) {
		t.Fatalf("focused button background=%v, want variant fill %v", got, wantFill)
	}
	if got, want := buf.Get(1, 0).FG, cell.ContrastColor(wantFill); !got.Equal(want) {
		t.Fatalf("focused button fg=%v, want contrast %v", got, want)
	}
}

func TestFormFieldPaintsDirtyMarkerOnModifiedLabel(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	value := StringValue("4")
	model.AddField(&FieldState{ID: "tab-size", Label: "Tab size", Type: FieldText, Value: value, Initial: value})
	form := NewForm("form", model, "Form", "Dirty tracking", tokens)
	host := NewHost(theme.DefaultDark(), form)
	host.Resize(60, 8)
	host.Layout()

	paint := func() string {
		buf := surface.NewCellBuffer(60, 8)
		host.Paint(buf)
		var b strings.Builder
		for y := 0; y < 8; y++ {
			for x := 0; x < 60; x++ {
				if c := buf.Get(x, y); c.Char != 0 {
					b.WriteRune(c.Char)
				}
			}
			b.WriteByte('\n')
		}
		return b.String()
	}

	if plain := paint(); strings.Contains(plain, "Tab size •") {
		t.Fatalf("unmodified field must not paint dirty marker:\n%s", plain)
	}
	model.SetValue("tab-size", StringValue("8"))
	if !model.Field("tab-size").Dirty {
		t.Fatal("field must be dirty after SetValue diverges from Initial")
	}
	if plain := paint(); !strings.Contains(plain, "Tab size •") {
		t.Fatalf("dirty field must paint • marker on the label:\n%s", plain)
	}
	model.Commit()
	if plain := paint(); strings.Contains(plain, "Tab size •") {
		t.Fatalf("committed field must drop the dirty marker:\n%s", plain)
	}
}

func TestFormMultiSelectTogglesChoicesWithoutLosingCursor(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	value := StringsValue(nil)
	model.AddField(&FieldState{
		ID: "choices", Label: "Choices", Type: FieldMultiSelect, Value: value, Initial: value, Required: true,
		Options: []FormOption{{ID: "one", Label: "One"}, {ID: "two", Label: "Two"}},
	})
	form := NewForm("form", model, "Form", "Multiple answers", tokens)
	host := NewHost(theme.DefaultDark(), form)
	host.Resize(60, 10)
	host.Layout()
	host.Focus(form.ID())

	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	values := model.Field("choices").Value.Strings()
	if len(values) != 2 || values[0] != "one" || values[1] != "two" {
		t.Fatalf("multi-select values=%v", values)
	}
	if selected, ok := form.Field("choices").selectList.Selected(); !ok || selected.ID != "two" {
		t.Fatalf("multi-select cursor=%#v", selected)
	}
}

func TestFormSubsectionLabelsIndentFromSectionTitles(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	on := BoolValue(true)
	model.AddField(&FieldState{ID: "features", Label: "Features", Type: FieldSeparator})
	model.AddField(&FieldState{ID: "diagnostics", Label: "Diagnostics", Type: FieldToggle, Value: on, Initial: on})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 6})
	buf := surface.NewCellBuffer(40, 6)
	form.Paint(buf, 0, 0, PaintContext{})

	titleX, fieldX := -1, -1
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if titleX < 0 && buf.Get(x, y).Char == 'F' && x+7 < buf.Width() && buf.Get(x+1, y).Char == 'e' {
				titleX = x
			}
			if fieldX < 0 && buf.Get(x, y).Char == 'D' && x+10 < buf.Width() && buf.Get(x+1, y).Char == 'i' {
				fieldX = x
			}
		}
	}
	if titleX != 0 {
		t.Fatalf("section title x=%d want 0", titleX)
	}
	if fieldX != tokens.FormIndent() {
		t.Fatalf("field label x=%d want FormIndent %d", fieldX, tokens.FormIndent())
	}
}

func TestFormPaintClipsFieldsToViewport(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	on := BoolValue(true)
	model := NewFormModel()
	for i := 0; i < 8; i++ {
		model.AddField(&FieldState{
			ID:          fmt.Sprintf("f%d", i),
			Label:       fmt.Sprintf("Field %d", i),
			Description: "description that must not leak",
			Type:        FieldToggle,
			Value:       on,
			Initial:     on,
		})
	}
	form := NewForm("form", model, "", "", tokens)
	const formX, formY, formW, formH = 2, 2, 40, 5
	form.Layout(LayoutContext{}, Rect{X: formX, Y: formY, W: formW, H: formH})

	assertClipped := func(t *testing.T, buf *surface.CellBuffer) {
		t.Helper()
		for y := 0; y < buf.Height(); y++ {
			for x := 0; x < buf.Width(); x++ {
				inside := x >= formX && x < formX+formW && y >= formY && y < formY+formH
				if inside {
					continue
				}
				if buf.Get(x, y).Char != '#' {
					t.Fatalf("form leaked %q at (%d,%d) outside viewport", string(buf.Get(x, y).Char), x, y)
				}
			}
		}
		found := false
		for y := formY; y < formY+formH; y++ {
			for x := formX; x < formX+formW; x++ {
				if buf.Get(x, y).Char != '#' && buf.Get(x, y).Char != 0 && buf.Get(x, y).Char != ' ' {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("viewport is empty after paint")
		}
	}

	paint := func() *surface.CellBuffer {
		buf := surface.NewCellBuffer(50, 16)
		sentinel := cell.Cell{Char: '#', Width: 1}
		for y := 0; y < buf.Height(); y++ {
			for x := 0; x < buf.Width(); x++ {
				buf.Set(x, y, sentinel)
			}
		}
		form.Paint(buf, 0, 0, PaintContext{})
		return buf
	}

	t.Run("bottom straddle", func(t *testing.T) {
		assertClipped(t, paint())
	})
	t.Run("top straddle", func(t *testing.T) {
		form.Scroll().ScrollTo(1)
		form.Layout(LayoutContext{}, Rect{X: formX, Y: formY, W: formW, H: formH})
		assertClipped(t, paint())
	})
	t.Run("mid scroll", func(t *testing.T) {
		form.Scroll().ScrollTo(3)
		form.Layout(LayoutContext{}, Rect{X: formX, Y: formY, W: formW, H: formH})
		assertClipped(t, paint())
	})
}

func TestFormArrowsClampAtEdgesByDefault(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	empty := StringValue("")
	model.AddField(&FieldState{ID: "a", Label: "A", Type: FieldText, Value: empty, Initial: empty})
	model.AddField(&FieldState{ID: "b", Label: "B", Type: FieldText, Value: empty, Initial: empty})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 6})
	form.FocusFirst()

	res := form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if res.Action != nil {
		t.Fatalf("Up at first field emitted %v", res.Action)
	}

	form.FocusLast()
	res = form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if res.Action != nil {
		t.Fatalf("Down at last field emitted %v", res.Action)
	}

	res = form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyTab}))
	if res.Action == nil || res.Action.ID != "form.exit.forward" {
		t.Fatalf("Tab at last field result=%#v", res)
	}

	form.FocusFirst()
	form.SetArrowExitsForm(true)
	res = form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if res.Action == nil || res.Action.ID != "form.exit.backward" {
		t.Fatalf("SetArrowExitsForm Up result=%#v", res)
	}
	form.FocusLast()
	res = form.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if res.Action == nil || res.Action.ID != "form.exit.forward" {
		t.Fatalf("SetArrowExitsForm Down result=%#v", res)
	}
}

func TestDockedFormIsSingleLineRows(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(&FieldState{
		ID: "multi", Label: "Multi-agent", Type: FieldCycle, Value: StringValue("off"),
		Description: "Allow this session to spawn researcher, crafter, and aeron sub-agents.",
		Options:     []FormOption{{ID: "on", Label: "on"}, {ID: "off", Label: "off"}},
	})
	model.AddField(&FieldState{
		ID: "default", Label: "Main Agent", Description: "opencode-go  ·  deepseek",
		Type: FieldNav, Inherited: true, InheritedFrom: "workspace",
	})
	form := NewDockedForm("form", model, "Session AI Settings", "", tokens)
	if got := form.PreferredHeight(40); got != 4 {
		t.Fatalf("compact dock height=%d, want title+rule+2 single-line fields", got)
	}
	form.Layout(LayoutContext{}, Rect{W: 72, H: 4})
	buf := surface.NewCellBuffer(72, 4)
	form.Paint(buf, 0, 0, PaintContext{Focused: true})
	var rows []string
	for y := 0; y < 4; y++ {
		var b strings.Builder
		for x := 0; x < 72; x++ {
			if c := buf.Get(x, y); c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		rows = append(rows, b.String())
	}
	if strings.Contains(rows[1], "subtitle") {
		t.Fatalf("compact header painted the subtitle on the rule row: %q", rows[1])
	}
	if strings.Contains(strings.Join(rows, "\n"), "Allow this session") {
		t.Fatal("compact dock painted the multi-agent description as its own row")
	}
	if !strings.Contains(rows[3], "Main Agent") || !strings.Contains(rows[3], "opencode-go") || !strings.Contains(rows[3], "inherited from workspace") {
		t.Fatalf("nav row must keep label, detail, and inheritance on one line: %q", rows[3])
	}
}

func TestDockedFormUsesShellBackground(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	shell := tokens.Color(ColorBg)
	popup := tokens.Color(ColorPopupBg)
	if shell.Equal(popup) {
		t.Fatal("dark theme must distinguish shell bg from popup bg")
	}
	model := NewFormModel()
	model.AddField(&FieldState{ID: "name", Label: "Name", Type: FieldText, Value: StringValue("hello")})
	form := NewDockedForm("form", model, "Docked", "Shares the chat surface", tokens)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 6})
	buf := surface.NewCellBuffer(40, 6)
	form.Paint(buf, 0, 0, PaintContext{})
	if got := buf.Get(0, 0).BG; !got.Equal(shell) {
		t.Fatalf("docked form title bg=%v want shell %v", got, shell)
	}
	if got := buf.Get(0, 0).BG; got.Equal(popup) {
		t.Fatal("docked form used popup background")
	}
}

func TestFormNavRowPaintsChevronNotPrompt(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(&FieldState{
		ID: "default", Label: "Main Agent", Description: "openrouter  ·  gpt-5",
		Type: FieldNav, Inherited: true, InheritedFrom: "workspace",
	})
	form := NewForm("form", model, "", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 60, H: 4})
	buf := surface.NewCellBuffer(60, 4)
	form.Paint(buf, 0, 0, PaintContext{Focused: true})
	var b strings.Builder
	for y := 0; y < 4; y++ {
		for x := 0; x < 60; x++ {
			if c := buf.Get(x, y); c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	plain := b.String()
	if strings.Contains(plain, "❯") {
		t.Fatalf("nav row must not use the chat prompt glyph:\n%s", plain)
	}
	if strings.Contains(plain, ") )") || strings.Contains(plain, "(inherited") {
		t.Fatalf("nav row must not wrap inherited text in parentheses:\n%s", plain)
	}
	if !strings.Contains(plain, theme.DefaultGlyphs().TreeCollapsed) {
		t.Fatalf("nav row missing TreeCollapsed chevron:\n%s", plain)
	}
	if !strings.Contains(plain, "inherited from workspace") {
		t.Fatalf("nav row missing inherited detail:\n%s", plain)
	}
}

func TestCompactTableUsesSharedInkAndDynamicGaps(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(&FieldState{
		ID: "default", Label: "Main Agent", Type: FieldNav,
		Columns:   []string{"deepseek-v4-flash", "opencode-go"},
		Inherited: true, InheritedFrom: "workspace",
	})
	model.AddField(&FieldState{
		ID: "crafter", Label: "Crafter", Type: FieldNav,
		Columns:   []string{"glm-5.1-precision", "crof"},
		Inherited: true, InheritedFrom: "workspace",
	})
	form := NewDockedForm("form", model, "Session Settings", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 80, H: 6})
	buf := surface.NewCellBuffer(80, 6)
	form.Paint(buf, 0, 0, PaintContext{})

	mainY, craftY := -1, -1
	for y := 0; y < 6; y++ {
		row := formRowText(buf, y)
		if strings.Contains(row, "Main Agent") {
			mainY = y
		}
		if strings.Contains(row, "Crafter") {
			craftY = y
		}
	}
	if mainY < 0 || craftY < 0 {
		t.Fatalf("missing table rows:\n%s", formBufferPlain(buf))
	}
	mainModel := formColOf(buf, mainY, "deepseek-v4-flash")
	craftModel := formColOf(buf, craftY, "glm-5.1-precision")
	if mainModel < 0 || mainModel != craftModel {
		t.Fatalf("model columns misaligned: main=%d crafter=%d", mainModel, craftModel)
	}
	labelEnd := formColOf(buf, mainY, "Main Agent") + rendering.VisibleWidth("Main Agent")
	if mainModel-labelEnd < TableMinGap {
		t.Fatalf("label/value gap=%d, want at least %d", mainModel-labelEnd, TableMinGap)
	}
	if got := buf.Get(formColOf(buf, mainY, "Main Agent"), mainY).FG; !got.Equal(tokens.Color(ColorFg)) {
		t.Fatalf("idle label fg=%v, want ColorFg", got)
	}
	if got := buf.Get(mainModel, mainY).FG; !got.Equal(tokens.Color(ColorFg)) {
		t.Fatalf("idle value fg=%v, want ColorFg", got)
	}
	inh := formColOf(buf, mainY, "inherited from workspace")
	if inh < 0 {
		t.Fatal("missing inherited text")
	}
	if got := buf.Get(inh, mainY).FG; !got.Equal(tokens.Color(ColorMuted)) {
		t.Fatalf("inherited fg=%v, want ColorMuted", got)
	}
	// The trailing meta column must leave a gutter before the nav chevron
	// (and the focused row's right edge) instead of painting flush against it.
	chevron := []rune(theme.DefaultGlyphs().TreeCollapsed)[0]
	chevronX := -1
	for x := 0; x < buf.Width(); x++ {
		if buf.Get(x, mainY).Char == chevron {
			chevronX = x
		}
	}
	if chevronX < 0 {
		t.Fatal("nav row missing chevron")
	}
	if gap := chevronX - (inh + rendering.VisibleWidth("inherited from workspace")); gap < 1 {
		t.Fatalf("gap between inherited text and chevron=%d, want at least 1", gap)
	}
}

func TestDockedFormRuleFullBleedSpansParentRow(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(&FieldState{ID: "name", Label: "Name", Type: FieldText, Value: StringValue("x")})
	form := NewDockedForm("form", model, "Session Settings", "", tokens)
	form.SetRuleBleed(0, 100)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 4})
	buf := surface.NewCellBuffer(100, 4)
	form.Paint(buf, 0, 0, PaintContext{})
	// Compact header: title on row 0, rule on row 1.
	if got := buf.Get(99, 1).Char; got != '─' {
		t.Fatalf("full-bleed header rule stopped before the parent row end: cell=%q", got)
	}

	form.SetRuleFullBleed(false)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 4})
	buf = surface.NewCellBuffer(100, 4)
	form.Paint(buf, 0, 0, PaintContext{})
	if got := buf.Get(41, 1).Char; got == '─' {
		t.Fatal("header rule expanded despite SetRuleFullBleed(false)")
	}
}

func TestFormSeparatorFieldUsesBleedWidth(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(&FieldState{ID: "sep", Label: "Advanced", Type: FieldSeparator})
	form := NewDockedForm("form", model, "Settings", "", tokens)
	form.SetRuleBleed(4, 100)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 5})
	if b := form.Field("sep").Bounds(); b.W != 100 || b.X != -4 {
		t.Fatalf("separator field bounds=%+v, want X=-4 W=100 (parent row)", b)
	}
	form.SetRuleBleed(0, 0)
	form.Layout(LayoutContext{}, Rect{W: 40, H: 5})
	if b := form.Field("sep").Bounds(); b.W != 40 || b.X != 0 {
		t.Fatalf("cleared bleed must bound the separator to the form, got %+v", b)
	}
}

func TestFormHoverMovesFieldHighlight(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(&FieldState{ID: "a", Label: "Alpha", Type: FieldNav, Target: "open.a"})
	model.AddField(&FieldState{ID: "b", Label: "Beta", Type: FieldNav, Target: "open.b"})
	model.AddField(&FieldState{ID: "gap", Label: "", Type: FieldSpacer})
	model.AddField(&FieldState{ID: "c", Label: "Gamma", Type: FieldNav, Target: "open.c"})
	form := NewDockedForm("form", model, "Settings", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 60, H: 8})
	if form.focused != 0 {
		t.Fatalf("initial focus=%d, want 0", form.focused)
	}
	// Compact form: header rows 0-1, fields at rows 2..4 (spacer included).
	res := form.Handle(EventContext{LocalX: 5, LocalY: 3}, MouseMotionEvent(tea.MouseMotionMsg{}))
	if !res.Handled || form.focused != 1 {
		t.Fatalf("motion over Beta: handled=%v focus=%d, want focus 1", res.Handled, form.focused)
	}
	if res.Invalidate&InvalidatePaint == 0 {
		t.Fatal("hover-focus change must invalidate paint")
	}
	res = form.Handle(EventContext{LocalX: 5, LocalY: 4}, MouseMotionEvent(tea.MouseMotionMsg{}))
	if form.focused != 1 || res.Invalidate != 0 {
		t.Fatalf("decorative spacer row must not take hover focus: focus=%d invalidate=%v", form.focused, res.Invalidate)
	}
	res = form.Handle(EventContext{LocalX: 5, LocalY: 7}, MouseMotionEvent(tea.MouseMotionMsg{}))
	if res.Handled || form.focused != 1 {
		t.Fatalf("motion outside fields: handled=%v focus=%d, want untouched", res.Handled, form.focused)
	}
}

func TestCycleControlAlignsWithSharedValueColumn(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	model := NewFormModel()
	model.AddField(&FieldState{
		ID: "default", Label: "Main Agent", Type: FieldNav,
		Columns: []string{"", "openrouter"}, Inherited: true, InheritedFrom: "workspace",
	})
	multi := BoolFieldState("multi", "Multi Agent", true, "")
	multi.Columns = []string{""}
	multi.Inherited = true
	multi.InheritedFrom = "workspace"
	model.AddField(multi)
	form := NewDockedForm("form", model, "Session Settings", "", tokens)
	form.Layout(LayoutContext{}, Rect{W: 80, H: 8})
	buf := surface.NewCellBuffer(80, 8)
	form.Paint(buf, 0, 0, PaintContext{})
	navY, cycleY := -1, -1
	for y := 0; y < 8; y++ {
		row := formRowText(buf, y)
		if strings.Contains(row, "Main Agent") {
			navY = y
		}
		if strings.Contains(row, "Multi Agent") {
			cycleY = y
		}
	}
	if navY < 0 || cycleY < 0 {
		t.Fatalf("missing rows:\n%s", formBufferPlain(buf))
	}
	provider := formColOf(buf, navY, "openrouter")
	cycle := formColOf(buf, cycleY, "← on →")
	if provider < 0 || cycle < 0 {
		t.Fatalf("missing value cells: provider=%d cycle=%d\n%s", provider, cycle, formBufferPlain(buf))
	}
	if provider != cycle {
		t.Fatalf("cycle control at col %d, want aligned with · provider at %d\n%s", cycle, provider, formBufferPlain(buf))
	}
}

func formRowText(buf *surface.CellBuffer, y int) string {
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		if c := buf.Get(x, y); c.Char != 0 {
			b.WriteRune(c.Char)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func formColOf(buf *surface.CellBuffer, y int, needle string) int {
	runes := []rune(needle)
	for x := 0; x <= buf.Width()-len(runes); x++ {
		ok := true
		for i, r := range runes {
			if buf.Get(x+i, y).Char != r {
				ok = false
				break
			}
		}
		if ok {
			return x
		}
	}
	return -1
}

func formBufferPlain(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		b.WriteString(formRowText(buf, y))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestTitleBreadcrumbJoinsHierarchy(t *testing.T) {
	if got := TitleBreadcrumb("Session Settings"); got != "Session Settings" {
		t.Fatalf("root=%q", got)
	}
	if got := TitleBreadcrumb("Session Settings", "Main Agent", "Model"); got != "Session Settings · Main Agent · Model" {
		t.Fatalf("nested=%q", got)
	}
	if got := TitleBreadcrumb("Session Settings", "", "Provider"); got != "Session Settings · Provider" {
		t.Fatalf("empty part=%q", got)
	}
}

func TestFilterFieldPaintsDistinctBackground(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	field := NewFilterField("filter", tokens)
	field.Layout(LayoutContext{}, Rect{W: 20, H: 1})
	buf := surface.NewCellBuffer(20, 1)
	field.Paint(buf, 0, 0, PaintContext{})
	got := buf.Get(0, 0).BG
	if got.Equal(tokens.Color(ColorBg)) || got.IsDefault() {
		t.Fatalf("filter bg=%v must be distinct from ColorBg", got)
	}
	if !got.Equal(FilterSurface(tokens)) {
		t.Fatalf("filter bg=%v want FilterSurface %v", got, FilterSurface(tokens))
	}
}
