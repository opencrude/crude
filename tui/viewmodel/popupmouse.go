package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/tui/ui/popup"
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
	case "filepicker", "workspace-settings", "find-in-files", "replace-in-files":
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: screenX, Y: screenY, Button: button})
	}
}

// closePopupWindowByID routes close by popup ID to the OverlayHost.
// Closing workspace settings also reverts any live theme preview so the X
// button matches Cancel/Esc.
func (m *App) closePopupWindowByID(id string) {
	if id == string(ui.OverlayWorkspaceSettings) {
		m.applyThemePreset(themePresetFromSettings(m.LoadAppSettings()))
	}
	m.overlayHost.Close(ui.OverlayID(id))
}

// popupClickOutsideBehavior is a reference: which popup ID has which behavior.
// The actual values are set at popup-creation time via wm.WithClickOutside.
// CloseRoute:  context-menu, references-popup, completion-popup,
//              outline-popup, diag-context
// KeepRoute:   find-replace, composer, command-palette,
//              textoverlay
// Ignore:      dirty-close-prompt, tree-prompt, tree-delete-confirm, filepicker,
//              workspace-settings

// =============================================================================
// Context menu: click & hover
//
// Layout (BuildContextMenuFrame / WindowFrame FramePopover):
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
	if res.Action == nil || res.Action.ID != ui.ContextMenuActivate {
		return nil
	}
	item, ok := res.Action.Payload.(ui.ListItem)
	if !ok || item.Disabled {
		return nil
	}
	p, ok := item.Payload.(ui.ContextMenuPayload)
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
	list := ui.ContextMenuList(cw)
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
		ui.SyncContextMenuCursor(cw, st)
		fw.MarkContentDirty()
	}
}

// =============================================================================
// Find/Replace: click
//
// Layout (BuildFindReplaceFrame / WindowFrame FramePopover):
// Row 0: ╭─ Find  ─╮
// Row 1: │ Find: xxx│
// Row 2: │ ⌕ 3/10   │ (or Replace if ReplaceMode)
// Row 3: │          │
// Row 4: │ matches  │ Aa \b .*
// Row 5: │ enter n..│
// Row N: ╰     ╯
// =============================================================================

// positionFindReplaceCursor places the popup's TextEdit cursor after a
// click on its TextField. fieldRect is the field's popup-local rect (the
// Host screen geometry), so the click lands exactly where the field paints
// regardless of frame padding. edit is the popup state's TextEdit that
// mirrors the field.
func (m *App) positionFindReplaceCursor(fr *popup.FindReplaceState, edit *ui.TextEdit, fieldRect ui.Rect, localX int) {
	if fr == nil || edit == nil {
		return
	}
	// Bare fields start their value at the field's own origin.
	relX := localX - fieldRect.X
	if relX < 0 {
		edit.SetCursor(0, false)
	} else {
		runes := []rune(edit.Value())
		pos := 0
		acc := 0
		for i, r := range runes {
			w := rendering.PlainWidth(string(r))
			if acc+w > relX {
				pos = i
				goto done
			}
			acc += w
		}
		pos = len(runes)
	done:
		edit.SetCursor(pos, false)
	}
	edit.ClearSelection()
}

// clickFindReplace routes a popup-local click. Toggles and action-row
// buttons hit-test against the Host's screen-rect registry (the painted
// geometry); input clicks resolve the target TextField through the Host so
// focus and cursor positioning track the real layout (including the gap
// rows between fields), never assumed row indexes.
func (m *App) clickFindReplace(localX, localY int) tea.Cmd {
	if m.FindReplace == nil {
		return nil
	}
	fr := m.FindReplace

	// Toggle clicks are hit-tested against the real ToggleButton bounds so
	// the interaction tracks the component geometry (no manual band math).
	if idx := m.findReplaceToggleAt(localX, localY); idx >= 0 {
		switch idx {
		case 0:
			fr.CaseSensitive = !fr.CaseSensitive
		case 1:
			fr.WholeWord = !fr.WholeWord
		case 2:
			fr.Regex = !fr.Regex
		}
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	}

	// Action-row buttons hit-test through the Host tree (same path as
	// confirm dialogs): no row/column arithmetic, clicks track the painted
	// geometry of the ui.Button components.
	if act := m.findReplaceButtonAt(localX, localY); act != "" {
		return m.activateFindReplaceButton(act)
	}

	// Input fields: resolve the clicked TextField through the Host so the
	// focus/cursor track the painted layout (the replace field sits two
	// content rows down in replace mode — one field + one gap row).
	fw := m.windowManager.Window("find-replace")
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
	switch id := cw.Host().HitTest(localX, localY); id {
	case ui.FRFindField, ui.FRReplaceField:
		fieldRect, _ := cw.Host().ScreenRect(id)
		if id == ui.FRFindField {
			fr.FocusedField = 0
			m.positionFindReplaceCursor(fr, fr.FindEdit(), fieldRect, localX)
		} else {
			fr.FocusedField = 1
			m.positionFindReplaceCursor(fr, fr.ReplaceEdit(), fieldRect, localX)
		}
		m.refreshFindReplacePopup()
	}
	return nil
}

