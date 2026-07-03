package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"

	"crude/tui/components"
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
func (m *App) handlePopupClickOutside(msg tea.MouseClickMsg) bool {
	if m.windowManager == nil || msg.Button != tea.MouseLeft {
		return false
	}
	mouse := msg.Mouse()

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
// to the correct content handler. localX/localY are relative to the window.
func (m *App) handlePopupContentClick(fw *wm.FloatingWindow, msg tea.MouseClickMsg) tea.Cmd {
	if fw == nil {
		return nil
	}
	mouse := msg.Mouse()
	fx, fy := fw.Position()
	localX := mouse.X - fx
	localY := mouse.Y - fy

	switch fw.ID {
	case "context-menu":
		return m.clickContextMenu(localX, localY)
	case "find-replace":
		return m.clickFindReplace(localX, localY)
	case "dirty-close-prompt":
		return m.clickDirtyPrompt(localX, localY)
	case "tree-prompt":
		return m.clickTreePrompt(localX, localY)
	case "tree-delete-confirm":
		return m.clickTreeDeleteConfirm(localX, localY)
	case "filepicker":
		return m.forwardEventToWindowContent(fw, msg)
	case "references-popup":
		return m.clickReferences(localX, localY)
	case "completion-popup":
		return m.clickCompletion(localX, localY)
	case "outline-popup":
		return m.clickOutline(localX, localY)
	default:
		return m.forwardEventToWindowContent(fw, msg)
	}
}

// handlePopupContentHover updates pre-selection when the mouse moves
// over a popup. Called from dispatchMouseMotion.
func (m *App) handlePopupContentHover(fw *wm.FloatingWindow, screenX, screenY int) {
	if fw == nil || !fw.Focusable() {
		return
	}
	fx, fy := fw.Position()
	localX := screenX - fx
	localY := screenY - fy

	switch fw.ID {
	case "context-menu":
		m.hoverContextMenu(localX, localY)
	case "find-replace":
		m.hoverFindReplace(localX, localY)
	case "dirty-close-prompt", "tree-delete-confirm":
		m.hoverDirtyPrompt(localX, localY)
	case "references-popup":
		m.hoverReferences(localX, localY)
	case "completion-popup":
		m.hoverCompletion(localX, localY)
	case "outline-popup":
		m.hoverOutline(localX, localY)
	}
}

// closePopupWindowByID routes close by popup ID to the correct cleanup function.
func (m *App) closePopupWindowByID(id string) {
	switch id {
	case "context-menu":
		m.closeContextMenu()
	case "find-replace":
		m.closeFindReplace()
	case "dirty-close-prompt":
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("dirty-close-prompt")
		}
		m.dirtyPromptPath = ""
		m.dirty = true
	case "tree-prompt":
		m.closeTreePrompt()
	case "tree-delete-confirm":
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("tree-delete-confirm")
		}
		m.treeDeletePending = ""
		m.dirty = true
	case "references-popup":
		m.closeReferencesPopup()
	case "completion-popup":
		m.closeCompletionPopup()
	case "outline-popup":
		m.closeOutlinePopup()
	case "filepicker":
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("filepicker")
		}
		m.setMode(ModeChat)
		m.dirty = true
	case "diag-hover":
		m.clearDiagHoverPopup()
	case "composer":
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("composer")
		}
		m.composer.Close()
		m.dirty = true
	case "command-palette":
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("command-palette")
		}
		m.commandPaletteModel = nil
		m.dirty = true
	default:
		if m.windowManager != nil {
			m.windowManager.RemoveWindow(id)
		}
	}
}

// popupClickOutsideBehavior is a reference: which popup ID has which behavior.
// The actual values are set at popup-creation time via wm.WithClickOutside.
//   CloseRoute:  context-menu, references-popup, completion-popup,
//                outline-popup, diag-context
//   KeepRoute:   find-replace, filepicker, composer, command-palette,
//                textoverlay
//   Ignore:      dirty-close-prompt, tree-prompt, tree-delete-confirm

// =============================================================================
// Context menu: click & hover
//
// Layout (after PopupChromeLines):
//   Row 0: ╭──────────╮  (top border)
//   Row 1: │ first item│  (first content line)
//   Row 2: │ second    │
//   ...
//   Row N: ╰──────────╯  (bottom border)
//
// Row 1 is contentY=1 (0-indexed). So contentRow = contentY - 1 for the
// first content item. We do this conversion consistently.
// =============================================================================

func (m *App) clickContextMenu(localX, localY int) tea.Cmd {
	if m.lastContextMenu == nil {
		return nil
	}
	st := m.lastContextMenu

	// Row 0 = top border. Content starts at row 1.
	contentRow := localY - 1
	if contentRow < 0 {
		return nil
	}
	_ = localX // all items full-width

	rowIdx := 0
	for si, sec := range st.Sections {
		if si > 0 {
			if rowIdx == contentRow {
				return nil
			}
			rowIdx++
		}
		if sec.SectionLabel != "" {
			if rowIdx == contentRow {
				return nil
			}
			rowIdx++
		}
		for ii, item := range sec.Items {
			if rowIdx == contentRow {
				if item.Disabled {
					return nil
				}
				if fwItem, ok := findFlatItemIndex(st, si, ii); ok {
					st.Cursor = fwItem
				}
				st.Activate()
				m.closeContextMenu()
				return nil
			}
			rowIdx++
		}
	}
	return nil
}

