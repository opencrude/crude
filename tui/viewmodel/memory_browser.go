package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/memory"
	memorybrowser "crude/tui/screens/memorybrowser"
	"crude/tui/ui"
	"crude/tui/wm"
)

// toggleMemoryBrowser opens or closes the workspace memory browser overlay.
func (m *App) toggleMemoryBrowser() (tea.Model, tea.Cmd) {
	if m.overlayHost == nil {
		return m, nil
	}
	if m.overlayHost.IsOpen(ui.OverlayMemoryBrowser) {
		m.closeMemoryBrowser()
		return m, nil
	}
	if !m.MemoryAvailable() {
		if m.StatusBar != nil {
			m.StatusBar.SetError("Workspace memory is unavailable")
		}
		return m, nil
	}

	browser := memorybrowser.New(m.Theme)
	browser.SetSearchFn(func(query string) []memory.ScoredRecord {
		return m.SearchMemories(query, 200)
	})
	browser.Open()

	w := max(64, min(m.width*72/100, 120))
	h := max(18, min(m.height*72/100, 40))
	if w > m.width-4 {
		w = max(30, m.width-4)
	}
	if h > m.height-4 {
		h = max(12, m.height-4)
	}
	title := "Workspace Memory"
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, string(ui.OverlayMemoryBrowser)+"-frame"),
		ui.FrameWindow,
		title,
		browser,
		nil,
		true,
		w,
		h,
	)
	fw := wm.NewFloatingWindow(string(ui.OverlayMemoryBrowser), title, frame, w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithResizable(true),
		wm.WithClosable(true),
		wm.WithFocusable(true),
		wm.WithMinSize(min(56, w), min(14, h)),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	browser.SetFocusHandler(frame.Focus)
	browser.FocusSearch()
	m.overlayHost.Open(ui.OverlayMemoryBrowser, fw, ui.DefaultOverlayPolicy(), func() {
		m.memoryBrowser = nil
	})
	m.memoryBrowser = browser
	return m, nil
}

func (m *App) closeMemoryBrowser() {
	if m.overlayHost == nil {
		return
	}
	m.overlayHost.Close(ui.OverlayMemoryBrowser)
}

func (m *App) handleMemoryBrowserPin(msg memorybrowser.PinMsg) {
	if m.memoryBrowser == nil || msg.Target != m.memoryBrowser {
		return
	}
	if err := m.PinMemory(msg.ID, msg.Pinned); err != nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError(err.Error())
		}
		return
	}
	m.memoryBrowser.Refresh()
	m.markMemoryBrowserDirty()
}

func (m *App) handleMemoryBrowserForget(msg memorybrowser.ForgetMsg) {
	if m.memoryBrowser == nil || msg.Target != m.memoryBrowser {
		return
	}
	if err := m.ForgetMemoryRecord(msg.ID); err != nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError(err.Error())
		}
		return
	}
	if m.StatusBar != nil {
		m.StatusBar.SetInfo("Memory forgotten (kept superseded for audit)")
	}
	m.memoryBrowser.Refresh()
	m.markMemoryBrowserDirty()
}

func (m *App) markMemoryBrowserDirty() {
	if m.windowManager == nil {
		return
	}
	if fw := m.windowManager.Window(string(ui.OverlayMemoryBrowser)); fw != nil {
		fw.MarkContentDirty()
	}
}
