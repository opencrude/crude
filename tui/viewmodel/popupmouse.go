package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/tui/widgets"
	"crude/tui/wm"
)

// =============================================================================
// Popup mouse dispatch — click-outside, click-on-content, and hover
// pre-selection for all popup windows.
// =============================================================================

// handlePopupClickOutside checks all open popups for click-outside behavior.
// Called at the TOP of dispatchMouseClick, before WM hit-testing.
// Returns true to suppress the click entirely (modal block).
// Routes through the OverlayHost when available; falls back to direct WM
// iteration for popups not yet opened through the host.
func (m *App) handlePopupClickOutside(msg tea.MouseClickMsg) bool {
	if m.windowManager == nil || msg.Button != tea.MouseLeft {
		return false
	}
	mouse := msg.Mouse()
	if m.overlayHost != nil {
		return m.overlayHost.HandleClickOutside(mouse.X, mouse.Y)
	}
	for i := len(m.windowManager.Windows()) - 1; i >= 0; i-- {
		fw := m.windowManager.Windows()[i]
		if !fw.Visible() || !fw.Focusable() {
			continue
		}
		if fw.ContainsPoint(mouse.X, mouse.Y) {
			continue
		}
		switch fw.ClickOutsideBehavior() {
		case wm.ClickOutsideIgnore:
			return true
		case wm.ClickOutsideCloseRoute:
			m.closePopupWindowByID(fw.ID)
		case wm.ClickOutsideKeepRoute:
		}
	}
	return false
}

// handlePopupContentClick routes a left-click that landed inside a popup
// to the correct content handler. Prefers OverlayHost dispatch; falls back
// to the legacy switch for popups not yet managed by the host.
func (m *App) handlePopupContentClick(fw *wm.FloatingWindow, msg tea.MouseClickMsg) tea.Cmd {
	if fw == nil {
		return nil
	}
	if m.overlayHost != nil && m.overlayHost.HasContentHandler(ui.OverlayID(fw.ID)) {
		return m.overlayHost.DispatchContentClick(fw, msg)
	}

	switch fw.ID {
	case "filepicker":
		return m.forwardEventToWindowContent(fw, msg)
	default:
		return m.forwardEventToWindowContent(fw, msg)
	}
}

// handlePopupContentHover updates pre-selection when the mouse moves
// over a popup. Prefers OverlayHost dispatch; falls back to the legacy
// switch for popups not yet managed by the host.
func (m *App) handlePopupContentHover(fw *wm.FloatingWindow, screenX, screenY int, button tea.MouseButton) {
	if fw == nil || !fw.Focusable() {
		return
	}
	if m.overlayHost != nil && m.overlayHost.HasHoverHandler(ui.OverlayID(fw.ID)) {
		m.overlayHost.DispatchContentHover(fw, screenX, screenY, button)
		return
	}

	switch fw.ID {
	case "filepicker", "workspace-settings", "find-in-files":
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: screenX, Y: screenY, Button: button})
	}
}

// closePopupWindowByID routes close by popup ID to the OverlayHost.
// All cleanup logic lives in the OnClose callback registered at open time.
func (m *App) closePopupWindowByID(id string) {
	m.overlayHost.Close(ui.OverlayID(id))
}

// popupClickOutsideBehavior is a reference: which popup ID has which behavior.
// The actual values are set at popup-creation time via wm.WithClickOutside.
// CloseRoute:  context-menu, references-popup, completion-popup,
//              outline-popup, diag-context
// KeepRoute:   find-replace, filepicker, composer, command-palette,
//              textoverlay
// Ignore:      dirty-close-prompt, tree-prompt, tree-delete-confirm

// =============================================================================
// Context menu: click & hover
//
// Layout (after PopupChromeLines):
// Row 0: ╭     ╮  (top border)
// Row 1: │ first item│  (first content line)
// Row 2: │ second    │
// ...
// Row N: ╰     ╯  (bottom border)
//
// Row 1 is contentY=1 (0-indexed). So contentRow = contentY - 1 for the
// first content item. We do this conversion consistently.
// =============================================================================

