package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/ui/popup"
	"crude/tui/wm"
)

func (m *App) openFindReplace(replaceMode bool) {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return
	}
	if m.FindReplace == nil {
		m.FindReplace = &popup.FindReplaceState{}
	}
	m.FindReplace.ReplaceMode = replaceMode
	// Pull current search state from the editor so the popup opens
	// with the existing query if the user already had one typed.
	popup.FindReplaceFromEditor(m.FindReplace, m.EditorState.Editor)
	m.FindReplace.FocusedField = 0
	// Move the find input cursor to the end of the query (the
	// standard "open the popup, ready to append" UX). The replace
	// input's cursor is wherever it was last left.

	// Wire callbacks to mutate the editor.
	fr := m.FindReplace
	fr.OnChange = func() {
		popup.FindReplaceToEditor(fr, m.EditorState.Editor)
		fr.MatchCount = len(m.EditorState.Editor.Search().Matches)
		fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
		m.refreshFindReplacePopup()
	}
	fr.OnNext = func() {
		if n, ok := m.EditorState.Editor.NextMatch(); ok {
			fr.MatchCount = n
			fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnPrev = func() {
		if n, ok := m.EditorState.Editor.PrevMatch(); ok {
			fr.MatchCount = n
			fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnReplace = func() {
		if n := m.EditorState.Editor.ReplaceCurrent(); n >= 0 {
			fr.MatchCount = n
			fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnReplaceAll = func() {
		count := m.EditorState.Editor.ReplaceAll()
		m.Feedback.NotifyInfo(fmt.Sprintf("Replaced %d occurrences", count))
		fr.MatchCount = len(m.EditorState.Editor.Search().Matches)
		fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
		m.refreshFindReplacePopup()
	}
	fr.OnClose = func() {
		m.closeFindReplace()
	}

	m.refreshFindReplacePopup()
}

// closeFindReplaceForTab closes the find/replace popover when the active
// editor view actually moved away from its owner. The popover is bound to
// the editor tab that summoned it: its OnChange/OnNext/OnReplace closures
// capture that tab's Editor at open time, so leaving it mounted across a
// switch would keep mutating the old tab's buffer (and drop its highlights)
// while the user looks at the new one. Query and filter options live in
// each editor's own SearchState and are restored on re-open, so closing
// here loses no state.
//
// No-op unless the view genuinely changed (prev != next): steady-state
// re-derivations (tick/agent syncs, same-tab opens) must never disturb an
// open popup. Callers pass prev as the ActiveView before re-derivation and
// next as the re-derived view ("" when no editor tab is active).
func (m *App) closeFindReplaceForTab(prev, next string) {
	if m.FindReplace == nil {
		return
	}
	if prev != "" && prev == next {
		return
	}
	m.closeFindReplace()
}

// closeFindReplace closes the find/replace popup. The editor's
// search state is preserved (so re-opening shows the same query),
// but highlights stop rendering because Open is set to false on the
// editor's SearchState.
func (m *App) closeFindReplace() {
	m.overlayHost.Close(ui.OverlayFindReplace)
}

// findReplaceSpec assembles the frame build/sync spec from live popup state
// plus the owning editor's per-tab title (BaseTitle, never the dirty dot).
func (m *App) findReplaceSpec(fr *popup.FindReplaceState, w, h int) ui.FindReplaceFrameSpec {
	title := "Find"
	if m.EditorState.Editor != nil {
		title = m.EditorState.Editor.BaseTitle()
	}
	if fr.ReplaceMode {
		title = "Find & Replace — " + title
	}
	return ui.FindReplaceFrameSpec{
		Title:         title,
		FindValue:     fr.Query(),
		ReplaceValue:  fr.Replace(),
		ReplaceMode:   fr.ReplaceMode,
		FocusedField:  fr.FocusedField,
		Width:         w,
		Height:        h,
		CaseSensitive: fr.CaseSensitive,
		WholeWord:     fr.WholeWord,
		Regex:         fr.Regex,
		MatchCount:    fr.MatchCount,
		CurrentIdx:    fr.CurrentIdx,
	}
}

// syncFindReplaceFrame pushes text values, filter toggles, the match counter,
// and the per-tab title into the live frame, marking content dirty so the
// change reaches the compositor.
func (m *App) syncFindReplaceFrame(fw *wm.FloatingWindow) {
	if fw == nil || m.FindReplace == nil {
		return
	}
	fr := m.FindReplace
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil {
		return
	}
	m.syncNewFindReplaceFrame(cw, fr)
	spec := m.findReplaceSpec(fr, 0, 0)
	ui.SyncFindReplaceFrame(cw, spec.CaseSensitive, spec.WholeWord, spec.Regex, spec.MatchCount, spec.CurrentIdx, spec.Title)
}

// refreshFindReplacePopup rebuilds the floating window's content
// using a WindowFrame popover. Called when the user types, toggles,
// navigates, or transforms between find and replace forms. The transform
// path always REBUILDS on an existing window (never sync-patches a foreign
// tree): RefreshReplacePopupHeight changes the content height, so the width
// clamp, the action row, and the replace field are all re-derived from the
// live spec. SetContent itself marks content dirty; Host relayout on the
// next render derives fresh geometry from the new tree.
func (m *App) refreshFindReplacePopup() {
	if m.windowManager == nil || m.FindReplace == nil {
		return
	}
	fr := m.FindReplace
	w := popup.FindReplacePopupWidth(fr)
	h := popup.FindReplacePopupHeight(fr)

	if fw := m.windowManager.Window("find-replace"); fw != nil {
		// Transform-safe rebuild: measure the fresh tree for its own size
		// (never trust the stale window geometry: Ctrl+H adds a row, Ctrl+F
		// removes one, and the popup sizer only estimates). The window keeps
		// position/focus; content swaps and the window adopts the measured
		// tree size. Both forms measure the SAME width, so a transform can
		// never push an edge off screen; the clamp below is the safety net.
		frame := ui.BuildFindReplaceFrame(m.Theme, m.findReplaceSpec(fr, w, h))
		m.syncNewFindReplaceFrame(frame, fr)
		if mw, mh := frame.Measure(w, 24); mh > 0 {
			w, h = mw, mh
		}
		fw.SetContent(frame)
		fw.SetSize(w, h)
		m.clampFindReplaceOnScreen(fw)
		fw.MarkContentDirty()
		// New tree = new hit/layout registry. The next Render relayouts,
		// but pointer routing between refresh and paint must already
		// resolve against the fresh geometry.
		if host := frame.Host(); host != nil {
			host.Resize(w, h)
			host.Layout()
		}
		return
	}

	// First time: create and position above the active editor. Measure the
	// fresh tree for its own size (same transform-truthful path as above).
	layout := m.computeLayout()
	frame := ui.BuildFindReplaceFrame(m.Theme, m.findReplaceSpec(fr, w, h))
	m.syncNewFindReplaceFrame(frame, fr)
	if mw, mh := frame.Measure(w, 24); mh > 0 {
		w, h = mw, mh
	}
	fw := wm.NewFloatingWindow(string(ui.OverlayFindReplace), "", frame, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(true),
		wm.WithResizable(false),
		wm.WithClosable(true),
		wm.WithModal(false),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	x := layout.SidebarW + m.EditorState.Editor.PadL + (layout.EditorW - w) - 2
	if x < layout.SidebarW {
		x = layout.SidebarW + 2
	}
	y := layout.ChatAreaTop + 1
	fw.SetPosition(x, y)
	m.clampFindReplaceOnScreen(fw)
	m.overlayHost.Open(ui.OverlayFindReplace, fw, ui.DefaultOverlayPolicy(), func() {
		if m.EditorState.Editor != nil {
			m.EditorState.Editor.SetSearchOpen(false)
		}
		m.FindReplace = nil
	})
}

// clampFindReplaceOnScreen pulls the popup fully inside the terminal: the
// window may grow (long query) or the terminal may shrink, and a right edge
// past the screen makes the close button and the buttons row unreachable.
// The popup never grows taller than the content area, so only X needs
// clamping in practice; Y is clamped too for safety on tiny terminals.
func (m *App) clampFindReplaceOnScreen(fw *wm.FloatingWindow) {
	if fw == nil || m.width <= 0 {
		return
	}
	w, h := fw.Size()
	x, y := fw.Position()
	if x+w > m.width {
		x = m.width - w
	}
	if x < 0 {
		x = 0
	}
	if y+h > m.height {
		y = m.height - h
	}
	if y < 0 {
		y = 0
	}
	fw.SetPosition(x, y)
}

// syncNewFindReplaceFrame binds a freshly built frame's TextFields to the
// popup state's TextEdits, so the painted fields ARE the find/replace text
// (value, cursor, selection, undo) rather than a mirrored copy.
func (m *App) syncNewFindReplaceFrame(frame *ui.ComponentWidget, fr *popup.FindReplaceState) {
	if find := ui.FindReplaceFrameFindField(frame); find != nil {
		find.BindEdit(fr.FindEdit())
	}
	if fr.ReplaceMode {
		if replace := ui.FindReplaceFrameReplaceField(frame); replace != nil {
			replace.BindEdit(fr.ReplaceEdit())
		}
	}
}

// findReplaceField returns the live frame's TextField bound to the focused
// field's TextEdit, or nil when the popup is not mounted.
func (m *App) findReplaceField(fr *popup.FindReplaceState, edit *ui.TextEdit) (*wm.FloatingWindow, *ui.TextField) {
	if m.windowManager == nil || fr == nil || edit == nil {
		return nil, nil
	}
	fw := m.windowManager.Window(string(ui.OverlayFindReplace))
	if fw == nil {
		return nil, nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil {
		return nil, nil
	}
	for _, field := range []*ui.TextField{ui.FindReplaceFrameFindField(cw), ui.FindReplaceFrameReplaceField(cw)} {
		if field != nil && field.Edit() == edit {
			return fw, field
		}
	}
	return nil, nil
}

// findReplaceText applies a key or paste to one find/replace field through
// the shared TextField contract (editing, selection, undo/redo, Ctrl+C/X/V,
// bracketed paste) and re-derives matches when the text changed. Ctrl+V's
// clipboard text re-enters here for the initiating field even if focus has
// moved to the other field, and is dropped once that popup closed.
func (m *App) findReplaceText(fr *popup.FindReplaceState, edit *ui.TextEdit, msg tea.Msg) tea.Cmd {
	fw, field := m.findReplaceField(fr, edit)
	if field == nil || m.FindReplace != fr {
		return nil
	}
	deliver := func(content string) tea.Cmd {
		return m.findReplaceText(fr, edit, tea.PasteMsg{Content: content})
	}
	changed, cmd := m.routeOverlayText(fw, field, msg, deliver)
	if changed && fr.OnChange != nil {
		fr.OnChange()
	}
	return cmd
}

// pasteFindReplace routes a bracketed paste to the focused find/replace field.
func (m *App) pasteFindReplace(msg tea.PasteMsg) tea.Cmd {
	if m.FindReplace == nil {
		return nil
	}
	return m.findReplaceText(m.FindReplace, m.FindReplace.ActiveEdit(), msg)
}

// handleFindReplaceKey routes keypresses when the find/replace popup is
// focused. Popup-level keys (Esc, Tab, Enter, F3, the previous-match and
// replace-all chords, Alt+C/W/R, Ctrl+F/H) are handled here; everything else
// edits the focused field through the shared TextField contract.
func (m *App) handleFindReplaceKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.FindReplace == nil {
		return nil
	}
	fr := m.FindReplace

	// Modified Enter chords first: the plain-Enter case below would swallow
	// Shift+Enter and Ctrl+Alt+Enter otherwise.
	if ui.IsReplaceAllChord(msg) {
		if fr.ReplaceMode && fr.OnReplaceAll != nil {
			fr.OnReplaceAll()
		}
		return nil
	}
	if ui.IsPreviousMatchChord(msg) {
		if fr.OnPrev != nil {
			fr.OnPrev()
		}
		return nil
	}
	switch msg.String() {
	case "f3":
		if fr.OnNext != nil {
			fr.OnNext()
		}
		return nil
	case "alt+c":
		fr.CaseSensitive = !fr.CaseSensitive
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "alt+w":
		fr.WholeWord = !fr.WholeWord
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "alt+r":
		fr.Regex = !fr.Regex
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "ctrl+f":
		// Mode transform while the popup owns focus: Ctrl+F morphs an open
		// Replace form back to Find and Ctrl+H morphs Find into Replace, rebuilt
		// in place in the same window (no close/reopen flicker).
		if fr.ReplaceMode {
			m.openFindReplace(false)
		}
		return nil
	case "ctrl+h":
		if !fr.ReplaceMode {
			m.openFindReplace(true)
		}
		return nil
	}

	switch msg.Code {
	case tea.KeyEsc:
		// The field drops an active selection first; Esc then closes.
		if fr.ActiveEdit().HasSelection() {
			return m.findReplaceText(fr, fr.ActiveEdit(), msg)
		}
		m.closeFindReplace()
		return nil
	case tea.KeyTab:
		if msg.Mod.Contains(tea.ModShift) {
			fr.FocusedField = 0
		} else {
			fr.FocusedField = 1 - fr.FocusedField
		}
		m.refreshFindReplacePopup()
		return nil
	case tea.KeyEnter:
		if fr.FocusedField == 1 && fr.ReplaceMode {
			if fr.OnReplace != nil {
				fr.OnReplace()
			}
			return nil
		}
		if fr.OnNext != nil {
			fr.OnNext()
		}
		return nil
	}
	return m.findReplaceText(fr, fr.ActiveEdit(), msg)
}

// syncFindReplaceFrameFromState copies the full FindReplaceState (TextEdit
// values, filter toggles, match counter, per-tab title) into the live frame
// for paint. It is the full-sync path used after every keystroke/toggle.
func (m *App) syncFindReplaceFrameFromState() {
	if m.windowManager == nil || m.FindReplace == nil {
		return
	}
	m.syncFindReplaceFrame(m.windowManager.Window("find-replace"))
}
