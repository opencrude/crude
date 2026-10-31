package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

func TestInlayHintsFormDefaultAndSparseCollection(t *testing.T) {
	fresh := New(theme.DefaultDark(), types.AppSettings{}, nil)
	field := fresh.categories[catLSP].Form.Field("inlay-hints")
	if field == nil || field.Value.String() != "off" || fresh.AppSettings().LSP.InlayHints || len(fresh.Edits()) != 0 {
		t.Fatal("fresh settings must show Off without creating an edit")
	}
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprint(scope, "/", enabled), func(t *testing.T) {
				app := types.AppSettings{LSP: types.DefaultLSPSettings()}
				types.ApplyAppDefaults(&app)
				app.LSP.InlayHints = !enabled
				stored := types.WorkspaceOverrides{LSP: json.RawMessage(`{"hover":false}`), Editor: json.RawMessage(`{"tab_size":8}`)}
				current := app
				if scope == types.SettingsScopeWorkspace {
					current = types.MergeAppWorkspace(app, stored)
				}
				m := NewScoped(theme.DefaultDark(), current, nil, scope, app, stored)
				value, literal := "off", "false"
				if enabled {
					value, literal = "on", "true"
				}
				m.SetValueForTest(catLSP, "inlay-hints", value)
				edits := m.Edits()
				if len(edits) != 1 || strings.Join(edits[0].Path, ".") != "lsp.inlay_hints" || string(edits[0].After) != literal {
					t.Fatalf("feature edit replayed neighboring leaves: %+v", edits)
				}
				want := types.CloneLSPSettings(current.LSP)
				want.InlayHints = enabled
				if got := m.AppSettings().LSP; !reflect.DeepEqual(got, want) {
					t.Fatalf("collection lost inlay value or changed existing features: %+v", got)
				}
				if scope == types.SettingsScopeWorkspace {
					got := m.Overrides()
					if string(got.LSP) != `{"hover":false,"inlay_hints":`+literal+`}` || string(got.Editor) != `{"tab_size":8}` {
						t.Fatalf("sparse overlay lost explicit value or neighbors: %+v", got)
					}
				}
			})
		}
	}
}

func TestInlayHintsNativeControlMetadataAndPaint(t *testing.T) {
	for _, size := range [][2]int{{130, 36}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			th := theme.DefaultDark()
			m := New(th, types.AppSettings{}, nil)
			m.SelectCategory(catLSP)
			s := NewSurface(th, m)
			host := ui.NewHost(th, s)
			host.Resize(size[0], size[1])
			host.Layout()
			id := ui.NewID(WorkspaceSettingsFormID, "inlay-hints")
			field, path := s.controlField(id)
			if field == nil || path != "lsp.inlay_hints" || field.Label != "Inlay hints" ||
				field.Type != ui.FieldCycle || field.Style != ui.StyleSwitch {
				t.Fatalf("missing ordinary native switch and canonical metadata: %+v %q", field, path)
			}
			for _, text := range []string{"Default Off", "read-only inline param/type", "advertised providers", "no commands/edits"} {
				if !strings.Contains(field.Description, text) {
					t.Fatalf("missing behavior constraint %q: %q", text, field.Description)
				}
			}
			effect, human, err := s.ControlMetadata(id, "form.changed")
			if err != nil || effect != control.Write || human || !s.ControlValueAllowed(id) || control.SettingsAuthority(path) {
				t.Fatalf("ordinary feature incorrectly treated as trust: %s %v %v", effect, human, err)
			}
			if !s.form.FocusField("inlay-hints") {
				t.Fatal("inlay hints cannot receive native focus")
			}
			for _, value := range []string{"on", "off"} {
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				result, err := s.form.Field("inlay-hints").HandleControl(ui.EventContext{}, ui.ControlOperation{Verb: "set_value", Value: raw})
				if err != nil || !result.Handled {
					t.Fatalf("native edit %s failed: %v", value, err)
				}
				host.MarkLayout()
				host.Layout()
				buf := surface.NewCellBuffer(size[0], size[1])
				host.Paint(buf)
				painted := bufferPlain(buf)
				var row string
				for _, line := range strings.Split(painted, "\n") {
					if strings.Contains(line, "Inlay hints") {
						row = line
						break
					}
				}
				if row == "" || !strings.Contains(row, strings.ToUpper(value[:1])+value[1:]) {
					t.Fatalf("native preference not painted after %s:\n%s", value, painted)
				}
				if m.AppSettings().LSP.InlayHints != (value == "on") {
					t.Fatal("native edit did not reach collector")
				}
				effect, human, err = s.ControlMetadata("save", "settings.save")
				if err != nil || human || effect != control.Write {
					t.Fatalf("ordinary inlay save became authority: %s %v %v", effect, human, err)
				}
			}
		})
	}
}

func TestInlayHintsPageResetInheritsLatestParent(t *testing.T) {
	app := types.AppSettings{LSP: types.DefaultLSPSettings()}
	types.ApplyAppDefaults(&app)
	stored := types.WorkspaceOverrides{
		LSP: json.RawMessage(`{"inlay_hints":false,"languages":{"go":{"enabled":false}}}`),
	}
	m := NewScoped(theme.DefaultDark(), types.MergeAppWorkspace(app, stored), nil, types.SettingsScopeWorkspace, app, stored)
	m.SelectCategory(catLSP)
	m.SetValueForTest(catEditor, "tab-size", "2")
	edits, ok := m.PageResetEdits()
	if !ok {
		t.Fatal("Code Intelligence reset unavailable")
	}
	if err := types.ApplySettingsEdits(&stored, edits); err != nil {
		t.Fatal(err)
	}
	app.LSP.InlayHints = true
	next := m.SectionResetModel(app, types.MergeAppWorkspace(app, stored), stored, catLSP)
	if !next.AppSettings().LSP.InlayHints || next.AppSettings().Editor.TabSize != 2 ||
		next.categories[catLSP].Form.Field("inlay-hints").Value.String() != "on" {
		t.Fatal("reset did not show latest parent or preserve unrelated draft")
	}
	if got := next.Overrides(); string(got.LSP) != `{"languages":{"go":{"enabled":false}}}` || string(got.Editor) != `{"tab_size":2}` {
		t.Fatalf("reset changed server inventory or preserved stale feature choice: %+v", got)
	}
}
