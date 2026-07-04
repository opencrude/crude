package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/tui/rendering"
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
	if m.EditorState.Editor != nil {
		m.EditorState.Editor.SetSearchOpen(false)
	}
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("find-replace")
	}
	m.FindReplace = nil
	m.dirty = true
}

// refreshFindReplacePopup rebuilds the floating window's content
// lines. Called when the user types, toggles an option, or
// navigates.
func (m *App) refreshFindReplacePopup() {
	if m.windowManager == nil || m.FindReplace == nil {
		return
	}
	lines := widgets.FindReplaceLines(m.Theme, m.FindReplace)
	pop := widgets.NewPopupContent(lines)

	// FindReplaceLines now self-renders the unified popup chrome.
	// Use the popup's own width/height functions.
	w := widgets.FindReplacePopupWidth(m.FindReplace)
	h := len(lines)

	if fw := m.windowManager.Window("find-replace"); fw != nil {
		// Just refresh the content. Don't re-center, since the
		// user may have moved it.
		fw.SetSize(w, h)
		fw.SetContent(pop)
		fw.MarkContentDirty()
		return
	}

	// First time: create and position above the active editor.
	layout := m.computeLayout()

	// The chrome is self-rendered. The FloatingWindow is a plain
	// positioned box.
	fw := wm.NewFloatingWindow("find-replace", "", pop, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false), // not modal — user can still click editor
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	// Position in the top-right of the editor area, with margin.
	x := layout.SidebarW + m.EditorState.Editor.PadL + (layout.EditorW - w) - 2
	if x < layout.SidebarW {
		x = layout.SidebarW + 2
	}
	y := layout.ChatAreaTop + 1
	fw.SetPosition(x, y)
	m.windowManager.AddWindow(fw)
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
	input := fr.ActiveInput()
	if input == nil {
		return nil
	}
	// If there's a selection, replace it; otherwise insert at cursor.
	if input.HasActiveSelection() {
		s, e := input.SelectionRange()
		input.DeleteRange(s, e)
	}
	input.InsertString(text)
	if fr.OnChange != nil {
		fr.OnChange()
	}
	return nil
}

// handleFindReplaceKey routes keypresses when the find/replace popup
// is focused. The text-editing keys (characters, Backspace, Delete,
// Left/Right, Home/End, Ctrl+Left/Right, Ctrl+A/E/U/K/W) are
// delegated to the embedded components.TextInput — so all the
// features that work in the chat input work here too (real selection,
// rune-safe cursor, word motion, etc.). The popup-level keys
// (Esc, Tab, Enter, Alt+C/W/R, Ctrl+Alt+Enter, Shift+Enter, Ctrl+C/V)
// are handled here.
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

	// MS Edit shortcuts. Now that the popup uses real TextInput
	// (with selection state), these work the same as in every
	// other text field in the app: Ctrl+C copies the selection (or
	// the whole field if no selection), Ctrl+X cuts, Ctrl+A
	// selects all, Ctrl+V pastes.
	switch msg.String() {
	case "ctrl+c":
		// Copy: if there's a selection, copy that. Otherwise, copy
		// the whole field (matches VSCode's find-dialog behavior).
		input := fr.ActiveInput()
		if input == nil {
			return nil
		}
		var text string
		if input.HasActiveSelection() {
			text = input.SelectedText()
		} else {
			text = input.Value()
		}
		if text != "" {
			return rendering.CopyToClipboardCmd(text)
		}
		return nil
	case "ctrl+x":
		// Cut: copy selection (or whole field) and delete it.
		input := fr.ActiveInput()
		if input == nil {
			return nil
		}
		var text string
		if input.HasActiveSelection() {
			text = input.SelectedText()
			s, e := input.SelectionRange()
			input.DeleteRange(s, e)
		} else {
			text = input.Value()
			input.Clear()
		}
		if fr.OnChange != nil {
			fr.OnChange()
		}
		if text != "" {
			return rendering.CopyToClipboardCmd(text)
		}
		return nil
	case "ctrl+a":
		// Select all: anchor=0, end=length. TextInput handles this
		// via the default key handler (Ctrl+A moves to col 0), so
		// we route it explicitly to set a real selection.
		input := fr.ActiveInput()
		if input != nil {
			input.SetSelection(0, input.Len())
		}
		m.refreshFindReplacePopup()
		return nil
	case "ctrl+v":
		// Paste: read clipboard, insert at cursor (replacing
		// selection if present). Synchronous read keeps the
		// popup's response on the same frame as the keypress.
		return m.pasteIntoFindReplace(fr)
	}

	// All other keys: delegate to the focused input's TextInput.
	// This gives us character insertion, Backspace, Delete, Left/Right,
	// Home/End, Ctrl+Left/Right (word motion), Ctrl+E/U/K/W (line ops),
	// Up/Down (history-like, harmless for single-line fields), etc.
	input := fr.ActiveInput()
	if input != nil {
		oldVal := input.Value()
		updated, cmd := input.Update(msg, false)
		*input = updated
		if input.Value() != oldVal && fr.OnChange != nil {
			fr.OnChange()
		}
		return cmd
	}
	return nil
}
