package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	sessionpicker "crude/tui/screens/sessionpicker"
	"crude/tui/ui"
	"crude/tui/wm"
)

// openSessionPicker opens the native past-sessions overlay.
// Always allowed: the multi-agent tab model resumes into an additional tab
// (ResumeSessionFromLauncher) without discarding the current agent/session.
func (m *App) openSessionPicker() {
	if m.overlayHost == nil {
		return
	}
	m.overlayHost.Close(ui.OverlaySessionPicker)

	sessions := m.ListSessions()
	if m.sessionPicker == nil {
		m.sessionPicker = sessionpicker.New(m.Theme)
	}
	m.sessionPicker.Open(sessions)

	w, h := fitFloatingSize(m.width, m.height, 78, 22, 48, 12)
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "session-picker-frame"),
		ui.FrameDialog,
		"Sessions",
		m.sessionPicker,
		nil,
		true,
		w,
		h,
	)
	m.sessionPicker.SetFocusHandler(frame.Focus)
	m.sessionPicker.FocusList()

	fw := wm.NewFloatingWindow(string(ui.OverlaySessionPicker), "Sessions", frame, w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithClosable(true),
		wm.WithResizable(true),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithMinSize(minInt(48, w), minInt(12, h)),
		wm.WithMaxSize(maxInt(w, m.width-2), maxInt(h, m.height-2)),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	picker := m.sessionPicker
	m.overlayHost.Open(ui.OverlaySessionPicker, fw, ui.ModalDialogPolicy(), func() {
		if picker != nil {
			picker.Close()
		}
		m.sessionPicker = nil
	})
}

func (m *App) closeSessionPicker() {
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlaySessionPicker)
	}
}

func (m *App) handleSessionPickerSelect(meta sessiondb.SessionMeta) tea.Cmd {
	m.closeSessionPicker()
	if meta.ID == "" {
		return nil
	}
	return m.launcherResumeSession(meta.ID, &meta)
}
