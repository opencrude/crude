package settings

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/textencoding"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestNewFileEncodingIsSparseAndInheritsLive(t *testing.T) {
	parent := types.AppSettings{Editor: types.EditorSettings{NewFileEncoding: textencoding.UTF16BE}}
	stored := types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":8}`)}
	th := theme.DefaultDark()
	m := NewScoped(th, types.MergeAppWorkspace(parent, stored), nil, types.SettingsScopeWorkspace, parent, stored)
	m.SelectCategory(catEditor)
	root := NewSurface(th, m)
	host := ui.NewHost(th, root)
	root.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(80, 24)
	host.Layout()
	if len(m.Edits()) != 0 {
		t.Fatal("opening the form changed inherited settings")
	}
	root.FocusForm()
	if !root.form.FocusField("new-file-encoding") {
		t.Fatal("encoding field is not focusable")
	}
	for i := 0; i < 3; i++ {
		host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	}
	edits := m.Edits()
	if len(edits) != 1 || strings.Join(edits[0].Path, ".") != "editor.new_file_encoding" {
		t.Fatalf("not a single explicit preference: %+v", edits)
	}
	if err := types.ApplySettingsEdits(&stored, edits); err != nil {
		t.Fatal(err)
	}
	if got := types.MergeAppWorkspace(parent, stored).Editor; got.NewFileEncoding != textencoding.UTF8 || got.TabSize != 8 {
		t.Fatalf("explicit UTF-8 disappeared behind inheritance: %+v", got)
	}
	if err := types.ApplySettingsEdits(&stored, []types.SettingEdit{{Path: []string{"editor", "new_file_encoding"}}}); err != nil {
		t.Fatal(err)
	}
	parent.Editor.NewFileEncoding = textencoding.UTF8BOM
	if got := types.MergeAppWorkspace(parent, stored).Editor; got.NewFileEncoding != textencoding.UTF8BOM || got.TabSize != 8 {
		t.Fatalf("reset did not inherit the live parent: %+v", got)
	}
}

func TestNewFileEncodingChoicesRemainReadableInNarrowSettings(t *testing.T) {
	// A Settings window inside an 80×24 terminal has a 70×18 client area.
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{}, nil)
	m.SelectCategory(catEditor)
	root := NewSurface(th, m)
	host := ui.NewHost(th, root)
	root.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(70, 18)
	host.Layout()
	root.FocusForm()
	if !root.form.FocusField("new-file-encoding") {
		t.Fatal("encoding field is not focusable")
	}
	for index, encoding := range []textencoding.Encoding{textencoding.UTF8, textencoding.UTF8BOM, textencoding.UTF16LE, textencoding.UTF16BE} {
		if index > 0 {
			host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
		}
		host.Layout()
		buf := surface.NewCellBuffer(70, 18)
		host.Paint(buf)
		row := bufferRow(buf, "New file encoding")
		text := strings.Split(bufferPlain(buf), "\n")
		if row < 0 || row >= len(text) || strings.Contains(text[row], "…") || !strings.Contains(text[row], encoding.Label()) {
			t.Fatalf("encoding choice %q is not readable:\n%s", encoding.Label(), bufferPlain(buf))
		}
	}
}
