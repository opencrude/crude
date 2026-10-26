package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

func TestAttachmentOpenInterfaceFieldChoicesAndSearch(t *testing.T) {
	for _, choice := range []string{"", "system", "file-manager", "crude", "unknown"} {
		t.Run(choice, func(t *testing.T) {
			m := New(theme.DefaultDark(), types.AppSettings{UI: types.UISettings{AttachmentOpen: choice}}, nil)
			form := m.categories[catUI].Form
			field := form.Field("attachment-open")
			if field == nil || field.Label != "Open attachments in" || field.Style != ui.StyleSegmented {
				t.Fatalf("missing native destination segments: %+v", field)
			}
			want := []ui.FormOption{{ID: "system", Label: "System app"}, {ID: "file-manager", Label: "File manager"}, {ID: "crude", Label: "Crude"}}
			if !reflect.DeepEqual(field.Options, want) {
				t.Fatalf("destination options = %+v", field.Options)
			}
			if section := form.Field("attachments"); section == nil || section.Label != "Attachments" {
				t.Fatal("missing Attachments section")
			}
			expected := choice
			if expected == "" || expected == "unknown" {
				expected = "system"
			}
			if field.Value.String() != expected || m.AppSettings().UI.AttachmentOpen != expected || len(m.Edits()) != 0 {
				t.Fatalf("untouched preference did not round-trip cleanly: %+v", field)
			}
			if !strings.Contains(field.Description, "low-resolution") || len(field.Description) > 80 {
				t.Fatalf("preview limitation missing or help too long: %q", field.Description)
			}
		})
	}
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	for _, query := range []string{"attachments", "attachment-open", "low-resolution"} {
		m.SetSearchQuery(query)
		if !slices.Contains(m.visibleCategoryIndices(), catUI) {
			t.Fatalf("Interface not discoverable by %q", query)
		}
	}
}

func TestAttachmentOpenSparseFormEditsPreserveSiblings(t *testing.T) {
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		t.Run(string(scope), func(t *testing.T) {
			app := types.AppSettings{UI: types.UISettings{AttachmentOpen: "file-manager", SidebarWidth: 42, ShowIcons: true, CompactMode: true, MessageDelay: 15, AnimationsEnabled: true}}
			types.ApplyAppDefaults(&app)
			stored := types.WorkspaceOverrides{UI: json.RawMessage(`{"show_hidden_files":true}`), Editor: json.RawMessage(`{"tab_size":8}`)}
			current := app
			if scope == types.SettingsScopeWorkspace {
				current = types.MergeAppWorkspace(app, stored)
			}
			m := NewScoped(theme.DefaultDark(), current, nil, scope, app, stored)
			m.SetValueForTest(catUI, "attachment-open", "system")
			edits := m.Edits()
			if len(edits) != 1 || strings.Join(edits[0].Path, ".") != "ui.attachment_open" || string(edits[0].After) != `"system"` {
				t.Fatalf("destination edit replayed neighbors: %+v", edits)
			}
			want := current.UI
			want.AttachmentOpen = "system"
			if got := m.AppSettings().UI; got != want {
				t.Fatalf("collector changed untouched Interface values: %+v, want %+v", got, want)
			}
			if scope == types.SettingsScopeWorkspace {
				got := m.Overrides()
				if string(got.UI) != `{"attachment_open":"system","show_hidden_files":true}` || string(got.Editor) != `{"tab_size":8}` {
					t.Fatalf("workspace save changed neighbors: %+v", got)
				}
			}
		})
	}
}

func TestAttachmentOpenNativeControlAndPaint(t *testing.T) {
	for _, size := range [][2]int{{130, 36}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			th := theme.DefaultDark()
			m := New(th, types.AppSettings{}, nil)
			m.SelectCategory(catUI)
			s := NewSurface(th, m)
			host := ui.NewHost(th, s)
			host.Resize(size[0], size[1])
			host.Layout()
			id := ui.NewID(WorkspaceSettingsFormID, "attachment-open")
			field, path := s.controlField(id)
			if field == nil || path != "ui.attachment_open" {
				t.Fatalf("wrong stable preference metadata: %q", path)
			}
			effect, human, err := s.ControlMetadata(id, "form.changed")
			if err != nil || effect != control.Write || human || !s.ControlValueAllowed(id) {
				t.Fatalf("ordinary preference control denied: %s, %v, %v", effect, human, err)
			}
			if !s.form.FocusField("attachment-open") {
				t.Fatal("attachment destination cannot receive focus")
			}
			native := s.form.Field("attachment-open")
			result, err := native.HandleControl(ui.EventContext{}, ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"crude"`)})
			if err != nil || !result.Handled {
				t.Fatalf("native destination edit failed: %v", err)
			}
			host.MarkLayout()
			host.Layout()
			buf := surface.NewCellBuffer(size[0], size[1])
			host.Paint(buf)
			painted := bufferPlain(buf)
			for _, text := range []string{"Attachments", "Open attachments in", "Crude"} {
				if !strings.Contains(painted, text) {
					t.Fatalf("destination UI missing %q:\n%s", text, painted)
				}
			}
			if m.AppSettings().UI.AttachmentOpen != "crude" || len(m.Edits()) != 1 {
				t.Fatal("native interaction did not update the preference draft")
			}
		})
	}
}

func TestAttachmentOpenInterfaceResetPreservesOtherDrafts(t *testing.T) {
	app := types.AppSettings{UI: types.UISettings{AttachmentOpen: "file-manager"}}
	types.ApplyAppDefaults(&app)
	stored := types.WorkspaceOverrides{UI: json.RawMessage(`{"attachment_open":"crude","sidebar_width":45}`), Editor: json.RawMessage(`{"tab_size":8}`)}
	m := NewScoped(theme.DefaultDark(), types.MergeAppWorkspace(app, stored), nil, types.SettingsScopeWorkspace, app, stored)
	m.SelectCategory(catUI)
	m.SetValueForTest(catUI, "attachment-open", "system")
	m.SetValueForTest(catEditor, "tab-size", "2")
	edits, ok := m.PageResetEdits()
	if !ok {
		t.Fatal("Interface reset unavailable")
	}
	if err := types.ApplySettingsEdits(&stored, edits); err != nil {
		t.Fatal(err)
	}
	next := m.SectionResetModel(app, types.MergeAppWorkspace(app, stored), stored, catUI)
	if next.AppSettings().UI.AttachmentOpen != "file-manager" || next.AppSettings().Editor.TabSize != 2 {
		t.Fatal("reset did not restore application choice and preserve unrelated draft")
	}
	if got := next.Overrides(); len(got.UI) != 0 || string(got.Editor) != `{"tab_size":2}` {
		t.Fatalf("reset saved stale Interface draft or lost Editor: %+v", got)
	}
}