func (m *App) clickContextMenu(localX, localY int) tea.Cmd {
	if m.LastContextMenu == nil || m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window("context-menu")
	if fw == nil {
		return nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil || cw.Host() == nil {
		return nil
	}
	ww, hh := fw.Size()
	cw.Host().Resize(ww, hh)
	cw.Host().Layout()
	res := cw.Host().Handle(tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != widgets.ContextMenuActivate {
		return nil
	}
	item, ok := res.Action.Payload.(ui.ListItem)
	if !ok || item.Disabled {
		return nil
	}
	p, ok := item.Payload.(widgets.ContextMenuPayload)
	if !ok {
		return nil
	}
	st := m.LastContextMenu
	if p.FlatIdx >= 0 {
		st.Cursor = p.FlatIdx
	}
	st.Activate()
	m.closeContextMenu()
	return nil
}

func (m *App) hoverContextMenu(localX, localY int) {
	if m.LastContextMenu == nil || m.windowManager == nil {
		return
	}
	st := m.LastContextMenu
	if st.FlatItems == nil {
		st.Build()
	}
	fw := m.windowManager.Window("context-menu")
	if fw == nil {
		return
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil || cw.Host() == nil {
		return
	}
	ww, hh := fw.Size()
	cw.Host().Resize(ww, hh)
	cw.Host().Layout()
	list := widgets.ContextMenuList(cw)
	if list == nil {
		return
	}
	// FramePopover content starts at Y=1 (title/border row).
	contentY := localY - 1
	if contentY < 0 {
		return
	}
	idx := list.Scroll().Offset() + contentY
	if idx < 0 || idx >= len(list.Items()) {
		return
	}
	it := list.Items()[idx]
	if !it.Selectable() {
		return
	}
	if list.SelectedIndex() != idx {
		list.SelectIndex(idx)
		widgets.SyncContextMenuCursor(cw, st)
		fw.MarkContentDirty()
	}
}

// =============================================================================
// Find/Replace: click
//
// Layout (after PopupChromeLines, undecorated):
// Row 0: ╭─ Find  ─╮
// Row 1: │ Find: xxx│
// Row 2: │ ⌕ 3/10   │ (or Replace if ReplaceMode)
// Row 3: │          │
// Row 4: │ matches  │ Aa \b .*
// Row 5: │ enter n..│
// Row N: ╰     ╯
// =============================================================================

// positionFindReplaceCursor translates a popup-local X coordinate
// into a rune position within the given find/replace field, and
// sets the input's cursor (and clears any selection). This gives
// the user click-to-position-cursor on the find/replace input,
// matching the chat input's behavior.
//
// The label parameter tells us the label prefix width (e.g. "Find: "
// is 6 cells; "Replace: " is 9 cells). The chrome adds "│ " on the
// left (2 cells of padding around the left border). We account for
// both when translating local X to visible column within the value.
func (m *App) positionFindReplaceCursor(fr *widgets.FindReplaceState, input *components.TextInput, localX int, label string) {
	if fr == nil || input == nil {
		return
	}
	// Chrome contributes: "│" border (1) + " " pad (1) + " " pad (1)
	// = 3 cells on the left. Then "Label: " is rendered.
	chromeW := 3
	labelW := len(label) + 2 // "Label" + ": "
	valueStartX := chromeW + labelW
	if localX < valueStartX {
		input.SetCursorFlat(0)
	} else {
		// Translate visible X to rune index in the value.
		// localX is in the popup's local coordinates; we count
		// from valueStartX into the value's visible-width position.
		relX := localX - valueStartX
		value := input.Value()
		runes := []rune(value)
		// Walk runes accumulating visible width until we exceed relX.
		pos := 0
		acc := 0
		for i, r := range runes {
			// Use grapheme-aware width (same as layout + UV), not per-rune wcwidth.
			w := rendering.PlainWidth(string(r))
			if acc+w > relX {
				pos = i
				goto done
			}
			acc += w
			pos = i + 1
		}
		pos = len(runes)
	done:
		input.SetCursorFlat(pos)
	}
	input.ClearSelection()
}

func (m *App) clickFindReplace(localX, localY int) tea.Cmd {
	if m.FindReplace == nil {
		return nil
	}
	fr := m.FindReplace

	contentRow := localY - 1
	if contentRow < 0 {
		return nil
	}

	// Row 0: Find input field
	if contentRow == 0 {
		fr.FocusedField = 0
		m.positionFindReplaceCursor(fr, fr.FindInput(), localX, "Find")
		m.refreshFindReplacePopup()
		return nil
	}

	// Row 1: Replace input field (if ReplaceMode)
	nextRow := 1
	if fr.ReplaceMode {
		if contentRow == 1 {
			fr.FocusedField = 1
			m.positionFindReplaceCursor(fr, fr.ReplaceInput(), localX, "Replace")
			m.refreshFindReplacePopup()
			return nil
		}
		nextRow = 2
	}

	// Row nextRow+1: Status + Toggle row
	// "matches <N>/<M>" or "type to search" or "no results"
	// Then "   " separator, then " Aa  \b  .* "
	if contentRow == nextRow+1 {
		// Compute the toggle X positions from the actual popup width.
		// The toggle group is right-aligned within the inner content
		// (innerW = fw.w - 6). Toggle group width: " Aa  \b  .* " = 14.
		chromePad := 3 // │ + 2 spaces
		fw := m.windowManager.Window("find-replace")
		if fw == nil {
			return nil
		}
		w, _ := fw.Size()
		innerW := w - 6
		toggleGroupW := 14
		toggleStartX := chromePad + innerW - toggleGroupW
		toggleBandW := toggleGroupW / 3 // ~5 cells per band (4 toggle + 1 separator)

		toggleSpecs := []*bool{
			&fr.CaseSensitive,
			&fr.WholeWord,
			&fr.Regex,
		}
		for i, ptr := range toggleSpecs {
			bandStart := toggleStartX + i*toggleBandW
			bandEnd := bandStart + toggleBandW
			if localX >= bandStart && localX < bandEnd {
				*ptr = !*ptr
				if fr.OnChange != nil {
					fr.OnChange()
				}
				return nil
			}
		}
		return nil
	}

	return nil
}

func (m *App) hoverFindReplace(localX, localY int) {
	if m.FindReplace == nil {
		return
	}
	fr := m.FindReplace

	contentRow := localY - 1
	if contentRow < 0 {
		return
	}

	nextRow := 1
	if fr.ReplaceMode {
		nextRow = 2
	}

	if contentRow == nextRow+1 {
		// Same band logic as click.
		fw := m.windowManager.Window("find-replace")
		if fw == nil {
			return
		}
		w, _ := fw.Size()
		innerW := w - 6
		chromePad := 3
		toggleGroupW := 14
		toggleStartX := chromePad + innerW - toggleGroupW
		toggleBandW := toggleGroupW / 3

		for i := range []int{0, 1, 2} {
			bandStart := toggleStartX + i*toggleBandW
			bandEnd := bandStart + toggleBandW
			if localX >= bandStart && localX < bandEnd {
				if fr.HoveredToggle != i {
					fr.HoveredToggle = i
				}
				return
			}
		}
	}

	// Not over a toggle — clear hover.
	if fr.HoveredToggle != -1 {
		fr.HoveredToggle = -1
	}
}

// =============================================================================
// Confirm prompt (dirty-close-prompt): click & hover
// WindowFrame + ui.Button row — hit-test via Host.
// =============================================================================

func (m *App) clickDirtyPrompt(localX, localY int) tea.Cmd {
	if m.DirtyPromptPath == "" {
		return nil
	}
	fw := m.windowManager.Window("dirty-close-prompt")
	if fw == nil {
		return nil
	}
	choice := confirmFrameButtonAt(fw, localX, localY)
	if choice < 0 {
		return nil
	}
	m.DirtyPromptChoice = choice
	return m.activateDirtyPromptButton(choice)
}

func (m *App) activateDirtyPromptButton(choice int) tea.Cmd {
	if m.DirtyPromptPath == "" {
		return nil
	}
	m.DirtyPromptChoice = choice
	switch choice {
	case 0:
		m.dirtyPromptSave()
	case 1:
		m.dirtyPromptDiscard()
	default:
		m.dirtyPromptCancel()
	}
	return nil
}

func (m *App) hoverDirtyPrompt(localX, localY int) {
	fw := m.windowManager.Window("dirty-close-prompt")
	if fw == nil {
		return
	}
	choice := confirmFrameButtonAt(fw, localX, localY)
	if choice < 0 {
		return
	}
	if m.DirtyPromptChoice != choice {
		m.DirtyPromptChoice = choice
		m.refreshDirtyPrompt()
	}
}

// =============================================================================
// Tree delete confirm: click — WindowFrame + Yes/No buttons.
// =============================================================================

func (m *App) clickTreeDeleteConfirm(localX, localY int) tea.Cmd {
	if m.TreeDeletePending == "" {
		return nil
	}
	fw := m.windowManager.Window("tree-delete-confirm")
	if fw == nil {
		return nil
	}
	choice := confirmFrameButtonAt(fw, localX, localY)
	if choice < 0 {
		return nil
	}
	m.TreeDeleteChoice = choice
	if choice == 0 {
		m.confirmTreeDelete()
	} else {
		m.cancelTreeDelete()
	}
	return nil
}

// =============================================================================
// Tree prompt: click
//
// Layout (from TextPromptLines):
// Row 0: ╭─ New File ─╮
// Row 1: │ prompt text│
// Row 2: │ <value>    │  (input)
// Row 3: │            │  (empty)
// Row 4: │ enter:cre..│  (hints)
// Row N: ╰    ─╯
// =============================================================================

func (m *App) clickTreePrompt(localX, localY int) tea.Cmd {
	if m.TreePrompt == nil {
		return nil
	}
	contentRow := localY - 1
	if contentRow == 1 {
		// Click on input field (row 2 in local = content row 1)
		// Input is rendered without a label, so the value starts at
		// chrome offset (3 cols) + 0 = col 3.
		charX := localX - 3
		if charX < 0 {
			charX = 0
		}
		if m.TreePrompt.Edit != nil {
			if charX > m.TreePrompt.Edit.Len() {
				charX = m.TreePrompt.Edit.Len()
			}
			m.TreePrompt.Edit.SetCursor(charX, false)
			m.TreePrompt.SyncFromEdit()
		} else {
			if charX > len(m.TreePrompt.Value) {
				charX = len(m.TreePrompt.Value)
			}
			m.TreePrompt.Cursor = charX
		}
		m.refreshTreePrompt()
	}
	return nil
}

// =============================================================================
// References popup: click & hover
//
// Each reference is one content row (after chrome).
// =============================================================================

func (m *App) clickReferences(localX, localY int) tea.Cmd {
	if m.LSPPopup.ReferencesID == "" || m.LSPPopup.RefRows == nil || m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window(m.LSPPopup.ReferencesID)
	if fw == nil {
		return nil
	}
	// WindowFrame path: activate via Host + List.
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil && cw.Host() != nil {
		ww, hh := fw.Size()
		cw.Host().Resize(ww, hh)
		cw.Host().Layout()
		// Prefer frame Handle so close button is checked before content.
		if root := cw.Root(); root != nil {
			res := root.Handle(ui.EventContext{LocalX: localX, LocalY: localY}, tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft})
			if res.Action != nil && res.Action.ID == "window.close" {
				m.closeReferencesPopup()
				return nil
			}
			if res.Action != nil && res.Action.ID == widgets.ListPopupActivate {
				if item, ok := res.Action.Payload.(ui.ListItem); ok {
					idx := -1
					if i, ok := item.Payload.(int); ok {
						idx = i
					} else {
						idx = m.listPopupSelectedIndex(cw)
					}
					if idx >= 0 && idx < len(m.LSPPopup.RefRows) {
						m.LSPPopup.RefCursor = idx
						m.jumpToReference(m.LSPPopup.RefRows[idx])
						m.closeReferencesPopup()
					}
				}
				return nil
			}
		}
		return nil
	}
	// Legacy fallback (hand-drawn chrome).
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.LSPPopup.RefRows) {
		m.LSPPopup.RefCursor = contentRow
		r := m.LSPPopup.RefRows[m.LSPPopup.RefCursor]
		m.jumpToReference(r)
		m.closeReferencesPopup()
	}
	return nil
}

func (m *App) hoverReferences(localX, localY int) {
	if m.LSPPopup.RefRows == nil || m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window(m.LSPPopup.ReferencesID)
	if fw == nil {
		return
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
		list := widgets.ListPopupList(cw)
		if list == nil {
			return
		}
		// Frame content starts at Y=1.
		contentY := localY - 1
		if contentY < 0 {
			return
		}
		idx := list.Scroll().Offset() + contentY
		if idx < 0 || idx >= len(list.Items()) {
			return
		}
		if !list.Items()[idx].Selectable() {
			return
		}
		if list.SelectedIndex() != idx {
			list.SelectIndex(idx)
			m.LSPPopup.RefCursor = idx
			fw.MarkContentDirty()
		}
		return
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.LSPPopup.RefRows) && contentRow != m.LSPPopup.RefCursor {
		m.LSPPopup.RefCursor = contentRow
		m.refreshReferencesPopup()
	}
}

// listPopupSelectedIndex returns the selected row index from a list popup frame.
func (m *App) listPopupSelectedIndex(cw *ui.ComponentWidget) int {
	list := widgets.ListPopupList(cw)
	if list == nil {
		return -1
	}
	return list.SelectedIndex()
}

// =============================================================================
// Completion popup: click & hover
// =============================================================================

func (m *App) clickCompletion(localX, localY int) tea.Cmd {
	if m.LSPPopup.CompletionItems == nil || m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window(m.LSPPopup.CompletionID)
	if fw == nil {
		return nil
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil && cw.Host() != nil {
		ww, hh := fw.Size()
		cw.Host().Resize(ww, hh)
		cw.Host().Layout()
		if root := cw.Root(); root != nil {
			res := root.Handle(ui.EventContext{LocalX: localX, LocalY: localY}, tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft})
			if res.Action != nil && res.Action.ID == "window.close" {
				m.closeCompletionPopup()
				return nil
			}
			if res.Action != nil && res.Action.ID == widgets.ListPopupActivate {
				if item, ok := res.Action.Payload.(ui.ListItem); ok {
					idx := -1
					if i, ok := item.Payload.(int); ok {
						idx = i
					} else {
						idx = m.listPopupSelectedIndex(cw)
					}
					if idx >= 0 && idx < len(m.LSPPopup.CompletionItems) {
						m.LSPPopup.CompletionCursor = idx
						m.acceptCompletion()
					}
				}
				return nil
			}
		}
		return nil
	}
	// Legacy fallback.
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.LSPPopup.CompletionItems) {
		m.LSPPopup.CompletionCursor = contentRow
		m.acceptCompletion()
	}
	return nil
}