func (m *App) hoverContextMenu(localX, localY int) {
	if m.lastContextMenu == nil {
		return
	}
	st := m.lastContextMenu
	if st.FlatItems == nil {
		st.Build()
	}

	contentRow := localY - 1
	if contentRow < 0 {
		return
	}

	rowIdx := 0
	for si, sec := range st.Sections {
		if si > 0 {
			if rowIdx == contentRow {
				return
			}
			rowIdx++
		}
		if sec.SectionLabel != "" {
			if rowIdx == contentRow {
				return
			}
			rowIdx++
		}
		for ii, item := range sec.Items {
			if rowIdx == contentRow {
				if !item.Disabled {
					if fwItem, ok := findFlatItemIndex(st, si, ii); ok && st.Cursor != fwItem {
						st.Cursor = fwItem
						m.dirty = true
					}
				}
				return
			}
			rowIdx++
		}
	}
}

// =============================================================================
// Find/Replace: click
//
// Layout (after PopupChromeLines, undecorated):
//   Row 0: ╭─ Find ───╮
//   Row 1: │ Find: xxx│
//   Row 2: │ ⌕ 3/10   │ (or Replace if ReplaceMode)
//   Row 3: │          │
//   Row 4: │ matches  │ Aa \b .*
//   Row 5: │ enter n..│
//   Row N: ╰──────────╯
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
			w := runewidth.RuneWidth(r)
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
	if m.findReplace == nil {
		return nil
	}
	fr := m.findReplace

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
	if m.findReplace == nil {
		return
	}
	fr := m.findReplace

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
					m.dirty = true
				}
				return
			}
		}
	}

	// Not over a toggle — clear hover.
	if fr.HoveredToggle != -1 {
		fr.HoveredToggle = -1
		m.dirty = true
	}
}

// =============================================================================
// Confirm prompt (dirty-close-prompt): click & hover
//
// Layout (from ConfirmPrompt3Button):
//   Row 0: ╭─ Unsaved ─╮
//   Row 1: │ i What do │     (question)
//   Row 2: │           │     (empty)
//   Row 3: │ detail    │
//   Row 4: │           │     (empty)
//   Row 5: │  Save Don't │  (3-button row)
//   Row 6: │ ← → ..   │     (hints)
//   Row N: ╰───────────╯
//
// Button row is at contentRow = innerH - 2 (second-to-last content row)
// (where innerH is the total window height minus 2 rows of chrome).
//
// The 3 buttons (Save | Don't Save | Cancel) live in the same row. We
// hit-test by approximating button widths — button widths are derived
// from the rendered label lengths, not hard-coded constants, so the
// detection stays correct if the labels ever change.
// =============================================================================

func (m *App) clickDirtyPrompt(localX, localY int) tea.Cmd {
	if m.dirtyPromptPath == "" {
		return nil
	}
	fw := m.windowManager.Window("dirty-close-prompt")
	if fw == nil {
		return nil
	}
	_, h := fw.Size()
	innerH := h - 2 // subtract chrome
	contentRow := localY - 1

	// Button row is at the second-to-last content row.
	buttonRow := innerH - 2
	if contentRow == buttonRow {
		choice := dirtyPromptButtonAtX(localX)
		if choice < 0 {
			return nil
		}
		m.dirtyPromptChoice = choice
		return m.activateDirtyPromptButton(choice)
	}
	return nil
}

func (m *App) activateDirtyPromptButton(choice int) tea.Cmd {
	if m.dirtyPromptPath == "" {
		return nil
	}
	m.dirtyPromptChoice = choice
	switch choice {
	case 0:
		m.dirtyPromptSave()
	case 1:
		m.dirtyPromptDiscard()
	default:
		m.dirtyPromptCancel()
	}
	m.dirty = true
	return nil
}

func (m *App) hoverDirtyPrompt(localX, localY int) {
	fw := m.windowManager.Window("dirty-close-prompt")
	if fw == nil {
		return
	}
	_, h := fw.Size()
	innerH := h - 2
	contentRow := localY - 1
	buttonRow := innerH - 2

	if contentRow != buttonRow {
		return
	}

	choice := dirtyPromptButtonAtX(localX)
	if choice < 0 {
		return
	}
	if m.dirtyPromptChoice != choice {
		m.dirtyPromptChoice = choice
		m.refreshDirtyPrompt()
	}
}

