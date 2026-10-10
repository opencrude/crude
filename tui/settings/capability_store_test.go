package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func paintCapabilityStore(host *ui.Host, width, height int) *surface.CellBuffer {
	host.Resize(width, height)
	host.MarkLayout()
	host.Layout()
	buffer := surface.NewCellBuffer(width, height)
	host.Paint(buffer)
	return buffer
}

func storeTextPoint(t *testing.T, buffer *surface.CellBuffer, text string) (int, int) {
	t.Helper()
	for row := 0; row < buffer.Height(); row++ {
		var line strings.Builder
		for col := 0; col < buffer.Width(); col++ {
			char := buffer.Get(col, row).Char
			if char == 0 {
				char = ' '
			}
			line.WriteRune(char)
		}
		if index := strings.Index(line.String(), text); index >= 0 {
			return len([]rune(line.String()[:index])), row
		}
	}
	t.Fatalf("%q missing from painted store:\n%s", text, bufferPlain(buffer))
	return 0, 0
}

func TestCapabilityStoreHasExplicitInstalledFilterAndInlineExactInstall(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	catalog := root.capCatalog
	catalog.filter.SetValue("guide")
	catalog.views[catalog.viewKey()] = capabilityListState{query: "guide"}
	request := capabilityRequest(t, capabilityAction(catalog, "search"))
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: request.Sequence, Entries: []captypes.Entry{
		{ID: "vendor:guide", Kind: captypes.KindSkill, Name: "Guide", Source: "vendor"},
		{ID: "community:new", Kind: captypes.KindSkill, Name: "New guide", Publisher: "maintainer/repository", Description: "Review and improve a working application", Source: "community"},
	}})
	catalog.consumeResult()
	buffer := paintCapabilityStore(host, 130, 36)
	if len(catalog.store.Items()) != 2 || !catalog.tabs.Bounds().Empty() || strings.Contains(bufferPlain(buffer), "Discover") {
		t.Fatal("catalog duplicates installed identities or retained the old Installed/Discover tab flow")
	}
	storeTextPoint(t, buffer, "Review and improve a working application")
	x, y := storeTextPoint(t, buffer, "[ Install ]")
	result := host.Handle(tea.MouseClickMsg{X: x + 2, Y: y, Button: tea.MouseLeft})
	request = capabilityRequest(t, result)
	if request.Action.ID != "community:new" || request.Action.Operation != "preview-install" {
		t.Fatalf("inline button acted on the selected neighbor: %+v", request.Action)
	}
	preview := captypes.AcquisitionPreview{ID: "community:new", Kind: captypes.KindSkill, Scope: captypes.ScopeApplication, Operation: "install", Candidate: captypes.Entry{ID: "community:new", Name: "New guide", Version: "exact-revision"}, Token: "reviewed-preimage"}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	completeCapability(model, catalog, request, string(encoded))
	if catalog.detailPage != "acquisition" {
		t.Fatal("inline install bypassed its immutable preview")
	}
	request = capabilityRequest(t, capabilityAction(catalog, "confirm-acquisition"))
	if request.Action.Preview == nil || request.Action.Preview.Token != "reviewed-preimage" {
		t.Fatal("confirmation lost exact preview")
	}
	snapshot := model.capabilitySnapshot
	snapshot.Generation++
	snapshot.Settings = captypes.Clone(snapshot.Settings)
	snapshot.Settings.Skills["community:new"] = captypes.SkillConfig{ID: "community:new", Name: "New guide", Source: "community", Scope: captypes.ScopeApplication}
	snapshot.Entries = append(snapshot.Entries, captypes.Entry{ID: "community:new", Kind: captypes.KindSkill, Name: "New guide", Description: "Review and improve a working application", Source: "community", Installed: true, Origin: captypes.ScopeApplication})
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: request.Sequence, Snapshot: snapshot})
	catalog.consumeResult()
	buffer = paintCapabilityStore(host, 130, 36)
	if catalog.detail || catalog.filter.Value() != "guide" || strings.Contains(bufferPlain(buffer), "[ Install ]") {
		t.Fatal("installation failed to return to retained results with installed status")
	}
	x, y = storeTextPoint(t, buffer, "Installed")
	result = host.Handle(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	if result.Cmd != nil || model.capabilityPending != nil || catalog.views[catalog.viewKey()].show != "installed" {
		t.Fatal("Installed-only filter initiated external activity")
	}
	if capabilityAction(catalog, "search").Cmd != nil {
		t.Fatal("installed search made a network request")
	}
}