// findReplaceButtonAt maps popup-local mouse coords to a find/replace
// action ID ("find.prev", "find.next", "find.replace.one",
// "find.replace.all") using the Host hit tree. Empty when the point is
// not on an action-row button or the button is disabled (no matches).
func (m *App) findReplaceButtonAt(localX, localY int) string {
	fw := m.windowManager.Window("find-replace")
	if fw == nil {
		return ""
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil || cw.Host() == nil {
		return ""
	}
	// Ensure layout matches painted size (undecorated WindowFrame paints
	// into the full floating-window size).
	ww, hh := fw.Size()
	cw.Host().Resize(ww, hh)
	cw.Host().Layout()
	act := ui.FindReplaceActionAt(cw, localX, localY)
	if act == "" {
		return ""
	}
	// Disabled buttons paint dimmed and must not fire (e.g. Up/Down with
	// zero matches). The bar owns the enabled state.
	if ui.FindReplaceButtonEnabled(cw, act) {
		return act
	}
	return ""
}

// activateFindReplaceButton fires a find/replace action-row button: match
// navigation or single/all replacement. Mirrors the keyboard paths
// (handleFindReplaceKey) so mouse and keys stay one behavior.
func (m *App) activateFindReplaceButton(action string) tea.Cmd {
	fr := m.FindReplace
	if fr == nil {
		return nil
	}
	switch action {
	case "find.prev":
		if fr.OnPrev != nil {
			fr.OnPrev()
		}
	case "find.next":
		if fr.OnNext != nil {
			fr.OnNext()
		}
	case "find.replace.one":
		if fr.OnReplace != nil {
			fr.OnReplace()
		}
	case "find.replace.all":
		if fr.OnReplaceAll != nil {
			fr.OnReplaceAll()
		}
	}
	return nil
}

// findReplaceToggleAt returns the filter-toggle index (0=case, 1=whole-word,
// 2=regex) under the popup-local point, or -1 when none. Bounds come from
// FindReplaceFrameToggleBounds — the Host's screen-rect registry, i.e. the
// exact geometry the pills are painted at.
func (m *App) findReplaceToggleAt(localX, localY int) int {
	fw := m.windowManager.Window("find-replace")
	if fw == nil {
		return -1
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil {
		return -1
	}
	for i := 0; i < 3; i++ {
		if r, ok := ui.FindReplaceFrameToggleBounds(cw, i); ok && r.Contains(localX, localY) {
			return i
		}
	}
	return -1
}

func (m *App) hoverFindReplace(localX, localY int) {
	if m.FindReplace == nil {
		return
	}
	fr := m.FindReplace

	fw := m.windowManager.Window("find-replace")
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

	// Toggle hover: mirror the hovered pill index (legacy field) and drive
	// the Host hover so ToggleButton.Paint's ctx.Hovered lights the pill.
	idx := m.findReplaceToggleAt(localX, localY)
	if fr.HoveredToggle != idx {
		fr.HoveredToggle = idx
	}
	toggleIDs := []ui.ComponentID{ui.FRToggleCase, ui.FRToggleWord, ui.FRToggleRegex}
	hoverID := ui.RootID
	if idx >= 0 {
		hoverID = toggleIDs[idx]
	}
	// Only touch Host hover when the point is not over the action row: the
	// buttons own their hover there (SetHoveredButton), and a pill would
	// never overlap the bar.
	if cw.Host().Hovered() != hoverID && ui.FindReplaceActionAt(cw, localX, localY) == "" {
		cw.Host().SetHovered(hoverID)
		cw.MarkContentDirty()
	}

	// Action-row hover paints through Host hover (subtle fill) without
	// stealing keyboard focus: hovered button ≠ focused control.
	ui.SetHoveredButton(cw, localX, localY)
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
		if cw, ok := fw.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmHover(cw, choice)
		}
	}
}

// hoverTreeDeleteConfirm mirrors hoverDirtyPrompt for the delete/git-discard
// dialog (its overlay previously routed to hoverDirtyPrompt, which looked up
// a different window and never painted hover).
func (m *App) hoverTreeDeleteConfirm(localX, localY int) {
	fw := m.windowManager.Window("tree-delete-confirm")
	if fw == nil {
		return
	}
	choice := confirmFrameButtonAt(fw, localX, localY)
	if choice < 0 {
		return
	}
	if m.TreeDeleteChoice != choice {
		m.TreeDeleteChoice = choice
		if cw, ok := fw.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmHover(cw, choice)
		}
	}
}

// =============================================================================
// Tree delete confirm: click — WindowFrame + Yes/No buttons.
// =============================================================================

func (m *App) clickTreeDeleteConfirm(localX, localY int) tea.Cmd {
	if m.TreeDeletePending == "" && m.GitDiscardPending == nil {
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
// Layout (BuildTextPromptFrame / WindowFrame FrameDialog):
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
			res := root.Handle(ui.EventContext{LocalX: localX, LocalY: localY}, ui.MouseClickEvent(tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft}))
			if res.Action != nil && res.Action.ID == "window.close" {
				m.closeReferencesPopup()
				return nil
			}
			if res.Action != nil && res.Action.ID == ui.ListPopupActivate {
				if item, ok := res.Action.Payload.(ui.ListItem); ok {
					var idx int
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
		list := ui.ListPopupList(cw)
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
	list := ui.ListPopupList(cw)
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
			res := root.Handle(ui.EventContext{LocalX: localX, LocalY: localY}, ui.MouseClickEvent(tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft}))
			if res.Action != nil && res.Action.ID == "window.close" {
				m.closeCompletionPopup()
				return nil
			}
			if res.Action != nil && res.Action.ID == ui.ListPopupActivate {
				if item, ok := res.Action.Payload.(ui.ListItem); ok {
					var idx int
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
		list := ui.ListPopupList(cw)
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
			res := root.Handle(ui.EventContext{LocalX: localX, LocalY: localY}, ui.MouseClickEvent(tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft}))
			if res.Action != nil && res.Action.ID == "window.close" {
				m.closeOutlinePopup()
				return nil
			}
			if res.Action != nil && res.Action.ID == ui.ListPopupActivate {
				if item, ok := res.Action.Payload.(ui.ListItem); ok {
					var idx int
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
		list := ui.ListPopupList(cw)
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
	if res.Action != nil && res.Action.ID == ui.CPActivate {
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
