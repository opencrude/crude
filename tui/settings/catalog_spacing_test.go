package settings

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
)

func TestSettingsHelpStaysConciseAndReadable(t *testing.T) {
	model, body, _ := capabilityFixture(t, "mcp")
	forms := []*ui.FormModel{model.buildAgentForm(model.agents.Categories["default"], true), model.buildAutoReviewerForm(), model.buildLSPForm(types.LSPSettings{}), model.buildAcquisitionForm()}
	forms = append(forms, formModel(mcpConnectionFields(model.capabilities.Connections["docs"])...))
	if err := model.beginAccountEdit(true, "openai"); err != nil {
		t.Fatal(err)
	}
	forms = append(forms, model.buildAccountEditorForm())
	for _, server := range model.lspServers() {
		forms = append(forms, buildLSPServerForm(server))
	}
	for _, kind := range []string{"skill", "source"} {
		body.capCatalog.kind, body.capCatalog.edit = captypes.Kind(kind), "add"
		body.capCatalog.editForm()
		forms = append(forms, model.capabilityDraft)
	}
	for _, form := range forms {
		for _, field := range form.Fields() {
			if field.Description != "" && rendering.VisibleWidth(field.Description) > 80 {
				t.Errorf("%s help overflows the ordinary Settings pane (%d columns): %s", field.ID, rendering.VisibleWidth(field.Description), field.Description)
			}
		}
	}
}

func TestEverySettingsCatalogActionsTouchFooterRule(t *testing.T) {
	for _, size := range []ui.Size{{W: 124, H: 28}, {W: 88, H: 24}, {W: 70, H: 18}} {
		for _, category := range []int{catAccounts, catAgents, catSkills, catMCP, catLanguageServers, catSources} {
			t.Run(fmt.Sprintf("%dx%d/%d", size.W, size.H, category), func(t *testing.T) {
				th := theme.DefaultDark()
				model := New(th, accountSettingsFixture(), nil)
				model.SelectCategory(category)
				body := NewSurface(th, model)
				frame := ui.NewWindowFrame("test/settings-frame", ui.FrameWindow, ui.NewTokenSet(th))
				frame.SetContent(body)
				host := ui.NewHost(th, frame)
				body.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
				host.Resize(size.W, size.H)
				host.Layout()
				buf := surface.NewCellBuffer(size.W, size.H)
				host.Paint(buf)
				// A real WindowFrame has a bottom border, footer buttons, rule,
				// then the catalog actions with no unused rows in between.
				y := size.H - 4
				row := settingsRow(buf, y, size.W)
				if !strings.Contains(row, "[ ") || !body.helpRect.Empty() {
					t.Fatalf("catalog actions are not adjacent to footer rule:\n%s", bufferPlain(buf))
				}
				if !strings.Contains(settingsRow(buf, y+2, size.W), "[ Close ]") {
					t.Fatal("catalog moved or obscured the shared footer")
				}
				// Exercise an enabled painted action, not layout alone.
				index := strings.Index(row, "[ ")
				if category == catSources { // Reset inherited is disabled in app scope.
					index = strings.LastIndex(row, "[ ")
				}
				x := utf8.RuneCountInString(row[:index]) + 2
				if result := host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); !result.Handled {
					t.Fatal("painted catalog action has no matching hit target")
				}
			})
		}
	}
}

func TestCapabilityDetailToOtherCatalogDropsHelpRowsOnFirstLayout(t *testing.T) {
	for _, category := range []int{catMCP, catSources} {
		model, body, host := capabilityFixture(t, "skill")
		body.capCatalog.id, body.capCatalog.detail = "vendor:guide", true
		body.capCatalog.detailForm("overview")
		paintCapabilityStore(host, 130, 36)
		model.SelectCategory(category)
		buffer := paintCapabilityStore(host, 130, 36)
		if !body.helpRect.Empty() || !strings.Contains(settingsRow(buffer, 33, 130), "[ Refresh ]") {
			t.Fatalf("detail -> catalog %d keeps stale form-help reservations:\n%s", category, bufferPlain(buffer))
		}
	}
}