func TestCapabilityStoreKeepsFuzzyResultsAndExposesPartialSourceFailure(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	catalog.filter.SetValue("git")
	catalog.views[catalog.viewKey()] = capabilityListState{query: "git"}
	request := capabilityRequest(t, capabilityAction(catalog, "search"))
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: request.Sequence, Entries: []captypes.Entry{
		{ID: "registry:fuzzy", Kind: captypes.KindMCP, Name: "Repository service", Source: "vendor", Description: "Inspect repository history"},
		{ID: "registry:unsupported", Kind: captypes.KindMCP, Name: "Old endpoint", Source: "vendor", State: "unsupported", Reason: "SSE transport is not supported"},
	}, Err: errors.New("Source community is unavailable")})
	catalog.consumeResult()
	buffer := paintCapabilityStore(host, 130, 36)
	storeTextPoint(t, buffer, "Repository service")
	storeTextPoint(t, buffer, "Source community is unavailable")
	storeTextPoint(t, buffer, "[ Details ]")
	for index, item := range catalog.store.Items() {
		if item.ID == "registry:unsupported" {
			catalog.store.SelectIndex(index)
		}
	}
	if capabilityAction(catalog, "install").Cmd != nil {
		t.Fatal("unsupported entry was installed")
	}
	// Editing the query invalidates old remote results even if their text would
	// otherwise pass a local substring test; late results cannot restore them.
	host.Focus(catalog.filter.ID())
	host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	buffer = paintCapabilityStore(host, 130, 36)
	if strings.Contains(bufferPlain(buffer), "Repository service") {
		t.Fatal("old external result attached to a newer query")
	}
}

func TestCapabilityStoreFiltersOfferChoicesAndSearchExactSelectedSource(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	model.capabilities.Sources["community"] = captypes.SourceConfig{ID: "community", Name: "Community", Kind: "skill-registry"}
	model.capabilities.Sources["disabled"] = captypes.SourceConfig{ID: "disabled", Name: "Disabled source", Kind: "skill-registry", Disabled: true}
	model.capabilityRevision++
	catalog := root.capCatalog
	capabilityAction(catalog, "filter-source")
	buffer := paintCapabilityStore(host, 130, 36)
	x, y := storeTextPoint(t, buffer, "Community")
	if strings.Contains(bufferPlain(buffer), "Disabled source") {
		t.Fatal("disabled source offered as a search choice")
	}
	host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if catalog.choosing != "" || catalog.views[catalog.viewKey()].source != "community" {
		t.Fatal("source picker did not apply the clicked exact source")
	}
	catalog.filter.SetValue("workflow")
	catalog.views[catalog.viewKey()] = capabilityListState{query: "workflow", source: "community"}
	request := capabilityRequest(t, capabilityAction(catalog, "search"))
	if request.Action.Arguments["source_id"] != "community" {
		t.Fatal("source filter only filtered the first page instead of querying its source")
	}
}

func TestCapabilityStoreChromeIsBoundedAndAbsentOnOtherSettingsPages(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	for _, width := range []int{72, 80, 100, 130, 160} {
		for _, category := range []int{catSkills, catMCP, catSources, catAccounts, catAgents, catAppearance} {
			model.SelectCategory(category)
			buffer := paintCapabilityStore(host, width, 24)
			plain := bufferPlain(buffer)
			if category != catSkills && category != catMCP && category != catSources && (strings.Contains(plain, "Installed only") || strings.Contains(plain, "[ Source:") || strings.Contains(plain, "[ Sort:")) {
				t.Fatalf("hidden catalog chrome paints over category %d at %d columns", category, width)
			}
			if !root.capabilitiesOpen() {
				continue
			}
			bounds := root.capCatalog.Bounds()
			for _, child := range root.capCatalog.Children() {
				r := child.Bounds()
				if !r.Empty() && (r.X < 0 || r.Y < 0 || r.X+r.W > bounds.W || r.Y+r.H > bounds.H) {
					t.Fatalf("%d columns: %s escapes catalog: %+v in %+v", width, child.ID(), r, bounds)
				}
			}
		}
	}
}

func TestCapabilityStoreWheelAndThumbMovePaintedCardsWithoutSnapback(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	for index := 0; index < 40; index++ {
		model.capabilitySnapshot.Entries = append(model.capabilitySnapshot.Entries, captypes.Entry{ID: fmt.Sprint(index), Name: fmt.Sprintf("Skill card %02d", index), Kind: captypes.KindSkill, Installed: true})
	}
	model.capabilityRevision++
	catalog := root.capCatalog
	buffer := paintCapabilityStore(host, 130, 36)
	before := bufferPlain(buffer)
	rect, _ := host.ScreenRect(catalog.store.ID())
	host.Handle(tea.MouseWheelMsg{X: rect.X + 2, Y: rect.Y + 2, Button: tea.MouseWheelDown})
	host.Handle(tea.MouseWheelMsg{X: rect.X + 2, Y: rect.Y + 2, Button: tea.MouseWheelDown})
	offset := catalog.store.Scroll().Offset()
	buffer = paintCapabilityStore(host, 130, 36)
	if offset == 0 || catalog.store.Scroll().Offset() != offset || before == bufferPlain(buffer) {
		t.Fatal("wheel changed no painted content or layout snapped back to selection")
	}
	// Theme re-projection preserves query, offset and the direct row action.
	host.SetTheme(theme.DefaultLight())
	buffer = paintCapabilityStore(host, 130, 36)
	if catalog.store.Scroll().Offset() != offset || !strings.Contains(bufferPlain(buffer), "[ Manage ]") {
		t.Fatal("theme change lost retained store state")
	}
}
