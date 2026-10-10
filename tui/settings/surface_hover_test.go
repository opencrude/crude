package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// The settings form and category rail have separate keyboard selection and
// pointer hover. Motion on the form must not change which field receives keys.
func TestSettingsFormMouseHoverKeepsKeyboardTarget(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Editor: types.EditorSettings{TabSize: 4}}, nil)
	body := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", body, nil, true, 100, 26)
	body.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(100, 26)
	host.Layout()

	// Select Editor by a real rail click; its first editable row is Tab size.
	for i, item := range body.categories.list.Items() {
		if item.Label != "Editor" {
			continue
		}
		body.categories.list.SelectIndex(i)
		host.MarkLayout()
		host.Layout()
		rail, ok := host.ScreenRect(WorkspaceSettingsListID)
		if !ok {
			t.Fatal("no rail hit bounds")
		}
		y := rail.Y + i - body.categories.list.Scroll().Offset()
		host.Handle(tea.MouseClickMsg{X: rail.X + 1, Y: y, Button: tea.MouseLeft})
		host.Handle(tea.MouseReleaseMsg{X: rail.X + 1, Y: y, Button: tea.MouseLeft})
		break
	}
	if model.SelectedCategory() != catEditor || body.form.Field("word-wrap") == nil {
		t.Fatalf("did not open Editor on rail click: category=%d", model.SelectedCategory())
	}
	host.Layout()
	formRect, ok := host.ScreenRect(WorkspaceSettingsFormID)
	if !ok {
		t.Fatal("no form hit bounds")
	}
	row := body.form.Field("word-wrap").Bounds()
	x, y := formRect.X+2, formRect.Y+row.Y
	// Hover from the rail, while the form is not focused: the pointer must
	// not change the region that receives the next key.
	res := host.Handle(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})
	if host.Focused() != WorkspaceSettingsListID || model.SelectedCategory() != catEditor || !res.Handled {
		t.Fatalf("hover changed rail focus or selected page: focus=%q category=%d result=%+v", host.Focused(), model.SelectedCategory(), res)
	}
	buf := surface.NewCellBuffer(100, 26)
	host.Paint(buf)
	// Hover lights the row's control, never the label column.
	if got, want := buf.Get(x, y).BG, body.tokens.Color(ui.ColorPopupBg); !got.Equal(want) {
		t.Fatalf("hovered label bg=%v want the surface %v", got, want)
	}
	lit := false
	for col := x; col < formRect.X+formRect.W; col++ {
		lit = lit || buf.Get(col, y).BG.Equal(body.tokens.Color(ui.ColorHover))
	}
	if !lit {
		t.Fatal("hovered row's control did not take the hover fill")
	}
	body.form.FocusFirst()
	body.FocusForm()
	host.Layout()
	res = host.Handle(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})
	if !res.Handled || res.Action != nil || model.value(catEditor, "word-wrap") != "off" {
		t.Fatalf("hover performed an action or changed value: %+v", res)
	}
	if host.Focused() != WorkspaceSettingsFormID {
		t.Fatalf("hover stole form focus: %q", host.Focused())
	}
	buf = surface.NewCellBuffer(100, 26)
	host.Paint(buf)
	if got, want := buf.Get(x, y).BG, body.tokens.Color(ui.ColorPopupBg); !got.Equal(want) {
		t.Fatalf("hovered label bg=%v want the surface %v", got, want)
	}
	lit = false
	for col := x; col < formRect.X+formRect.W; col++ {
		lit = lit || buf.Get(col, y).BG.Equal(body.tokens.Color(ui.ColorHover))
	}
	if !lit {
		t.Fatal("hovered row control did not take the hover fill")
	}
	if got, want := buf.Get(x+1, y).FG, body.tokens.Color(ui.ColorFg); !got.Equal(want) {
		t.Fatalf("hovered label fg=%v want unfocused %v", got, want)
	}

	// Keys still belong to Tab size; motion cannot toggle Word wrap.
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	host.Handle(tea.KeyPressMsg{Code: '8', Text: "8"})
	if got := model.value(catEditor, "tab-size"); got != "8" {
		t.Fatalf("keyboard edited %q want Tab size 8", got)
	}
	if got := model.value(catEditor, "word-wrap"); got != "off" {
		t.Fatalf("hover toggled Word wrap: %q", got)
	}
	// A click moves form focus and activates the row, as before.
	res = host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if !res.Handled || model.value(catEditor, "word-wrap") != "on" {
		t.Fatalf("click did not toggle Word wrap: result=%+v value=%q", res, model.value(catEditor, "word-wrap"))
	}
}

// Field help lives in the empty row above the footer rule: the hovered
// field's description, otherwise the focused field's. It never takes body
// rows, never sits beside a control, and the footer row stays one line.
func TestFieldHelpReadsInTheRowAboveTheFooterRule(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{Editor: types.EditorSettings{TabSize: 4}}, nil)
	model.SelectCategory(catEditor)
	body := NewSurface(th, model)
	host := ui.NewHost(th, body)
	body.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(120, 30)
	body.form.FocusField("tab-size")
	body.FocusForm()
	host.MarkLayout()
	host.Layout()
	helpRow := body.Bounds().H - footerRows - 1
	paint := func() string {
		buf := surface.NewCellBuffer(120, 30)
		host.Paint(buf)
		return settingsRow(buf, helpRow, 120)
	}
	tabSize, wordWrap := body.form.Field("tab-size").FieldState(), body.form.Field("word-wrap").FieldState()
	if row := paint(); !strings.Contains(row, tabSize.Description[:20]) {
		t.Fatalf("focused field's help not in the row above the rule: %q", row)
	}
	formRect, _ := host.ScreenRect(WorkspaceSettingsFormID)
	wrap := body.form.Field("word-wrap").Bounds()
	host.Handle(tea.MouseMotionMsg{X: formRect.X + 2, Y: formRect.Y + wrap.Y})
	if row := paint(); !strings.Contains(row, wordWrap.Description[:20]) {
		t.Fatalf("hovered field's help did not replace the focused one: %q", row)
	}
}
