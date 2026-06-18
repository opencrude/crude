package viewmodel

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/components"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/wm"
)



var paletteCommands = []components.ListItem{
	{ID: "composer", Label: "Composer", Detail: "compose a detailed prompt"},
	{ID: "settings", Label: "Settings", Detail: "configure provider, model, theme"},
	{ID: "sessions", Label: "Sessions", Detail: "browse past sessions"},
	{ID: "help", Label: "Help", Detail: "show commands and shortcuts"},
	{ID: "undo", Label: "Undo", Detail: "revert last file change"},
	{ID: "yolo", Label: "YOLO Mode", Detail: "toggle safety checks"},
	{ID: "toggle-sidebar", Label: "Sidebar", Detail: "toggle file tree"},
	{ID: "attach-file", Label: "Attach File", Detail: "add file reference"},
	{ID: "toggle-multiagent", Label: "Multi-Agent", Detail: "toggle sub-agent orchestration"},
	{ID: "orchestrator-settings", Label: "Orchestrator", Detail: "override model/provider"},
	{ID: "reset-orchestrator", Label: "Reset Overrides", Detail: "clear orchestrator overrides"},
	{ID: "quit", Label: "Quit", Detail: "exit the application"},
}

// commandPaletteModel manages the command palette's state for rendering and events.
type commandPaletteModel struct {
	list     components.List
	callback func(cmdID string) tea.Cmd
}

func newCommandPaletteModel(th *theme.Theme, callback func(cmdID string) tea.Cmd) *commandPaletteModel {
	list := components.NewList(th, 10)
	list.SetRenderMode(components.ListRenderPopup)
	list.SetConfig(components.ListConfig{
		ShowFilter: true,
		ShowFooter: false,
		ShowCount:  true,
		Floating:   true,
	})
	list.SetItems(paletteCommands)
	list.FilterMatchFn = func(item components.ListItem, filter string) bool {
		lower := strings.ToLower(filter)
		return strings.Contains(strings.ToLower(item.ID), lower) ||
			strings.Contains(strings.ToLower(item.Label), lower) ||
			strings.Contains(strings.ToLower(item.Detail), lower)
	}
	return &commandPaletteModel{
		list:     list,
		callback: callback,
	}
}

func (m *commandPaletteModel) View(width int) string {
	return m.list.View(width)
}

func (m *commandPaletteModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		updated, cmd := m.list.Update(msg)
		m.list = updated

		switch m.list.Action() {
		case components.ListActionSelect:
			if item, ok := m.list.Selected(); ok {
				if m.callback != nil {
					return m.callback(item.ID)
				}
			}
		case components.ListActionCancel:
			return nil
		}
		return cmd

	case tea.MouseClickMsg:
		updated, cmd := m.list.Update(msg)
		m.list = updated
		if m.list.Action() == components.ListActionSelect {
			if item, ok := m.list.Selected(); ok {
				if m.callback != nil {
					return m.callback(item.ID)
				}
			}
		}
		return cmd
	}
	return nil
}

func (m *App) toggleCommandPalette() (tea.Model, tea.Cmd) {
	if m.windowManager.Window("command-palette") != nil {
		m.windowManager.RemoveWindow("command-palette")
		m.commandPaletteModel = nil
		return m, nil
	}

	model := newCommandPaletteModel(m.theme, func(cmdID string) tea.Cmd {
		m.windowManager.RemoveWindow("command-palette")
		m.commandPaletteModel = nil
		return m.executePaletteCommand(cmdID)
	})

	viewFn := func() string {
		w := m.width
		if fw := m.windowManager.Window("command-palette"); fw != nil {
			b := fw.ContentBounds()
			w = b.W
		}
		return model.View(w - 4)
	}
	ansiWidget := rendering.NewANSIAdapter(viewFn)

	fw := wm.NewFloatingWindow("command-palette", "Command Palette", ansiWidget,
		65, 22,
		wm.WithResizable(false),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.windowManager.AddWindow(fw)

	m.commandPaletteModel = model
	return m, nil
}

func (m *App) executePaletteCommand(cmdID string) tea.Cmd {
	var cmds []tea.Cmd

	switch cmdID {
	case "composer":
		m.toggleComposer()
		return nil

	case "settings":
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)

	case "orchestrator-settings":
		m.openOrchestratorSettings()

	case "reset-orchestrator":
		m.resetOrchestratorOverrides()

	case "sessions":
		if m.anyActive() {
			m.statusbar.SetError("Cannot switch sessions while agent is running")
			return nil
		}
		if m.onListSessions != nil {
			sessions := m.onListSessions()
			if len(sessions) > 0 {
				m.launcher = NewLauncherState(sessions)
				m.lastInteractionTime = time.Now()
			} else {
				m.statusbar.SetInfo("No past sessions found")
			}
		}

	case "help":
		var b strings.Builder
		b.WriteString("Available commands:\n")
		for _, c := range input.AvailableCmds() {
			b.WriteString("  " + "/" + c.Label + " - " + c.Desc + "\n")
		}
		b.WriteString("\nKeyboard Shortcuts:\n")
		b.WriteString("  Ctrl+Shift+P  - Command Palette\n")
		b.WriteString("  Ctrl+P        - Command Palette (fallback)\n")
		b.WriteString("  Alt+C         - Open Composer\n")
		b.WriteString("  Ctrl+B        - Toggle Sidebar\n")
		b.WriteString("  Ctrl+O        - Attach File\n")
		b.WriteString("  Ctrl+S        - Open Settings\n")
		b.WriteString("  Ctrl+Q        - Quit\n")
		b.WriteString("  Esc           - Cancel\n")
		if ml := m.activeMsgList(); ml != nil {
			ml.AppendMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}

	case "undo":
		if m.OnUndoCmd != nil {
			go m.OnUndoCmd("/undo")
		}

	case "yolo":
		if m.yolo {
			m.yolo = false
			m.statusbar.SetInfo("YOLO mode disabled")
			if m.OnYoloCmd != nil {
				go m.OnYoloCmd(false)
			}
		} else {
			m.yoloDlg.Reset()
			m.setMode(ModeYolo)
		}

	case "toggle-sidebar":
		m.ToggleSidebar()

	case "attach-file":
		m.openFilePicker()

	case "toggle-multiagent":
		if m.OnSettingsSave != nil {
			m.settings.SaveSnapshot()
			s := m.settings.ToSettings()
			s.Agents.Enabled = !s.Agents.Enabled
			m.statusbar.SetInfo("Toggling multi-agent mode...")
			onSave := m.OnSettingsSave
			cmds = append(cmds, func() tea.Msg {
				err := onSave(s)
				return ttypes.SettingsSaveResultMsg{Settings: s, Err: err}
			})
		}

	case "quit":
		m.Quit = true
		return tea.Quit
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
