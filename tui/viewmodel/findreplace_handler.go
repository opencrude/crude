package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
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
		m.StatusBar.SetInfo(fmt.Sprintf("Replaced %d occurrences", count))
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
	m.invalidateClipboardFindReplace(m.FindReplace)
	m.overlayHost.Close(ui.OverlayFindReplace)
}

// findReplaceSpec assembles the frame build/sync spec from live popup state
// plus the owning editor's per-tab title (BaseTitle — never the dirty dot).
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
	if find := ui.FindReplaceFrameFindField(cw); find != nil {
		find.SyncEditFrom(fr.FindEdit())
	}
	if fr.ReplaceMode {
		if replace := ui.FindReplaceFrameReplaceField(cw); replace != nil {
			replace.SyncEditFrom(fr.ReplaceEdit())
		}
	}
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
		// (never trust the stale window geometry — Ctrl+H adds a row, Ctrl+F
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
		m.invalidateClipboardFindReplace(fr)
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

// syncNewFindReplaceFrame primes a freshly built frame's TextFields with the
// TextEdit state (values + cursors) before the first paint.
func (m *App) syncNewFindReplaceFrame(frame *ui.ComponentWidget, fr *popup.FindReplaceState) {
	if find := ui.FindReplaceFrameFindField(frame); find != nil {
		find.SyncEditFrom(fr.FindEdit())
	}
	if fr.ReplaceMode {
		if replace := ui.FindReplaceFrameReplaceField(frame); replace != nil {
			replace.SyncEditFrom(fr.ReplaceEdit())
		}
	}
}

// pasteIntoFindReplace captures the exact popup and TextEdit owner before the
// asynchronous clipboard read starts. The result handler performs the edit.
func (m *App) pasteIntoFindReplace(fr *popup.FindReplaceState) tea.Cmd {
	if fr == nil {
		return nil
	}
	ed := fr.ActiveEdit()
	if ed == nil {
		return nil
	}
	return m.beginFindReplaceClipboardPaste(fr, ed)
}

// handleFindReplaceKey routes keypresses when the find/replace popup
// is focused. Text-editing keys are delegated to ui.TextEdit via
// HandleTextEditKey. Popup-level keys (Esc, Tab, Enter, Alt+C/W/R,
// Ctrl+Alt+Enter, Shift+Enter, Ctrl+C/V/X/A) are handled here.
func (m *App) handleFindReplaceKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.FindReplace == nil {
		return nil
	}
	fr := m.FindReplace

	// Navigation/modifier keys first.
	switch msg.Code {
	case tea.KeyEsc:
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
			// Replace the current match.
			if fr.OnReplace != nil {
				fr.OnReplace()
			}
			return nil
		}
		// Default: next match.
		if fr.OnNext != nil {
			fr.OnNext()
		}
		return nil
	}

	// Toggle shortcuts.
	switch msg.String() {
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
	case "ctrl+alt+enter":
		if fr.OnReplaceAll != nil {
			fr.OnReplaceAll()
		}
		return nil
	case "shift+enter":
		if fr.OnPrev != nil {
			fr.OnPrev()
		}
		return nil
	}

	// Mode transform while the popup owns focus: Ctrl+H morphs the open
	// Find popup into the Replace form, Ctrl+F morphs an open Replace form
	// back to Find — the same window, rebuilt in place (smooth transform,
	// no close/reopen flicker). openFindReplace pulls the current query
	// and filter state from the owning editor and rebuilds the window with
	// the new form height.
	switch msg.String() {
	case "ctrl+f":
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

	// MS Edit shortcuts.
	switch msg.String() {
	case "ctrl+c":
		ed := fr.ActiveEdit()
		if ed == nil {
			return nil
		}
		var text string
		if ed.HasSelection() {
			text = ed.SelectedText()
		} else {
			text = ed.Value()
		}
		if text != "" {
			return rendering.CopyToClipboardCmd(text)
		}
		return nil
	case "ctrl+x":
		ed := fr.ActiveEdit()
		if ed == nil {
			return nil
		}
		var text string
		if ed.HasSelection() {
			text = ed.SelectedText()
			ed.Backspace()
		} else {
			text = ed.Value()
			ed.SetValue("")
		}
		// Cut always mutates (selection delete, or whole-value clear), so
		// recompute matches; OnChange rebuilds + resyncs in one path.
		if fr.OnChange != nil {
			fr.OnChange()
		}
		if text != "" {
			return rendering.CopyToClipboardCmd(text)
		}
		return nil
	case "ctrl+a":
		ed := fr.ActiveEdit()
		if ed != nil {
			ed.SelectAll()
		}
		m.syncFindReplaceFrameFromState()
		return nil
	case "ctrl+v":
		return m.pasteIntoFindReplace(fr)
	}

	// All other keys: delegate to the focused field's TextEdit,
	// then resync text + toggles + counter into the frame for paint.
	ed := fr.ActiveEdit()
	if ed != nil {
		oldVal := ed.Value()
		if ui.HandleTextEditKey(ed, msg) {
			if ed.Value() != oldVal {
				if fr.OnChange != nil {
					fr.OnChange()
				}
			} else {
				m.syncFindReplaceFrameFromState()
			}
		}
	}
	return nil
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
