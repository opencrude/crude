package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	settingsui "crude/tui/settings"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
	captypes "crude/types/capability"
)

func selectSettingsCategory(t *testing.T, body *settingsui.WorkspaceSettingsSurface, label string) {
	t.Helper()
	for _, item := range body.Model().CategoryItems() {
		if item.Label == label {
			body.Model().SelectCategory(item.Payload.(int))
			body.SetModel(body.Model())
			return
		}
	}
	t.Fatalf("missing Settings category %q", label)
}

func TestSettingsCloseXFromDeepPages(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 70, H: 18}} {
		for _, page := range []string{"preferences", "account", "new account", "agent", "skill detail", "MCP detail", "MCP editor", "source editor"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size.W, size.H, page), func(t *testing.T) {
				app, _, _ := newSettingsPromptApp(t)
				app.width, app.height = size.W, size.H
				app.openApplicationSettings()
				parent := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
				body := app.settingsSurface(parent)
				switch page {
				case "preferences":
					selectSettingsCategory(t, body, "Appearance")
				case "account", "new account":
					body.ShowAccounts()
					action := "settings.account.edit"
					if page == "new account" {
						action = "settings.account.begin-add"
					}
					body.HandleAction(ui.EventContext{}, ui.Action{ID: action})
				case "agent":
					body.ShowMainAgent()
				default:
					body.Model().SetCapabilitySnapshot(captypes.Snapshot{Generation: 1, Settings: captypes.Settings{
						Skills:      map[string]captypes.SkillConfig{"local:guide": {ID: "local:guide", Name: "Guide", Mode: "manual"}},
						Connections: map[string]captypes.ConnectionConfig{"docs": {ID: "docs", Name: "Docs", Transport: "http", URL: "https://example.test/mcp"}},
						Sources:     map[string]captypes.SourceConfig{"local": {ID: "local", Name: "Local", Kind: "local", Location: "/fixture"}},
					}})
					kind, id := captypes.KindSkill, "local:guide"
					if strings.HasPrefix(page, "MCP") {
						kind, id = captypes.KindMCP, "docs"
					}
					body.OpenCapabilities(kind, "", id)
					switch page {
					case "MCP editor":
						body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.capability.edit"})
					case "source editor":
						body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.capability.sources"})
						body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.capability.inspect"})
					}
				}
				frame := parent.Content.(*ui.WindowFrameWidget)
				w, h := parent.Size()
				frame.Host().MarkLayout()
				frame.Host().Layout()
				buf := surface.NewCellBuffer(w, h)
				frame.Host().Paint(buf)
				if painted := bufferPlain(buf); !strings.Contains(painted, body.PaneTitle()) {
					t.Fatalf("deep Settings page was not painted:\n%s", painted)
				}
				title := body.PaneTitle()
				x, y := parent.Position()
				_, cmd := app.Update(tea.MouseClickMsg{X: x + w - 2, Y: y, Button: tea.MouseLeft})
				if cmd == nil {
					t.Fatalf("× navigated back from %q instead of closing Settings", title)
				}
				msg := cmd()
				if _, ok := msg.(settingsui.CancelMsg); !ok {
					t.Fatalf("clean × returned %T instead of closing Settings", msg)
				}
				if body.PaneTitle() != title {
					t.Fatal("close changed the Settings page before closing the window")
				}
				_, _ = app.Update(msg)
				if app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) || app.windowManager.Window(parent.ID) != nil || app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) {
					t.Fatal("× left a Settings window or prompt mounted")
				}
			})
		}
	}
}

func TestSettingsCloseFooterFromCapabilityDetail(t *testing.T) {
	app, parent, body := newSettingsPromptApp(t)
	body.OpenCapabilities(captypes.KindMCP, "", "docs")
	col, row := settingsTextCell(t, parent, "[ Close ]")
	x, y := parent.Position()
	_, cmd := app.Update(tea.MouseClickMsg{X: x + col + 3, Y: y + row, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("footer close navigated back instead of closing Settings")
	}
	msg := cmd()
	if _, ok := msg.(settingsui.CancelMsg); !ok {
		t.Fatalf("footer close returned %T, want CancelMsg", msg)
	}
	_, _ = app.Update(msg)
	if app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("footer close left Settings open")
	}
}

