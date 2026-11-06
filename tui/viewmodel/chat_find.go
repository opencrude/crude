package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellchat"
	"crude/tui/ui"
	"crude/tui/ui/popup"
	"crude/tui/wm"
)

// Ctrl+F has one window design: the frame the editor's find uses, with the
// same filter pills, counter and Up/Down buttons. A chat is another search
// domain, not another kind of find bar.

// findDomain names the surface the open find window searches.
type findDomain uint8

const (
	findInEditor findDomain = iota
	findInChat
)

// chatFindChord reports whether msg opens find in the active chat.
func (m *App) chatFindChord(msg tea.KeyPressMsg) bool {
	if m.actionRegistry == nil || m.chatScreen == nil || m.rootHost == nil || m.chatRect.Empty() || m.AgentView.ActiveNode() == nil {
		return false
	}
	match := m.actionRegistry.MatchContext("chat", msg)
	return match != nil && match.ID == "chat.find"
}

// openChatFind shows the find window over the active chat's transcript, at the
// same top-right anchor the editor's window uses over its buffer.
func (m *App) openChatFind() {
	screen := m.chatScreen
	if screen == nil || m.rootHost == nil || m.chatRect.Empty() || m.windowManager == nil {
		return
	}
	inside := m.findDomain == findInChat && m.findChat == screen &&
		m.focusedPopup() != nil && m.focusedPopup().ID == string(ui.OverlayFindReplace)
	fr := m.FindReplace
	if !inside {
		if fr == nil || m.findDomain != findInChat {
			fr = &popup.FindReplaceState{CurrentIdx: -1}
			m.FindReplace = fr
		}
		m.findDomain, m.findChat = findInChat, screen
		screen.OpenFind()
		screen.SetFindChanged(func() { m.syncChatFindCounter() })
	}
	fr.ReplaceMode, fr.FocusedField = false, 0
	fr.OnChange = func() {
		if m.FindReplace != fr || m.findChat != screen {
			return
		}
		m.applyChatFindQuery(fr)
	}
	fr.OnNext = func() {
		if m.FindReplace != fr || m.findChat != screen {
			return
		}
		screen.StepFind(1)
		m.syncChatFindCounter()
	}
	fr.OnPrev = func() {
		if m.FindReplace != fr || m.findChat != screen {
			return
		}
		screen.StepFind(-1)
		m.syncChatFindCounter()
	}
	fr.OnReplace, fr.OnReplaceAll = nil, nil
	fr.OnClose = func() { m.closeFindReplace() }
	if !inside {
		fr.FindEdit().SelectAll()
	}
	m.applyChatFindQuery(fr)
	if fw := m.windowManager.Window(string(ui.OverlayFindReplace)); fw != nil {
		m.windowManager.FocusWindow(fw.ID)
	}
}

// applyChatFindQuery pushes the window's query and filters into the chat and
// lets the window show the resulting count.
func (m *App) applyChatFindQuery(fr *popup.FindReplaceState) {
	if m.findChat == nil || m.FindReplace != fr {
		return
	}
	m.findChat.SetFindQuery(fr.Query(), shellchat.FindOptions{
		CaseSensitive: fr.CaseSensitive,
		WholeWord:     fr.WholeWord,
		Regex:         fr.Regex,
	})
	m.refreshChatFindPopup()
}

// syncChatFindCounter keeps the window's counter current when the count or the
// selected match changes without the query changing.
func (m *App) syncChatFindCounter() {
	if m.FindReplace == nil || m.findDomain != findInChat || m.findChat == nil {
		return
	}
	m.refreshChatFindPopup()
}

// chatFindSpec is the window's state: the searched surface's name as the title,
// the chat's live count and filters, and its own counter text while a pass is
// still running.
func (m *App) chatFindSpec(fr *popup.FindReplaceState, w int) ui.FindReplaceFrameSpec {
	count, current := m.findChat.FindCounts()
	title := "Chat"
	if node := m.ws.AgentMgr().Get(m.findChat.AgentID()); node != nil {
		if label := m.agentLabel(node); label != "" {
			title = label
		}
	}
	return ui.FindReplaceFrameSpec{
		Title:         title,
		FindValue:     fr.Query(),
		Width:         w,
		FocusedField:  0,
		CaseSensitive: fr.CaseSensitive,
		WholeWord:     fr.WholeWord,
		Regex:         fr.Regex,
		MatchCount:    count,
		CurrentIdx:    current,
		Error:         m.findChat.FindError(),
		StatusText:    m.findChat.FindStatusText(),
	}
}

// refreshChatFindPopup rebuilds (or creates) the find window for the chat. The
// frame is rebuilt from live state on every change, exactly as the editor's
// window is, so the pills, counter and title can never drift.
func (m *App) refreshChatFindPopup() {
	fr := m.FindReplace
	if fr == nil || m.findChat == nil || m.windowManager == nil || !m.findChat.FindOpen() {
		return
	}
	w := popup.FindReplacePopupWidth(fr)
	frame := ui.BuildFindReplaceFrame(m.Theme, m.chatFindSpec(fr, w))
	m.syncNewFindReplaceFrame(frame, fr)
	if mw, mh := frame.Measure(w, 24); mh > 0 {
		w = max(w, mw)
	}
	if fw := m.windowManager.Window(string(ui.OverlayFindReplace)); fw != nil {
		h := popup.FindReplacePopupHeight(fr)
		fw.SetContent(frame)
		fw.SetSize(w, h)
		m.clampFindReplaceOnScreen(fw)
		fw.MarkContentDirty()
		if host := frame.Host(); host != nil {
			host.Resize(w, h)
			host.Layout()
		}
		return
	}
	h := popup.FindReplacePopupHeight(fr)
	fw := wm.NewFloatingWindow(string(ui.OverlayFindReplace), "", frame, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(true),
		wm.WithResizable(false),
		wm.WithClosable(true),
		wm.WithModal(false),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	// Top-right of the transcript, one row below its first line: the editor's
	// window sits in the same place over the buffer it searches.
	x := m.chatRect.X + m.chatRect.W - w - 2
	y := m.chatRect.Y + 1
	fw.SetPosition(x, y)
	m.clampFindReplaceOnScreen(fw)
	screen := m.findChat
	m.overlayHost.Open(ui.OverlayFindReplace, fw, ui.DefaultOverlayPolicy(), func() {
		// Detach first: closing the search must not look like new results to
		// the owner and mount the window it is being closed with.
		screen.SetFindChanged(nil)
		screen.CloseFind()
		if m.findChat == screen {
			m.findChat = nil
		}
		if m.FindReplace == fr {
			m.FindReplace = nil
		}
		if m.findDomain == findInChat {
			m.findDomain = findInEditor
		}
	})
}

// closeChatFind drops the chat search. Closing the window runs the same cleanup
// through its dispose callback, so both paths end in one place.
func (m *App) closeChatFind() {
	if m.findDomain != findInChat {
		return
	}
	m.closeFindReplace()
}

// chatFindPending reports whether a visible chat still counts matches, so the
// shell keeps frames coming until the count is final.
func (m *App) chatFindPending() bool {
	return m.findChat != nil && m.findChat.FindPending()
}
