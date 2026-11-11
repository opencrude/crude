package settings

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"testing"
)

func TestIndentationNativeFieldsCollectOnlyExplicitChoices(t *testing.T) {
	base := types.AppSettings{Editor: types.EditorSettings{TabSize: 8, IndentStyle: "", IndentSize: 0}}
	model := New(theme.DefaultDark(), base, nil)
	model.SelectCategory(catEditor)
	body := NewSurface(theme.DefaultDark(), model)
	host := ui.NewHost(theme.DefaultDark(), body)
	body.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(80, 24)
	host.Layout()
	body.FocusForm()
	if !body.form.FocusField("indent-style") {
		t.Fatal("style control missing")
	}
	host.MarkLayout()
	host.Layout()
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if !body.form.FocusField("indent-size") {
		t.Fatal("width control missing")
	}
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	host.Handle(tea.KeyPressMsg{Code: '3', Text: "3"})
	got := body.Model().AppSettings().Editor
	if got.IndentStyle != "space" || got.IndentSize != 3 || got.TabSize != 8 {
		t.Fatal("native fields confused indent width and tab width", got)
	}
	edits := body.Model().Edits()
	if len(edits) != 2 {
		t.Fatal("saving indentation includes unrelated choices", edits)
	}
	model.categories[catEditor].Form.SetValue("indent-style", ui.StringValue("auto"))
	model.categories[catEditor].Form.SetValue("indent-size", ui.StringValue("0"))
	got = model.AppSettings().Editor
	if got.IndentStyle != "" || got.IndentSize != 0 {
		t.Fatal("Detect was stored as a false style", got)
	}
}
