package settings

import (
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeCatalogControlMetadataAndRenderedEdit(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	s.agents.begin("role/default", false)
	s.agents.setPage("instructions")
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(130, 36)
	host.Layout()
	id := ui.NewID(AgentSettingsFormID, "agent-instructions")
	effect, human, err := s.ControlMetadata(id, "form.changed")
	if err != nil || effect != control.Write || human || !s.ControlValueAllowed(id) {
		t.Fatalf("native instructions: %s %v %v", effect, human, err)
	}
	field := s.agents.form.Field("agent-instructions")
	value, _ := json.Marshal("Native catalog retained instructions")
	if res, err := field.HandleControl(ui.EventContext{}, ui.ControlOperation{Verb: "set_value", Value: value}); err != nil || !res.Handled {
		t.Fatalf("native edit: %v %v", res.Handled, err)
	}
	buf := surface.NewCellBuffer(130, 36)
	host.Paint(buf)
	if !strings.Contains(bufferPlain(buf), "Native catalog retained instructions") {
		t.Fatal("native field edit was not painted")
	}
	effect, human, err = s.ControlMetadata(ui.NewID(AgentSettingsFormID, fieldMultiAgent), "form.changed")
	if err != nil || effect != control.Authority || !human {
		t.Fatal("multi-agent ceiling admitted", effect, human, err)
	}
	s.agents.begin("role/auto-review", false)
	effect, human, err = s.ControlMetadata(ui.NewID(AgentSettingsFormID, fieldModel), "form.changed")
	if err != nil || effect != control.Authority || !human {
		t.Fatal("reviewer model admitted", effect, human, err)
	}
}

func TestLanguageServerNativeFieldAuthorityAndFilterOwner(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	s.lsp.begin("go")
	for _, field := range []string{lspFieldSettings, lspFieldInitOptions} {
		if s.ControlValueAllowed(ui.NewID(LanguageServerFormID, field)) {
			t.Fatal("opaque language server object released", field)
		}
	}
	for _, tc := range []struct {
		id    string
		human bool
	}{{lspFieldCommand, true}, {lspFieldArgs, true}, {lspFieldEnvironment, true}} {
		id := ui.NewID(LanguageServerFormID, tc.id)
		effect, human, err := s.ControlMetadata(id, "form.changed")
		if err != nil || human != tc.human || (effect == control.Authority) != tc.human {
			t.Fatalf("%s: %s %v %v", tc.id, effect, human, err)
		}
		if s.ControlValueAllowed(id) == tc.human {
			t.Fatal("value release mismatch", tc.id)
		}
	}
	value, _ := json.Marshal("no-such-language-server")
	res, err := s.lsp.filter.HandleControl(ui.EventContext{}, ui.ControlOperation{Verb: "set_value", Value: value})
	if err != nil || !res.Handled || len(s.lsp.table.Rows()) != 0 {
		t.Fatal("catalog filter did not update its owner", err)
	}
}

func TestControlMetadataSaveIncludesOffPageSecurity(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catEditor)
	s.refreshForm()
	id := ui.NewID(WorkspaceSettingsFormID, "word-wrap")
	effect, human, err := s.ControlMetadata(id, "form.changed")
	if err != nil || effect != control.Write || human || !s.ControlValueAllowed(id) {
		t.Fatalf("ordinary metadata: %s %v %v", effect, human, err)
	}
	m.categories[catEditor].Form.SetValue("word-wrap", ui.StringValue("on"))
	effect, human, err = s.ControlMetadata("save", "settings.save")
	if err != nil || effect != control.Write || human {
		t.Fatal("ordinary save rejected", effect, human, err)
	}
	m.categories[catSafety].Form.SetValue(fieldSafetyConfirmAt, ui.StringValue("destructive"))
	effect, human, err = s.ControlMetadata("save", "settings.save")
	if err != nil || effect != control.Authority || !human {
		t.Fatal("off-page safety laundered", effect, human, err)
	}
	m.SelectCategory(catSafety)
	s.refreshForm()
	if s.ControlValueAllowed(ui.NewID(WorkspaceSettingsFormID, fieldSafetyRules)) {
		t.Fatal("authority value released")
	}
}

func TestControlMetadataUnknownFieldsFailClosed(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catEditor)
	s.refreshForm()
	m.categories[catEditor].Form.AddField(ui.TextFieldState("future", "ordinary looking label", "secret", ""))
	id := ui.NewID(WorkspaceSettingsFormID, "future")
	if _, _, err := s.ControlMetadata(id, "form.changed"); err == nil || s.ControlValueAllowed(id) {
		t.Fatal("unmapped field admitted")
	}
}

func TestAgentApplyProspectiveFullDraftAndSave(t *testing.T) {
	for _, role := range []string{"default", "crafter", "researcher"} {
		t.Run(role, func(t *testing.T) {
			m := New(theme.DefaultDark(), types.AppSettings{}, nil)
			s := NewSurface(theme.DefaultDark(), m)
			m.SelectCategory(catAgents)
			s.agents.begin("role/"+role, false)
			s.agents.setPage("instructions")
			host := ui.NewHost(theme.DefaultDark(), s)
			host.Resize(130, 36)
			host.Layout()
			id := ui.NewID(AgentSettingsFormID, "agent-instructions")
			page, err := host.InspectControls(control.Target{}, control.PageRequest{Limit: 128})
			if err != nil {
				t.Fatal(err)
			}
			var node control.Node
			for _, n := range page.Nodes {
				if n.ID == string(id) {
					node = n
				}
			}
			if node.ID == "" {
				t.Fatal("missing retained instructions")
			}
			res, err := host.Control(id, node.OpenEpoch, &node.Revision, ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"ordinary instructions"`)})
			if err != nil || !res.Handled {
				t.Fatal(err)
			}
			effect, human, err := s.ControlMetadata("apply", "agents.save")
			if err != nil || human || effect != control.Write {
				t.Fatalf("ordinary Apply: %s %v %v", effect, human, err)
			}
			if !s.agents.apply() {
				t.Fatal("apply failed")
			}
			effect, human, err = s.ControlMetadata("save", "settings.save")
			if err != nil || human || effect != control.Write {
				t.Fatalf("ordinary Save: %s %v %v", effect, human, err)
			}
			if m.AppSettings().Agents.Categories[role].Instructions != "ordinary instructions" {
				t.Fatal("not staged")
			}
			s.agents.begin("role/"+role, false)
			s.agents.draft.SetValue("agent-tool-mode", ui.StringValue("Custom selection"))
			s.agents.setPage("model")
			effect, human, err = s.ControlMetadata("apply", "agents.save")
			if err != nil || !human || effect != control.Authority {
				t.Fatal("offpage tool mode admitted", effect, human, err)
			}
		})
	}
}