// dirtyPromptButtonAtX maps a local-X coordinate (within the dirty-prompt
// popup, including the chrome's left border) to the 0-indexed button
// the user is hovering/clicking. Returns -1 if the X is not over any
// button (e.g. on the left padding).
//
// The buttons are rendered left-aligned as " Save  Don't Save  Cancel "
// inside the chrome-padded content. The chrome occupies the first
// `chromePad` columns; the first button starts there.
//
// Button labels: 0=Save (4 chars), 1=Don't Save (11 chars), 2=Cancel (6 chars).
// Each button (focused or not) renders as " Label " — 2 extra cells of padding
// per button. Separators are single spaces.
func dirtyPromptButtonAtX(localX int) int {
	chromePad := 3 // "│  " — left border + 2 pad cells
	// Total layout: " " + "Save" + " " + " " + "Don't Save" + " " + " " + "Cancel" + " "
	// = 1+4+1+1+11+1+1+6+1 = 27 cells inside the content area.
	// In local coords, the first button starts at column chromePad.
	// Approximate button start positions: Save at 4, Don't Save at 10, Cancel at 22.
	if localX < chromePad+1 || localX >= chromePad+27 {
		return -1
	}
	relX := localX - chromePad
	switch {
	case relX >= 0 && relX < 7: // " Save " (1+4+1+1 padding space)
		return 0
	case relX >= 7 && relX < 19: // " Don't Save " (1+11+1 = 13)
		return 1
	default:
		return 2
	}
}

// =============================================================================
// Tree delete confirm: click
// Same layout as dirty-close-prompt (ConfirmPrompt).
// =============================================================================

func (m *App) clickTreeDeleteConfirm(localX, localY int) tea.Cmd {
	if m.treeDeletePending == "" {
		return nil
	}
	fw := m.windowManager.Window("tree-delete-confirm")
	if fw == nil {
		return nil
	}
	_, h := fw.Size()
	innerH := h - 2
	contentRow := localY - 1
	buttonRow := innerH - 2

	if contentRow == buttonRow {
		if localX >= 3 && localX < 8 {
			m.confirmTreeDelete()
		} else {
			m.cancelTreeDelete()
		}
	}
	return nil
}

// =============================================================================
// Tree prompt: click
//
// Layout (from TextPromptLines):
//   Row 0: ╭─ New File ─╮
//   Row 1: │ prompt text│
//   Row 2: │ <value>    │  (input)
//   Row 3: │            │  (empty)
//   Row 4: │ enter:cre..│  (hints)
//   Row N: ╰────────────╯
// =============================================================================

func (m *App) clickTreePrompt(localX, localY int) tea.Cmd {
	if m.treePrompt == nil {
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
		if charX > len(m.treePrompt.Value) {
			charX = len(m.treePrompt.Value)
		}
		m.treePrompt.Cursor = charX
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
	if m.lspPopup.ReferencesID == "" || m.lspPopup.RefRows == nil {
		return nil
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.lspPopup.RefRows) {
		m.lspPopup.RefCursor = contentRow
		r := m.lspPopup.RefRows[m.lspPopup.RefCursor]
		m.jumpToReference(r)
		m.closeReferencesPopup()
	}
	return nil
}

func (m *App) hoverReferences(localX, localY int) {
	if m.lspPopup.RefRows == nil {
		return
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.lspPopup.RefRows) && contentRow != m.lspPopup.RefCursor {
		m.lspPopup.RefCursor = contentRow
		m.refreshReferencesPopup()
	}
}

// =============================================================================
// Completion popup: click & hover
// =============================================================================

func (m *App) clickCompletion(localX, localY int) tea.Cmd {
	if m.lspPopup.CompletionItems == nil {
		return nil
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.lspPopup.CompletionItems) {
		m.lspPopup.CompletionCursor = contentRow
		m.acceptCompletion()
	}
	return nil
}

func (m *App) hoverCompletion(localX, localY int) {
	if m.lspPopup.CompletionItems == nil {
		return
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.lspPopup.CompletionItems) && contentRow != m.lspPopup.CompletionCursor {
		m.lspPopup.CompletionCursor = contentRow
		m.dirty = true
	}
}

// =============================================================================
// Outline popup: click & hover
// =============================================================================

func (m *App) clickOutline(localX, localY int) tea.Cmd {
	if !m.lspPopup.OutlineOpen || m.lspPopup.OutlineRows == nil {
		return nil
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.lspPopup.OutlineRows) {
		m.lspPopup.OutlineCursor = contentRow
		sym := m.lspPopup.OutlineRows[m.lspPopup.OutlineCursor].sym
		m.editorState.Editor.SetCursorPosition(sym.Line, sym.Col)
		m.editorState.Editor.ScrollToCursor()
		layout := m.computeLayout()
		m.editorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
		m.closeOutlinePopup()
	}
	return nil
}

func (m *App) hoverOutline(localX, localY int) {
	if m.lspPopup.OutlineRows == nil {
		return
	}
	contentRow := localY - 1
	if contentRow >= 0 && contentRow < len(m.lspPopup.OutlineRows) && contentRow != m.lspPopup.OutlineCursor {
		m.lspPopup.OutlineCursor = contentRow
		m.dirty = true
	}
}
