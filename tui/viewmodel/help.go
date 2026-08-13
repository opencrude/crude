package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/input"
	helpscreen "crude/tui/screens/help"
	"crude/tui/ui"
	"crude/tui/wm"
)

func (m *App) toggleHelp() (tea.Model, tea.Cmd) {
	if m.overlayHost != nil && m.overlayHost.IsOpen(ui.OverlayHelp) {
		m.closeHelp()
		return m, nil
	}
	m.openHelp()
	return m, nil
}

func (m *App) openHelp() {
	if m.overlayHost == nil {
		return
	}
	m.overlayHost.Close(ui.OverlayHelp)

	if m.helpScreen == nil {
		m.helpScreen = helpscreen.New(m.Theme)
	}
	m.helpScreen.Open(m.helpCommands(), m.helpShortcuts())

	w, h := fitFloatingSize(m.width, m.height, 78, 24, 48, 14)
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "help-frame"),
		ui.FrameDialog,
		"Help",
		m.helpScreen,
		nil,
		true,
		w,
		h,
	)
	m.helpScreen.SetFocusHandler(frame.Focus)
	m.helpScreen.FocusSearch()

	fw := wm.NewFloatingWindow(string(ui.OverlayHelp), "Help", frame, w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithClosable(true),
		wm.WithResizable(true),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithMinSize(minInt(48, w), minInt(14, h)),
		wm.WithMaxSize(maxInt(w, m.width-2), maxInt(h, m.height-2)),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	screen := m.helpScreen
	m.overlayHost.Open(ui.OverlayHelp, fw, ui.ModalDialogPolicy(), func() {
		if screen != nil {
			screen.Close()
		}
		m.helpScreen = nil
	})
}

func (m *App) closeHelp() {
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayHelp)
	}
}

func (m *App) helpCommands() []helpscreen.Command {
	src := input.AvailableCmds()
	out := make([]helpscreen.Command, 0, len(src))
	for _, c := range src {
		out = append(out, helpscreen.Command{Name: c.Name, Label: c.Label, Desc: c.Desc})
	}
	return out
}

func (m *App) helpShortcuts() []helpscreen.Shortcut {
	if m.actionRegistry == nil {
		return nil
	}
	all := m.actionRegistry.All()
	out := make([]helpscreen.Shortcut, 0, len(all))
	for _, a := range all {
		hint := ui.FormatShortcut(m.actionRegistry.HintFor(a.ID))
		if hint == "" || a.DisplayOnly {
			continue
		}
		out = append(out, helpscreen.Shortcut{
			Category:    a.Category,
			Keys:        hint,
			Label:       a.Label,
			Description: a.Description,
		})
	}
	return out
}