func (m *App) hoverCompletion(localX, localY int) {
	if m.LSPPopup.CompletionItems == nil || m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window(m.LSPPopup.CompletionID)
	if fw == nil {
		return
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
		list := widgets.ListPopupList(cw)
		if list == nil {
			return
		}
		contentY := localY - 1
		if contentY < 0 {
			return
		}
		idx := list.Scroll().Offset() + contentY
		if idx < 0 || idx >= len(list.Items()) {
			return
		}
		if !list.Items()[idx].Selectable() {
			return
		}
		if list.SelectedIndex() != idx {
			list.SelectIndex(idx)
			m.LSPPopup.CompletionCursor = idx
			fw.MarkContentDirty()
		}
		return
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.LSPPopup.CompletionItems) && contentRow != m.LSPPopup.CompletionCursor {
		m.LSPPopup.CompletionCursor = contentRow
	}
}

// =============================================================================
// Outline popup: click & hover
// =============================================================================

func (m *App) clickOutline(localX, localY int) tea.Cmd {
	if !m.LSPPopup.OutlineOpen || m.LSPPopup.OutlineRows == nil || m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window(outlinePopupID)
	if fw == nil {
		return nil
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil && cw.Host() != nil {
		ww, hh := fw.Size()
		cw.Host().Resize(ww, hh)
		cw.Host().Layout()
		if root := cw.Root(); root != nil {
			res := root.Handle(ui.EventContext{LocalX: localX, LocalY: localY}, tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft})
			if res.Action != nil && res.Action.ID == "window.close" {
				m.closeOutlinePopup()
				return nil
			}
			if res.Action != nil && res.Action.ID == widgets.ListPopupActivate {
				if item, ok := res.Action.Payload.(ui.ListItem); ok {
					idx := -1
					if i, ok := item.Payload.(int); ok {
						idx = i
					} else {
						idx = m.listPopupSelectedIndex(cw)
					}
					if idx >= 0 && idx < len(m.LSPPopup.OutlineRows) {
						m.LSPPopup.OutlineCursor = idx
						m.jumpToOutline(idx)
						m.closeOutlinePopup()
					}
				}
				return nil
			}
		}
		return nil
	}
	// Legacy fallback.
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.LSPPopup.OutlineRows) {
		m.LSPPopup.OutlineCursor = contentRow
		m.jumpToOutline(contentRow)
		m.closeOutlinePopup()
	}
	return nil
}

