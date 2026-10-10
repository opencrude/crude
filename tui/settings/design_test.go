package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
)

func paintSettings(t *testing.T, root *WorkspaceSettingsSurface, width, height int) string {
	t.Helper()
	host := ui.NewHost(theme.DefaultDark(), root)
	root.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(width, height)
	host.Layout()
	buffer := surface.NewCellBuffer(width, height)
	host.Paint(buffer)
	return bufferPlain(buffer)
}

// Save is a promise that something will be written; it is offered only when
// a page holds unsaved work, and the footer and rail say where that work is.
func TestUnsavedWorkDrivesSaveFooterAndRailMarker(t *testing.T) {
	model := New(theme.DefaultDark(), types.AppSettings{}, nil)
	model.SelectCategory(catEditor)
	root := NewSurface(theme.DefaultDark(), model)
	plain := paintSettings(t, root, 140, 40)
	if root.actions.ActionEnabled("settings.save") || strings.Contains(plain, "Unsaved changes") {
		t.Fatalf("clean settings offered Save:\n%s", plain)
	}
	if !strings.Contains(plain, "[ Close ]") || strings.Contains(plain, "[ Cancel ]") {
		t.Fatalf("footer must always read Close:\n%s", plain)
	}
	model.categories[catEditor].Form.SetValue("word-wrap", ui.StringValue("on"))
	if !root.actions.ActionEnabled("settings.save") {
		t.Fatal("an edit did not enable Save")
	}
	marked := false
	for _, item := range model.CategoryItems() {
		if item.Label == "Editor" {
			marked = item.Detail == railModifiedMarker
		}
	}
	if !marked {
		t.Fatal("the edited page is not marked in the rail")
	}
	if plain = paintSettings(t, root, 140, 40); !strings.Contains(plain, "● Unsaved changes") {
		t.Fatalf("footer does not state unsaved work:\n%s", plain)
	}
}

// Every page carries the shared header, and the focused field's description
// is shown in full below the page rather than clipped beside its control.
func TestFormPagesShareHeaderAndFullHelpStrip(t *testing.T) {
	model := New(theme.DefaultDark(), types.AppSettings{}, nil)
	model.SelectCategory(catHistory)
	root := NewSurface(theme.DefaultDark(), model)
	root.form.FocusField(fieldHistoryExternal)
	plain := paintSettings(t, root, 140, 40)
	description := model.categories[catHistory].Form.Field(fieldHistoryExternal).Description
	if !strings.Contains(plain, "Local History") || !strings.Contains(plain, model.categories[catHistory].Description) {
		t.Fatalf("missing shared page header:\n%s", plain)
	}
	if !strings.Contains(plain, description) {
		t.Fatalf("focused field help %q not painted in full:\n%s", description, plain)
	}
	if strings.Contains(plain, "(MB)") || strings.Contains(plain, "(seconds)") {
		t.Fatalf("units must sit beside values, not in labels:\n%s", plain)
	}
}

// Typing in the Skills store searches enabled sources after a pause; only the
// newest query may start that search.
func TestStoreSearchesLiveAfterTypingPause(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	catalog := root.capCatalog
	host.Focus(catalog.filter.ID())
	result := host.Handle(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if result.Cmd == nil || catalog.searchGeneration != 1 {
		t.Fatalf("typing did not arm a live search (generation %d)", catalog.searchGeneration)
	}
	host.Handle(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if root.CapabilitySearchTick(CapabilitySearchTickMsg{Target: model, Generation: 1}) != nil {
		t.Fatal("a superseded keystroke started a search")
	}
	command := root.CapabilitySearchTick(CapabilitySearchTickMsg{Target: model, Generation: catalog.searchGeneration})
	if command == nil {
		t.Fatal("the quiet period did not start a search")
	}
	request, ok := command().(CapabilityRequestMsg)
	if !ok || request.Action.Operation != "discover" || request.Action.Query != "re" {
		t.Fatalf("live search request = %+v", request)
	}
	if !catalog.searching() || !root.NeedsFrame() {
		t.Fatal("a running live search must animate the progress rule")
	}
	if root.CapabilitySearchTick(CapabilitySearchTickMsg{Target: New(theme.DefaultDark(), types.AppSettings{}, nil), Generation: catalog.searchGeneration}) != nil {
		t.Fatal("a tick for another settings model started a search")
	}
}

// A newer live search replaces the one in flight; the stale result can no
// longer overwrite the newer query's results.
func TestNewerLiveSearchSupersedesInFlightSearch(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindSkill)
	catalog := root.capCatalog
	first, command := model.requestCapability(captypes.Action{Operation: "discover", Kind: captypes.KindSkill, Query: "re"})
	if command == nil {
		t.Fatal("first search refused")
	}
	second, command := model.requestCapability(captypes.Action{Operation: "discover", Kind: captypes.KindSkill, Query: "react"})
	if command == nil {
		t.Fatal("newer live search queued behind the old one")
	}
	if request := command().(CapabilityRequestMsg); request.Supersedes != first {
		t.Fatalf("supersedes %d, want %d", request.Supersedes, first)
	}
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: first, Entries: []captypes.Entry{{ID: "stale", Kind: captypes.KindSkill, Name: "Stale"}}})
	if model.capabilityDiscoveryQuery[captypes.KindSkill].Query == "re" || model.capabilityPending == nil {
		t.Fatal("the superseded result replaced the newer search")
	}
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: second, Entries: []captypes.Entry{{ID: "fresh", Kind: captypes.KindSkill, Name: "Fresh"}}})
	if model.capabilityDiscoveryQuery[captypes.KindSkill].Query != "react" || model.capabilityPending != nil || catalog.searching() {
		t.Fatal("the newest search did not settle")
	}
}

func TestCommandRulesRoundTripAsTable(t *testing.T) {
	rules := []types.CommandSeverityRule{{Pattern: "scripts/*.sh", Risk: "safe"}, {Pattern: "docker:run*", Risk: "destructive"}}
	text := formatCommandRules(rules)
	if got := parseCommandRules(text); len(got) != 2 || got[1].Pattern != "docker:run*" || got[1].Risk != "destructive" {
		t.Fatalf("rules = %+v from %q", got, text)
	}
	if errs := commandRulesValidator(ui.StringValue("deploy*: explosive")); len(errs) == 0 {
		t.Fatal("unknown severity accepted")
	}
}