// Locate ASCII labels in the actual painted frame, not guessed hit geometry.
func settingsTextCell(t *testing.T, parent *wm.FloatingWindow, label string) (int, int) {
	t.Helper()
	frame := parent.Content.(*ui.WindowFrameWidget)
	frame.Host().MarkLayout()
	frame.Host().Layout()
	w, h := parent.Size()
	buf := surface.NewCellBuffer(w, h)
	frame.Host().Paint(buf)
	for y := 0; y < h; y++ {
		for x := 0; x <= w-len(label); x++ {
			match := true
			for i, char := range label {
				if buf.Get(x+i, y).Char != char {
					match = false
					break
				}
			}
			if match {
				return x, y
			}
		}
	}
	t.Fatalf("Settings frame did not paint %q:\n%s", label, bufferPlain(buf))
	return 0, 0
}

func TestSettingsBreadcrumbStillReturnsToParent(t *testing.T) {
	app, parent, body := newSettingsPromptApp(t)
	body.ShowAccounts()
	body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	col, row := settingsTextCell(t, parent, "Accounts")
	x, y := parent.Position()
	_, cmd := app.Update(tea.MouseClickMsg{X: x + col, Y: y + row, Button: tea.MouseLeft})
	if cmd != nil || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || strings.Contains(body.PaneTitle(), "Edit account") {
		t.Fatal("breadcrumb closed Settings or failed to return to the Accounts catalog")
	}
	settingsTextCell(t, parent, "Search accounts")
}

func TestSettingsCloseXDiscardsAllDraftsAndCloses(t *testing.T) {
	for _, page := range []string{"account", "agent"} {
		t.Run(page, func(t *testing.T) {
			app, parent, body := newSettingsPromptApp(t)
			selectSettingsCategory(t, body, "Appearance")
			body.Model().SetValueForTest(body.Model().SelectedCategory(), "preset", "light")
			switch page {
			case "account":
				body.ShowAccounts()
				body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
				body.Model().SetValueForTest(body.Model().SelectedCategory(), "account-label", "Unsaved edit")
			case "agent":
				body.ShowMainAgent()
				body.Model().SetValueForTest(body.Model().SelectedCategory(), "agent-instructions", "Unsaved edit")
			}
			title := body.PaneTitle()
			x, y := parent.Position()
			w, _ := parent.Size()
			click := tea.MouseClickMsg{X: x + w - 2, Y: y, Button: tea.MouseLeft}
			_, cmd := app.Update(click)
			if cmd == nil {
				t.Fatal("dirty × did not request a close decision")
			}
			prompt, ok := cmd().(settingsui.PromptMsg)
			if !ok || prompt.Kind != settingsui.PromptDiscardSettings {
				t.Fatal("dirty × did not cover all Settings drafts")
			}
			_, _ = app.Update(prompt)
			_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || body.PaneTitle() != title || !body.PromptRelevant(prompt) {
				t.Fatal("Keep settings open lost the draft or navigated away")
			}
			_, cmd = app.Update(click)
			_, _ = app.Update(cmd())
			_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
			_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil || body.PaneTitle() != title {
				t.Fatal("confirmed × discard navigated back instead of closing")
			}
			msg := cmd()
			if _, ok := msg.(settingsui.CancelMsg); !ok {
				t.Fatalf("confirmed close returned %T", msg)
			}
			_, _ = app.Update(msg)
			if app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) || app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) {
				t.Fatal("confirmed close left Settings or its prompt open")
			}
			app.openApplicationSettings()
			reopened := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings)).Model().AppSettings()
			if reopened.Theme.Preset == "light" || reopened.Accounts["work"].Label == "Unsaved edit" || reopened.Agents.Categories["default"].Instructions == "Unsaved edit" {
				t.Fatal("closing Settings persisted unsaved drafts")
			}
		})
	}
}

func TestSettingsCloseXWorkspaceDeepPage(t *testing.T) {
	app, _, _ := newSettingsPromptApp(t)
	app.openWorkspaceSettings()
	parent := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(parent)
	body.OpenCapabilities(captypes.KindMCP, "", "docs")
	if body.Model().Scope() != types.SettingsScopeWorkspace {
		t.Fatal("fixture changed Settings scope")
	}
	x, y := parent.Position()
	w, _ := parent.Size()
	_, cmd := app.Update(tea.MouseClickMsg{X: x + w - 2, Y: y, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("workspace Settings × navigated back")
	}
	msg := cmd()
	if _, ok := msg.(settingsui.CancelMsg); !ok {
		t.Fatalf("workspace Settings × returned %T", msg)
	}
	_, _ = app.Update(msg)
	if app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("workspace Settings × left the window mounted")
	}
}