func (m *App) hoverOutline(localX, localY int) {
	if m.LSPPopup.OutlineRows == nil || m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window(outlinePopupID)
	if fw == nil {
		return
	}
	if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
		list := widgets.ListPopupList(cw)
		if list == nil {
			return
		}
		contentY := localY - 1
		if contentY < 0 {
			return
		}
		idx := list.Scroll().Offset() + contentY
		if idx < 0 || idx >= len(list.Items()) {
			return
		}
		if !list.Items()[idx].Selectable() {
			return
		}
		if list.SelectedIndex() != idx {
			list.SelectIndex(idx)
			m.LSPPopup.OutlineCursor = idx
			fw.MarkContentDirty()
		}
		return
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.LSPPopup.OutlineRows) && contentRow != m.LSPPopup.OutlineCursor {
		m.LSPPopup.OutlineCursor = contentRow
	}
}

// ─── Command palette mouse ─────────────────────────────────────────────

func (m *App) clickCommandPalette(fw *wm.FloatingWindow, localX, localY int) tea.Cmd {
	if fw == nil {
		return nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil {
		return nil
	}
	// Route via Host hit-test + Handle (same as filepicker/find-in-files).
	host := cw.Host()
	if host == nil {
		return nil
	}
	res := host.Handle(tea.MouseClickMsg{
		X:      localX,
		Y:      localY,
		Button: tea.MouseLeft,
	})
	// Check if the click activated a list item.
	if res.Action != nil && res.Action.ID == widgets.CPActivate {
		if entry, ok := res.Action.Payload.(ui.ListItem); ok {
			if ce, ok := entry.Payload.(cmdEntry); ok {
				cb := m.commandPaletteModel.callback
				m.overlayHost.Close(ui.OverlayCommandPalette)
				if cb != nil {
					return cb(ce.ID)
				}
			}
		}
	}
	fw.MarkContentDirty()
	return nil
}

func (m *App) hoverCommandPalette(fw *wm.FloatingWindow, localX, localY int) {
	if fw == nil {
		return
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil {
		return
	}
	host := cw.Host()
	if host == nil {
		return
	}
	host.Handle(tea.MouseMotionMsg{X: localX, Y: localY})
	fw.MarkContentDirty()
}
