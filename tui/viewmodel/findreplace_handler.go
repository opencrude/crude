package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/tui/widgets"
	"crude/tui/wm"
)

func (m *App) openFindReplace(replaceMode bool) {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return
	}
	if m.FindReplace == nil {
		m.FindReplace = &widgets.FindReplaceState{
			HoveredToggle: -1, // no toggle hovered initially
		}
	}
	m.FindReplace.ReplaceMode = replaceMode
	// Pull current search state from the editor so the popup opens
	// with the existing query if the user already had one typed.
	widgets.FindReplaceFromEditor(m.FindReplace, m.EditorState.Editor)
	m.FindReplace.FocusedField = 0
	// Move the find input cursor to the end of the query (the
	// standard "open the popup, ready to append" UX). The replace
	// input's cursor is wherever it was last left.

	// Wire callbacks to mutate the editor.
	fr := m.FindReplace
	fr.OnChange = func() {
		widgets.FindReplaceToEditor(fr, m.EditorState.Editor)
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

// closeFindReplace closes the find/replace popup. The editor's
// search state is preserved (so re-opening shows the same query),
// but highlights stop rendering because Open is set to false on the
// editor's SearchState.
func (m *App) closeFindReplace() {
	m.overlayHost.Close(ui.OverlayFindReplace)
}

// refreshFindReplacePopup rebuilds the floating window's content
// using a WindowFrame popover. Called when the user types, toggles,
// or navigates.
func (m *App) refreshFindReplacePopup() {
	if m.windowManager == nil || m.FindReplace == nil {
		return
	}
	fr := m.FindReplace
	w := widgets.FindReplacePopupWidth(fr)
	h := widgets.FindReplacePopupHeight(fr)

	if fw := m.windowManager.Window("find-replace"); fw != nil {
		// Mutate existing frame content
		if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
			find := widgets.FindReplaceFrameFindField(cw)
			if find != nil {
				find.SyncEditFrom(fr.FindEdit())
			}
			if fr.ReplaceMode {
				replace := widgets.FindReplaceFrameReplaceField(cw)
				if replace != nil {
					replace.SyncEditFrom(fr.ReplaceEdit())
				}
			}
			fw.SetSize(w, h)
			fw.MarkContentDirty()
			return
		}
		// Fallback: rebuild frame
		frame := widgets.BuildFindReplaceFrame(m.Theme, widgets.FindReplaceFrameSpec{
			FindValue:    fr.Query(),
			ReplaceValue: fr.Replace(),
			ReplaceMode:  fr.ReplaceMode,
			FocusedField: fr.FocusedField,
			Width:        w,
			Height:       h,
		})
		if find := widgets.FindReplaceFrameFindField(frame); find != nil {
			find.SyncEditFrom(fr.FindEdit())
		}
		if fr.ReplaceMode {
			if replace := widgets.FindReplaceFrameReplaceField(frame); replace != nil {
				replace.SyncEditFrom(fr.ReplaceEdit())
			}
		}
		fw.SetContent(frame)
		fw.SetSize(w, h)
		fw.MarkContentDirty()
		return
	}

	// First time: create and position above the active editor.
	layout := m.computeLayout()
	frame := widgets.BuildFindReplaceFrame(m.Theme, widgets.FindReplaceFrameSpec{
		FindValue:    fr.Query(),
		ReplaceValue: fr.Replace(),
		ReplaceMode:  fr.ReplaceMode,
		FocusedField: fr.FocusedField,
		Width:        w,
		Height:       h,
	})
	if find := widgets.FindReplaceFrameFindField(frame); find != nil {
		find.SyncEditFrom(fr.FindEdit())
	}
	if fr.ReplaceMode {
		if replace := widgets.FindReplaceFrameReplaceField(frame); replace != nil {
			replace.SyncEditFrom(fr.ReplaceEdit())
		}
	}
	fw := wm.NewFloatingWindow(string(ui.OverlayFindReplace), "", frame, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
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
	m.overlayHost.Open(ui.OverlayFindReplace, fw, ui.DefaultOverlayPolicy(), func() {
		if m.EditorState.Editor != nil {
			m.EditorState.Editor.SetSearchOpen(false)
		}
		m.FindReplace = nil
	})
}

// pasteIntoFindReplace reads the system clipboard and inserts the
// text at the cursor position (or replaces the current selection)
// in the find/replace popup's active input field.
func (m *App) pasteIntoFindReplace(fr *widgets.FindReplaceState) tea.Cmd {
	if fr == nil {
		return nil
	}
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		return nil
	}
	text := string(data)
	ed := fr.ActiveEdit()
	if ed == nil {
		return nil
	}
	ed.Insert(text)
	if fr.OnChange != nil {
		fr.OnChange()
	}
	return nil
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
	// then sync to the frame's TextField for paint.
	ed := fr.ActiveEdit()
	if ed != nil {
		oldVal := ed.Value()
		if ui.HandleTextEditKey(ed, msg) {
			if ed.Value() != oldVal {
				if fr.OnChange != nil {
					fr.OnChange()
				}
			}
			m.syncFindReplaceFrameFromState()
		}
	}
	return nil
}

// syncFindReplaceFrameFromState copies FindReplaceState TextEdit
// state to the frame TextFields for paint.
func (m *App) syncFindReplaceFrameFromState() {
	if m.windowManager == nil || m.FindReplace == nil {
		return
	}
	fw := m.windowManager.Window("find-replace")
	if fw == nil {
		return
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
		find := widgets.FindReplaceFrameFindField(cw)
		if find != nil {
			find.SyncEditFrom(m.FindReplace.FindEdit())
		}
		if m.FindReplace.ReplaceMode {
			replace := widgets.FindReplaceFrameReplaceField(cw)
			if replace != nil {
				replace.SyncEditFrom(m.FindReplace.ReplaceEdit())
			}
		}
		fw.MarkContentDirty()
	}
}
